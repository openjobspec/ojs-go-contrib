// Package ojsecho provides Echo web framework middleware for Open Job Spec.
//
// The middleware injects an OJS client into the Echo context, making it
// available to all downstream handlers via ClientFromContext or the
// Enqueue helper.
package ojsecho

import (
	"fmt"

	"github.com/labstack/echo/v4"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

const contextKey = "ojs.client"

// Middleware returns an Echo middleware that injects the OJS client into
// the request context. Downstream handlers can retrieve it via
// ClientFromContext or use the Enqueue helper.
func Middleware(client *ojs.Client) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(contextKey, client)
			return next(c)
		}
	}
}

// ClientFromContext retrieves the OJS client from an Echo context.
// Returns the client and true if found, or nil and false otherwise.
func ClientFromContext(c echo.Context) (*ojs.Client, bool) {
	v := c.Get(contextKey)
	if v == nil {
		return nil, false
	}
	client, ok := v.(*ojs.Client)
	return client, ok
}

// Enqueue retrieves the OJS client from the Echo context and enqueues a job.
// Returns an error if no client is found in the context.
func Enqueue(c echo.Context, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error {
	client, ok := ClientFromContext(c)
	if !ok {
		return fmt.Errorf("ojsecho: no OJS client in context; use ojsecho.Middleware")
	}
	_, err := client.Enqueue(c.Request().Context(), jobType, args, opts...)
	return err
}
