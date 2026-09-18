package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
)

type filterPayload struct {
	DescIncludeWords []string `json:"desc_include_words"`
	DescExcludeWords []string `json:"desc_exclude_words"`
	TitleInclude     []string `json:"title_include"`
	TitleExclude     []string `json:"title_exclude"`
	CompanyExclude   []string `json:"company_exclude"`
	NonRemotePhrases []string `json:"non_remote_phrases"`
}

func TestSearchSettings_PutNormalizesAndEchoesStoredRow(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	input := filterPayload{
		DescIncludeWords: []string{"  alpha  ", "ALPHA", "", "Beta", " beta "},
		DescExcludeWords: []string{"  no  ", "NO", "  ", "maybe"},
		TitleInclude:     []string{" ", "IT", "it", "Help Desk"},
		TitleExclude:     []string{"Sales", " sales ", "Manager"},
		CompanyExclude:   []string{"Acme", " acme ", "Other"},
		NonRemotePhrases: []string{"onsite", "OnSite", " travel "},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v0/search-settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/v0/search-settings status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.SearchSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := filterPayload{
		DescIncludeWords: []string{"alpha", "Beta"},
		DescExcludeWords: []string{"no", "maybe"},
		TitleInclude:     []string{"IT", "Help Desk"},
		TitleExclude:     []string{"Sales", "Manager"},
		CompanyExclude:   []string{"Acme", "Other"},
		NonRemotePhrases: []string{"onsite", "travel"},
	}
	if !reflect.DeepEqual(got.DescIncludeWords, want.DescIncludeWords) {
		t.Errorf("desc_include_words=%v want %v", got.DescIncludeWords, want.DescIncludeWords)
	}
	if !reflect.DeepEqual(got.DescExcludeWords, want.DescExcludeWords) {
		t.Errorf("desc_exclude_words=%v want %v", got.DescExcludeWords, want.DescExcludeWords)
	}
	if !reflect.DeepEqual(got.TitleInclude, want.TitleInclude) {
		t.Errorf("title_include=%v want %v", got.TitleInclude, want.TitleInclude)
	}
	if !reflect.DeepEqual(got.TitleExclude, want.TitleExclude) {
		t.Errorf("title_exclude=%v want %v", got.TitleExclude, want.TitleExclude)
	}
	if !reflect.DeepEqual(got.CompanyExclude, want.CompanyExclude) {
		t.Errorf("company_exclude=%v want %v", got.CompanyExclude, want.CompanyExclude)
	}
	if !reflect.DeepEqual(got.NonRemotePhrases, want.NonRemotePhrases) {
		t.Errorf("non_remote_phrases=%v want %v", got.NonRemotePhrases, want.NonRemotePhrases)
	}

	stored, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || stored == nil {
		t.Fatalf("GetSearchSettings: %v %#v", err, stored)
	}
	if got.ID != stored.ID || got.UpdatedAt != stored.UpdatedAt {
		t.Errorf("response must echo stored row, response=%#v stored=%#v", got, stored)
	}
	if !reflect.DeepEqual(got.DescIncludeWords, stored.DescIncludeWords) ||
		!reflect.DeepEqual(got.DescExcludeWords, stored.DescExcludeWords) ||
		!reflect.DeepEqual(got.TitleInclude, stored.TitleInclude) ||
		!reflect.DeepEqual(got.TitleExclude, stored.TitleExclude) ||
		!reflect.DeepEqual(got.CompanyExclude, stored.CompanyExclude) ||
		!reflect.DeepEqual(got.NonRemotePhrases, stored.NonRemotePhrases) {
		t.Errorf("response row != stored row response=%#v stored=%#v", got, stored)
	}
}

func TestSearchSettings_PutAllowsEmptyLists(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, err := json.Marshal(filterPayload{
		DescIncludeWords: []string{},
		DescExcludeWords: []string{},
		TitleInclude:     []string{},
		TitleExclude:     []string{},
		CompanyExclude:   []string{},
		NonRemotePhrases: []string{},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v0/search-settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/v0/search-settings empty status=%d body=%s", rec.Code, rec.Body.String())
	}
	stored, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || stored == nil {
		t.Fatalf("GetSearchSettings: %v %#v", err, stored)
	}
	for name, values := range map[string][]string{
		"desc_include_words": stored.DescIncludeWords,
		"desc_exclude_words": stored.DescExcludeWords,
		"title_include":      stored.TitleInclude,
		"title_exclude":      stored.TitleExclude,
		"company_exclude":    stored.CompanyExclude,
		"non_remote_phrases": stored.NonRemotePhrases,
	} {
		if len(values) != 0 {
			t.Errorf("%s len=%d want 0", name, len(values))
		}
	}
}

func TestSearchSettings_PutInvalidBodyReturnsBadRequest(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bodies := []string{
		`{"desc_include_words":"bad"}`,
		`{"desc_include_words":[],"desc_exclude_words":[],"title_include":[]}`,
		`{"desc_include_words":[],"desc_exclude_words":[],"title_include":[],"title_exclude":[],"company_exclude":[],"non_remote_phrases":[]`,
	}
	for _, body := range bodies {
		req := httptest.NewRequest(http.MethodPut, "/api/v0/search-settings", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid body status=%d body=%s", rec.Code, rec.Body.String())
		}
		var errBody map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if errBody["detail"] == "" {
			t.Fatalf("invalid body must return detail; body=%s", rec.Body.String())
		}
	}
}

func TestSearchSettings_ResetRestoresSeedAndDoesNotTouchProviders(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seed, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || seed == nil {
		t.Fatalf("seed settings: %v %#v", err, seed)
	}
	if _, err := pool.Exec(`
		UPDATE search_settings
		SET desc_include_words = '["changed"]', title_include = '["changed"]'
	`); err != nil {
		t.Fatalf("update search_settings: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET pages_to_scrape = 99, scrape_interval_seconds = 777
		WHERE job_source = 'LINKEDIN'
	`); err != nil {
		t.Fatalf("update scraper_settings: %v", err)
	}
	if _, err := pool.Exec(`
		UPDATE scraper_settings
		SET pages_to_scrape = 12, scrape_interval_seconds = 333
		WHERE job_source = 'INDEED'
	`); err != nil {
		t.Fatalf("update scraper_settings INDEED: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v0/search-settings/reset", nil)
	rec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/v0/search-settings/reset status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.SearchSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for name, values := range map[string][]string{
		"desc_include_words": got.DescIncludeWords,
		"desc_exclude_words": got.DescExcludeWords,
		"title_include":      got.TitleInclude,
		"title_exclude":      got.TitleExclude,
		"company_exclude":    got.CompanyExclude,
		"non_remote_phrases": got.NonRemotePhrases,
	} {
		var want []string
		switch name {
		case "desc_include_words":
			want = seed.DescIncludeWords
		case "desc_exclude_words":
			want = seed.DescExcludeWords
		case "title_include":
			want = seed.TitleInclude
		case "title_exclude":
			want = seed.TitleExclude
		case "company_exclude":
			want = seed.CompanyExclude
		case "non_remote_phrases":
			want = seed.NonRemotePhrases
		}
		if !reflect.DeepEqual(values, want) {
			t.Errorf("%s=%v want %v", name, values, want)
		}
	}
	stored, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || stored == nil {
		t.Fatalf("GetSearchSettings after reset: %v %#v", err, stored)
	}
	if got.ID != stored.ID || got.UpdatedAt != stored.UpdatedAt {
		t.Errorf("reset response must echo stored row, response=%#v stored=%#v", got, stored)
	}
	if !reflect.DeepEqual(got.DescIncludeWords, stored.DescIncludeWords) ||
		!reflect.DeepEqual(got.DescExcludeWords, stored.DescExcludeWords) ||
		!reflect.DeepEqual(got.TitleInclude, stored.TitleInclude) ||
		!reflect.DeepEqual(got.TitleExclude, stored.TitleExclude) ||
		!reflect.DeepEqual(got.CompanyExclude, stored.CompanyExclude) ||
		!reflect.DeepEqual(got.NonRemotePhrases, stored.NonRemotePhrases) {
		t.Errorf("reset response row != stored row response=%#v stored=%#v", got, stored)
	}

	var pages int
	var interval int
	if err := pool.QueryRow(`
		SELECT pages_to_scrape, scrape_interval_seconds
		FROM scraper_settings
		WHERE job_source = 'LINKEDIN'
	`).Scan(&pages, &interval); err != nil {
		t.Fatalf("select provider settings: %v", err)
	}
	if pages != 99 || interval != 777 {
		t.Errorf("reset must not touch provider rows, got pages=%d interval=%d", pages, interval)
	}
	var pagesIndeed int
	var intervalIndeed int
	if err := pool.QueryRow(`
		SELECT pages_to_scrape, scrape_interval_seconds
		FROM scraper_settings
		WHERE job_source = 'INDEED'
	`).Scan(&pagesIndeed, &intervalIndeed); err != nil {
		t.Fatalf("select provider settings indeed: %v", err)
	}
	if pagesIndeed != 12 || intervalIndeed != 333 {
		t.Errorf("reset must not touch provider rows, got INDEED pages=%d interval=%d", pagesIndeed, intervalIndeed)
	}
}
