package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/settingshare"
)

type settingsExportView struct {
	ShareCode string              `json:"share_code"`
	Settings  settingshare.Bundle `json:"settings"`
}

var providerQueryFields = map[db.JobSource]map[string]struct{}{
	db.SourceLinkedIn: {"keywords": {}, "location": {}, "f_WT": {}},
	db.SourceDice:     {"keywords": {}, "location": {}, "include_remote": {}},
	db.SourceIndeed: {
		"keywords": {}, "location": {}, "include_remote": {}, "include_hybrid": {}, "radius": {},
	},
}

var indeedOptionFields = map[string]struct{}{
	"country": {}, "jobType": {}, "fromDays": {}, "maxRows": {},
	"enableUniqueJobs": {}, "includeSimilarJobs": {},
}

// GET /api/v0/settings/export
func (h *Handler) ExportSettings(w http.ResponseWriter, r *http.Request) {
	bundle, err := h.shareableSettings(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	code, err := settingshare.EncodeShare(bundle)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settingsExportView{ShareCode: code, Settings: bundle})
}

// POST /api/v0/settings/import
func (h *Handler) ImportSettings(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "unable to read settings payload")
		return
	}
	bundle, err := parseSettingsImport(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prepared, err := h.prepareSharedSettings(bundle)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.applySharedSettings(r, prepared); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	stored, err := h.shareableSettings(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	code, err := settingshare.EncodeShare(stored)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settingsExportView{ShareCode: code, Settings: stored})
}

func parseSettingsImport(raw []byte) (settingshare.Bundle, error) {
	trimmed := bytes.TrimSpace(raw)
	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(trimmed, &envelope); err == nil && len(bytes.TrimSpace(envelope.Payload)) > 0 {
		payload := bytes.TrimSpace(envelope.Payload)
		if payload[0] == '"' {
			var text string
			if err := json.Unmarshal(payload, &text); err != nil {
				return settingshare.Bundle{}, err
			}
			return settingshare.Parse(text)
		}
		return settingshare.ParseBytes(payload)
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return settingshare.Bundle{}, err
		}
		return settingshare.Parse(text)
	}
	return settingshare.ParseBytes(trimmed)
}

func (h *Handler) shareableSettings(r *http.Request) (settingshare.Bundle, error) {
	search, err := db.GetSearchSettings(r.Context(), h.DB)
	if err != nil {
		return settingshare.Bundle{}, err
	}
	if search == nil {
		return settingshare.Bundle{}, fmt.Errorf("search settings not found")
	}
	enabled, err := db.GetNotificationsEnabled(r.Context(), h.DB)
	if err != nil {
		return settingshare.Bundle{}, err
	}
	all, err := db.AllScraperSettings(r.Context(), h.DB)
	if err != nil {
		return settingshare.Bundle{}, err
	}
	providers := make(map[string]settingshare.Provider, len(all))
	for _, row := range all {
		providers[string(row.JobSource)] = shareableProvider(row)
	}
	return settingshare.Bundle{
		Format:        settingshare.Format,
		Version:       settingshare.Version,
		Notifications: settingshare.Notifications{Enabled: enabled},
		Search: settingshare.Search{
			DescIncludeWords: nonNilList(search.DescIncludeWords),
			DescExcludeWords: nonNilList(search.DescExcludeWords),
			TitleInclude:     nonNilList(search.TitleInclude),
			TitleExclude:     nonNilList(search.TitleExclude),
			CompanyExclude:   nonNilList(search.CompanyExclude),
		},
		Providers: providers,
	}, nil
}

func shareableProvider(row db.ScraperSettings) settingshare.Provider {
	queries := make([]map[string]any, 0, len(row.SearchQueries))
	for _, query := range row.SearchQueries {
		item := map[string]any{
			"keywords": query["keywords"],
			"location": query["location"],
		}
		switch row.JobSource {
		case db.SourceLinkedIn:
			item["f_WT"] = query["f_WT"]
		case db.SourceDice:
			item["include_remote"] = storedFlag(query["include_remote"])
		case db.SourceIndeed:
			item["include_remote"] = storedFlag(query["include_remote"])
			item["include_hybrid"] = storedFlag(query["include_hybrid"])
			item["radius"] = query["radius"]
		}
		queries = append(queries, item)
	}
	global := nonNilList(row.GlobalSearches)
	out := settingshare.Provider{
		Enabled:               row.Enabled,
		ScrapeIntervalSeconds: row.ScrapeIntervalSeconds,
		TimespanCode:          row.TimespanCode,
		PagesToScrape:         row.PagesToScrape,
		Rounds:                row.Rounds,
		SearchQueries:         queries,
		GlobalSearches:        global,
	}
	if row.JobSource == db.SourceIndeed {
		opts, err := db.ParseIndeedOptions(row.ProviderOptions)
		if err != nil {
			opts = db.DefaultIndeedOptions()
		}
		out.ProviderOptions = db.IndeedOptionsMap(opts)
	}
	return out
}

func storedFlag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

type preparedShare struct {
	search    db.SearchSettings
	notify    bool
	providers map[db.JobSource]replaceScraperSettingsRequest
}

func (h *Handler) prepareSharedSettings(bundle settingshare.Bundle) (preparedShare, error) {
	out := preparedShare{
		search: db.SearchSettings{
			DescIncludeWords: cloneList(bundle.Search.DescIncludeWords),
			DescExcludeWords: cloneList(bundle.Search.DescExcludeWords),
			TitleInclude:     cloneList(bundle.Search.TitleInclude),
			TitleExclude:     cloneList(bundle.Search.TitleExclude),
			CompanyExclude:   cloneList(bundle.Search.CompanyExclude),
		},
		notify:    bundle.Notifications.Enabled,
		providers: map[db.JobSource]replaceScraperSettingsRequest{},
	}
	for rawSource, provider := range bundle.Providers {
		source, err := db.ParseJobSource(rawSource)
		if err != nil {
			return preparedShare{}, err
		}
		if err := rejectUnknownShareFields(source, provider); err != nil {
			return preparedShare{}, err
		}
		req, err := providerRequest(provider)
		if err != nil {
			return preparedShare{}, fmt.Errorf("%s: %w", source, err)
		}
		if err := req.validate(source); err != nil {
			return preparedShare{}, fmt.Errorf("%s: %w", source, err)
		}
		if *req.Enabled && !isProviderImplemented(source) {
			return preparedShare{}, fmt.Errorf("%s: provider scraper is not implemented", source)
		}
		out.providers[source] = req
	}
	return out, nil
}

func (h *Handler) applySharedSettings(r *http.Request, in preparedShare) error {
	if _, err := db.ReplaceSearchSettings(r.Context(), h.DB, in.search); err != nil {
		return err
	}
	if _, err := db.SetNotificationsEnabled(r.Context(), h.DB, in.notify); err != nil {
		return err
	}
	for source, req := range in.providers {
		existing, err := db.GetScraperSettings(r.Context(), h.DB, source)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%s scraper settings not found", source)
		}
		if _, err := db.ReplaceScraperSettings(r.Context(), h.DB, source, req.settings(source)); err != nil {
			return err
		}
	}
	return nil
}

func providerRequest(provider settingshare.Provider) (replaceScraperSettingsRequest, error) {
	raw, err := json.Marshal(provider)
	if err != nil {
		return replaceScraperSettingsRequest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req replaceScraperSettingsRequest
	if err := dec.Decode(&req); err != nil {
		return replaceScraperSettingsRequest{}, err
	}
	return req, nil
}

func nonNilList(in []string) []string {
	return append([]string{}, in...)
}

func rejectUnknownShareFields(source db.JobSource, provider settingshare.Provider) error {
	allowed, ok := providerQueryFields[source]
	if !ok {
		return fmt.Errorf("invalid job source: %s", source)
	}
	for i, query := range provider.SearchQueries {
		for key := range query {
			if _, known := allowed[key]; !known {
				return fmt.Errorf("%s search_queries[%d] has unknown field %s", source, i, key)
			}
		}
	}
	if source != db.SourceIndeed {
		if len(provider.ProviderOptions) > 0 {
			return fmt.Errorf("%s must not include provider_options", source)
		}
		return nil
	}
	for key := range provider.ProviderOptions {
		if _, known := indeedOptionFields[key]; !known {
			return fmt.Errorf("%s provider_options has unknown field %s", source, key)
		}
	}
	return nil
}
