package party

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TaxOfficeReference is a global GİB-sourced reference row. Code is nullable
// because the official list includes code-less branch records.
type TaxOfficeReference struct {
	ID           string  `json:"id"`
	Code         *string `json:"code"`
	Name         string  `json:"name"`
	ProvinceID   int64   `json:"province_id"`
	ProvinceName string  `json:"province_name"`
	DistrictName string  `json:"district_name"`
	OfficeType   string  `json:"office_type"`
	IsActive     bool    `json:"is_active"`
}

// ListTaxOfficeReferences returns the local, versioned GİB snapshot. The
// catalog is global, but access is still protected by the selected session's
// party.read permission like the address reference endpoints.
func (s *Service) ListTaxOfficeReferences(ctx context.Context, session identity.Session, provinceID int64, districtName, search string, limit int) ([]TaxOfficeReference, error) {
	if !authorized(session, "party.read") {
		return nil, identity.ErrForbidden
	}
	if provinceID < 0 || provinceID > 81 {
		return nil, fmt.Errorf("%w: geçersiz il", identity.ErrValidation)
	}
	limit = locationLimit(limit)
	districtName = strings.TrimSpace(districtName)
	search = strings.TrimSpace(search)
	args := []any{}
	where := ` WHERE true`

	if provinceID > 0 {
		args = append(args, provinceID)
		where += fmt.Sprintf(` AND t.province_id=$%d`, len(args))
	}
	if districtName != "" {
		addressDistrictExists := false
		if provinceID > 0 {
			var officialDistrictExists bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM turkish_districts
				WHERE province_id=$1 AND normalize_party_search_text(name)=normalize_party_search_text($2)
			), EXISTS(
				SELECT 1 FROM turkish_tax_offices
				WHERE province_id=$1 AND normalize_party_search_text(district_name)=normalize_party_search_text($2)
			)`, provinceID, districtName).Scan(&addressDistrictExists, &officialDistrictExists); err != nil {
				return nil, err
			}
			if !addressDistrictExists && !officialDistrictExists {
				return nil, fmt.Errorf("%w: ilçe seçilen ile ait değil", identity.ErrValidation)
			}
		}
		// GİB's list uses "Merkez" for some provincial-center offices while
		// the address catalog contains the modern administrative district name.
		// Keep the official district in the response, but allow a valid address
		// district to narrow the office name (for example İstanbul/Kadıköy).
		args = append(args, districtName)
		position := len(args)
		if addressDistrictExists {
			where += fmt.Sprintf(` AND (
				normalize_party_search_text(t.district_name)=normalize_party_search_text($%d)
				OR normalize_party_search_text(t.name) ILIKE '%%' || normalize_party_search_text($%d) || '%%'
			)`, position, position)
		} else {
			where += fmt.Sprintf(` AND normalize_party_search_text(t.district_name)=normalize_party_search_text($%d)`, position)
		}
	}
	if search != "" {
		args = append(args, search)
		position := len(args)
		where += fmt.Sprintf(` AND NULLIF(normalize_party_search_text($%d),'') IS NOT NULL
			AND normalize_party_search_text(concat_ws(' ',t.name,COALESCE(t.code,''),tp.name,t.district_name))
				ILIKE '%%' || normalize_party_search_text($%d) || '%%'`, position, position)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT t.id::text,t.code,t.name,t.province_id,tp.name,t.district_name,t.office_type,t.is_active
		FROM turkish_tax_offices t
		JOIN turkish_provinces tp ON tp.id=t.province_id%s
		ORDER BY t.is_active DESC,lower(t.name),t.code NULLS LAST,t.id
		LIMIT $%d`, where, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaxOfficeReference, 0)
	for rows.Next() {
		var item TaxOfficeReference
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.ProvinceID, &item.ProvinceName, &item.DistrictName, &item.OfficeType, &item.IsActive); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// resolveTaxOfficeSelection makes the selected reference authoritative. An
// existing party may round-trip its now-inactive selected row so editing an
// unrelated field does not destroy historical master data. A new or changed
// selection must be active.
func resolveTaxOfficeSelection(ctx context.Context, tx pgx.Tx, input *Input, existingInactiveID string) error {
	if strings.TrimSpace(input.TaxOfficeID) == "" {
		input.TaxOfficeID = ""
		return nil
	}
	if input.Kind != "ORGANIZATION" {
		return fmt.Errorf("%w: vergi dairesi yalnızca kurum carisinde seçilebilir", identity.ErrValidation)
	}
	id, err := uuid.Parse(strings.TrimSpace(input.TaxOfficeID))
	if err != nil {
		return fmt.Errorf("%w: vergi dairesi kimliği geçersiz", identity.ErrValidation)
	}
	input.TaxOfficeID = id.String()
	var canonicalName string
	var active bool
	err = tx.QueryRow(ctx, `SELECT name,is_active FROM turkish_tax_offices WHERE id=$1`, id).Scan(&canonicalName, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: vergi dairesi bulunamadı", identity.ErrValidation)
	}
	if err != nil {
		return err
	}
	if !active && input.TaxOfficeID != strings.TrimSpace(existingInactiveID) {
		return fmt.Errorf("%w: pasif vergi dairesi seçilemez", identity.ErrValidation)
	}
	input.TaxOffice = strings.TrimSpace(canonicalName)
	return nil
}
