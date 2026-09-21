package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type progressKey struct{}
type contractKey struct{}

// ProgressReporter receives scrape progress for Temporal heartbeats (or tests).
type ProgressReporter func(Progress)

const (
	progressStringLimit   = 240
	progressErrorLimit    = 500
	progressSampleKeyCap  = 24
	maxAPIContracts       = 40
)

// Progress is one scrape checkpoint shown in Temporal activity heartbeat details.
// Keep this small: durable API contracts belong on Result.Metadata.
type Progress struct {
	Phase         string `json:"phase"`
	Source        string `json:"source,omitempty"`
	Round         int    `json:"round,omitempty"`
	Rounds        int    `json:"rounds,omitempty"`
	QueryIndex    int    `json:"query_index,omitempty"`
	QueryCount    int    `json:"query_count,omitempty"`
	Keywords      string `json:"keywords,omitempty"`
	Location      string `json:"location,omitempty"`
	Page          int    `json:"page,omitempty"`
	Pages         int    `json:"pages,omitempty"`
	JobsCollected int    `json:"jobs_collected,omitempty"`
	ApifyRunID    string `json:"apify_run_id,omitempty"`
	ApifyStatus   string `json:"apify_status,omitempty"`
	Message       string `json:"message,omitempty"`
}

// ResultMetadata is durable debug data on the scrape activity result.
type ResultMetadata struct {
	APICalls []APIContract `json:"api_calls,omitempty"`
}

// APIContract is a compact, redacted view of one Apify HTTP exchange or dataset map.
// Token headers are never included.
type APIContract struct {
	Phase        string         `json:"phase,omitempty"`
	Method       string         `json:"method,omitempty"`
	Path         string         `json:"path,omitempty"`
	StatusCode   int            `json:"status_code,omitempty"`
	ActorID      string         `json:"actor_id,omitempty"`
	ChargeUSD    string         `json:"charge_usd,omitempty"`
	Request      map[string]any `json:"request,omitempty"`
	Response     map[string]any `json:"response,omitempty"`
	ErrorBody    string         `json:"error_body,omitempty"`
	DatasetID    string         `json:"dataset_id,omitempty"`
	ItemCount    int            `json:"item_count,omitempty"`
	MappedCount  int            `json:"mapped_count,omitempty"`
	SkippedCount int            `json:"skipped_count,omitempty"`
	SampleKeys   []string       `json:"sample_keys,omitempty"`
	SampleItem   map[string]any `json:"sample_item,omitempty"`
	Message      string         `json:"message,omitempty"`
}

type contractRecorder struct {
	mu    sync.Mutex
	calls []APIContract
}

// WithProgressReporter attaches an optional progress sink to ctx.
func WithProgressReporter(ctx context.Context, report ProgressReporter) context.Context {
	if report == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, report)
}

// WithContractRecorder attaches a sink that accumulates API contracts for Result.Metadata.
func WithContractRecorder(ctx context.Context) context.Context {
	if recorderFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, contractKey{}, &contractRecorder{})
}

// ReportProgress sends one checkpoint when a reporter is attached.
func ReportProgress(ctx context.Context, progress Progress) {
	report, _ := ctx.Value(progressKey{}).(ProgressReporter)
	if report == nil {
		return
	}
	report(progress)
}

// RecordAPIContract appends one durable contract when a recorder is attached.
func RecordAPIContract(ctx context.Context, contract APIContract) {
	rec := recorderFrom(ctx)
	if rec == nil {
		return
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) >= maxAPIContracts {
		return
	}
	rec.calls = append(rec.calls, contract)
}

func attachResultMetadata(ctx context.Context, res Result) Result {
	rec := recorderFrom(ctx)
	if rec == nil {
		return res
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) == 0 {
		return res
	}
	calls := make([]APIContract, len(rec.calls))
	copy(calls, rec.calls)
	res.Metadata = &ResultMetadata{APICalls: calls}
	return res
}

func recorderFrom(ctx context.Context) *contractRecorder {
	rec, _ := ctx.Value(contractKey{}).(*contractRecorder)
	return rec
}

func truncateProgressString(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

func anyToProgressMap(in any) map[string]any {
	if in == nil {
		return nil
	}
	if m, ok := in.(map[string]any); ok {
		return truncateProgressMap(m)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return map[string]any{"_error": err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"_raw": truncateProgressString(string(raw), progressStringLimit)}
	}
	return truncateProgressMap(out)
}

func truncateProgressMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = truncateProgressValue(key, value)
	}
	return out
}

func truncateProgressValue(key string, value any) any {
	lower := strings.ToLower(key)
	switch {
	case strings.Contains(lower, "token"),
		strings.Contains(lower, "authorization"),
		strings.Contains(lower, "password"),
		strings.Contains(lower, "secret"):
		return "[redacted]"
	}
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return truncateProgressString(v, progressStringLimit)
	case []byte:
		return truncateProgressString(string(v), progressStringLimit)
	case map[string]any:
		return truncateProgressMap(v)
	case []any:
		limit := len(v)
		if limit > 5 {
			limit = 5
		}
		out := make([]any, 0, limit+1)
		for i := 0; i < limit; i++ {
			out = append(out, truncateProgressValue(fmt.Sprintf("%s[%d]", key, i), v[i]))
		}
		if len(v) > limit {
			out = append(out, fmt.Sprintf("…+%d more", len(v)-limit))
		}
		return out
	default:
		return v
	}
}

func progressSampleKeys(item map[string]any) []string {
	if len(item) == 0 {
		return nil
	}
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > progressSampleKeyCap {
		keys = keys[:progressSampleKeyCap]
	}
	return keys
}

func datasetMapContract(datasetID string, items []map[string]any, mapped, skipped int, message string) APIContract {
	contract := APIContract{
		Phase:        "apify_dataset",
		DatasetID:    datasetID,
		ItemCount:    len(items),
		MappedCount:  mapped,
		SkippedCount: skipped,
		Message:      message,
	}
	if len(items) > 0 {
		contract.SampleKeys = progressSampleKeys(items[0])
		contract.SampleItem = truncateProgressMap(items[0])
	}
	return contract
}
