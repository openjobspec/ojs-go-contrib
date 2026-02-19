// Package ojschi provides Chi router middleware for Open Job Spec.
//
// The middleware injects an OJS client into the request context, making it
// available to all downstream handlers via ClientFromContext or the
// Enqueue helper.
//
// Chi is the router used by the official OJS backends (ojs-backend-redis,
// ojs-backend-postgres), making this integration a natural fit for projects
// that already use Chi.
package ojschi

import (
	"context"
	"fmt"
	"net/http"

	ojs "github.com/openjobspec/ojs-go-sdk"
)

type contextKeyType struct{}

var contextKey = contextKeyType{}

// Middleware returns a Chi-compatible middleware that injects the OJS client
// into the request context. Downstream handlers can retrieve it via
// ClientFromContext or use the Enqueue helper.
//
//	client, _ := ojs.NewClient("http://localhost:8080")
//	r := chi.NewRouter()
//	r.Use(ojschi.Middleware(client))
func Middleware(client *ojs.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), contextKey, client)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientFromContext retrieves the OJS client from a request context.
// Returns the client and true if found, or nil and false otherwise.
func ClientFromContext(ctx context.Context) (*ojs.Client, bool) {
	v := ctx.Value(contextKey)
	if v == nil {
		return nil, false
	}
	client, ok := v.(*ojs.Client)
	return client, ok
}

// ClientFromRequest is a convenience wrapper that retrieves the OJS client
// from an *http.Request's context.
func ClientFromRequest(r *http.Request) (*ojs.Client, bool) {
	return ClientFromContext(r.Context())
}

// Enqueue retrieves the OJS client from the request context and enqueues a job.
// Returns an error if no client is found in the context.
func Enqueue(r *http.Request, jobType string, args ojs.Args, opts ...ojs.EnqueueOption) error {
	client, ok := ClientFromContext(r.Context())
	if !ok {
		return fmt.Errorf("ojschi: no OJS client in context; use ojschi.Middleware")
	}
	_, err := client.Enqueue(r.Context(), jobType, args, opts...)
	return err
}

// MustClientFromContext retrieves the OJS client from the context and panics
// if it is not found. Use this only when the middleware is guaranteed to be
// registered upstream.
func MustClientFromContext(ctx context.Context) *ojs.Client {
	client, ok := ClientFromContext(ctx)
	if !ok {
		panic("ojschi: no OJS client in context; use ojschi.Middleware")
	}
	return client
}
