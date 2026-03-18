package ojsecho

import (
	"net/http"

	"github.com/labstack/echo/v4"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

// HealthCheckHandler returns an Echo handler that queries the OJS server
// health endpoint via the provided client. It returns 200 with the full
// [ojs.HealthStatus] payload when the server reports "healthy", and 503
// otherwise.
//
// This differs from [WorkerManager.HealthHandler] which only reports whether
// the local worker goroutine is running. Use both for a complete liveness +
// readiness probe setup:
//
//	e.GET("/healthz", ojsecho.HealthCheckHandler(client))  // OJS server health
//	e.GET("/readyz",  wm.HealthHandler())                  // local worker health
func HealthCheckHandler(client *ojs.Client) echo.HandlerFunc {
	return func(c echo.Context) error {
		status, err := client.Health(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "unhealthy",
				"error":  err.Error(),
			})
		}

		if status.Status != "healthy" {
			return c.JSON(http.StatusServiceUnavailable, status)
		}

		return c.JSON(http.StatusOK, status)
	}
}
