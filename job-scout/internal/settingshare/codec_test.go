package settingshare

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleBundle() Bundle {
	return Bundle{
		Format:        Format,
		Version:       Version,
		Notifications: Notifications{Enabled: true},
		Search: Search{
			DescIncludeWords: []string{"computer"},
			DescExcludeWords: []string{"travel"},
			TitleInclude:     []string{"IT"},
			TitleExclude:     []string{"manager"},
			CompanyExclude:   []string{"Bad Co"},
		},
		Providers: map[string]Provider{
			"LINKEDIN": {
				Enabled:               true,
				ScrapeIntervalSeconds: 900,
				TimespanCode:          "r86400",
				PagesToScrape:         1,
				Rounds:                1,
				SearchQueries: []map[string]any{
					{"keywords": "Support", "location": "101076143", "f_WT": "1,2"},
				},
				GlobalSearches: []string{"Remote IT Help Desk"},
			},
		},
	}
}

func TestEncodeShare_RoundTrip(t *testing.T) {
	bundle := sampleBundle()
	code, err := EncodeShare(bundle)
	if err != nil {
		t.Fatalf("EncodeShare: %v", err)
	}
	if !strings.HasPrefix(code, Prefix) {
		t.Fatalf("share code prefix = %q", code[:min(12, len(code))])
	}
	if strings.ContainsAny(code, " \n+/=\"") {
		t.Fatalf("share code has whitespace or padding: %q", code)
	}

	got, err := Parse(code)
	if err != nil {
		t.Fatalf("Parse share: %v", err)
	}
	if got.Search.TitleInclude[0] != "IT" || !got.Notifications.Enabled {
		t.Fatalf("round-trip bundle = %+v", got)
	}
	query := got.Providers["LINKEDIN"].SearchQueries[0]
	if query["keywords"] != "Support" || query["f_WT"] != "1,2" {
		t.Fatalf("round-trip query = %#v", query)
	}
}

func TestParse_JSONAndQuotedShareCode(t *testing.T) {
	bundle := sampleBundle()
	raw, err := MarshalCanonical(bundle)
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	fromJSON, err := Parse(string(raw))
	if err != nil {
		t.Fatalf("Parse JSON: %v", err)
	}
	if fromJSON.Format != Format {
		t.Fatalf("JSON format = %q", fromJSON.Format)
	}

	code, err := EncodeShare(bundle)
	if err != nil {
		t.Fatalf("EncodeShare: %v", err)
	}
	quoted, err := Parse(`"` + code + `"`)
	if err != nil {
		t.Fatalf("Parse quoted share: %v", err)
	}
	if quoted.Search.CompanyExclude[0] != "Bad Co" {
		t.Fatalf("quoted parse = %+v", quoted)
	}
}

func TestParse_RejectsSecretsChecksumAndUnknownFields(t *testing.T) {
	if _, err := Parse(`{"format":"jobscout-settings","version":1,"notifications":{"enabled":true},"search":{"desc_include_words":[],"desc_exclude_words":[],"title_include":[],"title_exclude":[],"company_exclude":[]},"providers":{},"api_key":"secret"}`); err == nil {
		t.Fatal("expected unknown field error")
	}
	if _, err := Parse(`{"format":"jobscout-settings","version":1,"notifications":{"enabled":true},"search":{"desc_include_words":[],"desc_exclude_words":[],"title_include":[],"title_exclude":[],"company_exclude":[]},"providers":{"LINKEDIN":{"enabled":true,"scrape_interval_seconds":900,"timespan_code":"r86400","pages_to_scrape":1,"rounds":1,"search_queries":[{"keywords":"IT","location":"1","token":"abc"}],"global_searches":[]}}}`); err == nil {
		t.Fatal("expected secret key error")
	}

	code, err := EncodeShare(sampleBundle())
	if err != nil {
		t.Fatalf("EncodeShare: %v", err)
	}
	payload := []byte(strings.TrimPrefix(code, Prefix))
	payload[4] ^= 0x01
	if _, err := Parse(Prefix + string(payload)); err == nil {
		t.Fatal("expected checksum error")
	}

	raw, _ := json.Marshal(map[string]any{
		"format":        Format,
		"version":       2,
		"notifications": map[string]any{"enabled": true},
		"search": map[string]any{
			"desc_include_words": []string{},
			"desc_exclude_words": []string{},
			"title_include":      []string{},
			"title_exclude":      []string{},
			"company_exclude":    []string{},
		},
		"providers": map[string]any{},
	})
	if _, err := ParseBytes(raw); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version error = %v", err)
	}
}
