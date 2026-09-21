package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
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

func TestProviderSettings_DiceGetPutResetWhitelistAndCadence(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	getReq := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET Dice status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var seed db.ScraperSettings
	if err := json.Unmarshal(getRec.Body.Bytes(), &seed); err != nil {
		t.Fatalf("decode Dice GET: %v", err)
	}
	if seed.JobSource != "DICE" {
		t.Errorf("GET job_source=%q, want DICE", seed.JobSource)
	}
	if !seed.Enabled || seed.ScrapeIntervalSeconds != 43200 || seed.TimespanCode != "24h" ||
		seed.PagesToScrape != 1 || seed.Rounds != 1 {
		t.Errorf("GET Dice seed cadence fields: %#v", seed)
	}
	if !reflect.DeepEqual(seed.GlobalSearches, []string{}) {
		t.Errorf("GET Dice global_searches=%#v, want empty list", seed.GlobalSearches)
	}
	if len(seed.SearchQueries) != 1 ||
		seed.SearchQueries[0]["keywords"] != "Desktop or Endpoint or Application Support" ||
		seed.SearchQueries[0]["location"] != "Port Orange, FL" ||
		seed.SearchQueries[0]["include_remote"] != "true" {
		t.Errorf("GET Dice search_queries=%#v", seed.SearchQueries)
	}
	if _, ok := seed.SearchQueries[0]["f_WT"]; ok {
		t.Errorf("Dice GET must not persist f_WT: %#v", seed.SearchQueries[0])
	}

	allReq := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings/all", nil)
	allRec := httptest.NewRecorder()
	handler.ServeHTTP(allRec, allReq)
	if allRec.Code != http.StatusOK {
		t.Fatalf("GET all status=%d body=%s", allRec.Code, allRec.Body.String())
	}
	var all map[string]db.ScraperSettings
	if err := json.Unmarshal(allRec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if _, ok := all["DICE"]; !ok {
		t.Fatalf("all scraper-settings keys=%v, want DICE", keysOf(all))
	}

	last := time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC)
	next := last.Add(3 * time.Hour)
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = $1, next_eligible_at = $2
		WHERE job_source = 'DICE'
	`, last, next); err != nil {
		t.Fatalf("set Dice cadence: %v", err)
	}

	putBody := map[string]any{
		"enabled":                 true,
		"scrape_interval_seconds": 120,
		"timespan_code":           "7d",
		"pages_to_scrape":         2,
		"rounds":                  2,
		"search_queries": []map[string]any{
			{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": true},
			{"keywords": "Desktop", "location": "Daytona Beach, FL", "include_remote": false},
			{"keywords": "Endpoint", "location": "Port Orange, FL", "include_remote": true},
			{"keywords": "Endpoint", "location": "Daytona Beach, FL", "include_remote": false},
		},
		"global_searches":  []string{},
		"last_scraped_at":  "2000-01-01T00:00:00Z",
		"next_eligible_at": nil,
		"job_source":       "LINKEDIN",
		"id":               999,
	}
	raw, err := json.Marshal(putBody)
	if err != nil {
		t.Fatalf("marshal Dice PUT: %v", err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/DICE", bytes.NewReader(raw))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT Dice status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(putRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode Dice PUT: %v", err)
	}
	stored, err := db.GetScraperSettings(t.Context(), pool, "DICE")
	if err != nil || stored == nil {
		t.Fatalf("stored Dice settings: %v %#v", err, stored)
	}
	if !reflect.DeepEqual(got, *stored) {
		t.Errorf("Dice PUT must echo stored row\ngot:  %#v\nwant: %#v", got, *stored)
	}
	if got.JobSource != "DICE" || got.ID == 999 {
		t.Errorf("path identity must win: %#v", got)
	}
	if got.LastScrapedAt == nil || !got.LastScrapedAt.Equal(last) ||
		got.NextEligibleAt == nil || !got.NextEligibleAt.Equal(next) {
		t.Errorf("Dice PUT changed cadence: last=%v next=%v", got.LastScrapedAt, got.NextEligibleAt)
	}
	wantQueries := []map[string]string{
		{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": "true"},
		{"keywords": "Desktop", "location": "Daytona Beach, FL", "include_remote": "false"},
		{"keywords": "Endpoint", "location": "Port Orange, FL", "include_remote": "true"},
		{"keywords": "Endpoint", "location": "Daytona Beach, FL", "include_remote": "false"},
	}
	if !reflect.DeepEqual(got.SearchQueries, wantQueries) {
		t.Errorf("Dice stored queries=%#v want %#v", got.SearchQueries, wantQueries)
	}
	for _, query := range got.SearchQueries {
		if _, ok := query["f_WT"]; ok {
			t.Errorf("Dice PUT must not persist f_WT: %#v", query)
		}
	}

	resetReq := httptest.NewRequest(http.MethodPost, "/api/v0/scraper-settings/DICE/reset", nil)
	resetRec := httptest.NewRecorder()
	handler.ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset Dice status=%d body=%s", resetRec.Code, resetRec.Body.String())
	}
	var reset db.ScraperSettings
	if err := json.Unmarshal(resetRec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode Dice reset: %v", err)
	}
	if reset.Enabled != seed.Enabled ||
		reset.ScrapeIntervalSeconds != seed.ScrapeIntervalSeconds ||
		reset.TimespanCode != seed.TimespanCode ||
		reset.PagesToScrape != seed.PagesToScrape || reset.Rounds != seed.Rounds ||
		!reflect.DeepEqual(reset.SearchQueries, seed.SearchQueries) ||
		!reflect.DeepEqual(reset.GlobalSearches, seed.GlobalSearches) {
		t.Errorf("Dice reset did not restore seed whitelist: got=%#v seed=%#v", reset, seed)
	}
	if reset.LastScrapedAt == nil || !reset.LastScrapedAt.Equal(last) ||
		reset.NextEligibleAt == nil || !reset.NextEligibleAt.Equal(next) {
		t.Errorf("Dice reset changed cadence: last=%v next=%v", reset.LastScrapedAt, reset.NextEligibleAt)
	}
}

func TestProviderSettings_DicePutRejectsInvalidMatrixAndLinkedInFields(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	tests := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"interval", func(body map[string]any) { body["scrape_interval_seconds"] = 59 }, "60"},
		{"rounds low", func(body map[string]any) { body["rounds"] = 0 }, "rounds"},
		{"rounds high", func(body map[string]any) { body["rounds"] = 4 }, "rounds"},
		{"timespan", func(body map[string]any) { body["timespan_code"] = "r86400" }, "24h"},
		{"missing location", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "", "include_remote": true},
			}
		}, "location"},
		{"global searches", func(body map[string]any) { body["global_searches"] = []string{"remote only"} }, "global"},
		{"f_WT", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": true, "f_WT": "2"},
			}
		}, "f_WT"},
		{"empty f_WT", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": true, "f_WT": ""},
			}
		}, "f_WT"},
		{"pages", func(body map[string]any) { body["pages_to_scrape"] = 6 }, "5"},
		{"queries cap", func(body map[string]any) {
			body["search_queries"] = expandedDiceQueries(11, 1)
		}, "quer"},
		{"locations cap", func(body map[string]any) {
			body["search_queries"] = expandedDiceQueries(1, 11)
		}, "location"},
		{"pairs cap", func(body map[string]any) {
			body["search_queries"] = expandedDiceQueries(5, 5)
		}, "20"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validDicePutBody()
			tc.edit(body)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/DICE", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			var detail map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail["detail"] == "" {
				t.Errorf("validation error must contain detail: err=%v body=%s", err, rec.Body.String())
				return
			}
			if strings.Contains(detail["detail"], "not implemented") {
				t.Errorf("rejected as unimplemented, want %s validation: %s", tc.name, detail["detail"])
			}
			if !strings.Contains(detail["detail"], tc.want) {
				t.Errorf("detail=%q, want substring %q", detail["detail"], tc.want)
			}
		})
	}

	linkedIn := providerPayload{
		Enabled:               true,
		ScrapeIntervalSeconds: 60,
		TimespanCode:          "r86400",
		PagesToScrape:         1,
		Rounds:                1,
		SearchQueries: []map[string]string{
			{"keywords": "Support", "location": "Remote", "f_WT": "2"},
		},
		GlobalSearches: []string{"Remote help desk"},
	}
	raw, err := json.Marshal(linkedIn)
	if err != nil {
		t.Fatalf("marshal LinkedIn: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/LINKEDIN", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("LinkedIn PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode LinkedIn PUT: %v", err)
	}
	if got.SearchQueries[0]["f_WT"] != "2" {
		t.Errorf("LinkedIn PUT must keep f_WT, got %#v", got.SearchQueries[0])
	}
	if _, ok := got.SearchQueries[0]["include_remote"]; ok {
		t.Errorf("LinkedIn PUT must omit include_remote, got %#v", got.SearchQueries[0])
	}
}

func TestProviderSettings_DicePutAcceptsInclusiveMatrixCaps(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	tests := []struct {
		name string
		edit func(map[string]any)
	}{
		{"10 queries", func(body map[string]any) { body["search_queries"] = expandedDiceQueries(10, 1) }},
		{"10 locations", func(body map[string]any) { body["search_queries"] = expandedDiceQueries(1, 10) }},
		{"20 pairs", func(body map[string]any) { body["search_queries"] = expandedDiceQueries(4, 5) }},
		{"5 pages", func(body map[string]any) { body["pages_to_scrape"] = 5 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validDicePutBody()
			tc.edit(body)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/DICE", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
			}
			var got db.ScraperSettings
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got.SearchQueries) == 0 {
				t.Fatal("stored search_queries must not be empty")
			}
			if _, ok := got.SearchQueries[0]["f_WT"]; ok {
				t.Errorf("Dice must not persist f_WT: %#v", got.SearchQueries[0])
			}
		})
	}
}

func validIndeedPutBody() map[string]any {
	return map[string]any{
		"enabled":                 true,
		"scrape_interval_seconds": 43200,
		"timespan_code":           "1",
		"pages_to_scrape":         1,
		"rounds":                  1,
		"search_queries": []map[string]any{
			{
				"keywords": "Desktop", "location": "Port Orange, FL",
				"include_remote": false, "include_hybrid": false, "radius": "15",
			},
		},
		"global_searches": []string{},
		"provider_options": map[string]any{
			"country": "us", "jobType": "fulltime", "fromDays": "1",
			"maxRows": 100, "enableUniqueJobs": true, "includeSimilarJobs": false,
		},
	}
}

func TestProviderSettings_IndeedGetPutResetWhitelistAndCadence(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	getReq := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=INDEED", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET Indeed status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var seed db.ScraperSettings
	if err := json.Unmarshal(getRec.Body.Bytes(), &seed); err != nil {
		t.Fatalf("decode Indeed GET: %v", err)
	}
	if seed.JobSource != "INDEED" {
		t.Errorf("GET job_source=%q, want INDEED", seed.JobSource)
	}
	if !seed.Enabled || seed.ScrapeIntervalSeconds != 43200 || seed.TimespanCode != "1" {
		t.Errorf("GET Indeed seed cadence fields: %#v", seed)
	}
	if len(seed.SearchQueries) != 1 ||
		seed.SearchQueries[0]["keywords"] != "Desktop or Endpoint or Application Support" ||
		seed.SearchQueries[0]["location"] != "Port Orange, FL" ||
		seed.SearchQueries[0]["include_remote"] != "false" ||
		seed.SearchQueries[0]["include_hybrid"] != "false" ||
		seed.SearchQueries[0]["radius"] != "15" {
		t.Errorf("GET Indeed search_queries=%#v", seed.SearchQueries)
	}
	opts, err := db.ParseIndeedOptions(seed.ProviderOptions)
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if opts.Country != "us" || opts.JobType != "fulltime" ||
		opts.FromDays != "1" || opts.MaxRows != 100 || !opts.EnableUniqueJobs || opts.IncludeSimilarJobs {
		t.Errorf("GET Indeed provider_options=%#v", opts)
	}

	last := time.Date(2026, 9, 20, 14, 0, 0, 0, time.UTC)
	next := last.Add(12 * time.Hour)
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET last_scraped_at = $1, next_eligible_at = $2
		WHERE job_source = 'INDEED'
	`, last, next); err != nil {
		t.Fatalf("set Indeed cadence: %v", err)
	}

	putBody := validIndeedPutBody()
	putBody["scrape_interval_seconds"] = 120
	putBody["search_queries"] = []map[string]any{
		{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": true, "include_hybrid": false, "radius": "25"},
		{"keywords": "Desktop", "location": "Daytona Beach, FL", "include_remote": false, "include_hybrid": true, "radius": "10"},
	}
	putBody["provider_options"] = map[string]any{
		"country": "us", "jobType": "contract", "fromDays": "3",
		"maxRows": 50, "enableUniqueJobs": true, "includeSimilarJobs": false,
	}
	putBody["timespan_code"] = "3"
	raw, err := json.Marshal(putBody)
	if err != nil {
		t.Fatalf("marshal Indeed PUT: %v", err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/INDEED", bytes.NewReader(raw))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT Indeed status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(putRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode Indeed PUT: %v", err)
	}
	if got.LastScrapedAt == nil || !got.LastScrapedAt.Equal(last) ||
		got.NextEligibleAt == nil || !got.NextEligibleAt.Equal(next) {
		t.Errorf("Indeed PUT changed cadence: last=%v next=%v", got.LastScrapedAt, got.NextEligibleAt)
	}
	wantQueries := []map[string]string{
		{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": "true", "include_hybrid": "false", "radius": "25"},
		{"keywords": "Desktop", "location": "Daytona Beach, FL", "include_remote": "false", "include_hybrid": "true", "radius": "10"},
	}
	if !reflect.DeepEqual(got.SearchQueries, wantQueries) {
		t.Errorf("Indeed stored queries=%#v want %#v", got.SearchQueries, wantQueries)
	}
	storedOpts, err := db.ParseIndeedOptions(got.ProviderOptions)
	if err != nil {
		t.Fatalf("parse stored options: %v", err)
	}
	if storedOpts.JobType != "contract" || storedOpts.FromDays != "3" || storedOpts.MaxRows != 50 {
		t.Errorf("stored Indeed options=%#v", storedOpts)
	}

	resetReq := httptest.NewRequest(http.MethodPost, "/api/v0/scraper-settings/INDEED/reset", nil)
	resetRec := httptest.NewRecorder()
	handler.ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset Indeed status=%d body=%s", resetRec.Code, resetRec.Body.String())
	}
	var reset db.ScraperSettings
	if err := json.Unmarshal(resetRec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode Indeed reset: %v", err)
	}
	if reset.Enabled != seed.Enabled ||
		reset.ScrapeIntervalSeconds != seed.ScrapeIntervalSeconds ||
		reset.TimespanCode != seed.TimespanCode ||
		!reflect.DeepEqual(reset.SearchQueries, seed.SearchQueries) {
		t.Errorf("Indeed reset did not restore seed whitelist: got=%#v seed=%#v", reset, seed)
	}
	if reset.LastScrapedAt == nil || !reset.LastScrapedAt.Equal(last) {
		t.Errorf("Indeed reset changed cadence: last=%v", reset.LastScrapedAt)
	}
}

func TestProviderSettings_IndeedPutRejectsInvalidOptions(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	tests := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"missing options", func(body map[string]any) { delete(body, "provider_options") }, "provider_options"},
		{"jobType", func(body map[string]any) {
			opts := body["provider_options"].(map[string]any)
			opts["jobType"] = "hourly"
		}, "jobType"},
		{"fromDays", func(body map[string]any) {
			opts := body["provider_options"].(map[string]any)
			opts["fromDays"] = "2"
			body["timespan_code"] = "2"
		}, "fromDays"},
		{"timespan mismatch", func(body map[string]any) { body["timespan_code"] = "7" }, "fromDays"},
		{"f_WT", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": false, "include_hybrid": false, "radius": "15", "f_WT": "2"},
			}
		}, "f_WT"},
		{"global", func(body map[string]any) { body["global_searches"] = []string{"remote"} }, "global"},
		{"hybrid type", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": false, "radius": "15"},
			}
		}, "include_hybrid"},
		{"radius", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": false, "include_hybrid": false, "radius": "12"},
			}
		}, "radius"},
		{"missing radius", func(body map[string]any) {
			body["search_queries"] = []map[string]any{
				{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": false, "include_hybrid": false},
			}
		}, "radius"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validIndeedPutBody()
			tc.edit(body)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/INDEED", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			var detail map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail["detail"] == "" {
				t.Errorf("validation error must contain detail: err=%v body=%s", err, rec.Body.String())
				return
			}
			if !strings.Contains(detail["detail"], tc.want) {
				t.Errorf("detail=%q, want substring %q", detail["detail"], tc.want)
			}
		})
	}
}

func validDicePutBody() map[string]any {
	return map[string]any{
		"enabled":                 true,
		"scrape_interval_seconds": 43200,
		"timespan_code":           "24h",
		"pages_to_scrape":         1,
		"rounds":                  1,
		"search_queries": []map[string]any{
			{"keywords": "Desktop", "location": "Port Orange, FL", "include_remote": true},
		},
		"global_searches": []string{},
	}
}

func expandedDiceQueries(queryCount, locationCount int) []map[string]any {
	out := make([]map[string]any, 0, queryCount*locationCount)
	for q := 0; q < queryCount; q++ {
		for loc := 0; loc < locationCount; loc++ {
			out = append(out, map[string]any{
				"keywords":       "query-" + strconv.Itoa(q),
				"location":       "loc-" + strconv.Itoa(loc),
				"include_remote": loc%2 == 0,
			})
		}
	}
	return out
}

func TestProviderSettings_DiceGetIncludesApifyBudget(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fixed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	t.Run("token_missing", func(t *testing.T) {
		var apifyCalls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			apifyCalls.Add(1)
			t.Error("empty token must not call Apify")
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "", 100, func() time.Time { return fixed })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET Dice status=%d body=%s", rec.Code, rec.Body.String())
		}
		if apifyCalls.Load() != 0 {
			t.Fatalf("Apify HTTP calls = %d, want 0", apifyCalls.Load())
		}
		budget := decodeSettingsBudget(t, rec.Body.Bytes())
		assertApifyBudget(t, budget, budgetExpect{
			reason:  "token_missing",
			blocked: true,
			start:   "2026-09-21T00:00:00Z",
			end:     "2026-10-21T00:00:00Z",
			limit:   "1.00",
		})
	})

	t.Run("ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Errorf("Authorization = %q, want Bearer test-token", r.Header.Get("Authorization"))
			}
			writeApifyRuns(w, []map[string]any{
				{"id": "ext-1", "status": "SUCCEEDED", "usageTotalUsd": 0.40, "startedAt": "2026-09-21T01:00:00.000Z"},
				{"id": "ext-2", "status": "SUCCEEDED", "usageTotalUsd": 0.15, "startedAt": "2026-09-21T02:00:00.000Z"},
			})
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET Dice status=%d body=%s", rec.Code, rec.Body.String())
		}
		used, remaining := "0.55", "0.45"
		assertApifyBudget(t, decodeSettingsBudget(t, rec.Body.Bytes()), budgetExpect{
			reason:    "ok",
			blocked:   false,
			start:     "2026-09-21T00:00:00Z",
			end:       "2026-10-21T00:00:00Z",
			limit:     "1.00",
			used:      &used,
			remaining: &remaining,
		})
		var settings db.ScraperSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
			t.Fatalf("Dice GET must still decode as scraper settings: %v", err)
		}
		if settings.JobSource != "DICE" {
			t.Errorf("job_source = %q, want DICE", settings.JobSource)
		}
	})

	t.Run("budget_exhausted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeApifyRuns(w, []map[string]any{
				{"id": "spent", "status": "SUCCEEDED", "usageTotalUsd": 1.00, "startedAt": "2026-09-21T01:00:00.000Z"},
			})
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET Dice status=%d body=%s", rec.Code, rec.Body.String())
		}
		used, remaining := "1.00", "0.00"
		assertApifyBudget(t, decodeSettingsBudget(t, rec.Body.Bytes()), budgetExpect{
			reason:    "budget_exhausted",
			blocked:   true,
			start:     "2026-09-21T00:00:00Z",
			end:       "2026-10-21T00:00:00Z",
			limit:     "1.00",
			used:      &used,
			remaining: &remaining,
		})
	})

	t.Run("usage_unknown", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeApifyRuns(w, []map[string]any{
				{"id": "live", "status": "RUNNING", "startedAt": "2026-09-21T03:00:00.000Z"},
			})
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET Dice status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertApifyBudget(t, decodeSettingsBudget(t, rec.Body.Bytes()), budgetExpect{
			reason:  "usage_unknown",
			blocked: true,
			start:   "2026-09-21T00:00:00Z",
			end:     "2026-10-21T00:00:00Z",
			limit:   "1.00",
		})
	})

	t.Run("apify_unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(srv.Close)
		handler := budgetTestHandler(pool, srv, "test-token", 100, func() time.Time { return fixed })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=DICE", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET Dice status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertApifyBudget(t, decodeSettingsBudget(t, rec.Body.Bytes()), budgetExpect{
			reason:  "apify_unavailable",
			blocked: false,
			start:   "2026-09-21T00:00:00Z",
			end:     "2026-10-21T00:00:00Z",
			limit:   "1.00",
		})
	})

	t.Run("linkedin_omits_budget", func(t *testing.T) {
		handler := &Handler{
			DB:  pool,
			Cfg: config.Config{ApifyMonthlyBudgetCents: 100},
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=LINKEDIN", nil)
		NewServer("", handler).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET LinkedIn status=%d body=%s", rec.Code, rec.Body.String())
		}
		dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
		dec.UseNumber()
		var body map[string]any
		if err := dec.Decode(&body); err != nil {
			t.Fatalf("decode LinkedIn settings: %v", err)
		}
		if _, ok := body["apify_budget"]; ok {
			t.Fatalf("LINKEDIN GET must omit apify_budget, got %#v", body["apify_budget"])
		}
	})
}

func decodeSettingsBudget(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var body map[string]any
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode scraper-settings: %v", err)
	}
	return requireApifyBudget(t, body)
}

func validFantasticPutBody() map[string]any {
	return map[string]any{
		"enabled":                 true,
		"scrape_interval_seconds": 43200,
		"timespan_code":           "all",
		"pages_to_scrape":         1,
		"rounds":                  1,
		"search_queries":          []map[string]any{},
		"global_searches":         []string{},
		"provider_options": map[string]any{
			"queries": []map[string]any{
				{
					"titleSearch":             []string{"Desktop Support", "Application Support"},
					"titleExclusionSearch":    []string{"Manager", "Director"},
					"locationSearch":          []string{"United States"},
					"locationExclusionSearch": []string{"India:*", "India"},
					"aiWorkArrangementFilter": []string{"Remote Solely"},
					"aiEmploymentTypeFilter":  []string{"FULL_TIME"},
					"limit":                   200,
				},
			},
		},
	}
}

func TestProviderSettings_FantasticGetPutResetQueries(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler

	getReq := httptest.NewRequest(http.MethodGet, "/api/v0/scraper-settings?job_source=FANTASTIC", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET Fantastic status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var seed db.ScraperSettings
	if err := json.Unmarshal(getRec.Body.Bytes(), &seed); err != nil {
		t.Fatalf("decode Fantastic GET: %v", err)
	}
	if seed.JobSource != "FANTASTIC" {
		t.Errorf("GET job_source=%q, want FANTASTIC", seed.JobSource)
	}
	if !seed.Enabled || seed.ScrapeIntervalSeconds != 43200 || seed.TimespanCode != "all" {
		t.Errorf("GET Fantastic seed cadence fields: %#v", seed)
	}
	opts, err := db.ParseFantasticOptions(seed.ProviderOptions)
	if err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if !reflect.DeepEqual(opts, db.DefaultFantasticOptions()) {
		t.Errorf("GET Fantastic provider_options=%#v", opts)
	}

	putBody := validFantasticPutBody()
	putBody["scrape_interval_seconds"] = 120
	putBody["provider_options"] = map[string]any{
		"queries": []map[string]any{
			{
				"titleSearch":             []string{"Endpoint Support"},
				"titleExclusionSearch":    []string{"Director"},
				"locationSearch":          []string{"Canada"},
				"locationExclusionSearch": []string{"India"},
				"aiWorkArrangementFilter": []string{"Hybrid", "Remote OK"},
				"aiEmploymentTypeFilter":  []string{"FULL_TIME", "CONTRACTOR"},
				"limit":                   400,
			},
			{
				"titleSearch":             []string{"Application Support"},
				"titleExclusionSearch":    []string{},
				"locationSearch":          []string{"United States"},
				"locationExclusionSearch": []string{},
				"aiWorkArrangementFilter": []string{"On-site"},
				"aiEmploymentTypeFilter":  []string{"FULL_TIME"},
				"limit":                   200,
			},
		},
	}
	raw, err := json.Marshal(putBody)
	if err != nil {
		t.Fatalf("marshal Fantastic PUT: %v", err)
	}
	putReq := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/FANTASTIC", bytes.NewReader(raw))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT Fantastic status=%d body=%s", putRec.Code, putRec.Body.String())
	}
	var got db.ScraperSettings
	if err := json.Unmarshal(putRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode Fantastic PUT: %v", err)
	}
	if got.ScrapeIntervalSeconds != 120 {
		t.Errorf("interval = %d, want 120", got.ScrapeIntervalSeconds)
	}
	storedOpts, err := db.ParseFantasticOptions(got.ProviderOptions)
	if err != nil {
		t.Fatalf("parse stored options: %v", err)
	}
	if len(storedOpts.Queries) != 2 {
		t.Fatalf("stored queries = %d, want 2", len(storedOpts.Queries))
	}
	if storedOpts.Queries[0].TitleSearch[0] != "Endpoint Support" || storedOpts.Queries[0].Limit != 400 {
		t.Errorf("stored query 0 = %#v", storedOpts.Queries[0])
	}
	if storedOpts.Queries[1].LocationSearch[0] != "United States" {
		t.Errorf("stored query 1 = %#v", storedOpts.Queries[1])
	}
	if got.SearchQueries[0]["keywords"] != "Endpoint Support" || got.SearchQueries[0]["location"] != "Canada" {
		t.Errorf("flattened search_queries = %#v", got.SearchQueries)
	}

	resetReq := httptest.NewRequest(http.MethodPost, "/api/v0/scraper-settings/FANTASTIC/reset", nil)
	resetRec := httptest.NewRecorder()
	handler.ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset Fantastic status=%d body=%s", resetRec.Code, resetRec.Body.String())
	}
	var reset db.ScraperSettings
	if err := json.Unmarshal(resetRec.Body.Bytes(), &reset); err != nil {
		t.Fatalf("decode Fantastic reset: %v", err)
	}
	resetOpts, err := db.ParseFantasticOptions(reset.ProviderOptions)
	if err != nil {
		t.Fatalf("parse reset options: %v", err)
	}
	if !reflect.DeepEqual(resetOpts, db.DefaultFantasticOptions()) {
		t.Errorf("Fantastic reset options=%#v", resetOpts)
	}
}

func TestProviderSettings_FantasticPutRejectsInvalidQueries(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewServer("", &Handler{DB: pool}).Handler
	tests := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"missing options", func(body map[string]any) { delete(body, "provider_options") }, "provider_options"},
		{"empty queries", func(body map[string]any) {
			body["provider_options"] = map[string]any{"queries": []any{}}
		}, "queries"},
		{"limit too low", func(body map[string]any) {
			opts := body["provider_options"].(map[string]any)
			queries := opts["queries"].([]map[string]any)
			queries[0]["limit"] = 50
		}, "limit"},
		{"bad work arrangement", func(body map[string]any) {
			opts := body["provider_options"].(map[string]any)
			queries := opts["queries"].([]map[string]any)
			queries[0]["aiWorkArrangementFilter"] = []string{"Remote"}
		}, "aiWorkArrangementFilter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := validFantasticPutBody()
			tc.edit(body)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			req := httptest.NewRequest(http.MethodPut, "/api/v0/scraper-settings/FANTASTIC", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body=%s, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func keysOf(all map[string]db.ScraperSettings) []string {
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	return keys
}
