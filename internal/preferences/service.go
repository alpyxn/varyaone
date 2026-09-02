package preferences

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

var tableKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var columnKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)

const maxColumnPreferences = 256

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type TablePreference struct {
	TableKey         string          `json:"table_key"`
	ColumnVisibility map[string]bool `json:"column_visibility"`
	Version          int64           `json:"version"`
	UpdatedAt        *time.Time      `json:"updated_at,omitempty"`
}

func (s *Service) GetTable(ctx context.Context, session identity.Session, tableKey string) (TablePreference, error) {
	tableKey, err := normalizeTableKey(tableKey)
	if err != nil {
		return TablePreference{}, err
	}
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return TablePreference{}, identity.ErrForbidden
	}

	var preference TablePreference
	var raw []byte
	var updatedAt time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT table_key,column_visibility,version,updated_at
		FROM user_table_preferences
		WHERE company_id=$1 AND user_id=$2 AND table_key=$3`,
		session.CurrentCompanyID, session.User.ID, tableKey).
		Scan(&preference.TableKey, &raw, &preference.Version, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TablePreference{TableKey: tableKey, ColumnVisibility: map[string]bool{}}, nil
		}
		return TablePreference{}, err
	}
	if err := json.Unmarshal(raw, &preference.ColumnVisibility); err != nil {
		return TablePreference{}, fmt.Errorf("decode table preference: %w", err)
	}
	if preference.ColumnVisibility == nil {
		preference.ColumnVisibility = map[string]bool{}
	}
	preference.UpdatedAt = &updatedAt
	return preference, nil
}

func (s *Service) SaveTable(ctx context.Context, session identity.Session, tableKey string, visibility map[string]bool) (TablePreference, error) {
	tableKey, err := normalizeTableKey(tableKey)
	if err != nil {
		return TablePreference{}, err
	}
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return TablePreference{}, identity.ErrForbidden
	}
	normalized, err := normalizeColumnVisibility(visibility)
	if err != nil {
		return TablePreference{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return TablePreference{}, err
	}

	var preference TablePreference
	var raw []byte
	var updatedAt time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO user_table_preferences(company_id,user_id,table_key,column_visibility)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(company_id,user_id,table_key) DO UPDATE
		SET column_visibility=excluded.column_visibility,updated_at=now(),version=user_table_preferences.version+1
		RETURNING table_key,column_visibility,version,updated_at`,
		session.CurrentCompanyID, session.User.ID, tableKey, payload).
		Scan(&preference.TableKey, &raw, &preference.Version, &updatedAt)
	if err != nil {
		return TablePreference{}, err
	}
	if err := json.Unmarshal(raw, &preference.ColumnVisibility); err != nil {
		return TablePreference{}, fmt.Errorf("decode saved table preference: %w", err)
	}
	preference.UpdatedAt = &updatedAt
	return preference, nil
}

func normalizeTableKey(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !tableKeyPattern.MatchString(value) {
		return "", fmt.Errorf("%w: geçersiz tablo tercih anahtarı", identity.ErrValidation)
	}
	return value, nil
}

func normalizeColumnVisibility(input map[string]bool) (map[string]bool, error) {
	if len(input) > maxColumnPreferences {
		return nil, fmt.Errorf("%w: çok fazla sütun tercihi", identity.ErrValidation)
	}
	result := make(map[string]bool, len(input))
	for key, visible := range input {
		key = strings.TrimSpace(key)
		if !columnKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("%w: geçersiz sütun tercih anahtarı", identity.ErrValidation)
		}
		// Visible columns are the default. Persisting only hidden columns keeps
		// removed/renamed columns from changing the user's default layout.
		if !visible {
			result[key] = false
		}
	}
	return result, nil
}
