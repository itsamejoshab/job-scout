package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/settingshare"
)

func TestSettingsExportImport_ShareCodeRoundTripAndStripsPrivateFields(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := NewServer("", &Handler{DB: pool})
	exportReq := httptest.NewRequest(http.MethodGet, "/api/v0/settings/export", nil)
	exportRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("GET export status=%d body=%s", exportRec.Code, exportRec.Body.String())
	}

	var exported settingsExportView
	if err := json.Unmarshal(exportRec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if !strings.HasPrefix(exported.ShareCode, settingshare.Prefix) {
		t.Fatalf("share_code = %q", exported.ShareCode)
	}
	settingsRaw, err := json.Marshal(exported.Settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	raw := string(settingsRaw)
	for _, leaked := range []string{
		"last_scraped_at", "next_eligible_at", "apify_budget", "created_at",
		"updated_at", "api_key", "token", "webhook", "APIFY",
	} {
		if strings.Contains(raw, leaked) {
			t.Errorf("export leaked %q", leaked)
		}
	}
	dice := exported.Settings.Providers["DICE"]
	if len(dice.SearchQueries) == 0 {
		t.Fatal("missing Dice queries")
	}
	if _, isBool := dice.SearchQueries[0]["include_remote"].(bool); !isBool {
		t.Fatalf("Dice include_remote type = %T value=%v", dice.SearchQueries[0]["include_remote"], dice.SearchQueries[0]["include_remote"])
	}
	if _, ok := dice.SearchQueries[0]["f_WT"]; ok {
		t.Fatal("Dice export included f_WT")
	}
	if dice.ProviderOptions != nil {
		t.Fatalf("Dice export included provider_options: %#v", dice.ProviderOptions)
	}

	if _, err := pool.Exec(`UPDATE search_settings SET title_include = '["ZZZ-live"]'`); err != nil {
		t.Fatalf("mutate search: %v", err)
	}
	if _, err := pool.Exec(`UPDATE search_settings SET notifications_enabled = false`); err != nil {
		t.Fatalf("mutate notify: %v", err)
	}

	importBody, err := json.Marshal(map[string]any{"payload": exported.ShareCode})
	if err != nil {
		t.Fatalf("marshal import: %v", err)
	}
	importReq := httptest.NewRequest(http.MethodPost, "/api/v0/settings/import", bytes.NewReader(importBody))
	importReq.Header.Set("Content-Type", "application/json")
	importRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("POST import status=%d body=%s", importRec.Code, importRec.Body.String())
	}

	stored, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || stored == nil {
		t.Fatalf("get search: %v %#v", err, stored)
	}
	if len(stored.TitleInclude) == 0 || stored.TitleInclude[0] == "ZZZ-live" {
		t.Fatalf("imported title_include = %v", stored.TitleInclude)
	}
	if !stored.NotificationsEnabled {
		t.Fatal("imported notifications_enabled = false")
	}
}

func TestSettingsImport_JSONPayloadAndRejectsSecrets(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSettings(t.Context(), pool); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := NewServer("", &Handler{DB: pool})

	exportReq := httptest.NewRequest(http.MethodGet, "/api/v0/settings/export", nil)
	exportRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(exportRec, exportReq)
	var exported settingsExportView
	if err := json.Unmarshal(exportRec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	exported.Settings.Search.TitleInclude = []string{"Imported Title"}
	exported.Settings.Notifications.Enabled = false

	jsonBody, err := json.Marshal(map[string]any{"payload": exported.Settings})
	if err != nil {
		t.Fatalf("marshal json import: %v", err)
	}
	jsonReq := httptest.NewRequest(http.MethodPost, "/api/v0/settings/import", bytes.NewReader(jsonBody))
	jsonRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(jsonRec, jsonReq)
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("JSON import status=%d body=%s", jsonRec.Code, jsonRec.Body.String())
	}
	stored, err := db.GetSearchSettings(t.Context(), pool)
	if err != nil || stored == nil {
		t.Fatalf("get search: %v", err)
	}
	if len(stored.TitleInclude) != 1 || stored.TitleInclude[0] != "Imported Title" {
		t.Fatalf("title_include = %v", stored.TitleInclude)
	}
	if stored.NotificationsEnabled {
		t.Fatal("notifications stayed enabled")
	}

	secret := []byte(`{"format":"jobscout-settings","version":1,"notifications":{"enabled":true},"search":{"desc_include_words":[],"desc_exclude_words":[],"title_include":[],"title_exclude":[],"company_exclude":[]},"providers":{"LINKEDIN":{"enabled":true,"scrape_interval_seconds":900,"timespan_code":"r86400","pages_to_scrape":1,"rounds":1,"search_queries":[{"keywords":"IT","location":"1","token":"abc"}],"global_searches":[]}}}`)
	secretReq := httptest.NewRequest(http.MethodPost, "/api/v0/settings/import", bytes.NewReader(secret))
	secretRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(secretRec, secretReq)
	if secretRec.Code != http.StatusBadRequest {
		t.Fatalf("secret import status=%d body=%s", secretRec.Code, secretRec.Body.String())
	}
	if !strings.Contains(secretRec.Body.String(), "token") {
		t.Fatalf("secret import body = %s", secretRec.Body.String())
	}

	badCode, err := json.Marshal(map[string]any{"payload": "!JS:1!not-a-valid-share-code"})
	if err != nil {
		t.Fatalf("marshal bad code: %v", err)
	}
	badReq := httptest.NewRequest(http.MethodPost, "/api/v0/settings/import", bytes.NewReader(badCode))
	badRec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad share code status=%d body=%s", badRec.Code, badRec.Body.String())
	}
}
