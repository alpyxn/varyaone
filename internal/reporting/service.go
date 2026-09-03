// Package reporting reads existing typed sales/purchasing/inventory/finance
// data live; it introduces no business tables of its own. v1 starts with
// live PostgreSQL queries per docs/domain.md, and every report
// here answers with company-scoped, permission-gated read models -- never a
// mutable cached balance.
package reporting

import (
	"context"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
)

type Service struct {
	pool    database.Querier
	finance *finance.Service
}

func NewService(pool database.Querier, financeService *finance.Service) *Service {
	if financeService == nil {
		financeService = finance.NewService(pool)
	}
	return &Service{pool: pool, finance: financeService}
}

func canRead(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission("reporting.read")
}

// StockValuationRow is one product/warehouse position's current cost-layer
// value. Value is the sum of every remaining, un-consumed layer quantity at
// its own base-currency cost plus every stock_cost_adjustments row against
// it (a landed-cost or invoice-price-difference correction never rewrites
// the layer itself). A position with only provisional (costless) layers
// still appears, with HasUnpricedStock set, so an incomplete valuation is
// visible rather than silently reported as zero-value real stock.
type StockValuationRow struct {
	WarehouseID      string `json:"warehouse_id"`
	WarehouseName    string `json:"warehouse_name"`
	ProductID        string `json:"product_id"`
	ProductName      string `json:"product_name"`
	Quantity         string `json:"quantity"`
	Value            string `json:"value"`
	BaseCurrency     string `json:"base_currency"`
	HasUnpricedStock bool   `json:"has_unpriced_stock"`
}

func (s *Service) StockValuation(ctx context.Context, session identity.Session) ([]StockValuationRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.name, p.id, p.name,
		       SUM(scl.remaining_quantity)::text,
		       SUM(scl.remaining_quantity * (COALESCE(scl.base_unit_cost,0) + COALESCE(adj.total,0)/scl.quantity))::numeric(24,4)::text,
		       COALESCE(MAX(scl.base_currency), c.base_currency),
		       bool_or(scl.is_provisional)
		  FROM stock_cost_layers scl
		  JOIN products p ON p.company_id=scl.company_id AND p.id=scl.product_id
		  JOIN warehouses w ON w.company_id=scl.company_id AND w.id=scl.warehouse_id
		  JOIN companies c ON c.id=scl.company_id
		  LEFT JOIN LATERAL (
		      SELECT SUM(a.base_amount) AS total FROM stock_cost_adjustments a
		       WHERE a.company_id=scl.company_id AND a.layer_id=scl.id
		  ) adj ON true
		 WHERE scl.company_id=$1 AND scl.remaining_quantity > 0
		 GROUP BY w.id, w.name, p.id, p.name, c.base_currency
		 ORDER BY w.name, p.name`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StockValuationRow{}
	for rows.Next() {
		var item StockValuationRow
		if err = rows.Scan(&item.WarehouseID, &item.WarehouseName, &item.ProductID, &item.ProductName, &item.Quantity, &item.Value, &item.BaseCurrency, &item.HasUnpricedStock); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// TopSellingProductRow ranks posted sales invoice product lines by base
// quantity sold in the given date range.
type TopSellingProductRow struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    string `json:"quantity"`
	Revenue     string `json:"revenue"`
}

func (s *Service) TopSellingProducts(ctx context.Context, session identity.Session, from, to time.Time, limit int) ([]TopSellingProductRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	if limit < 1 || limit > 200 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT l.product_id, p.name, SUM(l.base_quantity)::text, SUM(l.net_amount)::numeric(24,4)::text
		  FROM sales_invoice_lines l
		  JOIN sales_invoices h ON h.company_id=l.company_id AND h.id=l.document_id
		  JOIN products p ON p.company_id=l.company_id AND p.id=l.product_id
		 WHERE l.company_id=$1 AND h.status='POSTED' AND l.line_type='PRODUCT'
		   AND h.document_date BETWEEN $2 AND $3
		 GROUP BY l.product_id, p.name
		 ORDER BY SUM(l.base_quantity) DESC
		 LIMIT $4`, session.CurrentCompanyID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TopSellingProductRow{}
	for rows.Next() {
		var item TopSellingProductRow
		if err = rows.Scan(&item.ProductID, &item.ProductName, &item.Quantity, &item.Revenue); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SalesProfitabilityRow is one posted sales invoice's revenue against the
// FIFO cost of the goods that invoice's own dispatch/invoice actually
// consumed (stock_cost_consumptions on the OUT movement each product line
// posted). A line whose consumption is still provisional or unpriced is
// reported with HasUnpricedCost so an understated cost is never presented
// as a confirmed margin.
type SalesProfitabilityRow struct {
	DocumentID      string `json:"document_id"`
	DocumentNo      string `json:"document_no"`
	PartyName       string `json:"party_name"`
	Revenue         string `json:"revenue"`
	Cost            string `json:"cost"`
	GrossProfit     string `json:"gross_profit"`
	HasUnpricedCost bool   `json:"has_unpriced_cost"`
}

func (s *Service) SalesProfitability(ctx context.Context, session identity.Session, from, to time.Time) ([]SalesProfitabilityRow, error) {
	if !canRead(session) || !session.HasPermission("sales.cost.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT h.id, h.document_no, COALESCE(pt.display_name,''),
		       SUM(l.net_amount)::numeric(24,4)::text,
		       COALESCE(SUM(cogs.total),0)::numeric(24,4)::text,
		       (SUM(l.net_amount) - COALESCE(SUM(cogs.total),0))::numeric(24,4)::text,
		       bool_or(l.line_type='PRODUCT' AND cogs.total IS NULL)
		  FROM sales_invoices h
		  JOIN parties pt ON pt.company_id=h.company_id AND pt.id=h.party_id
		  JOIN sales_invoice_lines l ON l.company_id=h.company_id AND l.document_id=h.id
		  LEFT JOIN LATERAL (
		      SELECT SUM(c.quantity * COALESCE(c.base_unit_cost, c.unit_cost)) AS total
		        FROM stock_cost_consumptions c
		        JOIN stock_movements m ON m.company_id=c.company_id AND m.id=c.movement_id
		       WHERE m.company_id=l.company_id AND m.source_line_id=l.id AND m.direction='OUT'
		  ) cogs ON l.line_type='PRODUCT'
		 WHERE h.company_id=$1 AND h.status='POSTED' AND h.document_date BETWEEN $2 AND $3
		 GROUP BY h.id, h.document_no, h.document_date, pt.display_name
		 ORDER BY h.document_date DESC, h.document_no DESC`, session.CurrentCompanyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SalesProfitabilityRow{}
	for rows.Next() {
		var item SalesProfitabilityRow
		if err = rows.Scan(&item.DocumentID, &item.DocumentNo, &item.PartyName, &item.Revenue, &item.Cost, &item.GrossProfit, &item.HasUnpricedCost); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// OverdueRow is one posted sales invoice whose due date has passed and which
// still has a nonzero amount due. AmountDue is finance.ReadDocumentSettlement's
// own derivation (original minus reversal minus return minus allocation), the
// same value the invoice's own detail screen would show -- never a second,
// possibly-diverging computation.
type OverdueRow struct {
	DocumentID  string `json:"document_id"`
	DocumentNo  string `json:"document_no"`
	PartyName   string `json:"party_name"`
	DueDate     string `json:"due_date"`
	DaysOverdue int    `json:"days_overdue"`
	AmountDue   string `json:"amount_due"`
	Currency    string `json:"currency"`
}

func (s *Service) OverdueReceivables(ctx context.Context, session identity.Session, asOf time.Time) ([]OverdueRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT oi.document_id, h.document_no, pt.display_name, oi.due_date::text, oi.currency
		  FROM finance_invoice_open_items oi
		  JOIN sales_invoices h ON h.company_id=oi.company_id AND h.id=oi.document_id
		  JOIN parties pt ON pt.company_id=oi.company_id AND pt.id=oi.party_id
		 WHERE oi.company_id=$1 AND oi.side='RECEIVABLE' AND oi.due_date IS NOT NULL AND oi.due_date < $2
		 ORDER BY oi.due_date`, session.CurrentCompanyID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type candidate struct {
		documentID, documentNo, partyName, dueDate, currency string
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.documentID, &c.documentNo, &c.partyName, &c.dueDate, &c.currency); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	items := make([]OverdueRow, 0, len(candidates))
	for _, c := range candidates {
		settlement, settleErr := s.finance.ReadDocumentSettlement(ctx, session.CurrentCompanyID, c.documentID)
		if settleErr != nil {
			return nil, settleErr
		}
		outstanding := settlement.OutstandingAmount()
		if outstanding == "" || outstanding == "0" || outstanding == "0.0000" {
			continue
		}
		due, parseErr := time.Parse("2006-01-02", c.dueDate)
		daysOverdue := 0
		if parseErr == nil {
			daysOverdue = int(asOf.Sub(due).Hours() / 24)
		}
		items = append(items, OverdueRow{
			DocumentID: c.documentID, DocumentNo: c.documentNo, PartyName: c.partyName,
			DueDate: c.dueDate, DaysOverdue: daysOverdue, AmountDue: outstanding, Currency: c.currency,
		})
	}
	return items, nil
}
