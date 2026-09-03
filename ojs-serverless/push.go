package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// PushDeliveryRequest is the HTTP body sent by an OJS server for push delivery.
type PushDeliveryRequest struct {
	Job        JobEvent `json:"job"`
	WorkerID   string   `json:"worker_id"`
	DeliveryID string   `json:"delivery_id"`
}

// PushDeliveryResponse is the HTTP response body for push delivery.
type PushDeliveryResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *PushError      `json:"error,omitempty"`
}

// PushError describes a job processing failure.
type PushError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

const (
	pushStatusCompleted = "completed"
	pushStatusFailed    = "failed"

	pushCodeInvalidRequest            = "invalid_request"
	pushCodeHandlerError              = "handler_error"
	pushCodeAuthenticationFailed      = "authentication_failed"
	pushCodeAuthenticationUnavailable = "authentication_unavailable"
	pushCodeMethodNotAllowed          = "method_not_allowed"
)

// HandleHTTP returns an http.HandlerFunc for signed OJS push delivery.
func (h *LambdaHandler) HandleHTTP() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			h.writeHTTPPushResponse(w, http.StatusMethodNotAllowed, pushFailure(
				pushCodeMethodNotAllowed,
				"only POST is accepted",
				false,
			))
			return
		}

		body := http.MaxBytesReader(w, r.Body, h.maxBodySize)
		rawBody, err := io.ReadAll(body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				h.writeHTTPPushResponse(w, http.StatusRequestEntityTooLarge, pushFailure(
					pushCodeInvalidRequest,
					"request body exceeds the configured size limit",
					false,
				))
				return
			}
			h.writeHTTPPushResponse(w, http.StatusBadRequest, pushFailure(
				pushCodeInvalidRequest,
				"failed to read request body",
				false,
			))
			return
		}

		status, response := h.handlePush(r.Context(), rawBody, r.Header)
		h.writeHTTPPushResponse(w, status, response)
	}
}

func (h *LambdaHandler) handlePush(
	ctx context.Context,
	rawBody []byte,
	headers http.Header,
) (int, PushDeliveryResponse) {
	timestampValues := headers.Values(PushTimestampHeader)
	if len(timestampValues) > 1 {
		return http.StatusUnauthorized, pushFailure(
			pushCodeAuthenticationFailed,
			"invalid push authentication",
			false,
		)
	}
	timestamp := ""
	if len(timestampValues) == 1 {
		timestamp = timestampValues[0]
	}
	if err := h.authenticatePush(timestamp, headers.Values(PushSignatureHeader), rawBody, time.Now()); err != nil {
		return h.pushAuthenticationFailure(err)
	}

	var request PushDeliveryRequest
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	if err := decoder.Decode(&request); err != nil {
		return http.StatusBadRequest, pushFailure(
			pushCodeInvalidRequest,
			"failed to decode request body",
			false,
		)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return http.StatusBadRequest, pushFailure(
			pushCodeInvalidRequest,
			"request body must contain exactly one JSON object",
			false,
		)
	}
	if request.Job.ID == "" || request.Job.Type == "" ||
		request.WorkerID == "" || request.DeliveryID == "" {
		return http.StatusBadRequest, pushFailure(
			pushCodeInvalidRequest,
			"job id, job type, worker id, and delivery id are required",
			false,
		)
	}

	jobHeader := headers.Get(PushJobIDHeader)
	deliveryHeader := headers.Get(PushDeliveryIDHeader)
	if !h.insecureAllowUnsignedPushForLocalDevelopment &&
		(jobHeader == "" || deliveryHeader == "") {
		return http.StatusBadRequest, pushFailure(
			pushCodeInvalidRequest,
			"push delivery headers are required",
			false,
		)
	}
	if (jobHeader != "" && jobHeader != request.Job.ID) ||
		(deliveryHeader != "" && deliveryHeader != request.DeliveryID) {
		return http.StatusBadRequest, pushFailure(
			pushCodeInvalidRequest,
			"push delivery headers do not match the request body",
			false,
		)
	}

	if err := h.processJob(ctx, request.Job); err != nil {
		h.logger.Error("job processing failed",
			"job_id", request.Job.ID,
			"job_type", request.Job.Type,
			"delivery_id", request.DeliveryID,
			"error", err,
		)
		return http.StatusOK, pushFailure(pushCodeHandlerError, err.Error(), true)
	}

	h.logger.Info("job completed",
		"job_id", request.Job.ID,
		"job_type", request.Job.Type,
		"delivery_id", request.DeliveryID,
	)
	return http.StatusOK, PushDeliveryResponse{Status: pushStatusCompleted}
}

func (h *LambdaHandler) pushAuthenticationFailure(err error) (int, PushDeliveryResponse) {
	switch {
	case errors.Is(err, errPushAuthNotConfigured), errors.Is(err, errPushAuthInvalidConfig):
		h.logger.Error("push authentication unavailable", "error", err)
		return http.StatusServiceUnavailable, pushFailure(
			pushCodeAuthenticationUnavailable,
			"push authentication is unavailable",
			false,
		)
	case errors.Is(err, errPushAuthHeaderTooLarge):
		return http.StatusRequestHeaderFieldsTooLarge, pushFailure(
			pushCodeAuthenticationFailed,
			"invalid push authentication",
			false,
		)
	default:
		return http.StatusUnauthorized, pushFailure(
			pushCodeAuthenticationFailed,
			"invalid push authentication",
			false,
		)
	}
}

func pushFailure(code, message string, retryable bool) PushDeliveryResponse {
	return PushDeliveryResponse{
		Status: pushStatusFailed,
		Error: &PushError{
			Code:      code,
			Message:   message,
			Retryable: retryable,
		},
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("additional JSON value")
		}
		return err
	}
	return nil
}

func (h *LambdaHandler) writeHTTPPushResponse(
	w http.ResponseWriter,
	status int,
	response PushDeliveryResponse,
) {
	payload, err := json.Marshal(response)
	if err != nil {
		h.logger.Error("failed to encode push response", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(payload, '\n')); err != nil {
		h.logger.Error("failed to write push response", "error", err)
	}
}
