package ojsgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

// HealthCheckHandler returns a Gin handler that queries the OJS server
// health endpoint via the provided client. It returns 200 with the full
// [ojs.HealthStatus] payload when the server reports "healthy", and 503
// otherwise.
//
// This differs from [WorkerManager.HealthHandler] which only reports whether
// the local worker goroutine is running. Use both for a complete liveness +
// readiness probe setup:
//
//	r.GET("/healthz", ojsgin.HealthCheckHandler(client))  // OJS server health
//	r.GET("/readyz",  wm.HealthHandler())                 // local worker health
func HealthCheckHandler(client *ojs.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		status, err := client.Health(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}

		if status.Status != "healthy" {
			c.JSON(http.StatusServiceUnavailable, status)
			return
		}

		c.JSON(http.StatusOK, status)
	}
}
