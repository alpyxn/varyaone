package finance

import "testing"

func TestDeriveDocumentSettlementKeepsReturnAndPaymentStatusesSeparate(t *testing.T) {
	settlement := deriveDocumentSettlement("10000", "0", "4000", "10000", "RECEIVABLE")
	if settlement.InvoiceTotal != "10000.0000" || settlement.ReturnedTotal != "4000.0000" {
		t.Fatalf("unexpected totals: %+v", settlement)
	}
	if settlement.EffectiveInvoiceTotal != "6000.0000" || settlement.AmountDue != "0.0000" {
		t.Fatalf("unexpected effective balance: %+v", settlement)
	}
	if settlement.PaymentStatus != "PAID" || settlement.ReturnStatus != "PARTIAL" {
		t.Fatalf("payment and return statuses were conflated: %+v", settlement)
	}
	if settlement.CustomerCredit != "4000.0000" {
		t.Fatalf("customer credit=%s, want 4000.0000", settlement.CustomerCredit)
	}
}

func TestDeriveDocumentSettlementTreatsEffectiveBalanceAsPaid(t *testing.T) {
	settlement := deriveDocumentSettlement("10000", "0", "4000", "6000", "RECEIVABLE")
	if settlement.EffectiveInvoiceTotal != "6000.0000" || settlement.AmountDue != "0.0000" {
		t.Fatalf("effective balance=%+v", settlement)
	}
	if settlement.PaymentStatus != "PAID" || settlement.ReturnStatus != "PARTIAL" {
		t.Fatalf("effective payment/return status=%+v", settlement)
	}

	fullyReturned := deriveDocumentSettlement("10000", "0", "10000", "0", "RECEIVABLE")
	if fullyReturned.PaymentStatus != "UNPAID" || fullyReturned.AmountDue != "0.0000" {
		t.Fatalf("fully returned unpaid invoice=%+v", fullyReturned)
	}
}

func TestDeriveDocumentSettlementPurchaseMirror(t *testing.T) {
	settlement := deriveDocumentSettlement("10000", "0", "4000", "0", "PAYABLE")
	if settlement.PaidTotal != "0.0000" || settlement.AmountPayable != "6000.0000" {
		t.Fatalf("unexpected purchase balance: %+v", settlement)
	}
	if settlement.SupplierCredit != "0.0000" || settlement.PaymentStatus != "UNPAID" {
		t.Fatalf("unexpected purchase settlement: %+v", settlement)
	}
}

func TestDeriveDocumentSettlementClampsRoundingOverReturn(t *testing.T) {
	settlement := deriveDocumentSettlement("10.0000", "0", "10.0001", "0", "RECEIVABLE")
	if settlement.EffectiveInvoiceTotal != "0.0000" || settlement.AmountDue != "0.0000" {
		t.Fatalf("negative effective balance leaked: %+v", settlement)
	}
	if settlement.ReturnStatus != "FULL" {
		t.Fatalf("return status=%s, want FULL", settlement.ReturnStatus)
	}
}
