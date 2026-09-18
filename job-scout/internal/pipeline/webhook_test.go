package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jobscout/jobscout/internal/config"
)

func TestPostMessage_PostsJSONToWebhookBaseWithNotifyKey(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotCT     string
		gotAuth   string
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json <html>`))
	}))
	defer srv.Close()

	cfg := config.Config{
		WebhookBase:        srv.URL + "/api/webhook",
		WebhookID:          "hook-id",
		HTTPTimeoutSeconds: 30,
	}
	client := NewWebhookClient(cfg)
	if client.Client == nil || client.Client.Timeout != 30*time.Second {
		t.Fatalf("webhook HTTP timeout = %v, want 30s from HTTP_TIMEOUT_SECONDS", client.Client.Timeout)
	}
	wantTarget := strings.TrimRight(cfg.WebhookBase, "/")
	if client.Target != wantTarget {
		t.Errorf("client target = %q, want WEBHOOK_BASE %q", client.Target, wantTarget)
	}
	if client.Notify != cfg.WebhookID {
		t.Errorf("client notify = %q, want WEBHOOK_ID %q", client.Notify, cfg.WebhookID)
	}

	msg := "  1 job postings stored.\nNEW LEAD"
	if err := client.PostMessage(context.Background(), msg); err != nil {
		t.Fatalf("PostMessage 200: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/webhook" {
		t.Errorf("path = %q, want /api/webhook", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty when CLIENT_ID and CLIENT_SECRET are unset", gotAuth)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body JSON: %v (%q)", err, gotBody)
	}
	if payload["message"] != msg {
		t.Errorf("JSON message = %q, want %q", payload["message"], msg)
	}
	if payload["notify"] != "hook-id" {
		t.Errorf("JSON notify = %q, want %q", payload["notify"], "hook-id")
	}
	if len(payload) != 2 {
		t.Errorf("JSON object keys = %v, want notify and message", payload)
	}
}

func TestPostMessage_HTTP302IsErrorAndDoesNotFollowTo200(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/hook" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := NewWebhookClient(config.Config{
		WebhookBase:        srv.URL + "/hook",
		WebhookID:          "notify-id",
		HTTPTimeoutSeconds: 5,
	})
	if err := client.PostMessage(context.Background(), "x"); err == nil {
		t.Fatal("HTTP 302 must not count as success even if a follow-up path returns 200")
	}
	for _, p := range hits {
		if p == "/ok" {
			t.Fatalf("POST must not follow redirects, hits=%v", hits)
		}
	}
}

func TestPostMessage_UsesNonDefaultHTTPTimeout(t *testing.T) {
	client := NewWebhookClient(config.Config{
		WebhookBase:        "http://127.0.0.1:9",
		WebhookID:          "hook",
		HTTPTimeoutSeconds: 7,
	})
	if client.Client == nil || client.Client.Timeout != 7*time.Second {
		t.Fatalf("HTTP timeout = %v, want 7s from HTTP_TIMEOUT_SECONDS", client.Client.Timeout)
	}
}

func TestPostMessage_HTTP204AndNon200AreErrors(t *testing.T) {
	t.Parallel()
	cases := []int{http.StatusNoContent, http.StatusCreated, http.StatusInternalServerError}
	for _, status := range cases {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)
			client := NewWebhookClient(config.Config{
				WebhookBase:        srv.URL,
				WebhookID:          "hook",
				HTTPTimeoutSeconds: 5,
			})
			err := client.PostMessage(context.Background(), "x")
			if err == nil {
				t.Fatalf("HTTP %d must not count as success", status)
			}
		})
	}
}

func TestPostMessage_TransportErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := NewWebhookClient(config.Config{
		WebhookBase:        srv.URL,
		WebhookID:          "hook",
		HTTPTimeoutSeconds: 5,
	})
	srv.Close()
	if err := client.PostMessage(context.Background(), "x"); err == nil {
		t.Fatal("transport error must fail the POST")
	}
}

func TestPostMessage_OAuthBearerFromClientCredentials(t *testing.T) {
	var (
		tokenHits    int
		gotTokenCT   string
		gotTokenBody []byte
		gotAuth      string
		gotPath      string
		gotBody      []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			tokenHits++
			gotTokenCT = r.Header.Get("Content-Type")
			gotTokenBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":3600}`))
		case "/api/webhook":
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			gotBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewWebhookClient(config.Config{
		WebhookBase:        srv.URL + "/api/webhook",
		WebhookID:          "hook-id",
		ClientID:           "oauth-client",
		ClientSecret:       "oauth-secret",
		OAuthTokenURL:      srv.URL + "/oauth/token",
		HTTPTimeoutSeconds: 5,
	})
	msg := "NEW LEAD"
	if err := client.PostMessage(context.Background(), msg); err != nil {
		t.Fatalf("PostMessage OAuth: %v", err)
	}
	if tokenHits != 1 {
		t.Errorf("token POSTs = %d, want 1", tokenHits)
	}
	if gotTokenCT != "application/json" {
		t.Errorf("token Content-Type = %q, want application/json", gotTokenCT)
	}
	var tokenReq map[string]string
	if err := json.Unmarshal(gotTokenBody, &tokenReq); err != nil {
		t.Fatalf("token body JSON: %v (%q)", err, gotTokenBody)
	}
	if tokenReq["grant_type"] != "client_credentials" {
		t.Errorf("grant_type = %q, want client_credentials", tokenReq["grant_type"])
	}
	if tokenReq["client_id"] != "oauth-client" {
		t.Errorf("client_id = %q, want oauth-client", tokenReq["client_id"])
	}
	if tokenReq["client_secret"] != "oauth-secret" {
		t.Errorf("client_secret = %q, want oauth-secret", tokenReq["client_secret"])
	}
	if gotPath != "/api/webhook" {
		t.Errorf("webhook path = %q, want /api/webhook", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	var payload map[string]string
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("webhook body JSON: %v (%q)", err, gotBody)
	}
	if payload["notify"] != "hook-id" || payload["message"] != msg {
		t.Errorf("webhook JSON = %v, want notify=hook-id message=%q", payload, msg)
	}
}

func TestPostMessage_OAuthTokenHTTPErrorFailsWebhook(t *testing.T) {
	var webhookHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		webhookHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := NewWebhookClient(config.Config{
		WebhookBase:        srv.URL + "/hook",
		WebhookID:          "id",
		ClientID:           "oauth-client",
		ClientSecret:       "oauth-secret",
		OAuthTokenURL:      srv.URL + "/oauth/token",
		HTTPTimeoutSeconds: 5,
	})
	if err := client.PostMessage(context.Background(), "x"); err == nil {
		t.Fatal("token HTTP 401 must fail PostMessage")
	}
	if webhookHits != 0 {
		t.Errorf("webhook hits = %d, want 0 after token failure", webhookHits)
	}
}

func TestPostMessage_OAuthRequiresBothClientKnobs(t *testing.T) {
	client := NewWebhookClient(config.Config{
		WebhookBase:        "http://127.0.0.1:9",
		WebhookID:          "id",
		ClientID:           "oauth-client",
		HTTPTimeoutSeconds: 5,
	})
	if err := client.PostMessage(context.Background(), "x"); err == nil {
		t.Fatal("CLIENT_ID without CLIENT_SECRET must fail before HTTP")
	}
}
