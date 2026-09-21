// Package settingshare encodes operator settings for copy/paste sharing.
// The share code is a compact gzip+CRC payload
package settingshare

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"regexp"
	"strings"
)

const (
	Format  = "jobscout-settings"
	Version = 1
	Prefix  = "!JS:1!"
)

var secretKey = regexp.MustCompile(`(?i)^(api[_-]?key|access[_-]?token|auth(orization)?|password|secret|token|webhook(_id|_base|_url)?|apify(_api)?_token)$`)

// Bundle is the shareable operator settings snapshot. It omits IDs, timestamps,
// scrape cadence state, budgets, and credentials.
type Bundle struct {
	Format        string              `json:"format"`
	Version       int                 `json:"version"`
	Notifications Notifications       `json:"notifications"`
	Search        Search              `json:"search"`
	Providers     map[string]Provider `json:"providers"`
}

// Notifications is the on-screen delivery toggle only.
type Notifications struct {
	Enabled bool `json:"enabled"`
}

// Search is the universal filter lists shown on the settings page.
type Search struct {
	DescIncludeWords []string `json:"desc_include_words"`
	DescExcludeWords []string `json:"desc_exclude_words"`
	TitleInclude     []string `json:"title_include"`
	TitleExclude     []string `json:"title_exclude"`
	CompanyExclude   []string `json:"company_exclude"`
}

// Provider is one provider's on-screen scrape configuration.
type Provider struct {
	Enabled               bool             `json:"enabled"`
	ScrapeIntervalSeconds int              `json:"scrape_interval_seconds"`
	TimespanCode          string           `json:"timespan_code"`
	PagesToScrape         int              `json:"pages_to_scrape"`
	Rounds                int              `json:"rounds"`
	SearchQueries         []map[string]any `json:"search_queries"`
	GlobalSearches        []string         `json:"global_searches"`
	ProviderOptions       map[string]any   `json:"provider_options,omitempty"`
}

// Parse accepts a share code or JSON object.
func Parse(input string) (Bundle, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return Bundle{}, errors.New("settings payload is empty")
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		if strings.HasPrefix(inner, Prefix) {
			s = inner
		}
	}
	if strings.HasPrefix(s, Prefix) {
		return decodeShare(s)
	}
	return ParseBytes([]byte(s))
}

// ParseBytes decodes a JSON bundle and rejects unknown or private fields.
func ParseBytes(raw []byte) (Bundle, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return Bundle{}, errors.New("settings payload is empty")
	}
	if err := rejectSecretKeys(trimmed); err != nil {
		return Bundle{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var bundle Bundle
	if err := dec.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("invalid settings JSON: %w", err)
	}
	bundle.normalize()
	if err := bundle.validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// EncodeShare returns a share code for the bundle.
func EncodeShare(bundle Bundle) (string, error) {
	raw, err := MarshalCanonical(bundle)
	if err != nil {
		return "", err
	}
	var compressed bytes.Buffer
	gz, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := gz.Write(raw); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	sum := crc32.ChecksumIEEE(raw)
	packed := make([]byte, 4, 4+compressed.Len())
	binary.BigEndian.PutUint32(packed, sum)
	packed = append(packed, compressed.Bytes()...)
	return Prefix + base64.RawURLEncoding.EncodeToString(packed), nil
}

// MarshalCanonical returns stable compact JSON for checksums and downloads.
func MarshalCanonical(bundle Bundle) ([]byte, error) {
	bundle.normalize()
	if err := bundle.validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(bundle); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func decodeShare(code string) (Bundle, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, Prefix))
	if err != nil {
		return Bundle{}, errors.New("share code is not valid base64")
	}
	if len(payload) < 5 {
		return Bundle{}, errors.New("share code is truncated")
	}
	want := binary.BigEndian.Uint32(payload[:4])
	gz, err := gzip.NewReader(bytes.NewReader(payload[4:]))
	if err != nil {
		return Bundle{}, errors.New("share code is not valid gzip")
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		return Bundle{}, errors.New("share code gzip payload is corrupt")
	}
	if crc32.ChecksumIEEE(raw) != want {
		return Bundle{}, errors.New("share code checksum does not match")
	}
	return ParseBytes(raw)
}

func (b *Bundle) normalize() {
	if b.Providers == nil {
		b.Providers = map[string]Provider{}
	}
	for source, provider := range b.Providers {
		if provider.SearchQueries == nil {
			provider.SearchQueries = []map[string]any{}
		}
		if provider.GlobalSearches == nil {
			provider.GlobalSearches = []string{}
		}
		b.Providers[source] = provider
	}
}

func (b Bundle) validate() error {
	if b.Format != Format {
		return fmt.Errorf("settings format must be %s", Format)
	}
	if b.Version != Version {
		return fmt.Errorf("unsupported settings share version %d", b.Version)
	}
	if b.Search.DescIncludeWords == nil || b.Search.DescExcludeWords == nil ||
		b.Search.TitleInclude == nil || b.Search.TitleExclude == nil ||
		b.Search.CompanyExclude == nil {
		return errors.New("search lists must be present")
	}
	if b.Providers == nil {
		return errors.New("providers must be present")
	}
	return nil
}

func rejectSecretKeys(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid settings JSON: %w", err)
	}
	return walkSecrets(value)
}

func walkSecrets(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if secretKey.MatchString(key) {
				return fmt.Errorf("settings payload must not include %s", key)
			}
			if err := walkSecrets(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkSecrets(child); err != nil {
				return err
			}
		}
	}
	return nil
}
