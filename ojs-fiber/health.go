package ojsfiber

import (
	"github.com/gofiber/fiber/v2"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

// HealthCheckHandler returns a Fiber handler that queries the OJS server
// health endpoint via the provided client. It returns 200 with the full
// [ojs.HealthStatus] payload when the server reports "healthy", and 503
// otherwise.
//
// This differs from [WorkerManager.HealthHandler] which only reports whether
// the local worker goroutine is running. Use both for a complete liveness +
// readiness probe setup:
//
//	app.Get("/healthz", ojsfiber.HealthCheckHandler(client))  // OJS server health
//	app.Get("/readyz",  wm.HealthHandler())                   // local worker health
func HealthCheckHandler(client *ojs.Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		status, err := client.Health(c.UserContext())
		if err != nil {
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unhealthy",
				"error":  err.Error(),
			})
		}

		if status.Status != "healthy" {
			return c.Status(fiber.StatusServiceUnavailable).JSON(status)
		}

		return c.Status(fiber.StatusOK).JSON(status)
	}
}
