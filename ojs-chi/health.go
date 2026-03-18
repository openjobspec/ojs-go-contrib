package ojschi

import (
	"encoding/json"
	"net/http"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

// HealthCheckHandler returns an http.HandlerFunc that queries the OJS server
// health endpoint via the provided client. It returns 200 with the full
// [ojs.HealthStatus] payload when the server reports "healthy", and 503
// otherwise.
//
// This differs from [WorkerManager.HealthHandler] which only reports whether
// the local worker goroutine is running. Use both for a complete liveness +
// readiness probe setup:
//
//	r.Get("/healthz",  ojschi.HealthCheckHandler(client))  // OJS server health
//	r.Get("/readyz",   wm.HealthHandler())                 // local worker health
func HealthCheckHandler(client *ojs.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		status, err := client.Health(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}

		if status.Status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(status)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(status)
	}
}
