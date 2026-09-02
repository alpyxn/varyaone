package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const serviceReadyTimeout = 2 * time.Minute

// WaitForReady waits for the desktop HTTP server and its database/migrations
// readiness check. Starting in the service manager only proves that Windows
// accepted the request; this proves that Varya One can serve users.
func WaitForReady(ctx context.Context, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid HTTP port %d", port)
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/health/ready", port)
	client := &http.Client{Timeout: 2 * time.Second}
	return waitForReady(ctx, 500*time.Millisecond, func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		var health struct {
			Service string `json:"service"`
			Status  string `json:"status"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&health); err != nil {
			return fmt.Errorf("invalid health response: %w", err)
		}
		if health.Service != "api" || health.Status != "ok" {
			return errors.New("endpoint is not a ready Varya One API")
		}
		return nil
	})
}

func waitForReady(ctx context.Context, interval time.Duration, probe func(context.Context) error) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := probe(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("Varya One hazır olmadı: %w (son deneme: %v)", ctx.Err(), lastErr)
			}
			return fmt.Errorf("Varya One hazır olmadı: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
