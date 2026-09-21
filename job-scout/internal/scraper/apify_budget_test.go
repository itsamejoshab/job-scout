package scraper

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
)

func TestApifyBudgetPeriod_ResetsAt21stUTC(t *testing.T) {
	tests := []struct {
		name      string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "before 21st uses previous month",
			now:       time.Date(2026, 9, 20, 23, 59, 59, 999000000, time.UTC),
			wantStart: time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "exactly 21st 00:00 UTC starts new period",
			now:       time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "after 21st stays in current month",
			now:       time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "january before 21st crosses year",
			now:       time.Date(2027, 1, 5, 8, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2027, 1, 21, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "december 21st crosses year at end",
			now:       time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC),
			wantStart: time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2027, 1, 21, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end := ApifyBudgetPeriod(tc.now)
			if !start.Equal(tc.wantStart) || start.Location() != time.UTC {
				t.Errorf("start = %s, want %s UTC", start, tc.wantStart)
			}
			if !end.Equal(tc.wantEnd) || end.Location() != time.UTC {
				t.Errorf("end = %s, want %s UTC", end, tc.wantEnd)
			}
		})
	}
}

func TestParseUSDToCents_HalfUpAndInvalid(t *testing.T) {
	tests := []struct {
		raw    string
		want   int
		wantOK bool
	}{
		{raw: "1.00", want: 100, wantOK: true},
		{raw: "1", want: 100, wantOK: true},
		{raw: "0.015", want: 2, wantOK: true},
		{raw: "1.005", want: 101, wantOK: true},
		{raw: "1.004", want: 100, wantOK: true},
		{raw: "0", want: 0, wantOK: true},
		{raw: "-0.01", want: -1, wantOK: true},
		{raw: "", wantOK: false},
		{raw: "abc", wantOK: false},
		{raw: "1.00.1", wantOK: false},
	}
	for _, tc := range tests {
		got, ok := config.ParseUSDToCents(tc.raw)
		if ok != tc.wantOK {
			t.Errorf("ParseUSDToCents(%q) ok=%v, want %v", tc.raw, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("ParseUSDToCents(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestApifyClient_AccountBudget_SumsListedUsageInCents(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	var listCalls int
	var gotAuth []string
	var startedAfter []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		if r.Method != http.MethodGet || r.URL.Path != "/v2/actor-runs" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if tok := r.URL.Query().Get("token"); tok != "" {
			t.Errorf("token query must be empty, got %q", tok)
		}
		startedAfter = append(startedAfter, r.URL.Query().Get("startedAfter"))
		listCalls++
		offset := r.URL.Query().Get("offset")
		items := []map[string]any{}
		total := 2
		if offset == "" || offset == "0" {
			items = []map[string]any{{
				"id": "ext-1", "status": "SUCCEEDED", "usageTotalUsd": 0.40,
				"startedAt": "2026-09-21T01:00:00.000Z",
			}}
		} else {
			items = []map[string]any{{
				"id": "ext-2", "status": "SUCCEEDED", "usageTotalUsd": 0.15,
				"startedAt": "2026-09-21T02:00:00.000Z",
			}}
		}
		writeJSON(w, map[string]any{
			"data": map[string]any{"total": total, "count": len(items), "offset": json.Number(offsetOrZero(offset)), "limit": 1000, "items": items},
		})
	}))
	defer srv.Close()

	client := newTestApifyClient(srv.URL, "test-token", clock)
	snap, err := client.AccountBudget(t.Context(), 100)
	if err != nil {
		t.Fatalf("AccountBudget: %v", err)
	}
	if listCalls < 2 {
		t.Errorf("list pages = %d, want pagination across 2 items", listCalls)
	}
	for i, auth := range gotAuth {
		if auth != "Bearer test-token" {
			t.Errorf("list[%d] Authorization = %q, want Bearer test-token", i, auth)
		}
	}
	wantStart := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	if snap.PeriodStart != wantStart {
		t.Errorf("period start = %s, want %s", snap.PeriodStart, wantStart)
	}
	if snap.UsedCents != 55 {
		t.Errorf("used cents = %d, want 55 (0.40+0.15 half-up)", snap.UsedCents)
	}
	if snap.RemainingCents != 45 {
		t.Errorf("remaining cents = %d, want 45", snap.RemainingCents)
	}
	if len(startedAfter) == 0 || startedAfter[0] != wantStart.Format(time.RFC3339) {
		t.Errorf("startedAfter = %v, want %s", startedAfter, wantStart.Format(time.RFC3339))
	}
}

func TestApifyClient_AccountBudget_FailClosedNonTerminalAndUnknownUsage(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}

	t.Run("running_run", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"data": map[string]any{"total": 1, "count": 1, "items": []map[string]any{
				{"id": "run-live", "status": "RUNNING", "startedAt": "2026-09-21T03:00:00.000Z"},
			}}})
		}))
		defer srv.Close()
		_, err := newTestApifyClient(srv.URL, "test-token", clock).AccountBudget(t.Context(), 100)
		if !IsApifyFailClosed(err) {
			t.Errorf("err = %v, want fail-closed reservation for non-terminal run", err)
		}
	})

	t.Run("terminal_missing_usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]any{"data": map[string]any{"total": 1, "count": 1, "items": []map[string]any{
				{"id": "run-done", "status": "SUCCEEDED", "startedAt": "2026-09-21T03:00:00.000Z"},
			}}})
		}))
		defer srv.Close()
		_, err := newTestApifyClient(srv.URL, "test-token", clock).AccountBudget(t.Context(), 100)
		if !IsApifyFailClosed(err) {
			t.Errorf("err = %v, want fail-closed unknown usage", err)
		}
	})
}

func offsetOrZero(offset string) string {
	if offset == "" {
		return "0"
	}
	return offset
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
