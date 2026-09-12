package inventory

import "testing"

// TestListLotsExecutes is a regression guard for the query itself.
//
// ListLots had an unbalanced parenthesis in its SELECT clause, so every call
// failed with a PostgreSQL syntax error and the lot/serial screen returned 500
// on every load, in every deployment. Nothing caught it because no test called
// this method: the browser tests measured the page's layout, which an error
// screen satisfies perfectly.
//
// The assertion is deliberately about execution rather than about rows. A
// malformed query fails whatever the data is, and the row-level behaviour is
// the subject of the lot fixtures elsewhere.
func TestListLotsExecutes(t *testing.T) {
	f := newTransferStockFixture(t, "100", "0", true)

	// All four shapes, because the warehouse and user arguments each switch on
	// a separate branch of the query's scope filter.
	cases := []struct {
		name        string
		warehouseID string
		userID      string
	}{
		{"unscoped", "", ""},
		{"user", "", transferTestUser},
		// A warehouse filter is only meaningful with the actor it is checked
		// against; without one the scope guard refuses before the query runs.
		{"warehouse and user", transferTestSource, transferTestUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.service.ListLots(
				f.ctx, transferTestCompany, "", "", 50, tc.warehouseID, tc.userID,
			); err != nil {
				t.Fatalf("ListLots(%s): %v", tc.name, err)
			}
		})
	}

	// The search branch appends its own predicates to the same string.
	if _, err := f.service.ListLots(f.ctx, transferTestCompany, "", "LOT-ARA", 50); err != nil {
		t.Fatalf("ListLots with a search term: %v", err)
	}
}
