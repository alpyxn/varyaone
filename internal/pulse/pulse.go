// Package pulse reports two things, and only two things, to the external
// varya-pulse collector: a one-off anonymous install ping and user-submitted
// feedback.
//
// Privacy contract: the install ping carries an opaque install UUID, the app
// version and the setup timestamp — nothing else. Feedback carries what the
// user typed plus an opaque company UUID. No company name, tax number,
// address, e-mail, person name, document number, monetary amount or usage
// statistic ever leaves this process. See internal/pulse/pulse_test.go for the
// guard test.
//
// There is deliberately no usage telemetry here: the daily per-company metrics
// summary that used to live in this package was removed, along with the
// collector's /pulse/v1/report endpoint.
package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"

	"github.com/alpyxn/varyaone/internal/platform/config"
)

const (
	sendTimeout = 10 * time.Second

	installIDKey        = "pulse.install_id"
	installAnnouncedKey = "pulse.install_announced"
	feedbackRelPath     = "/pulse/v1/feedback"
	installRelPath      = "/pulse/v1/install"
)

// FeedbackInput is a single user-submitted bug report or suggestion. The
// company_id is an opaque UUID; no user identity is attached unless the user
// voluntarily fills in Contact.
type FeedbackInput struct {
	Category  string // "bug" or "idea"
	Message   string
	Contact   string
	CompanyID string
}

// Service owns the install ping and the feedback channel. Construct it with
// NewService and drive the install ping from the worker via AnnounceInstall.
type Service struct {
	pool     database.Querier
	client   *http.Client
	endpoint string
	key      string
	release  string
}

func NewService(pool database.Querier, cfg config.Config) *Service {
	return &Service{
		pool:     pool,
		client:   &http.Client{Timeout: sendTimeout},
		endpoint: strings.TrimRight(cfg.PulseEndpoint, "/"),
		key:      cfg.PulseIngestKey,
		release:  cfg.Release,
	}
}

// AnnounceInstall registers this instance in the collector's total install
// counter. It is idempotent on both sides (the collector keys on install_id, and
// this instance records a metadata flag once it succeeds) so it is safe to call
// on every startup / tick.
//
// The returned bool reports whether the install is now accounted for: true when
// the ping succeeded or was already sent, false when it is still pending (setup
// not finished, or the collector was unreachable). Callers use it to decide
// whether to keep retrying.
func (s *Service) AnnounceInstall(ctx context.Context) (bool, error) {
	var setupAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT completed_at FROM instance_setup LIMIT 1`).Scan(&setupAt)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return false, nil // setup not finished yet -> retry later
		}
		return false, err
	}

	var announced bool
	if e := s.pool.QueryRow(ctx,
		`SELECT true FROM platform_metadata WHERE key = $1`, installAnnouncedKey).
		Scan(&announced); e == nil && announced {
		return true, nil
	}

	installID, err := s.installID(ctx)
	if err != nil {
		return false, err
	}
	payload := map[string]any{
		"install_id":  installID,
		"app_version": s.release,
	}
	if setupAt != nil {
		payload["setup_at"] = setupAt.UTC().Format(time.RFC3339)
	}
	if err := s.post(ctx, installRelPath, payload); err != nil {
		return false, err
	}

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO platform_metadata (key, value, updated_at)
		 VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, installAnnouncedKey); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) installID(ctx context.Context) (string, error) {
	generated := uuid.NewString()
	var stored string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO platform_metadata (key, value, updated_at)
		VALUES ($1, to_jsonb($2::text), now())
		ON CONFLICT (key) DO UPDATE SET value = platform_metadata.value
		RETURNING value #>> '{}'`, installIDKey, generated).Scan(&stored)
	if err != nil {
		return "", err
	}
	return stored, nil
}

// SendFeedback ships a single user bug report / suggestion to the collector.
func (s *Service) SendFeedback(ctx context.Context, in FeedbackInput) error {
	installID, err := s.installID(ctx)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"install_id":  installID,
		"company_id":  in.CompanyID,
		"category":    in.Category,
		"message":     in.Message,
		"contact":     in.Contact,
		"app_version": s.release,
	}
	return s.post(ctx, feedbackRelPath, payload)
}

func (s *Service) post(ctx context.Context, relPath string, payload any) error {
	if s.endpoint == "" || s.key == "" {
		return fmt.Errorf("pulse endpoint or key not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+relPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+s.key)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pulse collector returned %s", resp.Status)
	}
	return nil
}
