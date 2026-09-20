package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jobscout/jobscout/internal/config"
)

const (
	ApifyLockKey            = "APIFY"
	ApifyDefaultBaseURL     = "https://api.apify.com"
	ApifyDiceActorID        = "shahidirfan~Dice-Job-Scraper"
	ApifyPollInterval       = 2 * time.Second
	ApifyPollTimeout        = 12 * time.Minute
	ApifyUsageWait          = 15 * time.Second
	ApifyDatasetPageSize    = 1000
	ApifyAbortTimeout       = 5 * time.Second
	ApifyGETAttempts        = 4
	ApifyGETBackoff         = time.Second
	DiceMinRemainingStart   = 13 * time.Minute
	ApifyBudgetViewCacheTTL = 60 * time.Second
	ApifyBudgetViewTimeout  = 2 * time.Second
	ApifySetupMessage       = "Create an Apify account and set APIFY_API_TOKEN in job-scout/.env to enable this provider."
)

const (
	BudgetReasonOK           = "ok"
	BudgetReasonTokenMissing = "token_missing"
	BudgetReasonExhausted    = "budget_exhausted"
	BudgetReasonUsageUnknown = "usage_unknown"
	BudgetReasonUnavailable  = "apify_unavailable"
)

var (
	ErrApifyTokenMissing  = errors.New("apify token is missing")
	ErrApifyBudgetBlocked = errors.New("apify monthly budget is exhausted")
	ErrApifyFailClosed    = errors.New("apify usage is unknown")
	ErrApifyLockBusy      = errors.New("apify advisory lock busy")
)

type budgetBlockedError struct {
	periodEnd time.Time
}

func (e budgetBlockedError) Error() string { return ErrApifyBudgetBlocked.Error() }
func (e budgetBlockedError) Unwrap() error { return ErrApifyBudgetBlocked }

type failClosedError struct {
	msg string
}

func (e failClosedError) Error() string {
	if e.msg == "" {
		return ErrApifyFailClosed.Error()
	}
	return e.msg
}
func (e failClosedError) Unwrap() error { return ErrApifyFailClosed }

// BudgetSnapshot is the account spend gate for one Apify period.
type BudgetSnapshot struct {
	PeriodStart    time.Time
	PeriodEnd      time.Time
	LimitCents     int
	UsedCents      int
	RemainingCents int
}

type usdCents int

func (c usdCents) MarshalJSON() ([]byte, error) {
	return []byte(FormatUSDFromCents(int(c))), nil
}

// ApifyBudgetView is the display-only operator budget payload.
type ApifyBudgetView struct {
	PeriodStart  time.Time `json:"period_start"`
	PeriodEnd    time.Time `json:"period_end"`
	LimitUSD     usdCents  `json:"limit_usd"`
	UsedUSD      *usdCents `json:"used_usd"`
	RemainingUSD *usdCents `json:"remaining_usd"`
	Blocked      bool      `json:"blocked"`
	Reason       string    `json:"reason"`
}

// BudgetViewCache stores one display snapshot for ApifyBudgetViewCacheTTL.
type BudgetViewCache struct {
	mu   sync.Mutex
	at   time.Time
	view ApifyBudgetView
}

func (c *BudgetViewCache) Get(now time.Time, load func() ApifyBudgetView) ApifyBudgetView {
	if c == nil {
		return load()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && now.Sub(c.at) < ApifyBudgetViewCacheTTL {
		return c.view
	}
	c.view = load()
	c.at = now
	return c.view
}

type apifyRun struct {
	ID               string
	Status           string
	StartedAt        time.Time
	UsageTotalUsd    *string
	DefaultDatasetID string
}

type diceActorInput struct {
	Keyword       string `json:"keyword"`
	Location      string `json:"location"`
	PostedDate    string `json:"posted_date"`
	IncludeRemote bool   `json:"includeRemote"`
	MaxPages      int    `json:"maxPages"`
}

// ApifyClient is the shared Apify REST client. Clock and sleep are injectable.
type ApifyClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
}

func ApifyBudgetPeriod(now time.Time) (start, end time.Time) {
	now = now.UTC()
	candidate := time.Date(now.Year(), now.Month(), 21, 0, 0, 0, 0, time.UTC)
	if now.Before(candidate) {
		start = candidate.AddDate(0, -1, 0)
		end = candidate
		return start, end
	}
	return candidate, candidate.AddDate(0, 1, 0)
}

func FormatUSDFromCents(cents int) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func IsApifyBudgetBlocked(err error) bool {
	return errors.Is(err, ErrApifyBudgetBlocked)
}

func IsApifyFailClosed(err error) bool {
	return errors.Is(err, ErrApifyFailClosed) || errors.Is(err, ErrApifyLockBusy)
}

func IsApifyTokenMissing(err error) bool {
	return errors.Is(err, ErrApifyTokenMissing)
}

func (c *ApifyClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func (c *ApifyClient) sleep(ctx context.Context, d time.Duration) error {
	if c != nil && c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	return sleep(ctx, d)
}

func (c *ApifyClient) http() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *ApifyClient) base() string {
	if c != nil && strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return ApifyDefaultBaseURL
}

func (c *ApifyClient) AccountBudget(ctx context.Context, limitCents int) (BudgetSnapshot, error) {
	start, end := ApifyBudgetPeriod(c.now())
	snap := BudgetSnapshot{PeriodStart: start, PeriodEnd: end, LimitCents: limitCents, RemainingCents: limitCents}
	if limitCents <= 0 {
		snap.RemainingCents = limitCents
		return snap, nil
	}
	used, err := c.sumPeriodUsage(ctx, start, end)
	if err != nil {
		return BudgetSnapshot{}, err
	}
	snap.UsedCents = used
	snap.RemainingCents = limitCents - used
	return snap, nil
}

// DisplayBudget maps AccountBudget into the operator JSON view.
// It does not drive scrape gating. Empty token skips HTTP.
func DisplayBudget(ctx context.Context, client *ApifyClient, token string, limitCents int) ApifyBudgetView {
	now := time.Now().UTC()
	if client != nil {
		now = client.now()
	}
	start, end := ApifyBudgetPeriod(now)
	view := ApifyBudgetView{
		PeriodStart: start,
		PeriodEnd:   end,
		LimitUSD:    usdCents(limitCents),
	}
	if strings.TrimSpace(token) == "" {
		view.Reason = BudgetReasonTokenMissing
		view.Blocked = true
		return view
	}
	if client == nil {
		view.Reason = BudgetReasonUnavailable
		view.Blocked = false
		return view
	}
	ctx, cancel := context.WithTimeout(ctx, ApifyBudgetViewTimeout)
	defer cancel()
	display := *client
	display.Token = token
	display.HTTP = &http.Client{
		Timeout:   ApifyBudgetViewTimeout,
		Transport: client.http().Transport,
	}
	snap, err := display.AccountBudget(ctx, limitCents)
	if err != nil {
		if IsApifyFailClosed(err) {
			view.Reason = BudgetReasonUsageUnknown
			view.Blocked = true
			return view
		}
		view.Reason = BudgetReasonUnavailable
		view.Blocked = false
		return view
	}
	used := usdCents(snap.UsedCents)
	remain := usdCents(snap.RemainingCents)
	view.PeriodStart = snap.PeriodStart
	view.PeriodEnd = snap.PeriodEnd
	view.UsedUSD = &used
	view.RemainingUSD = &remain
	if snap.RemainingCents <= 0 {
		view.Reason = BudgetReasonExhausted
		view.Blocked = true
		return view
	}
	view.Reason = BudgetReasonOK
	return view
}

func (c *ApifyClient) sumPeriodUsage(ctx context.Context, start, end time.Time) (int, error) {
	used := 0
	offset := 0
	for {
		q := url.Values{}
		q.Set("startedAfter", start.Format(time.RFC3339))
		q.Set("desc", "0")
		q.Set("limit", "1000")
		q.Set("offset", strconv.Itoa(offset))
		var payload struct {
			Data struct {
				Total  int            `json:"total"`
				Offset int            `json:"offset"`
				Count  int            `json:"count"`
				Items  []apifyRunJSON `json:"items"`
			} `json:"data"`
		}
		if err := c.getJSON(ctx, c.base()+"/v2/actor-runs?"+q.Encode(), &payload, nil); err != nil {
			return 0, err
		}
		items := payload.Data.Items
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			run := item.run()
			if !run.StartedAt.IsZero() && !run.StartedAt.Before(end) {
				continue
			}
			switch {
			case isNonTerminal(run.Status):
				return 0, failClosedError{msg: "apify run is still in flight"}
			case isTerminalSuccess(run.Status) || isTerminalFailure(run.Status):
				if run.UsageTotalUsd == nil {
					return 0, failClosedError{msg: "apify terminal run is missing usageTotalUsd"}
				}
				cents, ok := config.ParseUSDToCents(*run.UsageTotalUsd)
				if !ok {
					return 0, failClosedError{msg: "apify terminal run has unparsable usageTotalUsd"}
				}
				used += cents
			default:
				return 0, failClosedError{msg: "apify listed run has unknown status"}
			}
		}
		offset += len(items)
		if payload.Data.Total > 0 && offset >= payload.Data.Total {
			break
		}
		if payload.Data.Count > 0 && offset >= payload.Data.Count && payload.Data.Total == 0 && len(items) < 1000 {
			break
		}
	}
	return used, nil
}

func (c *ApifyClient) startRun(ctx context.Context, actorID string, chargeCents int, input diceActorInput) (apifyRun, error) {
	u := c.base() + "/v2/acts/" + actorID + "/runs?maxTotalChargeUsd=" + url.QueryEscape(FormatUSDFromCents(chargeCents))
	raw, err := json.Marshal(input)
	if err != nil {
		return apifyRun{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return apifyRun{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return apifyRun{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apifyRun{}, fmt.Errorf("apify start status %d", resp.StatusCode)
	}
	var payload struct {
		Data apifyRunJSON `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return apifyRun{}, err
	}
	run := payload.Data.run()
	if run.ID == "" {
		return apifyRun{}, fmt.Errorf("apify start returned no run id")
	}
	return run, nil
}

func (c *ApifyClient) getRun(ctx context.Context, id string) (apifyRun, error) {
	var payload struct {
		Data apifyRunJSON `json:"data"`
	}
	if err := c.getJSON(ctx, c.base()+"/v2/actor-runs/"+id, &payload, nil); err != nil {
		return apifyRun{}, err
	}
	return payload.Data.run(), nil
}

func (c *ApifyClient) pollRun(ctx context.Context, id string) (apifyRun, error) {
	deadline := c.now().Add(ApifyPollTimeout)
	for {
		run, err := c.getRun(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return run, ctx.Err()
			}
			return run, err
		}
		if isTerminalSuccess(run.Status) || isTerminalFailure(run.Status) {
			return run, nil
		}
		if !isNonTerminal(run.Status) {
			return run, fmt.Errorf("apify run %s has unknown status %q", id, run.Status)
		}
		if !c.now().Before(deadline) {
			return run, fmt.Errorf("apify run %s poll timed out", id)
		}
		if err := c.sleep(ctx, ApifyPollInterval); err != nil {
			return run, err
		}
	}
}

func (c *ApifyClient) waitUsage(ctx context.Context, id string) (apifyRun, error) {
	end := c.now().Add(ApifyUsageWait)
	for {
		run, err := c.getRun(ctx, id)
		if err != nil {
			return run, err
		}
		if run.UsageTotalUsd != nil {
			return run, nil
		}
		if !c.now().Before(end) {
			return run, failClosedError{msg: "apify usageTotalUsd missing after wait"}
		}
		if err := c.sleep(ctx, ApifyPollInterval); err != nil {
			return run, err
		}
	}
}

func (c *ApifyClient) datasetItems(ctx context.Context, datasetID string) ([]map[string]any, error) {
	if datasetID == "" {
		return nil, nil
	}
	var items []map[string]any
	offset := 0
	for {
		u := fmt.Sprintf("%s/v2/datasets/%s/items?offset=%d&limit=%d", c.base(), datasetID, offset, ApifyDatasetPageSize)
		var page []map[string]any
		hdr := http.Header{}
		if err := c.getJSON(ctx, u, &page, &hdr); err != nil {
			return items, err
		}
		items = append(items, page...)
		total := 0
		if raw := hdr.Get("X-Apify-Pagination-Total"); raw != "" {
			total, _ = strconv.Atoi(raw)
		}
		offset += len(page)
		if len(page) == 0 {
			break
		}
		if total > 0 && offset >= total {
			break
		}
		if total == 0 && len(page) < ApifyDatasetPageSize {
			break
		}
	}
	return items, nil
}

func (c *ApifyClient) abort(runID string) {
	if runID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), ApifyAbortTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v2/actor-runs/"+runID+"/abort", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.http().Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (c *ApifyClient) getJSON(ctx context.Context, rawURL string, dest any, headerOut *http.Header) error {
	var last error
	for attempt := 0; attempt < ApifyGETAttempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, ApifyGETBackoff); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		resp, err := c.http().Do(req)
		if err != nil {
			last = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			last = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			last = fmt.Errorf("apify GET %s status %d", rawURL, resp.StatusCode)
			continue
		}
		if err := json.Unmarshal(body, dest); err != nil {
			return err
		}
		if headerOut != nil {
			*headerOut = resp.Header.Clone()
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("apify GET %s failed", rawURL)
	}
	return last
}

type apifyRunJSON struct {
	ID               string      `json:"id"`
	Status           string      `json:"status"`
	StartedAt        time.Time   `json:"startedAt"`
	UsageTotalUsd    *jsonNumber `json:"usageTotalUsd"`
	DefaultDatasetID string      `json:"defaultDatasetId"`
}

type jsonNumber string

func (n *jsonNumber) UnmarshalJSON(raw []byte) error {
	if n == nil {
		return nil
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		*n = ""
		return nil
	}
	*n = jsonNumber(strings.Trim(s, `"`))
	return nil
}

func (j apifyRunJSON) run() apifyRun {
	out := apifyRun{
		ID:               j.ID,
		Status:           j.Status,
		StartedAt:        j.StartedAt,
		DefaultDatasetID: j.DefaultDatasetID,
	}
	if j.UsageTotalUsd != nil && string(*j.UsageTotalUsd) != "" {
		s := string(*j.UsageTotalUsd)
		out.UsageTotalUsd = &s
	}
	return out
}

func isTerminalSuccess(status string) bool { return status == "SUCCEEDED" }

func isTerminalFailure(status string) bool {
	switch status {
	case "FAILED", "ABORTED", "TIMED-OUT":
		return true
	default:
		return false
	}
}

func isNonTerminal(status string) bool {
	switch status {
	case "RUNNING", "READY", "TIMING-OUT":
		return true
	default:
		return false
	}
}
