/*
 * internal/health/server.go
 *
 * Thin HTTP server exposing a single GET /healthz endpoint.
 * Reports system health by querying prompt and video-job counts
 * from SQLite. Designed for Docker health checks, load-balancer
 * probes, and monitoring dashboards.
 *
 * Uses only the Go standard library — no frameworks, no new deps.
 *
 * Copyright (C) 2026 hereticrush — Licensed under GPL-3.0
 */
package health

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hereticrush/bap/internal/db"
)

//go:embed dashboard.html
var dashboardFS embed.FS

/* Server wraps an http.Server and a database handle. */
type Server struct {
	httpServer *http.Server
	database   *sql.DB
}

/*
 * healthResponse is the nested JSON structure returned by /healthz.
 *
 * Example:
 *   {
 *     "status": "ok",
 *     "prompts": { "unused": 7 },
 *     "jobs":    { "pending": 2, "failed": 0 }
 *   }
 */
type healthResponse struct {
	Status  string         `json:"status"`
	Prompts promptsSummary `json:"prompts"`
	Jobs    jobsSummary    `json:"jobs"`
	Error   string         `json:"error,omitempty"`
}

type promptsSummary struct {
	Unused int `json:"unused"`
}

type jobsSummary struct {
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

/*
 * New creates a health Server listening on the given port.
 * The server registers a single route: GET /healthz.
 */
func New(port int, database *sql.DB) *Server {
	mux := http.NewServeMux()

	s := &Server{
		httpServer: &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: mux,
		},
		database: database,
	}

	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/jobs", s.handleListJobs)

	/* Serve dynamic dark-mode dashboard at the root */
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := dashboardFS.ReadFile("dashboard.html")
		if err != nil {
			slog.Error("failed to read embedded dashboard.html", "error", err)
			http.Error(w, "dashboard template missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	})

	/* Expose static video and image folders for browser playback previews */
	mux.Handle("/videos/", http.StripPrefix("/videos/", http.FileServer(http.Dir("data/videos"))))
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir("data/images"))))

	return s
}

/*
 * ListenAndServe starts the HTTP listener.
 * Blocks until the server is shut down or a fatal listen error occurs.
 * Returns http.ErrServerClosed on graceful shutdown (not a real error).
 */
func (s *Server) ListenAndServe() error {
	slog.Info("health server listening", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

/*
 * Shutdown gracefully drains in-flight requests within the
 * deadline imposed by ctx, then stops the listener.
 */
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

/*
 * handleHealthz queries the database for prompt and job counts,
 * then writes a JSON response.
 *
 * 200 OK           — database is reachable, counts retrieved
 * 503 Unavailable  — database query failed ("status": "degraded")
 */
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := healthResponse{Status: "ok"}

	/* Query prompt counts */
	unusedPrompts, err := db.CountByStatus(s.database, "UNUSED")
	if err != nil {
		slog.Error("healthz: failed to count prompts", "error", err)
		writeDegraded(w, err)
		return
	}
	resp.Prompts.Unused = unusedPrompts

	/* Query job counts */
	pendingJobs, err := db.CountJobsByStatus(s.database, "PENDING")
	if err != nil {
		slog.Error("healthz: failed to count pending jobs", "error", err)
		writeDegraded(w, err)
		return
	}
	resp.Jobs.Pending = pendingJobs

	failedJobs, err := db.CountJobsByStatus(s.database, "FAILED")
	if err != nil {
		slog.Error("healthz: failed to count failed jobs", "error", err)
		writeDegraded(w, err)
		return
	}
	resp.Jobs.Failed = failedJobs

	writeJSON(w, http.StatusOK, resp)
}

/*
 * writeDegraded sends a 503 response with status "degraded"
 * and the error message included for diagnostics.
 */
func writeDegraded(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusServiceUnavailable, healthResponse{
		Status: "degraded",
		Error:  err.Error(),
	})
}

/*
 * writeJSON marshals v to JSON and writes it to w with the
 * given HTTP status code and Content-Type header.
 */
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

/*
 * handleListJobs returns the most recent 50 video jobs from the pipeline.
 */
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	jobs, err := db.GetRecentJobs(s.database, 50)
	if err != nil {
		slog.Error("api/jobs: failed to fetch recent jobs", "error", err)
		writeDegraded(w, err)
		return
	}

	writeJSON(w, http.StatusOK, jobs)
}
