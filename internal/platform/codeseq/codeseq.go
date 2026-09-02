// Package codeseq generates company-scoped, zero-padded sequential codes
// (for example E-0001) when a caller leaves a code field blank.
package codeseq

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Next returns the next free "<prefix>-NNNN" code (4-digit) for a company-scoped
// table. It must be called inside a transaction; a per-company advisory lock
// serialises concurrent generators so two blank creates never collide.
func Next(ctx context.Context, tx pgx.Tx, companyID, table, column, prefix string) (string, error) {
	return NextWidth(ctx, tx, companyID, table, column, prefix, 4)
}

// NextWidth is Next with an explicit zero-pad width (for example width 5 →
// "P-00001").
func NextWidth(ctx context.Context, tx pgx.Tx, companyID, table, column, prefix string, width int) (string, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, companyID+":"+table+":"+column); err != nil {
		return "", err
	}
	pattern := "^" + prefix + `-[0-9]+$`
	query := fmt.Sprintf(
		`SELECT COALESCE(MAX(NULLIF(regexp_replace(%s, '\D', '', 'g'), '')::bigint), 0) + 1
		 FROM %s WHERE company_id=$1 AND %s ~ $2`, column, table, column)
	var next int64
	if err := tx.QueryRow(ctx, query, companyID, pattern).Scan(&next); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%0*d", prefix, width, next), nil
}
