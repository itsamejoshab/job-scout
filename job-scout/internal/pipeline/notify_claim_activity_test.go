package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/db/pgtest"
	"github.com/jobscout/jobscout/internal/domain"
)

func TestActivities_ClaimNotifyBatchUsesMaxJobsThenPostAndFinish(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.Migrate(pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	urls := []string{
		"https://www.linkedin.com/jobs/view/act-old/",
		"https://www.linkedin.com/jobs/view/act-mid/",
		"https://www.linkedin.com/jobs/view/act-new/",
	}
	for _, u := range urls {
		if _, err := db.InsertJobIfNew(ctx, pool, db.Job{
			JobSource: db.SourceLinkedIn,
			Title:     "IT Help Desk",
			Company:   "Acme",
			Location:  "Remote",
			JobURL:    u,
		}); err != nil {
			t.Fatalf("insert %s: %v", u, err)
		}
	}
	if _, err := pool.Exec(`UPDATE jobs SET state = 'eligible'`); err != nil {
		t.Fatalf("eligible: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '3 minutes' WHERE job_url LIKE '%act-old/'`); err != nil {
		t.Fatalf("age old: %v", err)
	}
	if _, err := pool.Exec(`UPDATE jobs SET created_at = now() - interval '2 minutes' WHERE job_url LIKE '%act-mid/'`); err != nil {
		t.Fatalf("age mid: %v", err)
	}

	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotBody   []byte
		hits      int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<not-json>`))
	}))
	defer srv.Close()

	acts := &Activities{
		DB: pool,
		Webhook: NewWebhookClient(config.Config{
			WebhookBase:        srv.URL + "/api/webhook",
			WebhookID:          "hook",
			WebhookURL:         srv.URL + "/old-url",
			HTTPTimeoutSeconds: 12,
		}),
		NotifyMaxJobs: 2,
	}
	if acts.Webhook == nil || acts.Webhook.Client.Timeout != 12*time.Second {
		t.Fatalf("HA client timeout = %v, want 12s from HTTP_TIMEOUT_SECONDS", acts.Webhook.Client.Timeout)
	}

	batch, err := acts.claim_notification_batch(ctx)
	if err != nil {
		t.Fatalf("ClaimNotifyBatch: %v", err)
	}
	if len(batch.Jobs) != 2 {
		t.Fatalf("claimed = %d, want 2 from NotifyMaxJobs", len(batch.Jobs))
	}
	if batch.Jobs[0].JobURL != urls[0] || batch.Jobs[1].JobURL != urls[1] {
		t.Errorf("claimed URLs = %s, %s, want oldest then next", batch.Jobs[0].JobURL, batch.Jobs[1].JobURL)
	}
	if batch.Counts.Notifying != 2 || batch.Counts.Eligible != 1 || batch.Counts.Total != 3 {
		t.Errorf("counts after claim = %+v, want notifying=2 eligible leftover=1 total=3", batch.Counts)
	}

	msg := domain.BuildMessage(batch.Counts, []string{batch.Jobs[0].JobURL, batch.Jobs[1].JobURL})
	if err := acts.send_notification(ctx, msg); err != nil {
		t.Fatalf("NotifyWebhook: %v", err)
	}
	if hits != 1 || gotMethod != http.MethodPost || gotPath != "/api/webhook" || gotCT != "application/json" {
		t.Errorf("POST method=%q path=%q ct=%q hits=%d, want POST /api/webhook application/json once", gotMethod, gotPath, gotCT, hits)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body: %v (%q)", err, gotBody)
	}
	if payload["message"] != msg {
		t.Errorf("JSON message = %q, want %q", payload["message"], msg)
	}
	if payload["notify"] != "hook" {
		t.Errorf("JSON notify = %q, want %q", payload["notify"], "hook")
	}

	ids := []int64{batch.Jobs[0].ID, batch.Jobs[1].ID}
	if err := acts.finish_notification_batch(ctx, FinishNotifyBatchInput{IDs: ids, State: domain.StateNotified}); err != nil {
		t.Fatalf("FinishNotifyBatch: %v", err)
	}
	var notified, eligible int
	if err := pool.QueryRow(`SELECT COUNT(*) FILTER (WHERE state = 'notified'), COUNT(*) FILTER (WHERE state = 'eligible') FROM jobs`).Scan(&notified, &eligible); err != nil {
		t.Fatalf("counts: %v", err)
	}
	if notified != 2 || eligible != 1 {
		t.Errorf("after 200: notified=%d eligible=%d, want 2 notified and leftover 1 eligible", notified, eligible)
	}
}
