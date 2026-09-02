package inventory

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStockCountEngineStartHashIsStableAcrossScopeOrder(t *testing.T) {
	first := StockCountEngineStartInput{
		CompanyID: "00000000-0000-4000-8000-000000000001", WarehouseID: "00000000-0000-4000-8000-000000000002",
		IdempotencyKey: "start-1", Scopes: []StockCountEngineScopeInput{
			{ProductID: "00000000-0000-4000-8000-000000000004"},
			{ProductID: "00000000-0000-4000-8000-000000000003"},
		},
	}
	_, firstHash, err := normalizeStockCountEngineStart(first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Scopes[0], second.Scopes[1] = second.Scopes[1], second.Scopes[0]
	_, secondHash, err := normalizeStockCountEngineStart(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstHash, secondHash) {
		t.Fatal("scope order changed the start idempotency payload hash")
	}
}

func TestStockCountEngineRejectsBlindStart(t *testing.T) {
	_, _, err := normalizeStockCountEngineStart(StockCountEngineStartInput{
		CompanyID:      "00000000-0000-4000-8000-000000000001",
		WarehouseID:    "00000000-0000-4000-8000-000000000002",
		BlindCount:     true,
		IdempotencyKey: "blind-start",
	})
	if err == nil || !strings.Contains(err.Error(), "blind count is no longer supported") {
		t.Fatalf("blind start error = %v", err)
	}
}

func TestStockCountEngineNormalizesDescriptionWithoutChangingPayloadRules(t *testing.T) {
	input := StockCountEngineStartInput{
		CompanyID:      "00000000-0000-4000-8000-000000000001",
		WarehouseID:    "00000000-0000-4000-8000-000000000002",
		Description:    "  Dönem sonu sayımı  ",
		IdempotencyKey: "description-start",
	}
	normalized, _, err := normalizeStockCountEngineStart(input)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Description != "Dönem sonu sayımı" {
		t.Fatalf("description=%q", normalized.Description)
	}
	tooLong := input
	tooLong.Description = strings.Repeat("x", 501)
	if _, _, err = normalizeStockCountEngineStart(tooLong); err == nil {
		t.Fatal("expected a maximum description length validation error")
	}
}

func TestStockCountEngineEventRetryHashDoesNotUseAssignedClock(t *testing.T) {
	input := StockCountEngineEventInput{EventID: "device-1", EventType: StockCountEngineScan, Barcode: "869000000001", Quantity: "1"}
	first, firstHash, err := normalizeStockCountEngineEvent(input, "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := normalizeStockCountEngineEvent(input, "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.OccurredAt.Equal(second.OccurredAt) || !bytes.Equal(firstHash, secondHash) {
		t.Fatal("retry hash changed with the server-assigned occurred_at")
	}
}

func TestStockCountEngineEventQuantityUsesCanonicalDecimalText(t *testing.T) {
	input, _, err := normalizeStockCountEngineEvent(
		StockCountEngineEventInput{EventID: "decimal-event", EventType: StockCountEngineScan, Barcode: "869000000001", Quantity: "3.00000000"},
		"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.Quantity != "3" {
		t.Fatalf("quantity=%q, want 3", input.Quantity)
	}
}

func TestStockCountEngineEffectiveQuantityRequiresExplicitZeroEvent(t *testing.T) {
	events := []struct {
		scopeID, eventType, quantity string
		recorded                     time.Time
	}{
		{scopeID: "scope-a", eventType: StockCountEngineZero, quantity: "0"},
	}
	result := calculateEngineEffective(events)
	if !result["scope-a"].hasResponse || result["scope-a"].quantity != "0" {
		t.Fatalf("zero confirmation was not retained as a response: %+v", result)
	}
	if _, ok := result["missing"]; ok {
		t.Fatal("an absent scope was treated as explicitly zero")
	}
}

func TestStockCountEngineEffectiveQuantityMasksOnlyBeforeReview(t *testing.T) {
	if got := engineDecimalAdd("1.25000000", "-0.25000000"); got != "1" {
		t.Fatalf("decimal addition=%q, want 1", got)
	}
	if StockCountEngineOpen == StockCountEngineBlind || StockCountEngineOpen != "OPEN" {
		t.Fatal("open pass mode contract is not selected")
	}
}

func TestStartStockCountEngineMaterializesSnapshotBeforeScopeInserts(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{
		CompanyID: transferTestCompany, ActorUserID: transferTestUser,
		WarehouseID:    transferTestSource,
		IdempotencyKey: "count-engine-reader-close",
		Scopes:         []StockCountEngineScopeInput{{ProductID: transferTestProduct, VariantID: transferTestVariant}},
	})
	if err != nil {
		t.Fatalf("start stock count engine: %v", err)
	}
	if len(count.Scopes) != 1 {
		t.Fatalf("scope count=%d, want 1", len(count.Scopes))
	}
	if count.Scopes[0].VariantID == nil || *count.Scopes[0].VariantID == "" {
		t.Fatal("variant scope lost its variant id")
	}
}

func TestStartStockCountEngineAllocatesCompanyNumbersAtomically(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	inputs := []StockCountEngineStartInput{
		{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, IdempotencyKey: "concurrent-count-1"},
		{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, IdempotencyKey: "concurrent-count-2"},
	}
	results := make([]StockCountEngine, len(inputs))
	errorsByIndex := make([]error, len(inputs))
	var wait sync.WaitGroup
	for index, input := range inputs {
		wait.Add(1)
		go func(index int, input StockCountEngineStartInput) {
			defer wait.Done()
			results[index], errorsByIndex[index] = fixture.service.StartStockCountEngine(fixture.ctx, input)
		}(index, input)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent start %d: %v", index, err)
		}
	}
	if results[0].CountNo == results[1].CountNo || results[0].CountNo == "" || results[1].CountNo == "" {
		t.Fatalf("duplicate or empty count numbers: %q and %q", results[0].CountNo, results[1].CountNo)
	}
}

func TestStockCountEngineAcceptsVariantBarcode(t *testing.T) {
	fixture := newTransferStockFixture(t, "5", "0", true)
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES('10000000-0000-4000-8000-000000000099',$1,$2,$3,'VARIANT-COUNT-1','CODE128',true)`, transferTestCompany, transferTestProduct, transferTestVariant); err != nil {
		t.Fatal(err)
	}
	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, IdempotencyKey: "variant-count"})
	if err != nil {
		t.Fatal(err)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: transferTestUser})
	if err != nil {
		t.Fatal(err)
	}
	input := StockCountEngineBatchInput{CountID: count.ID, PassID: pass.ID, Events: []StockCountEngineEventInput{{EventID: "variant-scan-1", EventType: StockCountEngineScan, Barcode: "VARIANT-COUNT-1", Quantity: "1"}}}
	events, err := fixture.service.BatchScanStockCount(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ResolutionStatus != "ACCEPTED" || events[0].ScopeID == nil {
		t.Fatalf("variant scan result=%+v", events)
	}
	if _, err = fixture.service.BatchScanStockCount(fixture.ctx, input); err != nil {
		t.Fatalf("idempotent variant retry: %v", err)
	}
	if _, err = fixture.service.BatchScanStockCount(fixture.ctx, StockCountEngineBatchInput{CountID: count.ID, PassID: pass.ID, Events: []StockCountEngineEventInput{{EventID: "variant-scan-2", EventType: StockCountEngineScan, Barcode: "VARIANT-COUNT-1", Quantity: "1"}}}); err != nil {
		t.Fatalf("second variant scan: %v", err)
	}
	view, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Scopes) != 1 || view.Scopes[0].VariantID == nil || *view.Scopes[0].VariantID != transferTestVariant || view.Scopes[0].CountedQuantity == nil || decimalCompare(*view.Scopes[0].CountedQuantity, "2") != 0 {
		t.Fatalf("variant count scope=%+v", view.Scopes)
	}
}

func TestSubmitStockCountPassMaterializesScopesBeforeReviewQueries(t *testing.T) {
	fixture := newTransferStockFixture(t, "5", "0")
	const actorID = "10000000-0000-4000-8000-000000000020"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'stock-count-submit@example.test','Stock Count Submitter','test-hash')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, transferTestCompany, actorID); err != nil {
		t.Fatal(err)
	}
	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{CompanyID: transferTestCompany, WarehouseID: transferTestSource, IdempotencyKey: "submit-materializes-scopes", ActorUserID: actorID, Scopes: []StockCountEngineScopeInput{{ProductID: transferTestProduct}}})
	if err != nil {
		t.Fatal(err)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: actorID})
	if err != nil {
		t.Fatal(err)
	}
	view, err := fixture.service.SubmitStockCountPassAndReviewForCompany(fixture.ctx, transferTestCompany, count.ID, pass.ID, actorID)
	if !errors.Is(err, ErrStockCountEngineReviewRequired) {
		t.Fatalf("submit error=%v, want review required", err)
	}
	if view.ID != "" {
		t.Fatalf("incomplete submit returned a view=%+v, want the transaction rolled back", view)
	}
	current, getErr := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID, actorID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if current.State != StockCountEngineInProgress || current.Version != count.Version {
		t.Fatalf("incomplete submit changed count: %+v", current)
	}
	if len(current.Passes) != 1 || current.Passes[0].State != "IN_PROGRESS" {
		t.Fatalf("passes=%+v, want open pass", current.Passes)
	}
	if len(current.Exceptions) != 0 {
		t.Fatalf("incomplete submit created exceptions=%+v", current.Exceptions)
	}
}

func TestPostStockCountWithUnresolvedReviewLeavesCountAndPostingHistoryUnchanged(t *testing.T) {
	fixture := newTransferStockFixture(t, "5", "0")
	const actorID = "10000000-0000-4000-8000-000000000022"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'stock-count-blocked-post@example.test','Blocked Count Poster','test-hash')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, transferTestCompany, actorID); err != nil {
		t.Fatal(err)
	}

	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{
		CompanyID: transferTestCompany, WarehouseID: transferTestSource, IdempotencyKey: "blocked-post-start",
		ActorUserID: actorID, Scopes: []StockCountEngineScopeInput{{ProductID: transferTestProduct}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{
		CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.service.CorrectStockCount(fixture.ctx, StockCountEngineEventInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: pass.ID, ScopeID: count.Scopes[0].ID,
		EventID: "blocked-post-correction", Quantity: "3", Reason: "Blocked post test", ActorUserID: actorID,
	}); err != nil {
		t.Fatalf("correct count: %v", err)
	}
	if _, err = fixture.service.BatchScanStockCount(fixture.ctx, StockCountEngineBatchInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: pass.ID, ActorUserID: actorID,
		Events: []StockCountEngineEventInput{{EventID: "blocked-post-unknown", EventType: StockCountEngineScan, Barcode: "BLOCKED-UNKNOWN", Quantity: "1", ActorUserID: actorID}},
	}); err != nil {
		t.Fatalf("scan unknown barcode: %v", err)
	}
	review, err := fixture.service.SubmitStockCountPassAndReviewForCompany(fixture.ctx, transferTestCompany, count.ID, pass.ID, actorID)
	if !errors.Is(err, ErrStockCountEngineReviewRequired) {
		t.Fatalf("submit error=%v, want review required", err)
	}
	if review.State != StockCountEngineReview || review.FinishedAt != nil {
		t.Fatalf("review state=%s finished_at=%v, want REVIEW and no finish time", review.State, review.FinishedAt)
	}

	before, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeMovements, beforePostCommands int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND source_type='STOCK_COUNT_ENGINE' AND source_id=$2`, transferTestCompany, count.ID).Scan(&beforeMovements); err != nil {
		t.Fatal(err)
	}
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='POST'`, transferTestCompany, count.ID).Scan(&beforePostCommands); err != nil {
		t.Fatal(err)
	}

	_, err = fixture.service.PostStockCountEngine(fixture.ctx, StockCountEnginePostInput{
		CompanyID: transferTestCompany, CountID: count.ID, IdempotencyKey: "blocked-post-command",
		ExpectedVersion: before.Version, ActorUserID: actorID,
	})
	if !errors.Is(err, ErrStockCountEngineReviewRequired) {
		t.Fatalf("post error=%v, want review required", err)
	}

	after, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.Version != before.Version || after.FinishedAt != nil {
		t.Fatalf("blocked post changed count: before=%+v after=%+v", before, after)
	}
	var afterMovements, afterPostCommands int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND source_type='STOCK_COUNT_ENGINE' AND source_id=$2`, transferTestCompany, count.ID).Scan(&afterMovements); err != nil {
		t.Fatal(err)
	}
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='POST'`, transferTestCompany, count.ID).Scan(&afterPostCommands); err != nil {
		t.Fatal(err)
	}
	if afterMovements != beforeMovements || afterPostCommands != beforePostCommands {
		t.Fatalf("blocked post changed posting history: movements %d -> %d, post commands %d -> %d", beforeMovements, afterMovements, beforePostCommands, afterPostCommands)
	}
}

func TestReopenStockCountForRecountCreatesNewPassAndPreservesReviewFacts(t *testing.T) {
	fixture := newTransferStockFixture(t, "5", "0")
	const actorID = "10000000-0000-4000-8000-000000000023"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'stock-count-recount@example.test','Stock Count Recounter','test-hash')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, transferTestCompany, actorID); err != nil {
		t.Fatal(err)
	}

	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{
		CompanyID: transferTestCompany, WarehouseID: transferTestSource, IdempotencyKey: "recount-start",
		ActorUserID: actorID, Scopes: []StockCountEngineScopeInput{{ProductID: transferTestProduct}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{
		CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.service.CorrectStockCount(fixture.ctx, StockCountEngineEventInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: pass.ID, ScopeID: count.Scopes[0].ID,
		EventID: "recount-first-correction", Quantity: "3", Reason: "İlk sayım", ActorUserID: actorID,
	}); err != nil {
		t.Fatalf("correct count: %v", err)
	}
	if _, err = fixture.service.BatchScanStockCount(fixture.ctx, StockCountEngineBatchInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: pass.ID, ActorUserID: actorID,
		Events: []StockCountEngineEventInput{{EventID: "recount-unknown", EventType: StockCountEngineScan, Barcode: "RECOUNT-UNKNOWN", Quantity: "1", ActorUserID: actorID}},
	}); err != nil {
		t.Fatalf("scan unknown barcode: %v", err)
	}
	review, err := fixture.service.SubmitStockCountPassAndReviewForCompany(fixture.ctx, transferTestCompany, count.ID, pass.ID, actorID)
	if !errors.Is(err, ErrStockCountEngineReviewRequired) {
		t.Fatalf("submit error=%v, want review required", err)
	}
	if review.State != StockCountEngineReview || len(review.Exceptions) != 1 || review.Exceptions[0].Status != "OPEN" {
		t.Fatalf("review=%+v, want one open exception", review)
	}

	recountInput := StockCountEngineRecountInput{
		CompanyID: transferTestCompany, CountID: count.ID, ExpectedVersion: review.Version,
		IdempotencyKey: "recount-command", ActorUserID: actorID,
	}
	recounted, err := fixture.service.ReopenStockCountEngineForRecount(fixture.ctx, recountInput)
	if err != nil {
		t.Fatalf("reopen for recount: %v", err)
	}
	if recounted.State != StockCountEngineInProgress || recounted.Version != review.Version+1 {
		t.Fatalf("recounted state/version=%s/%d, want IN_PROGRESS/%d", recounted.State, recounted.Version, review.Version+1)
	}
	if len(recounted.Passes) != 2 || recounted.Passes[0].State != "COMPLETED" || recounted.Passes[1].State != "IN_PROGRESS" {
		t.Fatalf("passes=%+v, want completed old pass and open new pass", recounted.Passes)
	}
	if len(recounted.Exceptions) != 1 || recounted.Exceptions[0].Status != "OPEN" {
		t.Fatalf("exceptions=%+v, want the original exception preserved", recounted.Exceptions)
	}

	if _, err = fixture.service.CorrectStockCount(fixture.ctx, StockCountEngineEventInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: recounted.Passes[1].ID, ScopeID: recounted.Scopes[0].ID,
		EventID: "recount-second-correction", Quantity: "4", Reason: "Tekrar sayım", ActorUserID: actorID,
	}); err != nil {
		t.Fatalf("correct recount: %v", err)
	}
	retry, err := fixture.service.ReopenStockCountEngineForRecount(fixture.ctx, recountInput)
	if err != nil {
		t.Fatalf("idempotent recount retry: %v", err)
	}
	if retry.Version != recounted.Version || len(retry.Passes) != len(recounted.Passes) {
		t.Fatalf("idempotent retry changed recount: before=%+v after=%+v", recounted, retry)
	}

	_, err = fixture.service.ReopenStockCountEngineForRecount(fixture.ctx, StockCountEngineRecountInput{
		CompanyID: transferTestCompany, CountID: count.ID, ExpectedVersion: review.Version,
		IdempotencyKey: "stale-recount-command", ActorUserID: actorID,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale recount error=%v, want conflict", err)
	}
	current, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != StockCountEngineInProgress || current.Version != recounted.Version || len(current.Passes) != 2 {
		t.Fatalf("stale recount changed count: %+v", current)
	}
}

func TestPostStockCountMaterializesScopesBeforeSnapshotQuery(t *testing.T) {
	fixture := newTransferStockFixture(t, "5", "0")
	const actorID = "10000000-0000-4000-8000-000000000021"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'stock-count-post@example.test','Stock Count Poster','test-hash')`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, transferTestCompany, actorID); err != nil {
		t.Fatal(err)
	}

	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{
		CompanyID: transferTestCompany, WarehouseID: transferTestSource, IdempotencyKey: "post-materializes-scopes",
		ActorUserID: actorID, Scopes: []StockCountEngineScopeInput{{ProductID: transferTestProduct}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count.StartedAt.IsZero() {
		t.Fatal("started count did not expose its snapshot start time")
	}
	if count.FinishedAt != nil {
		t.Fatal("in-progress count unexpectedly exposed a finish time")
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{
		CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: actorID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.service.CorrectStockCount(fixture.ctx, StockCountEngineEventInput{
		CompanyID: transferTestCompany, CountID: count.ID, PassID: pass.ID, ScopeID: count.Scopes[0].ID,
		EventID: "post-count-correction", Quantity: "3", Reason: "Post test", ActorUserID: actorID,
	}); err != nil {
		t.Fatalf("correct count: %v", err)
	}
	if _, err = fixture.service.SubmitStockCountPassAndReviewForCompany(fixture.ctx, transferTestCompany, count.ID, pass.ID, actorID); err != nil {
		t.Fatalf("submit count: %v", err)
	}

	view, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID, actorID)
	if err != nil {
		t.Fatal(err)
	}
	postInput := StockCountEnginePostInput{
		CompanyID: transferTestCompany, CountID: count.ID, IdempotencyKey: "post-count-command", ExpectedVersion: view.Version, ActorUserID: actorID,
	}
	posted, err := fixture.service.PostStockCountEngine(fixture.ctx, postInput)
	if err != nil {
		t.Fatalf("post count: %v", err)
	}
	if posted.State != StockCountEnginePosted {
		t.Fatalf("posted state=%s, want %s", posted.State, StockCountEnginePosted)
	}
	if !posted.StartedAt.Equal(count.StartedAt) {
		t.Fatalf("posted start time changed: before=%v after=%v", count.StartedAt, posted.StartedAt)
	}
	if posted.FinishedAt == nil || posted.FinishedAt.Before(posted.StartedAt) {
		t.Fatalf("posted count finish time=%v, want a timestamp after start=%v", posted.FinishedAt, posted.StartedAt)
	}

	var movementCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND source_type='STOCK_COUNT_ENGINE' AND source_id=$2`, transferTestCompany, count.ID).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 1 {
		t.Fatalf("stock count movement count=%d, want 1", movementCount)
	}
	var commandCompleted bool
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT completed_at IS NOT NULL FROM stock_count_engine_commands WHERE company_id=$1 AND count_id=$2 AND command_name='POST' AND idempotency_key=$3`, transferTestCompany, count.ID, postInput.IdempotencyKey).Scan(&commandCompleted); err != nil {
		t.Fatal(err)
	}
	if !commandCompleted {
		t.Fatal("post command was not completed")
	}

	if _, err = fixture.service.PostStockCountEngine(fixture.ctx, postInput); err != nil {
		t.Fatalf("idempotent post retry: %v", err)
	}
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND source_type='STOCK_COUNT_ENGINE' AND source_id=$2`, transferTestCompany, count.ID).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 1 {
		t.Fatalf("idempotent retry created %d movements, want 1", movementCount)
	}
}

func TestFullStockCountDiscoversZeroBalanceVariantByBarcode(t *testing.T) {
	fixture := newTransferStockFixture(t, "0", "0", true)
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES('10000000-0000-4000-8000-000000000098',$1,$2,$3,'ZERO-VARIANT-1','CODE128',true)`, transferTestCompany, transferTestProduct, transferTestVariant); err != nil {
		t.Fatal(err)
	}
	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, IdempotencyKey: "zero-variant-count"})
	if err != nil {
		t.Fatal(err)
	}
	if len(count.Scopes) != 0 || count.ScopeMode != "FULL" {
		t.Fatalf("initial full count=%+v", count)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: transferTestUser})
	if err != nil {
		t.Fatal(err)
	}
	events, err := fixture.service.BatchScanStockCount(fixture.ctx, StockCountEngineBatchInput{CountID: count.ID, PassID: pass.ID, Events: []StockCountEngineEventInput{{EventID: "zero-variant-scan", EventType: StockCountEngineScan, Barcode: "ZERO-VARIANT-1", Quantity: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ResolutionStatus != "ACCEPTED" {
		t.Fatalf("zero variant scan=%+v", events)
	}
	view, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Scopes) != 1 || view.Scopes[0].SnapshotQuantity == nil || decimalCompare(*view.Scopes[0].SnapshotQuantity, "0") != 0 {
		t.Fatalf("discovered scope=%+v", view.Scopes)
	}
}

func TestStockCountEventRetryDoesNotResolveChangedBarcodeOrExpandScope(t *testing.T) {
	fixture := newTransferStockFixture(t, "0", "0", true)
	count, err := fixture.service.StartStockCountEngine(fixture.ctx, StockCountEngineStartInput{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, IdempotencyKey: "retry-barcode-count"})
	if err != nil {
		t.Fatal(err)
	}
	pass, err := fixture.service.StartStockCountPass(fixture.ctx, StockCountEnginePassInput{CompanyID: transferTestCompany, CountID: count.ID, Mode: StockCountEngineOpen, ActorUserID: transferTestUser})
	if err != nil {
		t.Fatal(err)
	}
	input := StockCountEngineBatchInput{CountID: count.ID, PassID: pass.ID, Events: []StockCountEngineEventInput{{EventID: "stable-unknown-event", EventType: StockCountEngineScan, Barcode: "LATER-ASSIGNED", Quantity: "1"}}}
	first, err := fixture.service.BatchScanStockCount(fixture.ctx, input)
	if err != nil || len(first) != 1 || first[0].ResolutionStatus != "UNKNOWN" {
		t.Fatalf("initial unknown scan=%+v err=%v", first, err)
	}
	if _, err = fixture.pool.Exec(fixture.ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES('10000000-0000-4000-8000-000000000097',$1,$2,$3,'LATER-ASSIGNED','CODE128',true)`, transferTestCompany, transferTestProduct, transferTestVariant); err != nil {
		t.Fatal(err)
	}
	retried, err := fixture.service.BatchScanStockCount(fixture.ctx, input)
	if err != nil || len(retried) != 1 || retried[0].ResolutionStatus != "UNKNOWN" || retried[0].ScopeID != nil {
		t.Fatalf("retried scan changed resolution=%+v err=%v", retried, err)
	}
	view, err := fixture.service.GetStockCountEngine(fixture.ctx, transferTestCompany, count.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Scopes) != 0 {
		t.Fatalf("idempotent retry expanded count scope: %+v", view.Scopes)
	}
}
