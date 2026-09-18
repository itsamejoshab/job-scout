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

// WebhookClient POSTs one JSON message to Home Assistant.
type WebhookClient struct {
	Target string
	Client *http.Client
}

func NewWebhookClient(cfg config.Config) *WebhookClient {
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &WebhookClient{
		Target: cfg.WebhookTarget(),
		Client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// PostMessage POSTs {"message": message} once. Success is HTTP 200 only.
func (c *WebhookClient) PostMessage(ctx context.Context, message string) error {
	body, err := json.Marshal(struct {
		Message string `json:"message"`
	}{Message: message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
