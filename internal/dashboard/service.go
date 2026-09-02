// Package dashboard backs the home workspace: per-user pinned shortcuts and a
// company-scoped "recent activity" feed aggregated from the immutable business
// ledgers. It follows the internal/preferences pattern: raw pgx queries, no
// sqlc, hand-rolled JSON models.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

var shortcutKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,63}$`)

const maxPinnedShortcuts = 32

const (
	defaultRecentLimit = 15
	maxRecentLimit     = 50
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

// Shortcuts is the persisted home-screen launcher layout for one user in one
// company. An absent row is reported as a nil slice so the frontend can apply
// its own default set.
type Shortcuts struct {
	PinnedShortcuts []string   `json:"pinned_shortcuts"`
	Version         int64      `json:"version"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

// RecentEntry is a single line in the recent-activity feed. The frontend maps
// Kind + RefType + RefID to an icon and a detail-page href.
type RecentEntry struct {
	Kind       string    `json:"kind"`
	TitleCode  string    `json:"title_code"`
	Label      string    `json:"label"`
	PartyName  string    `json:"party_name,omitempty"`
	Amount     string    `json:"amount,omitempty"`
	Currency   string    `json:"currency,omitempty"`
	Direction  string    `json:"direction,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	RefType    string    `json:"ref_type,omitempty"`
	RefID      string    `json:"ref_id,omitempty"`
	EntryID    string    `json:"entry_id"`
}

func (s *Service) GetShortcuts(ctx context.Context, session identity.Session) (Shortcuts, error) {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return Shortcuts{}, identity.ErrForbidden
	}
	var raw []byte
	var result Shortcuts
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT pinned_shortcuts,version,updated_at
		FROM user_dashboard_preferences
		WHERE company_id=$1 AND user_id=$2`,
		session.CurrentCompanyID, session.User.ID).
		Scan(&raw, &result.Version, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Shortcuts{PinnedShortcuts: nil}, nil
		}
		return Shortcuts{}, err
	}
	if err := json.Unmarshal(raw, &result.PinnedShortcuts); err != nil {
		return Shortcuts{}, fmt.Errorf("decode dashboard shortcuts: %w", err)
	}
	result.UpdatedAt = &updatedAt
	return result, nil
}

func (s *Service) SaveShortcuts(ctx context.Context, session identity.Session, keys []string) (Shortcuts, error) {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return Shortcuts{}, identity.ErrForbidden
	}
	normalized, err := normalizeShortcutKeys(keys)
	if err != nil {
		return Shortcuts{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return Shortcuts{}, err
	}

	var raw []byte
	var result Shortcuts
	var updatedAt time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO user_dashboard_preferences(company_id,user_id,pinned_shortcuts)
		VALUES($1,$2,$3)
		ON CONFLICT(company_id,user_id) DO UPDATE
		SET pinned_shortcuts=excluded.pinned_shortcuts,updated_at=now(),version=user_dashboard_preferences.version+1
		RETURNING pinned_shortcuts,version,updated_at`,
		session.CurrentCompanyID, session.User.ID, payload).
		Scan(&raw, &result.Version, &updatedAt)
	if err != nil {
		return Shortcuts{}, err
	}
	if err := json.Unmarshal(raw, &result.PinnedShortcuts); err != nil {
		return Shortcuts{}, fmt.Errorf("decode saved dashboard shortcuts: %w", err)
	}
	result.UpdatedAt = &updatedAt
	return result, nil
}

func normalizeShortcutKeys(input []string) ([]string, error) {
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, key := range input {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		if !shortcutKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("%w: geçersiz kısayol anahtarı", identity.ErrValidation)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	if len(result) > maxPinnedShortcuts {
		return nil, fmt.Errorf("%w: çok fazla kısayol", identity.ErrValidation)
	}
	return result, nil
}

// RecentActivity returns the newest business events the caller is allowed to
// see, most recent first. Each source is only unioned in when the session holds
// the matching read permission, so a user without ledger access never sees
// ledger lines.
func (s *Service) RecentActivity(ctx context.Context, session identity.Session, limit int) ([]RecentEntry, error) {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 {
		limit = defaultRecentLimit
	}
	if limit > maxRecentLimit {
		limit = maxRecentLimit
	}

	var blocks []string
	if hasAny(session, "party.ledger.read") {
		blocks = append(blocks, `
			SELECT 'ledger' AS kind, ple.entry_type AS title_code, ple.description AS label,
			       p.display_name AS party_name, (ple.debit - ple.credit)::text AS amount,
			       ple.currency AS currency,
			       CASE WHEN ple.debit > 0 THEN 'DEBIT' ELSE 'CREDIT' END AS direction,
			       COALESCE(ple.posted_at, ple.created_at) AS occurred_at,
			       ple.source_type AS ref_type, ple.source_id::text AS ref_id, ple.id::text AS entry_id
			FROM party_ledger_entries ple
			JOIN parties p ON p.company_id = ple.company_id AND p.id = ple.party_id
			WHERE ple.company_id = $1`)
	}
	if hasAny(session, "inventory.read") {
		blocks = append(blocks, `
			SELECT 'stock' AS kind, sm.movement_type AS title_code, pr.name AS label,
			       '' AS party_name, sm.quantity::text AS amount, COALESCE(sm.currency, '') AS currency,
			       sm.direction AS direction, sm.posted_at AS occurred_at,
			       sm.source_type AS ref_type, sm.source_id::text AS ref_id, sm.id::text AS entry_id
			FROM stock_movements sm
			JOIN products pr ON pr.company_id = sm.company_id AND pr.id = sm.product_id
			WHERE sm.company_id = $1`)
	}
	if hasAny(session, "sales.invoice.read", "sales.order.read", "sales.quote.read", "sales.dispatch.read",
		"sales.return.read", "purchase.order.read", "purchase.invoice.post", "commercial.document.read") {
		blocks = append(blocks, `
			SELECT 'document' AS kind, d.document_type_code AS title_code, d.document_no AS label,
			       p.display_name AS party_name, d.grand_total::text AS amount, d.currency_code AS currency,
			       '' AS direction, COALESCE(d.posted_at, d.created_at) AS occurred_at,
			       'document' AS ref_type, d.id::text AS ref_id, d.id::text AS entry_id
			FROM documents d
			JOIN parties p ON p.company_id = d.company_id AND p.id = d.party_id
			WHERE d.company_id = $1 AND d.status = 'DRAFT'`)
	}
	if len(blocks) == 0 {
		return []RecentEntry{}, nil
	}

	query := fmt.Sprintf("SELECT * FROM (%s) feed ORDER BY occurred_at DESC LIMIT %d",
		strings.Join(blocks, " UNION ALL "), limit)
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]RecentEntry, 0, limit)
	for rows.Next() {
		var e RecentEntry
		if err := rows.Scan(&e.Kind, &e.TitleCode, &e.Label, &e.PartyName, &e.Amount,
			&e.Currency, &e.Direction, &e.OccurredAt, &e.RefType, &e.RefID, &e.EntryID); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func hasAny(session identity.Session, permissions ...string) bool {
	for _, permission := range permissions {
		if session.HasPermission(permission) {
			return true
		}
	}
	return false
}
