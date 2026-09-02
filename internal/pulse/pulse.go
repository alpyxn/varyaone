// Package pulse gathers an anonymous usage summary ("Nabız") from the local
// database and ships it to an external varya-pulse collector.
//
// Privacy contract: only integer counts plus opaque UUIDs, the app/PostgreSQL
// version and non-identifying locale fields (base currency, timezone) ever leave
// this process. No company name, tax number, address, e-mail, person name,
// document number or monetary amount is collected. See internal/pulse/pulse_test.go
// for the guard test.
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
	schemaVersion   = 1
	minSendInterval = 20 * time.Hour
	sendTimeout     = 10 * time.Second

	installIDKey        = "pulse.install_id"
	installAnnouncedKey = "pulse.install_announced"
	lastSentKey         = "pulse.last_sent_at"
	reportRelPath       = "/pulse/v1/report"
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

// metricQueries maps an anonymous metric name to the count query that produces
// it. Every query MUST select only (company_id::text, count(*)) so that no
// identifying column can leak. The guard test enforces this shape.
var metricQueries = map[string]string{
	"parties_total":     `SELECT company_id::text, count(*) FROM parties GROUP BY 1`,
	"parties_active":    `SELECT company_id::text, count(*) FROM parties WHERE is_active GROUP BY 1`,
	"sales_quotes":      `SELECT company_id::text, count(*) FROM sales_quotes GROUP BY 1`,
	"sales_orders":      `SELECT company_id::text, count(*) FROM sales_orders GROUP BY 1`,
	"sales_dispatches":  `SELECT company_id::text, count(*) FROM sales_dispatches GROUP BY 1`,
	"sales_invoices":    `SELECT company_id::text, count(*) FROM sales_invoices GROUP BY 1`,
	"sales_returns":     `SELECT company_id::text, count(*) FROM sales_returns GROUP BY 1`,
	"purchase_orders":   `SELECT company_id::text, count(*) FROM purchase_orders GROUP BY 1`,
	"purchase_invoices": `SELECT company_id::text, count(*) FROM purchase_invoices GROUP BY 1`,
	"purchase_returns":  `SELECT company_id::text, count(*) FROM purchase_returns GROUP BY 1`,
	"products":          `SELECT company_id::text, count(*) FROM products GROUP BY 1`,
	"warehouses":        `SELECT company_id::text, count(*) FROM warehouses GROUP BY 1`,
	"employees":         `SELECT company_id::text, count(*) FROM employees GROUP BY 1`,
}

// Service owns the collect + send cycle. Construct it with NewService and drive
// it from the worker via RunDue.
type Service struct {
	pool     database.Querier
	client   *http.Client
	endpoint string
	key      string
	release  string
	now      func() time.Time
}

func NewService(pool database.Querier, cfg config.Config) *Service {
	return &Service{
		pool:     pool,
		client:   &http.Client{Timeout: sendTimeout},
		endpoint: strings.TrimRight(cfg.PulseEndpoint, "/"),
		key:      cfg.PulseIngestKey,
		release:  cfg.Release,
		now:      time.Now,
	}
}

// CompanyReport is the per-company slice of a snapshot.
type CompanyReport struct {
	CompanyID    string           `json:"company_id"`
	BaseCurrency string           `json:"base_currency,omitempty"`
	Timezone     string           `json:"timezone,omitempty"`
	Metrics      map[string]int64 `json:"metrics"`
}

// Report is the full payload POSTed to the collector.
type Report struct {
	SchemaVersion int             `json:"schema_version"`
	InstallID     string          `json:"install_id"`
	CapturedAt    string          `json:"captured_at"`
	AppVersion    string          `json:"app_version,omitempty"`
	PGVersion     string          `json:"pg_version,omitempty"`
	Companies     []CompanyReport `json:"companies"`
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

// RunDue is the worker entry point. It is a no-op when the last successful send
// was less than minSendInterval ago.
func (s *Service) RunDue(ctx context.Context) error {
	due, err := s.isDue(ctx)
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	report, err := s.Snapshot(ctx)
	if err != nil {
		return err
	}
	if err := s.send(ctx, report); err != nil {
		return err
	}
	return s.markSent(ctx)
}

func (s *Service) isDue(ctx context.Context) (bool, error) {
	var raw *string
	err := s.pool.QueryRow(ctx,
		`SELECT value #>> '{}' FROM platform_metadata WHERE key = $1`, lastSentKey).
		Scan(&raw)
	if err != nil {
		// No row yet -> never sent -> due.
		if strings.Contains(err.Error(), "no rows") {
			return true, nil
		}
		return false, err
	}
	if raw == nil {
		return true, nil
	}
	last, perr := time.Parse(time.RFC3339, *raw)
	if perr != nil {
		return true, nil
	}
	return s.now().Sub(last) >= minSendInterval, nil
}

func (s *Service) markSent(ctx context.Context) error {
	stamp := s.now().UTC().Format(time.RFC3339)
	_, err := s.pool.Exec(ctx,
		`INSERT INTO platform_metadata (key, value, updated_at)
		 VALUES ($1, to_jsonb($2::text), now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		lastSentKey, stamp)
	return err
}

// Snapshot builds the anonymous report without sending it.
func (s *Service) Snapshot(ctx context.Context) (Report, error) {
	installID, err := s.installID(ctx)
	if err != nil {
		return Report{}, err
	}

	companies, err := s.companies(ctx)
	if err != nil {
		return Report{}, err
	}

	usersTotal, err := s.scalar(ctx, `SELECT count(*) FROM users WHERE is_active`)
	if err != nil {
		return Report{}, err
	}

	byMetric := make(map[string]map[string]int64, len(metricQueries))
	for metric, query := range metricQueries {
		vals, cerr := s.countByCompany(ctx, query)
		if cerr != nil {
			return Report{}, fmt.Errorf("pulse metric %s: %w", metric, cerr)
		}
		byMetric[metric] = vals
	}

	docKinds, err := s.documentKinds(ctx)
	if err != nil {
		return Report{}, err
	}

	pgVersion, _ := s.text(ctx, `SHOW server_version`)

	report := Report{
		SchemaVersion: schemaVersion,
		InstallID:     installID,
		CapturedAt:    s.now().UTC().Format(time.RFC3339),
		AppVersion:    s.release,
		PGVersion:     pgVersion,
	}

	for _, c := range companies {
		metrics := map[string]int64{
			// instance-wide, repeated per company for convenience
			"users_instance": usersTotal,
		}
		for metric, vals := range byMetric {
			metrics[metric] = vals[c.id]
		}
		for kind, n := range docKinds[c.id] {
			metrics["documents_"+strings.ToLower(kind)] = n
		}
		report.Companies = append(report.Companies, CompanyReport{
			CompanyID:    c.id,
			BaseCurrency: c.baseCurrency,
			Timezone:     c.timezone,
			Metrics:      metrics,
		})
	}

	return report, nil
}

type companyRow struct {
	id           string
	baseCurrency string
	timezone     string
}

func (s *Service) companies(ctx context.Context) ([]companyRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, base_currency, timezone FROM companies WHERE is_active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []companyRow
	for rows.Next() {
		var c companyRow
		if err := rows.Scan(&c.id, &c.baseCurrency, &c.timezone); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) countByCompany(ctx context.Context, query string) (map[string]int64, error) {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Service) documentKinds(ctx context.Context) (map[string]map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.company_id::text, dt.kind, count(*)
		  FROM documents d
		  JOIN document_types dt ON dt.code = d.document_type_code
		 GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int64{}
	for rows.Next() {
		var id, kind string
		var n int64
		if err := rows.Scan(&id, &kind, &n); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]int64{}
		}
		out[id][kind] = n
	}
	return out, rows.Err()
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

func (s *Service) scalar(ctx context.Context, query string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, query).Scan(&n)
	return n, err
}

func (s *Service) text(ctx context.Context, query string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, query).Scan(&v)
	return v, err
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

func (s *Service) send(ctx context.Context, report Report) error {
	return s.post(ctx, reportRelPath, report)
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
