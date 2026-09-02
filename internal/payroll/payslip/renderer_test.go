package payslip

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestGoPDFRendererUsesFinalizedSnapshotAndUnicodeFont(t *testing.T) {
	source := sampleSnapshot()
	var output bytes.Buffer
	metadata, err := (GoPDFRenderer{TemplateVersion: "tr-payslip-v2"}).Render(context.Background(), source, &output)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.PageCount != 1 || metadata.MIMEType != "application/pdf" || !bytes.HasPrefix(output.Bytes(), []byte("%PDF-")) {
		t.Fatalf("metadata=%+v bytes=%d", metadata, output.Len())
	}
	if bytes.Contains(output.Bytes(), []byte("12345678901")) || bytes.Contains(output.Bytes(), []byte("TR001234567890123456789012")) {
		t.Fatal("full sensitive identifier leaked into PDF")
	}
	if path := strings.TrimSpace(os.Getenv("VARYA_PAYSLIP_SAMPLE")); path != "" {
		if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoPDFRendererRejectsNonFinalizedPayroll(t *testing.T) {
	source := sampleSnapshot()
	source.PayrollStatus = "CALCULATED"
	if _, err := (GoPDFRenderer{}).Render(context.Background(), source, &bytes.Buffer{}); err != ErrPayrollNotFinalized {
		t.Fatalf("error=%v", err)
	}
}

func TestGoPDFRendererGrowsToSecondPageForManyLines(t *testing.T) {
	source := sampleSnapshot()
	for i := 0; i < 40; i++ {
		source.Deductions = append(source.Deductions, LineItem{Label: "Diğer Kesinti", Amount: "10,00 TL"})
	}
	md, err := (GoPDFRenderer{}).Render(context.Background(), source, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if md.PageCount < 2 {
		t.Fatalf("expected multi-page payslip, got %d", md.PageCount)
	}
}

func sampleSnapshot() Snapshot {
	return Snapshot{
		PayrollStatus: "FINALIZED", SourceChecksum: strings.Repeat("a", 64),
		Run:     RunSnapshot{Number: "BORDRO-2026-08-0002", Period: "Ağustos 2026", PaymentDate: "30.08.2026"},
		Company: CompanySnapshot{LegalName: "Varya Örnek Sanayi ve Ticaret A.Ş."},
		Employee: EmployeeSnapshot{
			Code: "PRS-0001", FullName: "Çağrı Öztürk", PositionTitle: "Üretim Uzmanı",
			WageType: "Aylık", MonthlyGross: "50.000,00 TL",
		},
		Work: []KeyValue{
			{Label: "Ücret Gün Sayısı", Value: "30"},
			{Label: "SGK Prim Gün Sayısı", Value: "30"},
		},
		Earnings: []LineItem{{Label: "Asıl Ücret", Amount: "33.030,00 TL"}},
		Deductions: []LineItem{
			{Label: "SGK İşçi Payı", Amount: "4.624,20 TL"},
			{Label: "İşsizlik Sigortası İşçi Payı", Amount: "330,30 TL"},
		},
		Totals: Totals{Gross: "33.030,00 TL", Deductions: "4.954,50 TL", Net: "28.075,50 TL"},
	}
}
