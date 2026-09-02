package inventory

import (
	"strings"
	"testing"
)

func TestStockCountsListQueryQualifiesJoinedStockCountColumns(t *testing.T) {
	query, args := stockCountsListQuery("company-id", " in_progress ", 25, "user-id")

	for _, fragment := range []string{
		"c.warehouse_id IN (SELECT w.id",
		"AND c.state=$3",
		"ORDER BY c.created_at DESC,c.id DESC LIMIT $4",
		"membership_branch_scopes",
		"membership_warehouse_scopes",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("stock-count list query missing %q:\n%s", fragment, query)
		}
	}
	for _, fragment := range []string{
		" AND warehouse_id IN ",
		" AND state=$",
		" ORDER BY created_at DESC,id DESC ",
	} {
		if strings.Contains(query, fragment) {
			t.Fatalf("stock-count list query contains unqualified fragment %q:\n%s", fragment, query)
		}
	}

	if len(args) != 4 || args[0] != "company-id" || args[1] != "user-id" || args[2] != "IN_PROGRESS" || args[3] != 25 {
		t.Fatalf("stock-count list query args=%#v, want company, actor, normalized state and limit", args)
	}
}
