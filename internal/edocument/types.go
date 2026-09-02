package edocument

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

type DocumentType string

const (
	DocumentInvoice  DocumentType = "E_INVOICE"
	DocumentArchive  DocumentType = "E_ARCHIVE"
	DocumentDispatch DocumentType = "E_DISPATCH"
)

type Direction string

const (
	Outgoing Direction = "OUTGOING"
	Incoming Direction = "INCOMING"
)

type Status string

const (
	Draft           Status = "DRAFT"
	Queued          Status = "QUEUED"
	Submitting      Status = "SUBMITTING"
	Submitted       Status = "SUBMITTED"
	Accepted        Status = "ACCEPTED"
	Rejected        Status = "REJECTED"
	CancelRequested Status = "CANCEL_REQUESTED"
	Cancelled       Status = "CANCELLED"
	Failed          Status = "FAILED"
)

var (
	ErrNotFound            = errors.New("e-document not found")
	ErrInvalidTransition   = errors.New("invalid e-document lifecycle transition")
	ErrProviderUnavailable = errors.New("e-document provider unavailable")
	ErrProviderRejected    = errors.New("e-document provider rejected request")
	ErrSensitiveData       = errors.New("sensitive provider data is not accepted")
)

type Address struct {
	Name        string `json:"name,omitempty"`
	AddressLine string `json:"address_line,omitempty"`
	District    string `json:"district,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

type Party struct {
	ID        string  `json:"id,omitempty"`
	Code      string  `json:"code,omitempty"`
	TradeName string  `json:"trade_name,omitempty"`
	LegalName string  `json:"legal_name,omitempty"`
	TaxNumber string  `json:"tax_number,omitempty"`
	TaxOffice string  `json:"tax_office,omitempty"`
	Address   Address `json:"address,omitempty"`
}

type Line struct {
	Code        string `json:"code,omitempty"`
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
	UnitPrice   string `json:"unit_price"`
	TaxRate     string `json:"tax_rate,omitempty"`
	LineTotal   string `json:"line_total"`
}

// CanonicalDocument is deliberately provider-neutral. Provider response data
// belongs to Document.ProviderResult and is never embedded in this payload.
type CanonicalDocument struct {
	SourceType   string       `json:"source_type"`
	SourceID     string       `json:"source_id"`
	DocumentType DocumentType `json:"document_type"`
	Direction    Direction    `json:"direction"`
	DocumentDate string       `json:"document_date"`
	Currency     string       `json:"currency"`
	DocumentNo   string       `json:"document_no,omitempty"`
	Supplier     Party        `json:"supplier,omitempty"`
	Customer     Party        `json:"customer,omitempty"`
	Lines        []Line       `json:"lines"`
	Subtotal     string       `json:"subtotal"`
	TaxTotal     string       `json:"tax_total"`
	GrandTotal   string       `json:"grand_total"`
	Notes        string       `json:"notes,omitempty"`
}

type ProviderResult struct {
	ProviderKey string `json:"provider_key"`
	ExternalID  string `json:"external_id,omitempty"`
	Status      Status `json:"status"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
}

type Document struct {
	ID             string            `json:"id"`
	CompanyID      string            `json:"company_id"`
	ProviderKey    string            `json:"provider_key"`
	Status         Status            `json:"status"`
	Version        int64             `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Payload        CanonicalDocument `json:"payload"`
	ProviderResult *ProviderResult   `json:"provider_result,omitempty"`
}

type CreateInput struct {
	ProviderKey string            `json:"provider_key"`
	Payload     CanonicalDocument `json:"payload"`
}

type ListOptions struct {
	Status    Status
	Type      DocumentType
	Direction Direction
	Limit     int
}

type ListResult struct {
	Items []Document `json:"items"`
}

type Provider interface {
	Key() string
	Submit(context.Context, CanonicalDocument) (ProviderResult, error)
	Cancel(context.Context, Document, string) (ProviderResult, error)
}

func NewMockProvider() Provider { return mockProvider{} }

type mockProvider struct{}

func (mockProvider) Key() string { return "mock" }

func (mockProvider) Submit(_ context.Context, payload CanonicalDocument) (ProviderResult, error) {
	if strings.Contains(strings.ToLower(payload.Notes), "reject") {
		return ProviderResult{ProviderKey: "mock", Status: Rejected, Code: "MOCK_REJECTED", Message: "Mock sağlayıcı belgeyi reddetti."}, nil
	}
	return ProviderResult{ProviderKey: "mock", ExternalID: "MOCK-" + stableID(payload.SourceID), Status: Accepted, Code: "MOCK_ACCEPTED", Message: "Mock sağlayıcı belgeyi kabul etti."}, nil
}

func (mockProvider) Cancel(_ context.Context, document Document, _ string) (ProviderResult, error) {
	return ProviderResult{ProviderKey: "mock", ExternalID: "MOCK-CANCEL-" + stableID(document.ID), Status: Cancelled, Code: "MOCK_CANCELLED", Message: "Mock sağlayıcı iptal talebini kabul etti."}, nil
}

func stableID(value string) string {
	if parsed, err := uuid.Parse(value); err == nil {
		return strings.ToUpper(strings.ReplaceAll(parsed.String()[:8], "-", ""))
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) > 12 {
		return value[:12]
	}
	if value == "" {
		return "DOCUMENT"
	}
	return value
}

func validateCreate(input CreateInput) error {
	p := input.Payload
	if input.ProviderKey == "" || p.SourceType == "" || p.SourceID == "" || p.Currency == "" || p.DocumentDate == "" {
		return fmt.Errorf("%w: kaynak, sağlayıcı, tarih ve para birimi zorunludur", identity.ErrValidation)
	}
	if p.DocumentType != DocumentInvoice && p.DocumentType != DocumentArchive && p.DocumentType != DocumentDispatch {
		return fmt.Errorf("%w: desteklenmeyen e-belge türü", identity.ErrValidation)
	}
	if p.Direction != Outgoing && p.Direction != Incoming {
		return fmt.Errorf("%w: belge yönü geçersiz", identity.ErrValidation)
	}
	if len(p.Lines) == 0 {
		return fmt.Errorf("%w: belge en az bir satır içermelidir", identity.ErrValidation)
	}
	if strings.Contains(strings.ToLower(input.ProviderKey), "secret") || strings.Contains(strings.ToLower(input.ProviderKey), "token") {
		return ErrSensitiveData
	}
	return nil
}

func canTransition(from, to Status) bool {
	switch from {
	case Draft:
		return to == Queued
	case Queued:
		return to == Submitting || to == Failed
	case Submitting:
		return to == Submitted || to == Accepted || to == Rejected || to == Failed
	case Submitted:
		return to == Accepted || to == Rejected || to == CancelRequested || to == Failed
	case Accepted:
		return to == CancelRequested
	case CancelRequested:
		return to == Cancelled || to == Failed
	case Rejected, Failed:
		return to == Queued
	default:
		return false
	}
}
