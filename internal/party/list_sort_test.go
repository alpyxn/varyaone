package party

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestPartySortRejectsInvalidFieldsDirectionsAndCursors(t *testing.T) {
	for _, sort := range []string{"code", "code:sideways", "code:asc;DROP TABLE parties", "unknown:asc", "code:asc,code:desc"} {
		if _, err := parsePartySort(sort); !errors.Is(err, identity.ErrValidation) {
			t.Fatalf("accepted sort %q: %v", sort, err)
		}
	}
	for _, after := range []partySortCursor{
		{Sort: "code:desc", Values: []string{"a"}, ID: "00000000-0000-0000-0000-000000000001"},
		{Sort: "credit_limit:asc", Values: []string{"1; DROP TABLE parties"}, ID: "00000000-0000-0000-0000-000000000001"},
		{Sort: "credit_limit:asc", Values: []string{}, ID: "invalid"},
	} {
		raw, _ := json.Marshal(after)
		if _, _, _, err := sortedPartyQuery(partySelect, []any{"company"}, "credit_limit:asc", base64.RawURLEncoding.EncodeToString(raw), 2); !errors.Is(err, identity.ErrValidation) {
			t.Fatalf("accepted cursor %+v: %v", after, err)
		}
	}
}

func TestPartySortBindsCursorValuesAndKeepsMixedDirectionTieBreakers(t *testing.T) {
	raw, _ := json.Marshal(partySortCursor{Sort: "trade_name:asc,credit_limit:desc", Values: []string{"O'Reilly", "10.25"}, ID: "00000000-0000-0000-0000-000000000001"})
	query, args, _, err := sortedPartyQuery(partySelect, []any{"company"}, "trade_name:asc,credit_limit:desc", base64.RawURLEncoding.EncodeToString(raw), 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, "O'Reilly") || !strings.Contains(query, "p.id>$4::uuid") || !strings.Contains(query, "<$3::numeric") || args[1] != "O'Reilly" || args[2] != "10.25" {
		t.Fatalf("unsafe or incomplete cursor query: %s / %v", query, args)
	}
}
