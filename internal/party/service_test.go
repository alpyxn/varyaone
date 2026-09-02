package party

import (
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCanonicalDecimalNormalizesTransportScale(t *testing.T) {
	value, err := money.ParseDecimal("10.0", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := canonicalDecimal(value, 4); got != "10.0000" {
		t.Fatalf("canonicalDecimal()=%q, want 10.0000", got)
	}
}

func TestBaseAmountRoundsToZeroDetectsTinyForeignMovement(t *testing.T) {
	if !baseAmountRoundsToZero("0.0001", "0", "0.0001") {
		t.Fatal("tiny foreign movement was not rejected at four base decimals")
	}
	if baseAmountRoundsToZero("10", "0", "1") {
		t.Fatal("ordinary movement was incorrectly classified as zero")
	}
}

func TestMapConstraintReturnsActionablePartyMessages(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		message    string
	}{
		{name: "tax number", constraint: "parties_tax_number_format", message: "vergi numarası 10 haneli olmalıdır"},
		{name: "identity number", constraint: "parties_identity_number_format", message: "T.C. kimlik numarası 11 haneli olmalıdır"},
		{name: "party code", constraint: "parties_company_code_unique", message: "cari kodu bu firmada zaten kullanılıyor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mapConstraint(&pgconn.PgError{ConstraintName: test.constraint})
			if !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("mapConstraint(%s) did not preserve validation class: %v", test.constraint, err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("mapConstraint(%s) message=%q, want substring %q", test.constraint, err, test.message)
			}
		})
	}
}

func TestValidateImmutableIdentity(t *testing.T) {
	tests := []struct {
		name    string
		input   Input
		code    string
		kind    string
		message string
	}{
		{name: "same identity", input: Input{Code: "CAR-0001", Kind: "ORGANIZATION"}, code: "CAR-0001", kind: "ORGANIZATION"},
		{name: "code cannot change", input: Input{Code: "CAR-0002", Kind: "ORGANIZATION"}, code: "CAR-0001", kind: "ORGANIZATION", message: "cari kodu oluşturulduktan sonra değiştirilemez"},
		{name: "kind cannot change", input: Input{Code: "CAR-0001", Kind: "PERSON"}, code: "CAR-0001", kind: "ORGANIZATION", message: "cari türü oluşturulduktan sonra değiştirilemez"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateImmutableIdentity(test.input, test.code, test.kind)
			if test.message == "" {
				if err != nil {
					t.Fatalf("validateImmutableIdentity returned unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, identity.ErrValidation) {
				t.Fatalf("validateImmutableIdentity did not preserve validation class: %v", err)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("validateImmutableIdentity message=%q, want substring %q", err, test.message)
			}
		})
	}
}

func TestNormalizeAndValidateDerivesListNameFromVisibleCariNames(t *testing.T) {
	tests := []struct {
		name  string
		input Input
		want  string
	}{
		{
			name:  "trade name has priority for organization",
			input: Input{Kind: "organization", IsCustomer: true, LegalName: "Resmî Unvan AŞ", TradeName: "Mağaza Adı", DefaultCurrency: "try"},
			want:  "Mağaza Adı",
		},
		{
			name:  "legal name is organization fallback",
			input: Input{Kind: "organization", IsCustomer: true, LegalName: "Resmî Unvan AŞ", DefaultCurrency: "try"},
			want:  "Resmî Unvan AŞ",
		},
		{
			name:  "person name is composed from first and last name",
			input: Input{Kind: "person", IsSupplier: true, FirstName: "Ayşe", LastName: "Yılmaz", DefaultCurrency: "try"},
			want:  "Ayşe Yılmaz",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			if err := normalizeAndValidate(&input); err != nil {
				t.Fatalf("normalizeAndValidate returned %v", err)
			}
			if input.DisplayName != test.want {
				t.Fatalf("display name=%q, want %q", input.DisplayName, test.want)
			}
		})
	}
}

func TestNormalizePartySearchQueryCreatesSafePrefixTokens(t *testing.T) {
	got := normalizePartySearchQuery("  CAR-0001   %25  _  ")
	if got != `car:* & 0001:* & 25:*` {
		t.Fatalf("normalizePartySearchQuery() = %q, want safe prefix tsquery", got)
	}
}

func TestNormalizePartySearchQueryFoldsTurkishAndLatinAccents(t *testing.T) {
	got := normalizePartySearchQuery("Resmî İstanbul / Çağrı Şirketi")
	if got != `resmi:* & istanbul:* & cagri:* & sirketi:*` {
		t.Fatalf("normalizePartySearchQuery() = %q, want accent-insensitive prefix tsquery", got)
	}
}

func TestNormalizePartySearchQueryBoundsBroadInput(t *testing.T) {
	got := normalizePartySearchQuery("bir iki üç dört beş altı yedi sekiz dokuz on onbir oniki onüç ondört onbeş onaltı onyedi")
	if strings.Count(got, ":*") != 16 {
		t.Fatalf("normalizePartySearchQuery() returned %d tokens, want 16", strings.Count(got, ":*"))
	}
}
