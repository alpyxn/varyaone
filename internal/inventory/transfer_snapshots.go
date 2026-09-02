package inventory

import (
	"context"
	"errors"
	"strings"
)

// TransferLineIdentity is the immutable, company-scoped display identity of a
// transfer line. It deliberately reads only snapshot columns; catalog rows
// must never be used as a history fallback after migration 66.
type TransferLineIdentity struct {
	ID                 string
	CompanyID          string
	ProductID          string
	VariantID          *string
	ProductCode        string
	ProductName        string
	VariantCode        string
	VariantDescription string
}

// LoadTransferLineIdentities returns all requested line identities with one
// company-scoped query. Missing IDs are omitted, which prevents a line from a
// different company from becoming observable through this method.
func (s *Service) LoadTransferLineIdentities(ctx context.Context, companyID string, lineIDs []string) ([]TransferLineIdentity, error) {
	companyID, err := requireUUID("company_id", companyID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(lineIDs))
	seen := make(map[string]struct{}, len(lineIDs))
	for _, lineID := range lineIDs {
		lineID = strings.TrimSpace(lineID)
		if lineID == "" {
			continue
		}
		lineID, err = requireUUID("transfer_line_id", lineID)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[lineID]; ok {
			continue
		}
		seen[lineID] = struct{}{}
		ids = append(ids, lineID)
	}
	if len(ids) == 0 {
		return []TransferLineIdentity{}, nil
	}
	if s == nil || s.pool == nil {
		return nil, errors.New("inventory service database is not configured")
	}

	rows, err := s.pool.Query(ctx, `SELECT id,company_id,product_id,variant_id,
		product_code_snapshot,product_name_snapshot,variant_code_snapshot,variant_description_snapshot
		FROM warehouse_transfer_lines
		WHERE company_id=$1 AND id=ANY($2::uuid[])
		ORDER BY line_no,id`, companyID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]TransferLineIdentity, 0, len(ids))
	for rows.Next() {
		var item TransferLineIdentity
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.ProductID, &item.VariantID,
			&item.ProductCode, &item.ProductName, &item.VariantCode, &item.VariantDescription); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
