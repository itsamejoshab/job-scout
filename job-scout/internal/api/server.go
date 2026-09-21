package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/jobscout/jobscout/webui"
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
	mux.HandleFunc("PUT /api/v0/search-settings", h.ReplaceSearchSettings)
	mux.HandleFunc("POST /api/v0/search-settings/reset", h.ResetSearchSettings)
	mux.HandleFunc("GET /api/v0/notification-settings", h.NotificationSettings)
	mux.HandleFunc("PUT /api/v0/notification-settings", h.ReplaceNotificationSettings)
	mux.HandleFunc("GET /api/v0/scraper-settings", h.ScraperSettings)
	mux.HandleFunc("GET /api/v0/scraper-settings/all", h.AllScraperSettings)
	mux.HandleFunc("PUT /api/v0/scraper-settings/{job_source}", h.ReplaceScraperSettings)
	mux.HandleFunc("POST /api/v0/scraper-settings/{job_source}/reset", h.ResetScraperSettings)
	mux.HandleFunc("GET /api/v0/settings/export", h.ExportSettings)
	mux.HandleFunc("POST /api/v0/settings/import", h.ImportSettings)
	mux.HandleFunc("GET /api/v0/jobs", h.Jobs)
	mux.HandleFunc("GET /api/v0/jobs/stats", h.JobStats)
	mux.HandleFunc("POST /api/v0/jobs/re-evaluate", h.ReEvaluateJobs)
	mux.HandleFunc("POST /api/v0/jobs/{id}/review", h.ReviewJob)
	mux.HandleFunc("GET /api/v0/jobs/{id}", h.Job)
	mux.HandleFunc("GET /api/v0/dashboard/stats", h.DashboardStats)
	mux.HandleFunc("GET /api/v0/temporal-test", h.TemporalTest)
	mux.HandleFunc("GET /api/v0/workflow/{id}", h.WorkflowStatus)
	mux.HandleFunc("GET /api/v0/config", h.Config)
	mux.HandleFunc("GET /api/v0/status", h.Status)

	// Plain health endpoint used by the Docker healthcheck.
	mux.HandleFunc("GET /health", h.Health)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", shellHandler(webui.Dist))

	return &http.Server{Addr: addr, Handler: mux}
}

func shellHandler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "." {
			if info, err := fs.Stat(assets, name); err == nil && !info.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}

		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "operator shell unavailable")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(index)
	})
}
