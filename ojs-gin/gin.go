// Package ojsgin provides Gin web framework middleware for Open Job Spec.
//
// The middleware injects an OJS client into the Gin context, making it
// available to all downstream handlers via ClientFromContext or the
// Enqueue helper.
package ojsgin

import (
	"fmt"

	"github.com/gin-gonic/gin"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

const contextKey = "ojs.client"

// Middleware returns a Gin middleware that injects the OJS client into
// the request context. Downstream handlers can retrieve it via
// ClientFromContext or use the Enqueue helper.
func Middleware(client *ojs.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(contextKey, client)
		c.Next()
	}
}

// ClientFromContext retrieves the OJS client from a Gin context.
// Returns the client and true if found, or nil and false otherwise.
func ClientFromContext(c *gin.Context) (*ojs.Client, bool) {
	v, exists := c.Get(contextKey)
	if !exists {
		return nil, false
	}
	client, ok := v.(*ojs.Client)
	return client, ok
}

// Enqueue retrieves the OJS client from the Gin context and enqueues a job.
// Returns an error if no client is found in the context.
func Enqueue(c *gin.Context, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error {
	client, ok := ClientFromContext(c)
	if !ok {
		return fmt.Errorf("ojsgin: no OJS client in context; use ojsgin.Middleware")
	}
	_, err := client.Enqueue(c.Request.Context(), jobType, args, opts...)
	return err
}
