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
	OnsiteKeywords   []string `json:"onsite_keywords"`
	RemoteKeywords   []string `json:"remote_keywords"`
	HybridKeywords   []string `json:"hybrid_keywords"`
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
		OnsiteKeywords:   []string{" onsite ", "ONSITE", "in office"},
		RemoteKeywords:   []string{" Remote ", "remote", "wfh"},
		HybridKeywords:   []string{" hybrid ", "HYBRID"},
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
		OnsiteKeywords:   []string{"onsite", "in office"},
		RemoteKeywords:   []string{"Remote", "wfh"},
		HybridKeywords:   []string{"hybrid"},
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
	if !reflect.DeepEqual(got.OnsiteKeywords, want.OnsiteKeywords) {
		t.Errorf("onsite_keywords=%v want %v", got.OnsiteKeywords, want.OnsiteKeywords)
	}
	if !reflect.DeepEqual(got.RemoteKeywords, want.RemoteKeywords) {
		t.Errorf("remote_keywords=%v want %v", got.RemoteKeywords, want.RemoteKeywords)
	}
	if !reflect.DeepEqual(got.HybridKeywords, want.HybridKeywords) {
		t.Errorf("hybrid_keywords=%v want %v", got.HybridKeywords, want.HybridKeywords)
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
		!reflect.DeepEqual(got.OnsiteKeywords, stored.OnsiteKeywords) ||
		!reflect.DeepEqual(got.RemoteKeywords, stored.RemoteKeywords) ||
		!reflect.DeepEqual(got.HybridKeywords, stored.HybridKeywords) {
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
		OnsiteKeywords:   []string{},
		RemoteKeywords:   []string{},
		HybridKeywords:   []string{},
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
		"onsite_keywords":    stored.OnsiteKeywords,
		"remote_keywords":    stored.RemoteKeywords,
		"hybrid_keywords":    stored.HybridKeywords,
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
		t.Fatalf("POST reset status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got db.SearchSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode reset: %v", err)
	}
	for name, values := range map[string][2][]string{
		"desc_include_words": {got.DescIncludeWords, seed.DescIncludeWords},
		"desc_exclude_words": {got.DescExcludeWords, seed.DescExcludeWords},
		"title_include":      {got.TitleInclude, seed.TitleInclude},
		"title_exclude":      {got.TitleExclude, seed.TitleExclude},
		"company_exclude":    {got.CompanyExclude, seed.CompanyExclude},
		"onsite_keywords":    {got.OnsiteKeywords, seed.OnsiteKeywords},
		"remote_keywords":    {got.RemoteKeywords, seed.RemoteKeywords},
		"hybrid_keywords":    {got.HybridKeywords, seed.HybridKeywords},
	} {
		if !reflect.DeepEqual(values[0], values[1]) {
			t.Errorf("%s=%v want seed %v", name, values[0], values[1])
		}
	}

	var linkedInPages, linkedInInterval, indeedPages, indeedInterval int
	if err := pool.QueryRow(`
		SELECT pages_to_scrape, scrape_interval_seconds FROM scraper_settings WHERE job_source = 'LINKEDIN'
	`).Scan(&linkedInPages, &linkedInInterval); err != nil {
		t.Fatalf("linkedin scraper: %v", err)
	}
	if linkedInPages != 99 || linkedInInterval != 777 {
		t.Errorf("reset must not change LinkedIn scraper settings pages=%d interval=%d", linkedInPages, linkedInInterval)
	}
	if err := pool.QueryRow(`
		SELECT pages_to_scrape, scrape_interval_seconds FROM scraper_settings WHERE job_source = 'INDEED'
	`).Scan(&indeedPages, &indeedInterval); err != nil {
		t.Fatalf("indeed scraper: %v", err)
	}
	if indeedPages != 12 || indeedInterval != 333 {
		t.Errorf("reset must not change Indeed scraper settings pages=%d interval=%d", indeedPages, indeedInterval)
	}
}

func TestSearchSettings_GetAfterPutReturnsCurrentRow(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, err := json.Marshal(filterPayload{
		DescIncludeWords: []string{"only"},
		DescExcludeWords: []string{},
		TitleInclude:     []string{"Help Desk"},
		TitleExclude:     []string{},
		CompanyExclude:   []string{},
		OnsiteKeywords:   []string{"onsite"},
		RemoteKeywords:   []string{"remote"},
		HybridKeywords:   []string{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	put := httptest.NewRequest(http.MethodPut, "/api/v0/search-settings", bytes.NewReader(body))
	put.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/v0/search-settings", nil)
	getRec := httptest.NewRecorder()
	NewServer("", &Handler{DB: pool}).Handler.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var got db.SearchSettings
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !reflect.DeepEqual(got.DescIncludeWords, []string{"only"}) {
		t.Errorf("desc_include_words=%v", got.DescIncludeWords)
	}
	if !reflect.DeepEqual(got.OnsiteKeywords, []string{"onsite"}) {
		t.Errorf("onsite_keywords=%v", got.OnsiteKeywords)
	}
	if !reflect.DeepEqual(got.RemoteKeywords, []string{"remote"}) {
		t.Errorf("remote_keywords=%v", got.RemoteKeywords)
	}
}
