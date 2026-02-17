// Package ojsfiber provides Fiber web framework middleware for Open Job Spec.
//
// The middleware injects an OJS client into Fiber's Locals, making it
// available to all downstream handlers via ClientFromContext or the
// Enqueue helper.
package ojsfiber

import (
	"fmt"

	"github.com/gofiber/fiber/v2"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

const localsKey = "ojs.client"

// Middleware returns a Fiber middleware that injects the OJS client into
// Fiber's Locals. Downstream handlers can retrieve it via
// ClientFromContext or use the Enqueue helper.
func Middleware(client *ojs.Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals(localsKey, client)
		return c.Next()
	}
}

// ClientFromContext retrieves the OJS client from Fiber's Locals.
// Returns the client and true if found, or nil and false otherwise.
func ClientFromContext(c *fiber.Ctx) (*ojs.Client, bool) {
	v := c.Locals(localsKey)
	if v == nil {
		return nil, false
	}
	client, ok := v.(*ojs.Client)
	return client, ok
}

// Enqueue retrieves the OJS client from Fiber's Locals and enqueues a job.
// Returns an error if no client is found in the context.
func Enqueue(c *fiber.Ctx, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error {
	client, ok := ClientFromContext(c)
	if !ok {
		return fmt.Errorf("ojsfiber: no OJS client in context; use ojsfiber.Middleware")
	}
	_, err := client.Enqueue(c.UserContext(), jobType, args, opts...)
	return err
}
