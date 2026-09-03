package serverless

import (
	"context"
	"encoding/json"
	"sync"
)

// SQSEvent represents an AWS SQS event containing one or more messages.
type SQSEvent struct {
	Records []SQSMessage `json:"Records"`
}

// SQSMessage represents a single SQS message containing an OJS job.
type SQSMessage struct {
	MessageID     string            `json:"messageId"`
	Body          string            `json:"body"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	MD5OfBody     string            `json:"md5OfBody,omitempty"`
	EventSourceID string            `json:"eventSource,omitempty"`
	ReceiptHandle string            `json:"receiptHandle,omitempty"`
}

// SQSBatchResponse is the response format for SQS batch item failures.
type SQSBatchResponse struct {
	BatchItemFailures []BatchItemFailure `json:"batchItemFailures"`
}

// BatchItemFailure identifies a single failed message in an SQS batch.
type BatchItemFailure struct {
	ItemIdentifier string `json:"itemIdentifier"`
}

// HandleSQS processes an SQS event and returns deterministic partial failures.
func (h *LambdaHandler) HandleSQS(ctx context.Context, event SQSEvent) (SQSBatchResponse, error) {
	ctx, err := h.beginInvocation(ctx, TriggerSQS, "")
	if err != nil {
		return SQSBatchResponse{}, err
	}
	return h.handleSQS(ctx, event), nil
}

func (h *LambdaHandler) handleSQS(ctx context.Context, event SQSEvent) SQSBatchResponse {
	failures := make([]bool, len(event.Records))
	concurrency := min(h.sqsConcurrency, len(event.Records))
	if concurrency == 0 {
		return SQSBatchResponse{BatchItemFailures: []BatchItemFailure{}}
	}

	indices := make(chan int)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				record := event.Records[index]
				recordCtx := h.enrichContext(ctx, TriggerSQS, record.MessageID)
				if err := h.processSQSRecord(recordCtx, record); err != nil {
					failures[index] = true
				}
			}
		}()
	}
	for index := range event.Records {
		indices <- index
	}
	close(indices)
	wg.Wait()

	response := SQSBatchResponse{BatchItemFailures: make([]BatchItemFailure, 0)}
	for index, failed := range failures {
		if failed {
			response.BatchItemFailures = append(response.BatchItemFailures, BatchItemFailure{
				ItemIdentifier: event.Records[index].MessageID,
			})
		}
	}
	return response
}

func (h *LambdaHandler) processSQSRecord(ctx context.Context, record SQSMessage) error {
	var job JobEvent
	if err := json.Unmarshal([]byte(record.Body), &job); err != nil {
		h.logger.Error("failed to unmarshal SQS message",
			"message_id", record.MessageID,
			"error", err,
		)
		return err
	}
	if err := h.processJob(ctx, job); err != nil {
		h.logger.Error("job processing failed",
			"message_id", record.MessageID,
			"job_id", job.ID,
			"job_type", job.Type,
			"error", err,
		)
		return err
	}
	h.logger.Info("job completed",
		"message_id", record.MessageID,
		"job_id", job.ID,
		"job_type", job.Type,
	)
	return nil
}
