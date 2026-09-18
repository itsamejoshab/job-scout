package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jobscout/jobscout/internal/config"
)

// WebhookClient POSTs one JSON message to the notify webhook.
type WebhookClient struct {
	Target       string
	Notify       string
	ClientID     string
	ClientSecret string
	TokenURL     string
	Client       *http.Client
}

func NewWebhookClient(cfg config.Config) *WebhookClient {
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tokenURL := cfg.OAuthTokenURL
	if tokenURL == "" {
		tokenURL = config.DefaultOAuthTokenURL
	}
	return &WebhookClient{
		Target:       cfg.WebhookTarget(),
		Notify:       cfg.WebhookNotify(),
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     tokenURL,
		Client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *WebhookClient) usesOAuth() bool {
	return c.ClientID != "" || c.ClientSecret != ""
}

// PostMessage POSTs {"notify": webhook_id, "message": message} once. Success is HTTP 200 only.
func (c *WebhookClient) PostMessage(ctx context.Context, message string) error {
	if c.usesOAuth() && (c.ClientID == "" || c.ClientSecret == "") {
		return fmt.Errorf("CLIENT_ID and CLIENT_SECRET must both be set for OAuth")
	}
	body, err := json.Marshal(struct {
		Notify  string `json:"notify"`
		Message string `json:"message"`
	}{Notify: c.Notify, Message: message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.usesOAuth() {
		token, err := c.fetchAccessToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *WebhookClient) fetchAccessToken(ctx context.Context) (string, error) {
	body, err := json.Marshal(struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}{
		GrantType:    "client_credentials",
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth token HTTP %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("oauth token JSON: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("oauth token response missing access_token")
	}
	return out.AccessToken, nil
}
