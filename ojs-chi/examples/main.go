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
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	ojs "github.com/openjobspec/ojs-go-sdk"
	ojschi "github.com/openjobspec/ojs-go-contrib/ojs-chi"
)

func main() {
	client, err := ojs.NewClient("http://localhost:8080")
	if err != nil {
		log.Fatal(err)
	}

	// Set up Chi router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(ojschi.Middleware(client))

	// Set up worker
	worker := ojschi.NewWorkerManager(ojschi.WorkerOptions{
		URL:         "http://localhost:8080",
		Queues:      []string{"default", "emails"},
		Concurrency: 10,
	})

	worker.Register("email.send", handleEmailSend)

	// Routes
	r.Post("/send-email", sendEmailHandler)
	r.Get("/jobs/{id}", getJobHandler)
	r.Get("/health", worker.HealthHandler())

	// Start worker and server with graceful shutdown
	ctx, cancel := ojschi.GracefulShutdown()
	defer cancel()

	if err := worker.StartAsync(ctx); err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: ":3000", Handler: r}
	go func() {
		log.Println("Server listening on :3000")
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down...")
	srv.Shutdown(context.Background())
	worker.Stop()
}

func sendEmailHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	err := ojschi.Enqueue(r, "email.send", ojs.Args{
		"to":      body.To,
		"subject": body.Subject,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "enqueued"})
}

func getJobHandler(w http.ResponseWriter, r *http.Request) {
	client, ok := ojschi.ClientFromRequest(r)
	if !ok {
		http.Error(w, "OJS client not available", http.StatusInternalServerError)
		return
	}

	jobID := chi.URLParam(r, "id")
	job, err := client.GetJob(r.Context(), jobID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func handleEmailSend(ctx context.Context, job *ojs.JobContext) error {
	to, _ := job.Args["to"].(string)
	subject, _ := job.Args["subject"].(string)
	fmt.Printf("Sending email to %s: %s\n", to, subject)
	return nil
}
