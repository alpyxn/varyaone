package reporting

import (
	"context"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
)

// ReportFilters carries the optional, semantically-shared dimensions a report
// can be narrowed by. An empty string means "no filter on this dimension";
// every query applies it as ($n = ” OR col = $n::uuid) so a blank value is a
// no-op rather than a match against the empty string.
type ReportFilters struct {
	WarehouseID  string
	ProductID    string
	PartyID      string
	CategoryID   string
	CurrencyCode string
	SalesRepID   string
	AccountID    string
	MovementType string
	OnlyNonZero  bool
}

// PartyBalanceRow is one party's net ledger position in a single currency.
// SignedAmount is debit minus credit in the entry currency (positive means the
// party owes us); BaseSignedAmount is the same in the company base currency.
type PartyBalanceRow struct {
	PartyID          string `json:"party_id"`
	PartyName        string `json:"party_name"`
	Currency         string `json:"currency"`
	SignedAmount     string `json:"signed_amount"`
	BaseCurrency     string `json:"base_currency"`
	BaseSignedAmount string `json:"base_signed_amount"`
}

func (s *Service) PartyBalances(ctx context.Context, session identity.Session, filters ReportFilters) ([]PartyBalanceRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.party_id, pt.display_name, e.currency,
		       SUM(e.signed_amount)::numeric(24,4)::text,
		       MAX(e.base_currency),
		       SUM(e.base_signed_amount)::numeric(24,4)::text
		  FROM party_ledger_balance_effects e
		  JOIN parties pt ON pt.company_id=e.company_id AND pt.id=e.party_id
		 WHERE e.company_id=$1
		   AND ($2='' OR e.party_id=$2::uuid)
		   AND ($3='' OR e.currency=$3)
		 GROUP BY e.party_id, pt.display_name, e.currency
		 HAVING ($4::bool = false OR SUM(e.signed_amount) <> 0)
		 ORDER BY pt.display_name, e.currency`,
		session.CurrentCompanyID, filters.PartyID, filters.CurrencyCode, filters.OnlyNonZero)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PartyBalanceRow{}
	for rows.Next() {
		var item PartyBalanceRow
		if err = rows.Scan(&item.PartyID, &item.PartyName, &item.Currency, &item.SignedAmount, &item.BaseCurrency, &item.BaseSignedAmount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// StockStatusRow is one product/warehouse position's on-hand, reserved and
// available quantity, aggregated across locations/lots/serials.
type StockStatusRow struct {
	WarehouseID       string `json:"warehouse_id"`
	WarehouseName     string `json:"warehouse_name"`
	ProductID         string `json:"product_id"`
	ProductName       string `json:"product_name"`
	CategoryName      string `json:"category_name"`
	PhysicalQuantity  string `json:"physical_quantity"`
	ReservedQuantity  string `json:"reserved_quantity"`
	AvailableQuantity string `json:"available_quantity"`
}

func (s *Service) StockStatus(ctx context.Context, session identity.Session, filters ReportFilters) ([]StockStatusRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.name, p.id, p.name, COALESCE(cat.name,''),
		       SUM(sp.physical_quantity)::numeric(24,4)::text,
		       SUM(sp.reserved_quantity)::numeric(24,4)::text,
		       SUM(sp.available_quantity)::numeric(24,4)::text
		  FROM stock_positions sp
		  JOIN products p ON p.company_id=sp.company_id AND p.id=sp.product_id
		  JOIN warehouses w ON w.company_id=sp.company_id AND w.id=sp.warehouse_id
		  LEFT JOIN product_categories cat ON cat.company_id=p.company_id AND cat.id=p.category_id
		 WHERE sp.company_id=$1
		   AND ($2='' OR sp.warehouse_id=$2::uuid)
		   AND ($3='' OR sp.product_id=$3::uuid)
		   AND ($4='' OR p.category_id=$4::uuid)
		 GROUP BY w.id, w.name, p.id, p.name, cat.name
		 HAVING ($5::bool = false OR SUM(sp.physical_quantity) <> 0)
		 ORDER BY w.name, p.name`,
		session.CurrentCompanyID, filters.WarehouseID, filters.ProductID, filters.CategoryID, filters.OnlyNonZero)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StockStatusRow{}
	for rows.Next() {
		var item StockStatusRow
		if err = rows.Scan(&item.WarehouseID, &item.WarehouseName, &item.ProductID, &item.ProductName, &item.CategoryName, &item.PhysicalQuantity, &item.ReservedQuantity, &item.AvailableQuantity); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// StockMovementRow is one posted stock movement in the requested window.
type StockMovementRow struct {
	PostedAt      string `json:"posted_at"`
	MovementType  string `json:"movement_type"`
	Direction     string `json:"direction"`
	WarehouseName string `json:"warehouse_name"`
	ProductName   string `json:"product_name"`
	Quantity      string `json:"quantity"`
	UnitCost      string `json:"unit_cost"`
	Reason        string `json:"reason"`
	SourceType    string `json:"source_type"`
}

func (s *Service) StockMovements(ctx context.Context, session identity.Session, from, to time.Time, filters ReportFilters) ([]StockMovementRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.posted_at::text, m.movement_type, m.direction, w.name, p.name,
		       m.quantity::numeric(24,4)::text,
		       COALESCE(m.unit_cost::numeric(24,4)::text,''),
		       m.reason_description, m.source_type
		  FROM stock_movements m
		  JOIN products p ON p.company_id=m.company_id AND p.id=m.product_id
		  JOIN warehouses w ON w.company_id=m.company_id AND w.id=m.warehouse_id
		 WHERE m.company_id=$1 AND m.posted_at >= $2 AND m.posted_at < $3
		   AND ($4='' OR m.warehouse_id=$4::uuid)
		   AND ($5='' OR m.product_id=$5::uuid)
		   AND ($6='' OR m.movement_type=$6)
		 ORDER BY m.posted_at DESC
		 LIMIT 5000`,
		session.CurrentCompanyID, from, to.AddDate(0, 0, 1), filters.WarehouseID, filters.ProductID, filters.MovementType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []StockMovementRow{}
	for rows.Next() {
		var item StockMovementRow
		if err = rows.Scan(&item.PostedAt, &item.MovementType, &item.Direction, &item.WarehouseName, &item.ProductName, &item.Quantity, &item.UnitCost, &item.Reason, &item.SourceType); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SalesDocumentRow is one posted sales invoice header in the requested window.
type SalesDocumentRow struct {
	DocumentDate  string `json:"document_date"`
	DocumentNo    string `json:"document_no"`
	PartyName     string `json:"party_name"`
	Currency      string `json:"currency"`
	Subtotal      string `json:"subtotal"`
	DiscountTotal string `json:"discount_total"`
	TaxTotal      string `json:"tax_total"`
	GrandTotal    string `json:"grand_total"`
	SalesRep      string `json:"sales_rep"`
}

func (s *Service) SalesList(ctx context.Context, session identity.Session, from, to time.Time, filters ReportFilters) ([]SalesDocumentRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT h.document_date::text, h.document_no, pt.display_name, h.currency_code,
		       h.subtotal::numeric(24,4)::text, h.discount_total::numeric(24,4)::text,
		       h.tax_total::numeric(24,4)::text, h.grand_total::numeric(24,4)::text,
		       COALESCE(u.display_name,'')
		  FROM sales_invoices h
		  JOIN parties pt ON pt.company_id=h.company_id AND pt.id=h.party_id
		  LEFT JOIN users u ON u.id=h.sales_rep_user_id
		 WHERE h.company_id=$1 AND h.status='POSTED' AND h.document_date BETWEEN $2 AND $3
		   AND ($4='' OR h.party_id=$4::uuid)
		   AND ($5='' OR h.currency_code=$5)
		   AND ($6='' OR h.sales_rep_user_id=$6::uuid)
		 ORDER BY h.document_date DESC, h.document_no DESC`,
		session.CurrentCompanyID, from, to, filters.PartyID, filters.CurrencyCode, filters.SalesRepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SalesDocumentRow{}
	for rows.Next() {
		var item SalesDocumentRow
		if err = rows.Scan(&item.DocumentDate, &item.DocumentNo, &item.PartyName, &item.Currency, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.GrandTotal, &item.SalesRep); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PurchaseDocumentRow is one posted purchase invoice header in the window.
type PurchaseDocumentRow struct {
	DocumentDate  string `json:"document_date"`
	DocumentNo    string `json:"document_no"`
	PartyName     string `json:"party_name"`
	Currency      string `json:"currency"`
	Subtotal      string `json:"subtotal"`
	DiscountTotal string `json:"discount_total"`
	TaxTotal      string `json:"tax_total"`
	PayableTotal  string `json:"payable_total"`
}

func (s *Service) PurchaseList(ctx context.Context, session identity.Session, from, to time.Time, filters ReportFilters) ([]PurchaseDocumentRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT h.invoice_date::text, h.invoice_no, pt.display_name, h.currency,
		       h.subtotal::numeric(24,4)::text, h.discount_total::numeric(24,4)::text,
		       h.tax_total::numeric(24,4)::text, h.payable_total::numeric(24,4)::text
		  FROM purchase_invoices h
		  JOIN parties pt ON pt.company_id=h.company_id AND pt.id=h.supplier_id
		 WHERE h.company_id=$1 AND h.status='POSTED' AND h.invoice_date BETWEEN $2 AND $3
		   AND ($4='' OR h.supplier_id=$4::uuid)
		   AND ($5='' OR h.currency=$5)
		 ORDER BY h.invoice_date DESC, h.invoice_no DESC`,
		session.CurrentCompanyID, from, to, filters.PartyID, filters.CurrencyCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PurchaseDocumentRow{}
	for rows.Next() {
		var item PurchaseDocumentRow
		if err = rows.Scan(&item.DocumentDate, &item.DocumentNo, &item.PartyName, &item.Currency, &item.Subtotal, &item.DiscountTotal, &item.TaxTotal, &item.PayableTotal); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// AccountMovementRow is one cash or bank account movement in the window.
type AccountMovementRow struct {
	TransactionDate string `json:"transaction_date"`
	AccountName     string `json:"account_name"`
	Currency        string `json:"currency"`
	MovementKind    string `json:"movement_kind"`
	Direction       string `json:"direction"`
	Amount          string `json:"amount"`
	Description     string `json:"description"`
	SourceType      string `json:"source_type"`
}

func (s *Service) accountMovements(ctx context.Context, session identity.Session, accountType string, from, to time.Time, filters ReportFilters) ([]AccountMovementRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT mv.transaction_date::text, a.name, a.currency, mv.movement_kind, mv.direction,
		       mv.amount::numeric(24,4)::text, mv.description, mv.source_type
		  FROM finance_account_movements mv
		  JOIN finance_accounts a ON a.company_id=mv.company_id AND a.id=mv.account_id
		 WHERE mv.company_id=$1 AND a.account_type=$2
		   AND mv.transaction_date BETWEEN $3 AND $4
		   AND ($5='' OR mv.account_id=$5::uuid)
		 ORDER BY mv.transaction_date DESC, mv.posted_at DESC
		 LIMIT 5000`,
		session.CurrentCompanyID, accountType, from, to, filters.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AccountMovementRow{}
	for rows.Next() {
		var item AccountMovementRow
		if err = rows.Scan(&item.TransactionDate, &item.AccountName, &item.Currency, &item.MovementKind, &item.Direction, &item.Amount, &item.Description, &item.SourceType); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CashMovements(ctx context.Context, session identity.Session, from, to time.Time, filters ReportFilters) ([]AccountMovementRow, error) {
	if !canRead(session) || !session.HasPermission("finance.cash_movement.read") {
		return nil, identity.ErrForbidden
	}
	return s.accountMovements(ctx, session, "CASH", from, to, filters)
}

func (s *Service) BankMovements(ctx context.Context, session identity.Session, from, to time.Time, filters ReportFilters) ([]AccountMovementRow, error) {
	if !canRead(session) || !session.HasPermission("finance.bank_movement.read") {
		return nil, identity.ErrForbidden
	}
	return s.accountMovements(ctx, session, "BANK", from, to, filters)
}

// OverduePayables is the supplier-side mirror of OverdueReceivables: posted
// purchase documents whose due date has passed and which still carry a nonzero
// amount due, per finance.ReadDocumentSettlement.
func (s *Service) OverduePayables(ctx context.Context, session identity.Session, asOf time.Time) ([]OverdueRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT oi.document_id, COALESCE(d.document_no,''), pt.display_name, oi.due_date::text, oi.currency
		  FROM finance_invoice_open_items oi
		  LEFT JOIN documents d ON d.company_id=oi.company_id AND d.id=oi.document_id
		  JOIN parties pt ON pt.company_id=oi.company_id AND pt.id=oi.party_id
		 WHERE oi.company_id=$1 AND oi.side='PAYABLE' AND oi.due_date IS NOT NULL AND oi.due_date < $2
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
		if settlement.AmountDue == "" || settlement.AmountDue == "0" || settlement.AmountDue == "0.0000" {
			continue
		}
		due, parseErr := time.Parse("2006-01-02", c.dueDate)
		daysOverdue := 0
		if parseErr == nil {
			daysOverdue = int(asOf.Sub(due).Hours() / 24)
		}
		items = append(items, OverdueRow{
			DocumentID: c.documentID, DocumentNo: c.documentNo, PartyName: c.partyName,
			DueDate: c.dueDate, DaysOverdue: daysOverdue, AmountDue: settlement.AmountDue, Currency: c.currency,
		})
	}
	return items, nil
}

// TaxSummaryRow aggregates posted document lines by their effective tax rate.
type TaxSummaryRow struct {
	Rate              string `json:"rate"`
	TaxBase           string `json:"tax_base"`
	TaxAmount         string `json:"tax_amount"`
	WithholdingAmount string `json:"withholding_amount"`
}

// TaxSummary groups posted sales or purchase invoice lines by tax rate.
// direction is "SALES" or "PURCHASE"; any other value defaults to "SALES".
func (s *Service) TaxSummary(ctx context.Context, session identity.Session, from, to time.Time, direction string) ([]TaxSummaryRow, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `
		SELECT COALESCE(NULLIF(l.tax_snapshot->>'rate',''),'0') AS rate,
		       SUM(l.net_amount)::numeric(24,4)::text,
		       SUM(l.tax_amount)::numeric(24,4)::text,
		       SUM(l.withholding_amount)::numeric(24,4)::text
		  FROM sales_invoice_lines l
		  JOIN sales_invoices h ON h.company_id=l.company_id AND h.id=l.document_id
		 WHERE l.company_id=$1 AND h.status='POSTED' AND h.document_date BETWEEN $2 AND $3
		 GROUP BY rate
		 ORDER BY rate`
	if direction == "PURCHASE" {
		query = `
			SELECT CASE WHEN l.tax_base > 0
			            THEN round(l.tax_amount/l.tax_base*100, 2)::text
			            ELSE '0' END AS rate,
			       SUM(l.tax_base)::numeric(24,4)::text,
			       SUM(l.tax_amount)::numeric(24,4)::text,
			       SUM(l.withholding_amount)::numeric(24,4)::text
			  FROM purchase_invoice_lines l
			  JOIN purchase_invoices h ON h.company_id=l.company_id AND h.id=l.invoice_id
			 WHERE l.company_id=$1 AND h.status='POSTED' AND h.invoice_date BETWEEN $2 AND $3
			 GROUP BY rate
			 ORDER BY rate`
	}
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaxSummaryRow{}
	for rows.Next() {
		var item TaxSummaryRow
		if err = rows.Scan(&item.Rate, &item.TaxBase, &item.TaxAmount, &item.WithholdingAmount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
