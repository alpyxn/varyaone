package inventory

import "time"

// Movement directions are deliberately strings at the domain boundary.  They
// map one-to-one to the database CHECK constraint and keep API handlers free
// to pass values received from JSON without leaking sqlc enums.
const (
	DirectionIn  = "IN"
	DirectionOut = "OUT"

	MovementPurchaseReceipt  = "PURCHASE_RECEIPT"
	MovementSalesDispatch    = "SALES_DISPATCH"
	MovementSalesReturn      = "SALES_RETURN"
	MovementPurchaseReturn   = "PURCHASE_RETURN"
	MovementTransferOut      = "TRANSFER_OUT"
	MovementTransferIn       = "TRANSFER_IN"
	MovementCountAdjustment  = "COUNT_ADJUSTMENT"
	MovementManualAdjustment = "MANUAL_ADJUSTMENT"
	MovementDamage           = "DAMAGE"
	MovementWaste            = "WASTE"
	MovementReconciliation   = "RECONCILIATION"
)

const (
	WarehouseStandard   = "STANDARD"
	WarehouseQuarantine = "QUARANTINE"
	WarehouseTransit    = "TRANSIT"
	WarehouseReturn     = "RETURN"
)

const (
	TransferResolutionDeliver = "DELIVER"
	TransferResolutionReturn  = "RETURN"
	TransferResolutionWaste   = "WASTE"
)

type Warehouse struct {
	ID                string    `json:"id"`
	CompanyID         string    `json:"company_id"`
	BranchID          *string   `json:"branch_id,omitempty"`
	Code              string    `json:"code"`
	Name              string    `json:"name"`
	Type              string    `json:"type"`
	Address           string    `json:"address"`
	ResponsibleUserID *string   `json:"responsible_user_id,omitempty"`
	UsesLocations     bool      `json:"uses_locations"`
	IsTransit         bool      `json:"is_transit"`
	IsSystem          bool      `json:"is_system"`
	IsActive          bool      `json:"is_active"`
	CanDelete         bool      `json:"can_delete"`
	Version           int64     `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type WarehouseInput struct {
	CompanyID         string `json:"company_id"`
	BranchID          string `json:"branch_id,omitempty"`
	Code              string `json:"code"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	Address           string `json:"address"`
	ResponsibleUserID string `json:"responsible_user_id,omitempty"`
	UsesLocations     bool   `json:"uses_locations"`
	IsActive          *bool  `json:"is_active,omitempty"`
	ActorUserID       string `json:"-"`
}

// WarehouseUpdateInput deliberately excludes branch and location settings.
// Those relationships are retained as backend data, but changing them is not
// part of the warehouse lifecycle operation.
type WarehouseUpdateInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Address  string `json:"address"`
	IsActive *bool  `json:"is_active,omitempty"`
}

type Location struct {
	ID          string    `json:"id"`
	CompanyID   string    `json:"company_id"`
	WarehouseID string    `json:"warehouse_id"`
	ParentID    *string   `json:"parent_id,omitempty"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	IsActive    bool      `json:"is_active"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type LocationInput struct {
	CompanyID   string `json:"company_id"`
	WarehouseID string `json:"warehouse_id"`
	ParentID    string `json:"parent_id,omitempty"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	IsActive    *bool  `json:"is_active,omitempty"`
	ActorUserID string `json:"-"`
}

type MovementInput struct {
	ID                   string         `json:"id,omitempty"`
	CompanyID            string         `json:"company_id"`
	WarehouseID          string         `json:"warehouse_id"`
	LocationID           string         `json:"location_id,omitempty"`
	ProductID            string         `json:"product_id"`
	VariantID            string         `json:"variant_id,omitempty"`
	LotID                string         `json:"lot_id,omitempty"`
	LotNumber            string         `json:"lot_number,omitempty"`
	LotManufacturedAt    *time.Time     `json:"lot_manufactured_at,omitempty"`
	LotExpiresAt         *time.Time     `json:"lot_expires_at,omitempty"`
	SupplierLotNo        string         `json:"supplier_lot_no,omitempty"`
	SerialID             string         `json:"serial_id,omitempty"`
	SerialNumber         string         `json:"serial_number,omitempty"`
	MovementType         string         `json:"movement_type"`
	Direction            string         `json:"direction"`
	Quantity             string         `json:"quantity"`
	EnteredQuantity      string         `json:"entered_quantity,omitempty"`
	UnitCode             string         `json:"unit_code,omitempty"`
	ConversionFactor     string         `json:"conversion_factor,omitempty"`
	UnitCost             string         `json:"unit_cost,omitempty"`
	Currency             string         `json:"currency,omitempty"`
	ReasonCode           string         `json:"reason_code"`
	ReasonDescription    string         `json:"reason_description,omitempty"`
	SourceType           string         `json:"source_type"`
	SourceID             string         `json:"source_id,omitempty"`
	SourceLineID         string         `json:"source_line_id,omitempty"`
	IdempotencyKey       string         `json:"idempotency_key"`
	ReversalOfID         string         `json:"reversal_of_id,omitempty"`
	ExpiryOverride       bool           `json:"expiry_override,omitempty"`
	ExpiryOverrideReason string         `json:"expiry_override_reason,omitempty"`
	Metadata             map[string]any `json:"metadata,omitempty"`
	ActorUserID          string         `json:"actor_user_id,omitempty"`
}

// StockMovementOperationInput is the command boundary for a single manual
// stock operation. The header is shared by every line; each line identifies a
// variant and its entered quantity/cost. The service posts one immutable
// stock_movements row per line in the same database transaction.
type StockMovementOperationInput struct {
	ID                string                       `json:"id,omitempty"`
	CompanyID         string                       `json:"company_id"`
	WarehouseID       string                       `json:"warehouse_id"`
	ProductID         string                       `json:"product_id"`
	MovementType      string                       `json:"movement_type"`
	Direction         string                       `json:"direction"`
	UnitCode          string                       `json:"unit_code,omitempty"`
	Currency          string                       `json:"currency,omitempty"`
	ReasonCode        string                       `json:"reason_code"`
	ReasonDescription string                       `json:"reason_description,omitempty"`
	IdempotencyKey    string                       `json:"idempotency_key"`
	Lines             []StockMovementOperationLine `json:"lines"`
	ActorUserID       string                       `json:"actor_user_id,omitempty"`
}

type StockMovementOperationLine struct {
	ID        string `json:"id,omitempty"`
	VariantID string `json:"variant_id"`
	Quantity  string `json:"quantity"`
	UnitCost  string `json:"unit_cost,omitempty"`
}

type StockMovementOperation struct {
	ID                string                         `json:"id"`
	CompanyID         string                         `json:"company_id"`
	WarehouseID       string                         `json:"warehouse_id"`
	WarehouseCode     string                         `json:"warehouse_code,omitempty"`
	WarehouseName     string                         `json:"warehouse_name,omitempty"`
	ProductID         string                         `json:"product_id"`
	ProductCode       string                         `json:"product_code,omitempty"`
	ProductName       string                         `json:"product_name,omitempty"`
	MovementType      string                         `json:"movement_type"`
	Direction         string                         `json:"direction"`
	UnitCode          string                         `json:"unit_code,omitempty"`
	StockUnit         string                         `json:"stock_unit,omitempty"`
	Currency          string                         `json:"currency,omitempty"`
	ReasonCode        string                         `json:"reason_code"`
	ReasonDescription string                         `json:"reason_description,omitempty"`
	IdempotencyKey    string                         `json:"-"`
	ActorUserID       *string                        `json:"actor_user_id,omitempty"`
	ActorName         string                         `json:"actor_name,omitempty"`
	PostedAt          time.Time                      `json:"posted_at"`
	Lines             []StockMovementOperationResult `json:"lines"`
}

type StockMovementOperationResult struct {
	ID             string         `json:"id"`
	LineNo         int            `json:"line_no"`
	MovementID     string         `json:"movement_id"`
	VariantID      string         `json:"variant_id"`
	VariantCode    string         `json:"variant_code,omitempty"`
	VariantDisplay map[string]any `json:"variant_display,omitempty"`
	Quantity       string         `json:"quantity"`
	BaseQuantity   string         `json:"base_quantity"`
	UnitCost       *string        `json:"unit_cost,omitempty"`
	Currency       *string        `json:"currency,omitempty"`
}

type Movement struct {
	ID                   string         `json:"id"`
	CompanyID            string         `json:"company_id"`
	WarehouseID          string         `json:"warehouse_id"`
	LocationID           *string        `json:"location_id,omitempty"`
	ProductID            string         `json:"product_id"`
	VariantID            *string        `json:"variant_id,omitempty"`
	LotID                *string        `json:"lot_id,omitempty"`
	SerialID             *string        `json:"serial_id,omitempty"`
	MovementType         string         `json:"movement_type"`
	Direction            string         `json:"direction"`
	Quantity             string         `json:"quantity"`
	BaseQuantity         string         `json:"base_quantity"`
	QuantityDelta        string         `json:"quantity_delta"`
	EnteredQuantity      string         `json:"entered_quantity,omitempty"`
	UnitCode             string         `json:"unit_code,omitempty"`
	StockUnit            string         `json:"stock_unit,omitempty"`
	ConversionFactor     string         `json:"conversion_factor,omitempty"`
	ProductCode          string         `json:"product_code,omitempty"`
	ProductName          string         `json:"product_name,omitempty"`
	VariantCode          string         `json:"variant_code,omitempty"`
	VariantDisplay       map[string]any `json:"variant_display,omitempty"`
	WarehouseCode        string         `json:"warehouse_code,omitempty"`
	WarehouseName        string         `json:"warehouse_name,omitempty"`
	LocationCode         string         `json:"location_code,omitempty"`
	LocationName         string         `json:"location_name,omitempty"`
	LotNumber            string         `json:"lot_number,omitempty"`
	SerialNumber         string         `json:"serial_number,omitempty"`
	UnitCost             *string        `json:"unit_cost,omitempty"`
	Currency             *string        `json:"currency,omitempty"`
	ReasonCode           string         `json:"reason_code"`
	ReasonDescription    string         `json:"reason_description,omitempty"`
	SourceType           string         `json:"source_type"`
	SourceID             string         `json:"source_id"`
	SourceDocumentNo     string         `json:"source_document_no,omitempty"`
	SourceDocumentType   string         `json:"source_document_type,omitempty"`
	Status               string         `json:"status"`
	SourceLineID         *string        `json:"source_line_id,omitempty"`
	IdempotencyKey       string         `json:"-"`
	ReversalOfID         *string        `json:"reversal_of_id,omitempty"`
	ReversedByID         *string        `json:"reversed_by_id,omitempty"`
	ExpiryOverride       bool           `json:"expiry_override"`
	ExpiryOverrideReason string         `json:"expiry_override_reason,omitempty"`
	Metadata             map[string]any `json:"metadata"`
	ActorUserID          *string        `json:"actor_user_id,omitempty"`
	ActorName            string         `json:"actor_name,omitempty"`
	PostedAt             time.Time      `json:"posted_at"`
}

type Position struct {
	ID                string         `json:"id"`
	CompanyID         string         `json:"company_id"`
	WarehouseID       string         `json:"warehouse_id"`
	LocationID        *string        `json:"location_id,omitempty"`
	ProductID         string         `json:"product_id"`
	VariantID         *string        `json:"variant_id,omitempty"`
	VariantCode       string         `json:"variant_code,omitempty"`
	VariantDisplay    map[string]any `json:"variant_display,omitempty"`
	IsAggregate       bool           `json:"is_aggregate,omitempty"`
	LotID             *string        `json:"lot_id,omitempty"`
	SerialID          *string        `json:"serial_id,omitempty"`
	PhysicalQuantity  string         `json:"physical_quantity"`
	ReservedQuantity  string         `json:"reserved_quantity"`
	AvailableQuantity string         `json:"available_quantity"`
}

type Lot struct {
	ID                string         `json:"id"`
	CompanyID         string         `json:"company_id"`
	ProductID         string         `json:"product_id"`
	LotNumber         string         `json:"lot_number"`
	AvailableQuantity string         `json:"available_quantity"`
	ManufacturedAt    *time.Time     `json:"manufactured_at,omitempty"`
	ExpiresAt         *time.Time     `json:"expires_at,omitempty"`
	SupplierReference string         `json:"supplier_reference"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         time.Time      `json:"created_at"`
}

type LotInput struct {
	CompanyID         string         `json:"company_id"`
	ProductID         string         `json:"product_id"`
	LotNumber         string         `json:"lot_number"`
	ManufacturedAt    *time.Time     `json:"manufactured_at,omitempty"`
	ExpiresAt         *time.Time     `json:"expires_at,omitempty"`
	SupplierReference string         `json:"supplier_reference,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	ActorUserID       string         `json:"-"`
}

type SerialNumber struct {
	ID                string    `json:"id"`
	CompanyID         string    `json:"company_id"`
	ProductID         string    `json:"product_id"`
	SerialNumber      string    `json:"serial_number"`
	Status            string    `json:"status"`
	ActiveWarehouseID *string   `json:"active_warehouse_id,omitempty"`
	ActiveLocationID  *string   `json:"active_location_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type SerialNumberInput struct {
	CompanyID         string `json:"company_id"`
	ProductID         string `json:"product_id"`
	SerialNumber      string `json:"serial_number"`
	Status            string `json:"status,omitempty"`
	ActiveWarehouseID string `json:"active_warehouse_id,omitempty"`
	ActiveLocationID  string `json:"active_location_id,omitempty"`
	ActorUserID       string `json:"-"`
}

type Transfer struct {
	ID                       string `json:"id"`
	CompanyID                string `json:"company_id"`
	TransferNo               string `json:"transfer_no"`
	TransferType             string `json:"transfer_type"`
	SourceWarehouseID        string `json:"source_warehouse_id"`
	SourceWarehouseName      string `json:"source_warehouse_name,omitempty"`
	DestinationWarehouseID   string `json:"destination_warehouse_id"`
	DestinationWarehouseName string `json:"destination_warehouse_name,omitempty"`
	// Transit is an internal routing implementation detail. It remains in the
	// aggregate for ledger commands but is never exposed in public JSON.
	TransitWarehouseID string         `json:"-"`
	State              string         `json:"state"`
	Version            int64          `json:"version"`
	RequestedAt        *time.Time     `json:"requested_at,omitempty"`
	ApprovedAt         *time.Time     `json:"approved_at,omitempty"`
	ShippedAt          *time.Time     `json:"shipped_at,omitempty"`
	ReceivedAt         *time.Time     `json:"received_at,omitempty"`
	ArrivalAt          *time.Time     `json:"arrival_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	Lines              []TransferLine `json:"lines"`
}

type TransferInput struct {
	CompanyID              string              `json:"company_id"`
	TransferNo             string              `json:"transfer_no"`
	TransferType           string              `json:"transfer_type,omitempty"`
	SourceWarehouseID      string              `json:"source_warehouse_id"`
	DestinationWarehouseID string              `json:"destination_warehouse_id"`
	TransitWarehouseID     string              `json:"transit_warehouse_id,omitempty"`
	RequestedBy            string              `json:"requested_by,omitempty"`
	Lines                  []TransferLineInput `json:"lines"`
	IdempotencyKey         string              `json:"-"`
}

type TransferLine struct {
	ID                    string         `json:"id"`
	LineNo                int            `json:"line_no"`
	ProductID             string         `json:"product_id"`
	ProductCode           string         `json:"product_code"`
	ProductName           string         `json:"product_name"`
	VariantID             *string        `json:"variant_id,omitempty"`
	VariantCode           string         `json:"variant_code,omitempty"`
	VariantDescription    string         `json:"variant_description,omitempty"`
	VariantDisplay        map[string]any `json:"variant_display,omitempty"`
	LotID                 *string        `json:"lot_id,omitempty"`
	SerialID              *string        `json:"serial_id,omitempty"`
	SourceLocationID      *string        `json:"source_location_id,omitempty"`
	DestinationLocationID *string        `json:"destination_location_id,omitempty"`
	Quantity              string         `json:"quantity"`
	ShippedQuantity       string         `json:"shipped_quantity"`
	ReceivedQuantity      string         `json:"received_quantity"`
	DamagedQuantity       string         `json:"damaged_quantity"`
	ResolvedQuantity      string         `json:"resolved_quantity"`
	DiscrepancyReason     string         `json:"discrepancy_reason,omitempty"`
}

type TransferLineInput struct {
	ProductID             string `json:"product_id"`
	VariantID             string `json:"variant_id,omitempty"`
	LotID                 string `json:"lot_id,omitempty"`
	SerialID              string `json:"serial_id,omitempty"`
	SourceLocationID      string `json:"source_location_id,omitempty"`
	DestinationLocationID string `json:"destination_location_id,omitempty"`
	Quantity              string `json:"quantity"`
}

type ReceiveLineInput struct {
	LineID           string `json:"line_id"`
	ReceivedQuantity string `json:"received_quantity"`
	DamagedQuantity  string `json:"damaged_quantity"`
	Reason           string `json:"reason"`
}

type TransferResolutionInput struct {
	LineID         string `json:"line_id"`
	ResolutionType string `json:"resolution_type"`
	Quantity       string `json:"quantity"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type StockCount struct {
	ID          string           `json:"id"`
	CompanyID   string           `json:"company_id"`
	WarehouseID string           `json:"warehouse_id"`
	State       string           `json:"state"`
	BlindCount  bool             `json:"blind_count"`
	SnapshotAt  *time.Time       `json:"snapshot_at,omitempty"`
	PostedAt    *time.Time       `json:"posted_at,omitempty"`
	Version     int64            `json:"version"`
	Lines       []StockCountLine `json:"lines"`
}

type StockCountInput struct {
	CompanyID   string `json:"company_id"`
	WarehouseID string `json:"warehouse_id"`
	BlindCount  bool   `json:"blind_count"`
	PostedBy    string `json:"posted_by,omitempty"`
}

type StockCountLine struct {
	ID                 string         `json:"id"`
	LineNo             int            `json:"line_no"`
	ProductID          string         `json:"product_id"`
	VariantID          *string        `json:"variant_id,omitempty"`
	VariantCode        string         `json:"variant_code,omitempty"`
	VariantDisplay     map[string]any `json:"variant_display,omitempty"`
	LocationID         *string        `json:"location_id,omitempty"`
	LotID              *string        `json:"lot_id,omitempty"`
	SerialID           *string        `json:"serial_id,omitempty"`
	SnapshotQuantity   *string        `json:"snapshot_quantity,omitempty"`
	CountedQuantity    *string        `json:"counted_quantity,omitempty"`
	ExpectedQuantity   *string        `json:"expected_quantity,omitempty"`
	DifferenceQuantity *string        `json:"difference_quantity,omitempty"`
}

type LotSuggestion struct {
	LotID        string     `json:"lot_id"`
	LotNumber    string     `json:"lot_number"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	AvailableQty string     `json:"available_quantity"`
}
