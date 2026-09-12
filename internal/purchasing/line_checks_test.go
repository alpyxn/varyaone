package purchasing

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// countingRow / countingQuerier stand in for the transaction: they count the
// validation reads a save issues without needing a database, which is the
// thing under test here. Whether the read itself is correct is covered by the
// integration tests that run it against real rows.
type countingRow struct {
	kind  string
	flags []bool
	err   error
}

// Scan fills the destinations in order: the product probe reads a kind and two
// flags (is_active, variants_enabled), the scope probe reads one. Filling them
// positionally rather than by type is what lets a test say "active, without
// variants" — the two flags mean opposite things.
func (r countingRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	next := 0
	for _, target := range dest {
		switch typed := target.(type) {
		case *string:
			*typed = r.kind
		case *bool:
			if next < len(r.flags) {
				*typed = r.flags[next]
			}
			next++
		}
	}
	return nil
}

type countingQuerier struct {
	queries int
	kind    string
	flags   []bool
	err     error
}

func (q *countingQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	q.queries++
	return countingRow{kind: q.kind, flags: q.flags, err: q.err}
}

// activeProduct answers the product probe with "physical, active, no variants".
func activeProduct() *countingQuerier {
	return &countingQuerier{kind: "PHYSICAL", flags: []bool{true, false}}
}

// The point of the memo: a document that names the same product on every line
// asks the database once, not once per line. A hundred-line receipt of one
// product used to issue a hundred identical reads.
func TestRepeatedProductIsCheckedOnce(t *testing.T) {
	for _, lines := range []int{1, 10, 100} {
		querier := activeProduct()
		checks := newPurchaseLineChecks()
		for index := 0; index < lines; index++ {
			if err := checks.product(context.Background(), querier, "c1", "p1", "", "PRODUCT"); err != nil {
				t.Fatalf("%d satır: %v", lines, err)
			}
		}
		if querier.queries != 1 {
			t.Fatalf("%d satırda %d sorgu, 1 bekleniyordu", lines, querier.queries)
		}
	}
}

// And it stays a memo, not a blanket: every distinguishing part of the key is
// a different question and must be asked again.
func TestEachDistinctKeyIsCheckedSeparately(t *testing.T) {
	// Only the product probe runs here: no variant is named, so the two
	// variant reads never happen and the count is one per distinct key.
	querier := activeProduct()
	checks := newPurchaseLineChecks()
	ctx := context.Background()
	cases := [][4]string{
		{"c1", "p1", "", "PRODUCT"},
		{"c1", "p2", "", "PRODUCT"}, // başka ürün
		{"c2", "p1", "", "PRODUCT"}, // başka şirket
		{"c1", "p1", "", "PRODUCT"}, // tekrar: sorulmaz
		{"c1", "p2", "", "PRODUCT"}, // tekrar: sorulmaz
	}
	for _, key := range cases {
		if err := checks.product(ctx, querier, key[0], key[1], key[2], key[3]); err != nil {
			t.Fatal(err)
		}
	}
	if querier.queries != 3 {
		t.Fatalf("%d sorgu, 3 bekleniyordu", querier.queries)
	}

	// Satır türü de anahtarın parçası: aynı ürünün hizmet satırı ayrı bir
	// sorudur ve ürün kartı buna "hayır" der.
	service := activeProduct()
	serviceChecks := newPurchaseLineChecks()
	if err := serviceChecks.product(ctx, service, "c1", "p1", "", "PRODUCT"); err != nil {
		t.Fatal(err)
	}
	if err := serviceChecks.product(ctx, service, "c1", "p1", "", "SERVICE"); err == nil {
		t.Fatal("fiziksel ürün hizmet satırında kabul edildi")
	}
	if service.queries != 2 {
		t.Fatalf("%d sorgu, 2 bekleniyordu", service.queries)
	}
}

// A failure is never remembered. Remembering one would be harmless today,
// because the save stops at the line that produced it — but it would make the
// memo's meaning "the last answer" instead of "a proven yes", and a later
// caller that retries would then get a cached refusal.
func TestFailureIsNotRemembered(t *testing.T) {
	// Pasif ürün: kart var ama kullanılamaz.
	querier := &countingQuerier{kind: "PHYSICAL", flags: []bool{false, false}}
	checks := newPurchaseLineChecks()
	ctx := context.Background()
	first := checks.product(ctx, querier, "c1", "p1", "", "PRODUCT")
	if first == nil {
		t.Fatal("geçersiz ürün kabul edildi")
	}
	second := checks.product(ctx, querier, "c1", "p1", "", "PRODUCT")
	if second == nil {
		t.Fatal("geçersiz ürün ikinci çağrıda kabul edildi")
	}
	if querier.queries != 2 {
		t.Fatalf("%d sorgu, 2 bekleniyordu (hata hatırlanmamalı)", querier.queries)
	}
}

// A read error is an error, not a "no". It must reach the caller unchanged, so
// a save fails loudly instead of reporting the line as invalid.
func TestReadErrorPropagates(t *testing.T) {
	wanted := errors.New("bağlantı koptu")
	querier := &countingQuerier{err: wanted}
	checks := newPurchaseLineChecks()
	if err := checks.product(context.Background(), querier, "c1", "p1", "", "PRODUCT"); !errors.Is(err, wanted) {
		t.Fatalf("hata = %v, %v bekleniyordu", err, wanted)
	}
}
