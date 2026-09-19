package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

type providerPayload struct {
	Enabled               bool                `json:"enabled"`
	ScrapeIntervalSeconds int                 `json:"scrape_interval_seconds"`
	TimespanCode          string              `json:"timespan_code"`
	PagesToScrape         int                 `json:"pages_to_scrape"`
	Rounds                int                 `json:"rounds"`
	SearchQueries         []map[string]string `json:"search_queries"`
	GlobalSearches        []string            `json:"global_searches"`
}

func TestProviderSettings_PutWhitelistsValidatesAndEchoes(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	last := time.Date(2026, 9, 18, 18, 0, 0, 0, time.UTC)
	next := last.Add(10 * time.Minute)
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = $1, next_eligible_at = $2
		WHERE job_source = 'LINKEDIN'
	`, last, next); err != nil {
		t.Fatalf("set cadence: %v", err)
	}

	body := map[string]any{
		"enabled":                 true,
		"scrape_interval_seconds": 60,
		"timespan_code":           "  r86400  ",
		"pages_to_scrape":         4,
		"rounds":                  3,
		"search_queries": []map[string]string{
			{"keywords": "  Support  ", "location": "  Remote  ", "f_WT": " 2 "},
		},
		"global_searches":     []string{"  Remote help desk near Port Orange  "},
		"id":                  999,
		"job_source":          "INDEED",
		"last_scraped_at":     "2000-01-01T00:00:00Z",
		"next_eligible_at":    nil,
		"created_at":          "2000-01-01T00:00:00Z",
		"updated_at":          "2000-01-01T00:00:00Z",
		"non_whitelist_field": "ignored",
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/LINKEDIN", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT provider status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	stored, err := db.GetScraperSettings(t.Context(), pool, db.SourceLinkedIn)
	if err != nil || stored == nil {
		t.Fatalf("stored settings: %v %#v", err, stored)
	}
	if !reflect.DeepEqual(got, *stored) {
		t.Errorf("response must echo stored row\ngot:  %#v\nwant: %#v", got, *stored)
	}
	if got.JobSource != db.SourceLinkedIn || got.ID == 999 {
		t.Errorf("path and stored identity must win: %#v", got)
	}
	if got.LastScrapedAt == nil || !got.LastScrapedAt.Equal(last) ||
		got.NextEligibleAt == nil || !got.NextEligibleAt.Equal(next) {
		t.Errorf("cadence changed: last=%v next=%v", got.LastScrapedAt, got.NextEligibleAt)
	}
	if got.TimespanCode != "r86400" ||
		got.SearchQueries[0]["keywords"] != "Support" ||
		got.SearchQueries[0]["location"] != "Remote" ||
		got.SearchQueries[0]["f_WT"] != "2" {
		t.Errorf("stored row was not normalized: %#v", got)
	}
	if !reflect.DeepEqual(got.GlobalSearches, []string{"Remote help desk near Port Orange"}) {
		t.Errorf("global searches not normalized: %#v", got.GlobalSearches)
	}
}

func TestProviderSettings_PutRejectsInvalidAndUnavailableProviders(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	valid := providerPayload{
		Enabled:               true,
		ScrapeIntervalSeconds: 60,
		TimespanCode:          "r86400",
		PagesToScrape:         1,
		Rounds:                1,
		SearchQueries: []map[string]string{
			{"keywords": "Support", "location": "Remote"},
		},
		GlobalSearches: []string{"Remote help desk"},
	}
	tests := []struct {
		name string
		path string
		edit func(*providerPayload)
		code int
	}{
		{"interval", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.ScrapeIntervalSeconds = 59 }, 400},
		{"pages", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.PagesToScrape = 0 }, 400},
		{"rounds low", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.Rounds = 0 }, 400},
		{"rounds high", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.Rounds = 4 }, 400},
		{"timespan", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.TimespanCode = " " }, 400},
		{"query keywords", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.SearchQueries[0]["keywords"] = " " }, 400},
		{"query location", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.SearchQueries[0]["location"] = "" }, 400},
		{"global empty", "/api/v0/scraper-settings/LINKEDIN", func(p *providerPayload) { p.GlobalSearches[0] = " " }, 400},
		{"stub enabled", "/api/v0/scraper-settings/INDEED", func(*providerPayload) {}, 400},
		{"unknown", "/api/v0/scraper-settings/UNKNOWN", func(*providerPayload) {}, 404},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := valid
			payload.SearchQueries = []map[string]string{{
				"keywords": "Support", "location": "Remote",
			}}
			payload.GlobalSearches = []string{"Remote help desk"}
			tc.edit(&payload)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, tc.path, bytes.NewReader(raw))
			rec := httptest.NewRecorder()
			NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Errorf("status=%d want %d body=%s", rec.Code, tc.code, rec.Body.String())
			}
			if tc.code == http.StatusBadRequest {
				var detail map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail["detail"] == "" {
					t.Errorf("validation error must contain detail: err=%v body=%s", err, rec.Body.String())
				}
			}
		})
	}

	if _, err := pool.Exec(`DELETE FROM scraper_settings WHERE job_source = 'INDEED'`); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	valid.Enabled = false
	raw, _ := json.Marshal(valid)
	req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/INDEED", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing row status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestProviderSettings_ResetRestoresSeedWhitelistAndPreservesCadence(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seed, err := db.GetScraperSettings(t.Context(), pool, db.SourceLinkedIn)
	if err != nil || seed == nil {
		t.Fatalf("get seed: %v %#v", err, seed)
	}
	last := time.Date(2026, 9, 18, 19, 0, 0, 0, time.UTC)
	next := last.Add(15 * time.Minute)
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET enabled = false, scrape_interval_seconds = 123, timespan_code = 'changed',
		    pages_to_scrape = 77, rounds = 3, search_queries = '[]', global_searches = '[]',
		    last_scraped_at = $1, next_eligible_at = $2
		WHERE job_source = 'LINKEDIN'
	`, last, next); err != nil {
		t.Fatalf("change provider: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings SET pages_to_scrape = 44 WHERE job_source = 'INDEED'
	`); err != nil {
		t.Fatalf("change other provider: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE search_settings SET title_include = '["keep me"]'
	`); err != nil {
		t.Fatalf("change filters: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v0/scraper-settings/LINKEDIN/reset", nil)
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	if got.Enabled != seed.Enabled ||
		got.ScrapeIntervalSeconds != seed.ScrapeIntervalSeconds ||
		got.TimespanCode != seed.TimespanCode ||
		got.PagesToScrape != seed.PagesToScrape || got.Rounds != seed.Rounds ||
		!reflect.DeepEqual(got.SearchQueries, seed.SearchQueries) ||
		!reflect.DeepEqual(got.GlobalSearches, seed.GlobalSearches) {
		t.Errorf("reset did not restore provider seed: got=%#v seed=%#v", got, seed)
	}
	if got.LastScrapedAt == nil || !got.LastScrapedAt.Equal(last) ||
		got.NextEligibleAt == nil || !got.NextEligibleAt.Equal(next) {
		t.Errorf("reset changed cadence: last=%v next=%v", got.LastScrapedAt, got.NextEligibleAt)
	}
	var otherPages int
	if err := pool.QueryRow(`
		SELECT pages_to_scrape FROM scraper_settings WHERE job_source = 'INDEED'
	`).Scan(&otherPages); err != nil || otherPages != 44 {
		t.Errorf("other provider changed: pages=%d err=%v", otherPages, err)
	}
	var filters []byte
	if err := pool.QueryRow(`SELECT title_include FROM search_settings LIMIT 1`).Scan(&filters); err != nil ||
		string(filters) != `["keep me"]` {
		t.Errorf("filter settings changed: filters=%s err=%v", filters, err)
	}

	unknownReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v0/scraper-settings/UNKNOWN/reset",
		nil,
	)
	unknownRec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(unknownRec, unknownReq)
	if unknownRec.Code != http.StatusNotFound {
		t.Errorf("unknown reset status=%d want 404 body=%s", unknownRec.Code, unknownRec.Body.String())
	}
}
