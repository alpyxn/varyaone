package sales

import (
	"context"
	"math/big"
	"strings"

	"github.com/jackc/pgx/v5"
)

// resolveSalesReturnLineCostTx prices a physical sales return the way the
// FIFO cost ledger prices every other IN movement: from what actually left
// the warehouse. A sales return always carries a source invoice/dispatch
// line (CONTEXT.md: "Kaynak fatura veya irsaliye ilişkisini ... koruyan"),
// so the original OUT movement's FIFO consumption is the return's cost --
// the quantity-weighted average of whatever layers that sale actually drew
// from. Sending "" here, as this used to, meant apply_stock_cost_layer()
// opened no cost layer at all for the returned quantity, and the FIFO ledger
// permanently diverged from the physical stock ledger the moment a later
// sale consumed past what the (missing) layer accounted for.
func resolveSalesReturnLineCostTx(ctx context.Context, tx pgx.Tx, companyID string, sourceLineID *string) (unitCost, currency string, err error) {
	if sourceLineID == nil || strings.TrimSpace(*sourceLineID) == "" {
		return "", "", nil
	}
	rows, err := tx.Query(ctx, `
		SELECT c.quantity::text, c.unit_cost::text, c.currency
		  FROM stock_cost_consumptions c
		  JOIN stock_movements m ON m.company_id = c.company_id AND m.id = c.movement_id
		 WHERE c.company_id = $1 AND m.source_line_id = $2 AND m.direction = 'OUT'`,
		companyID, *sourceLineID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	totalQuantity := new(big.Rat)
	totalCost := new(big.Rat)
	lastCurrency := ""
	for rows.Next() {
		var quantityText, unitCostText, rowCurrency string
		if scanErr := rows.Scan(&quantityText, &unitCostText, &rowCurrency); scanErr != nil {
			return "", "", scanErr
		}
		quantity, ok := new(big.Rat).SetString(quantityText)
		if !ok {
			continue
		}
		cost, ok := new(big.Rat).SetString(unitCostText)
		if !ok {
			continue
		}
		totalQuantity.Add(totalQuantity, quantity)
		totalCost.Add(totalCost, new(big.Rat).Mul(quantity, cost))
		lastCurrency = rowCurrency
	}
	if err = rows.Err(); err != nil {
		return "", "", err
	}
	if totalQuantity.Sign() <= 0 {
		// The source sale's own consumption could not be found (for example,
		// it was itself an unpriced/provisional movement). Leaving UnitCost
		// empty here is safe: the shared inventory port's cost-layer trigger
		// opens a provisional layer and records an UNPRICED_RECEIPT event for
		// it rather than losing the quantity.
		return "", "", nil
	}
	weightedAverage := new(big.Rat).Quo(totalCost, totalQuantity)
	return weightedAverage.FloatString(8), lastCurrency, nil
}
