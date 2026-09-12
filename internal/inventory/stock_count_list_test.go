package inventory

import (
	"slices"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
)

func TestStockCountsListFiltersAndScopes(t *testing.T) {
	f := newTransferStockFixture(t, "0", "0")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	branchA, branchB, otherCompany := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$3,'A','A'),($2,$3,'B','B')`, branchA, branchB, transferTestCompany)
	exec(`UPDATE warehouses SET branch_id=$1 WHERE company_id=$2 AND id=$3`, branchA, transferTestCompany, transferTestSource)
	exec(`UPDATE warehouses SET branch_id=$1 WHERE company_id=$2 AND id=$3`, branchB, transferTestCompany, transferTestDestination)
	exec(`INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'Other','Other','LEGAL_ENTITY')`, otherCompany)
	exec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type) VALUES($1,$2,'OTHER','Other','STANDARD')`, transferTestSource, otherCompany)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertCount := func(company, warehouse, state string, hour int) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO stock_counts(id,company_id,warehouse_id,state,created_at) VALUES($1,$2,$3,$4,$5)`, id, company, warehouse, state, at.Add(time.Duration(hour)*time.Hour))
		return id
	}
	older := insertCount(transferTestCompany, transferTestSource, "IN_PROGRESS", 0)
	newer := insertCount(transferTestCompany, transferTestSource, "IN_PROGRESS", 1)
	review := insertCount(transferTestCompany, transferTestSource, "REVIEW", 2)
	destination := insertCount(transferTestCompany, transferTestDestination, "IN_PROGRESS", 3)
	insertCount(otherCompany, transferTestSource, "IN_PROGRESS", 4)
	insertCount(transferTestCompany, transferTestTransit, "IN_PROGRESS", 5)

	// The HTTP request pins one connection; listing must drain its ID rows before
	// loading each count's details on that same connection.
	conn, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	ctx := database.ContextWithConn(f.ctx, conn)
	service := NewService(database.NewScoped(f.pool))
	check := func(name, state string, limit int, actor string, want ...string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			items, err := service.ListStockCounts(ctx, transferTestCompany, state, limit, actor)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(items))
			for i, item := range items {
				ids[i] = item.ID
				if item.CompanyID != transferTestCompany {
					t.Fatalf("another company's count was returned: %+v", item)
				}
			}
			if !slices.Equal(ids, want) {
				t.Fatalf("count IDs = %v, want %v", ids, want)
			}
		})
	}
	check("all states ordered newest first", "", 25, "", destination, review, newer, older)
	check("normalized state", " in_progress ", 25, "", destination, newer, older)
	check("limit", "IN_PROGRESS", 1, "", destination)
	check("unrestricted actor", "IN_PROGRESS", 25, transferTestUser, destination, newer, older)

	exec(`UPDATE warehouses SET is_active=false WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestDestination)
	check("inactive warehouse excluded", "IN_PROGRESS", 25, "", newer, older)
	exec(`UPDATE warehouses SET is_active=true WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestDestination)

	exec(`INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, transferTestCompany, transferTestUser, branchA)
	check("branch scope", "IN_PROGRESS", 25, transferTestUser, newer, older)
	exec(`INSERT INTO membership_warehouse_scopes(company_id,user_id,warehouse_id) VALUES($1,$2,$3)`, transferTestCompany, transferTestUser, transferTestDestination)
	check("warehouse permission cannot bypass branch scope", "IN_PROGRESS", 25, transferTestUser)
	exec(`DELETE FROM membership_branch_scopes WHERE company_id=$1 AND user_id=$2`, transferTestCompany, transferTestUser)
	check("warehouse scope", "IN_PROGRESS", 25, transferTestUser, destination)
}
