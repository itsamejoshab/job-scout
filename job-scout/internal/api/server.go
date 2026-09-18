package api

import (
	"net/http"
)

// NewServer wires routes (Go 1.22 method+path patterns) onto a ServeMux.
func NewServer(addr string, h *Handler) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v0/run", h.Run)
	mux.HandleFunc("POST /api/v0/notify", h.Notify)
	mux.HandleFunc("POST /api/v0/scrape", h.Scrape)
	mux.HandleFunc("POST /api/v0/test", h.Test)
	mux.HandleFunc("GET /api/v0/health", h.Health)
	mux.HandleFunc("GET /api/v0/db-test", h.DBTest)
	mux.HandleFunc("GET /api/v0/search-settings", h.SearchSettings)
	mux.HandleFunc("GET /api/v0/scraper-settings", h.ScraperSettings)
	mux.HandleFunc("GET /api/v0/scraper-settings/all", h.AllScraperSettings)
	mux.HandleFunc("GET /api/v0/jobs", h.Jobs)
	mux.HandleFunc("GET /api/v0/jobs/stats", h.JobStats)
	mux.HandleFunc("GET /api/v0/temporal-test", h.TemporalTest)
	mux.HandleFunc("GET /api/v0/workflow/{id}", h.WorkflowStatus)
	mux.HandleFunc("GET /api/v0/config", h.Config)

	// Plain health endpoint used by the Docker healthcheck.
	mux.HandleFunc("GET /health", h.Health)

	return &http.Server{Addr: addr, Handler: mux}
}
