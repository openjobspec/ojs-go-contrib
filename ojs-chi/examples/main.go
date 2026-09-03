// Example Chi server with OJS integration.
//
// Run with Docker Compose:
//
//	docker-compose up -d
//	go run main.go
//
// Then enqueue a job:
//
//	curl -X POST http://localhost:3000/send-email \
//	  -H "Content-Type: application/json" \
//	  -d '{"to":"user@example.com","subject":"Hello"}'
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	ojschi "github.com/openjobspec/ojs-go-contrib/ojs-chi"
	ojs "github.com/openjobspec/ojs-go-sdk"
)

const maxRequestBodySize = 1 << 20

func main() {
	ojsURL := envOrDefault("OJS_URL", "http://localhost:8080")
	var clientOptions []ojs.ClientOption
	var workerOptions []ojs.WorkerOption
	if token := os.Getenv("OJS_AUTH_TOKEN"); token != "" {
		clientOptions = append(clientOptions, ojs.WithAuthToken(token))
		workerOptions = append(workerOptions, ojs.WithWorkerAuth(token))
	}
	client, err := ojs.NewClient(ojsURL, clientOptions...)
	if err != nil {
		log.Fatal(err)
	}

	// Set up Chi router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(ojschi.Middleware(client))

	// Set up worker
	worker := ojschi.NewWorkerManagerWithSDKOptions(ojschi.WorkerOptions{
		URL:         ojsURL,
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	}, workerOptions...)

	worker.Register("email.send", handleEmailSend)

	// Routes
	registerRoutes(r, client, worker)

	// Start worker and server with graceful shutdown
	ctx, cancel := ojschi.GracefulShutdown()
	defer cancel()

	if err := worker.StartAsync(ctx); err != nil {
		log.Printf("start worker: %v", err)
		return
	}

	srv := &http.Server{
		Addr:              ":3000",
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Println("Server listening on :3000")
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
		}
		cancel()
	}
	log.Println("Shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := worker.Stop(); err != nil {
		log.Printf("worker shutdown: %v", err)
	}
}

func registerRoutes(r chi.Router, client *ojs.Client, worker *ojschi.WorkerManager) {
	r.Post("/send-email", sendEmailHandler)
	r.Get("/jobs/{id}", getJobHandler)
	r.Get("/healthz", ojschi.HealthCheckHandler(client))
	r.Get("/readyz", worker.HealthHandler())
}

func sendEmailHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	if err := decodeJSON(r.Body, &body); err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(body.To) == "" || strings.TrimSpace(body.Subject) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "to and subject are required",
		})
		return
	}

	err := ojschi.Enqueue(r, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		log.Printf("enqueue email.send: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to enqueue job"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "enqueued"})
}

func getJobHandler(w http.ResponseWriter, r *http.Request) {
	client, ok := ojschi.ClientFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "OJS client not available",
		})
		return
	}

	jobID := strings.TrimSpace(chi.URLParam(r, "id"))
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "job id is required"})
		return
	}
	job, err := client.GetJob(r.Context(), jobID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ojs.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": "failed to retrieve job"})
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func handleEmailSend(ctx context.Context, job *ojs.JobContext) error {
	to, _ := job.Job.Args["to"].(string)
	subject, _ := job.Job.Args["subject"].(string)
	fmt.Printf("Sending email to %s: %s\n", to, subject)
	return nil
}

func decodeJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(payload, '\n')); err != nil {
		log.Printf("write response: %v", err)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
