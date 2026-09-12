package finance

import (
	"fmt"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPaymentPlanListValidation(t *testing.T) {
	for _, o := range []PaymentPlanListOptions{
		{Side: "RECEIVABLE", DueFrom: "2026-02-30"}, {Side: "PAYABLE", DueFrom: "2026-09-12", DueTo: "2026-09-11"},
		{Side: "RECEIVABLE", Sort: "description;DROP"}, {Side: "RECEIVABLE", Status: "OVERDUE"}, {Side: "RECEIVABLE", PartyID: "invalid"},
	} {
		if _, err := normalizePaymentPlanList(o); err == nil {
			t.Fatalf("invalid filters accepted: %+v", o)
		}
	}
}

func TestPaymentPlanListFindsOldRecordsAndPaginates(t *testing.T) {
	f := newReturnFixture(t, "plan_list")
	today := time.Now().UTC().Format("2006-01-02")
	oldID := ""
	for i := 0; i < 205; i++ {
		docID, _ := f.document("SALES_INVOICE", fmt.Sprintf("LIST-SF-%03d", i), "2026-01-10", "100", "1")
		oi := f.openItem(docID, "2026-01-10", "100")
		description := fmt.Sprintf("Plan %03d", i)
		if i == 0 {
			description = "Eski aranacak plan"
		}
		p, err := f.service.CreatePaymentPlan(f.ctx, f.session, PaymentPlanInput{PartyID: f.partyID, Side: "RECEIVABLE", Currency: "TRY", Description: description, OpenItemIDs: []string{oi}, Installments: []PlanInstallmentInput{{DueDate: today, Amount: "100"}}, IdempotencyKey: uuid.NewString()}, identity.RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldID = p.ID
		}
	}
	o := PaymentPlanListOptions{Side: "RECEIVABLE", Status: "OPEN", Sort: "created_at:desc", Limit: 37}
	seen := map[string]bool{}
	for {
		page, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, o)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page.Items {
			if seen[p.ID] {
				t.Fatal("duplicate page item")
			}
			seen[p.ID] = true
			if p.OpenAmount != "100.0000" || p.NextDueDate == nil || *p.NextDueDate != today {
				t.Fatalf("summary: %+v", p)
			}
		}
		if page.NextCursor == "" {
			break
		}
		o.Cursor = page.NextCursor
	}
	if len(seen) != 205 || !seen[oldID] {
		t.Fatalf("incomplete pagination: %d", len(seen))
	}
	for _, search := range []string{"Eski aranacak", "LIST-SF-000"} {
		page, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Search: search})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != oldID {
			t.Fatalf("old record search %q: %+v %v", search, page, err)
		}
	}
	first, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("missing next due cursor")
	}
	second, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("due cursor: %+v %v", second, err)
	}
	_, err = f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Limit: 2, Status: "CLOSED", Cursor: first.NextCursor})
	if err == nil {
		t.Fatal("cursor accepted across filter sets")
	}
	for _, preset := range []string{"TODAY", "NEXT_7_DAYS"} {
		page, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Preset: preset, Search: "Eski"})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("preset: %+v %v", page, err)
		}
	}
	page, err := f.service.ListPaymentPlanSummaries(f.ctx, f.session, PaymentPlanListOptions{Side: "RECEIVABLE", Preset: "OVERDUE"})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("today incorrectly overdue: %+v %v", page, err)
	}
	eligible, err := f.service.ListOpenItemsPage(f.ctx, f.session, f.partyID, "TRY", "RECEIVABLE", "", 50, true)
	if err != nil || len(eligible.Items) != 0 {
		t.Fatalf("planned invoice offered: %+v %v", eligible, err)
	}
}
