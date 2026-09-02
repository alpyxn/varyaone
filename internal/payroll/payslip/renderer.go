// Package payslip renders finalized payroll snapshots without live master-data
// reads. The renderer performs no payroll arithmetic: every number it prints is
// taken verbatim from the finalized snapshot it is handed.
package payslip

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/signintech/gopdf"
)

var ErrPayrollNotFinalized = errors.New("PAYROLL_NOT_FINALIZED")

// Snapshot is the fully-resolved, display-ready payslip. The caller (delivery
// service) formats money, dates and labels and splits the calculation
// components into earnings/deductions before handing it over.
type Snapshot struct {
	PayrollStatus  string
	Draft          bool
	Run            RunSnapshot
	Company        CompanySnapshot
	Employee       EmployeeSnapshot
	Work           []KeyValue
	Earnings       []LineItem
	Deductions     []LineItem
	Totals         Totals
	SourceChecksum string
}

type RunSnapshot struct {
	Number      string // Bordro No
	Period      string // "Ağustos 2026"
	PaymentDate string // "30.08.2026"
}

type CompanySnapshot struct {
	LegalName   string
	LogoDataURI string
}

type EmployeeSnapshot struct {
	Code          string
	FullName      string
	PositionTitle string
	WageType      string // "Aylık"
	MonthlyGross  string // "50.000,00 TL" — açıklama amaçlı, boşsa gösterilmez
}

type KeyValue struct{ Label, Value string }

// LineItem is one earning or deduction row. Amount is already formatted, e.g.
// "8.333,33 TL".
type LineItem struct{ Label, Amount string }

type Totals struct {
	Gross      string // Brüt Toplam
	Deductions string // Toplam Kesinti
	Net        string // Net Ödenecek
}

type Metadata struct {
	TemplateVersion, MIMEType string
	PageCount                 int
}

type Renderer interface {
	Render(context.Context, Snapshot, io.Writer) (Metadata, error)
}

func ValidateSource(source Snapshot) error {
	if source.PayrollStatus != "FINALIZED" {
		return ErrPayrollNotFinalized
	}
	if source.SourceChecksum == "" || source.Run.Number == "" || source.Company.LegalName == "" || source.Employee.Code == "" {
		return errors.New("PAYSLIP_SNAPSHOT_INVALID")
	}
	return nil
}

//go:embed assets/NotoSans.ttf
var notoSans []byte

const (
	pageLeft   = 40.0
	pageRight  = 555.0
	pageBottom = 792.0
	contentW   = pageRight - pageLeft
)

type GoPDFRenderer struct{ TemplateVersion string }

func (r GoPDFRenderer) Render(ctx context.Context, source Snapshot, destination io.Writer) (Metadata, error) {
	if err := ValidateSource(source); err != nil {
		return Metadata{}, err
	}
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	version := strings.TrimSpace(r.TemplateVersion)
	if version == "" {
		version = "tr-payslip-v2"
	}

	d := &doc{pdf: &gopdf.GoPdf{}, src: source}
	d.pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := d.pdf.AddTTFFontData("NotoSans", notoSans); err != nil {
		return Metadata{}, fmt.Errorf("register payslip font: %w", err)
	}

	d.newPage()
	d.companyHeader()
	d.documentTitle()
	d.metaBlock()
	d.personelBlock()
	if len(source.Work) > 0 {
		d.section("ÇALIŞMA BİLGİLERİ")
		for _, kv := range source.Work {
			d.keyValueRow(kv.Label, kv.Value)
		}
		d.y += 6
	}

	d.section("KAZANÇLAR")
	for _, item := range source.Earnings {
		d.amountRow(item.Label, item.Amount, false)
	}
	d.totalRow("BRÜT TOPLAM", source.Totals.Gross)

	d.section("KESİNTİLER")
	if len(source.Deductions) == 0 {
		d.amountRow("Kesinti yok", "", false)
	}
	for _, item := range source.Deductions {
		d.amountRow(item.Label, item.Amount, false)
	}
	d.totalRow("TOPLAM KESİNTİ", source.Totals.Deductions)

	d.netBlock(source.Totals.Net)
	d.footer()

	if source.Draft {
		d.watermark("TASLAK")
	}

	payload, err := d.pdf.GetBytesPdfReturnErr()
	if err != nil {
		return Metadata{}, fmt.Errorf("compile payslip PDF: %w", err)
	}
	if _, err = destination.Write(payload); err != nil {
		return Metadata{}, fmt.Errorf("write payslip PDF: %w", err)
	}
	return Metadata{TemplateVersion: version, MIMEType: "application/pdf", PageCount: d.pages}, nil
}

// ---- layout primitives ----

type doc struct {
	pdf   *gopdf.GoPdf
	src   Snapshot
	y     float64
	pages int
}

func (d *doc) newPage() {
	d.pdf.AddPage()
	d.pages++
	d.y = 42
	_ = d.pdf.SetFont("NotoSans", "", 9)
	if d.pages > 1 {
		d.text(pageLeft, d.y, contentW, 12, 8,
			d.src.Employee.FullName+"  ·  "+d.src.Run.Period+"  ·  "+d.src.Run.Number, gopdf.Left, rgb(120))
		d.text(pageLeft, d.y, contentW, 12, 8, fmt.Sprintf("Sayfa %d", d.pages), gopdf.Right, rgb(120))
		d.y += 20
		d.rule()
	}
}

// space makes sure `need` points of vertical room remain, starting a new page
// otherwise.
func (d *doc) space(need float64) {
	if d.y+need > pageBottom {
		d.newPage()
	}
}

func (d *doc) rule() {
	d.pdf.SetLineWidth(0.5)
	d.pdf.SetStrokeColor(210, 214, 220)
	d.pdf.Line(pageLeft, d.y, pageRight, d.y)
	d.pdf.SetStrokeColor(0, 0, 0)
	d.y += 12
}

func rgb(v uint8) *[3]uint8 { return &[3]uint8{v, v, v} }

func (d *doc) text(x, y, w, h, size float64, value string, align int, color *[3]uint8) {
	_ = d.pdf.SetFontSize(size)
	if color != nil {
		d.pdf.SetTextColor(color[0], color[1], color[2])
	} else {
		d.pdf.SetTextColor(20, 22, 26)
	}
	d.pdf.SetXY(x, y)
	opt := gopdf.CellOption{Align: align}
	_ = d.pdf.CellWithOption(&gopdf.Rect{W: w, H: h}, value, opt)
	d.pdf.SetTextColor(20, 22, 26)
	_ = d.pdf.SetFontSize(9)
}

func (d *doc) companyHeader() {
	if d.src.Company.LogoDataURI != "" {
		if raw, err := decodeLogo(d.src.Company.LogoDataURI); err == nil {
			if holder, e := gopdf.ImageHolderByBytes(raw); e == nil {
				_ = d.pdf.ImageByHolder(holder, pageRight-60, d.y-4, &gopdf.Rect{W: 60, H: 34})
			}
		}
	}
	d.text(pageLeft, d.y, contentW-70, 16, 11, strings.ToUpper(d.src.Company.LegalName), gopdf.Left, nil)
	d.y += 26
}

func (d *doc) documentTitle() {
	d.text(pageLeft, d.y, contentW, 22, 17, "ÜCRET HESAP PUSULASI", gopdf.Center, nil)
	d.y += 30
	d.rule()
}

func (d *doc) metaBlock() {
	rows := [][2]string{
		{"Bordro No", d.src.Run.Number},
		{"Dönem", d.src.Run.Period},
		{"Ödeme Tarihi", d.src.Run.PaymentDate},
	}
	for _, row := range rows {
		d.keyValueRow(row[0], row[1])
	}
	d.y += 10
}

func (d *doc) personelBlock() {
	d.section("PERSONEL")
	d.keyValueRow("Personel Kodu", d.src.Employee.Code)
	d.keyValueRow("Ad Soyad", d.src.Employee.FullName)
	if strings.TrimSpace(d.src.Employee.PositionTitle) != "" {
		d.keyValueRow("Pozisyon", d.src.Employee.PositionTitle)
	}
	if strings.TrimSpace(d.src.Employee.WageType) != "" {
		d.keyValueRow("Ücret Türü", d.src.Employee.WageType)
	}
	if strings.TrimSpace(d.src.Employee.MonthlyGross) != "" {
		d.keyValueRow("Aylık Brüt Ücret", d.src.Employee.MonthlyGross)
	}
	d.y += 6
}

func (d *doc) section(title string) {
	d.space(60)
	d.y += 4
	d.text(pageLeft, d.y, contentW, 14, 9.5, title, gopdf.Left, &[3]uint8{90, 96, 104})
	d.y += 15
	d.pdf.SetLineWidth(0.8)
	d.pdf.SetStrokeColor(60, 64, 70)
	d.pdf.Line(pageLeft, d.y, pageRight, d.y)
	d.pdf.SetStrokeColor(0, 0, 0)
	d.y += 8
}

func (d *doc) keyValueRow(label, value string) {
	d.space(20)
	d.text(pageLeft, d.y, 150, 13, 9, label, gopdf.Left, &[3]uint8{110, 116, 124})
	d.text(pageLeft+150, d.y, contentW-150, 13, 9, value, gopdf.Left, nil)
	d.y += 15
}

func (d *doc) amountRow(label, amount string, strong bool) {
	d.space(18)
	size := 9.0
	var color *[3]uint8
	if strong {
		size = 9.5
	}
	d.text(pageLeft, d.y, contentW-140, 13, size, label, gopdf.Left, color)
	d.text(pageLeft+contentW-140, d.y, 140, 13, size, amount, gopdf.Right, color)
	d.y += 15
}

func (d *doc) totalRow(label, amount string) {
	d.space(24)
	d.y += 2
	d.pdf.SetLineWidth(0.5)
	d.pdf.SetStrokeColor(60, 64, 70)
	d.pdf.Line(pageLeft, d.y, pageRight, d.y)
	d.pdf.SetStrokeColor(0, 0, 0)
	d.y += 5
	d.text(pageLeft, d.y, contentW-140, 14, 9.5, label, gopdf.Left, nil)
	d.text(pageLeft+contentW-140, d.y, 140, 14, 9.5, amount, gopdf.Right, nil)
	d.y += 18
}

func (d *doc) netBlock(net string) {
	d.space(56)
	d.y += 10
	d.pdf.SetFillColor(238, 240, 243)
	d.pdf.RectFromUpperLeftWithStyle(pageLeft, d.y, contentW, 42, "F")
	d.pdf.SetFillColor(0, 0, 0)
	d.text(pageLeft+14, d.y+8, 200, 14, 9.5, "NET ÖDENECEK", gopdf.Left, &[3]uint8{90, 96, 104})
	d.text(pageLeft+contentW-260, d.y+6, 246, 22, 16, net, gopdf.Right, nil)
	d.y += 54
}

func (d *doc) footer() {
	d.pdf.SetXY(pageLeft, pageBottom+18)
	_ = d.pdf.SetFontSize(7.5)
	d.pdf.SetTextColor(130, 136, 144)
	_ = d.pdf.CellWithOption(&gopdf.Rect{W: contentW, H: 10},
		"Bu belge kesinleşmiş bordro kayıtlarından oluşturulmuştur.", gopdf.CellOption{Align: gopdf.Center})

	// Varya One markası: 3 kırmızı + 1 siyah dikey bar, ardından marka satırı.
	markX := pageLeft + contentW/2 - 62
	markY := pageBottom + 31
	for i := 0; i < 3; i++ {
		d.pdf.SetFillColor(193, 39, 45)
		d.pdf.RectFromUpperLeftWithStyle(markX+float64(i)*5, markY, 3, 9, "F")
	}
	d.pdf.SetFillColor(26, 26, 26)
	d.pdf.RectFromUpperLeftWithStyle(markX+15, markY, 3, 9, "F")
	d.pdf.SetFillColor(0, 0, 0)

	d.pdf.SetXY(markX+24, markY-1)
	_ = d.pdf.SetFontSize(7.5)
	d.pdf.SetTextColor(90, 96, 104)
	_ = d.pdf.CellWithOption(&gopdf.Rect{W: 260, H: 10},
		"Varya One · Bu belge Varya One ile oluşturulmuştur.", gopdf.CellOption{Align: gopdf.Left})

	d.pdf.SetTextColor(20, 22, 26)
	_ = d.pdf.SetFontSize(9)
}

func (d *doc) watermark(label string) {
	_ = d.pdf.SetFontSize(60)
	d.pdf.SetTextColor(232, 232, 232)
	d.pdf.SetXY(pageLeft, 380)
	_ = d.pdf.CellWithOption(&gopdf.Rect{W: contentW, H: 80}, label, gopdf.CellOption{Align: gopdf.Center})
	d.pdf.SetTextColor(20, 22, 26)
	_ = d.pdf.SetFontSize(9)
}

func decodeLogo(value string) ([]byte, error) {
	comma := strings.IndexByte(value, ',')
	if comma < 0 || !strings.Contains(value[:comma], ";base64") {
		return nil, errors.New("invalid logo data URI")
	}
	return base64.StdEncoding.DecodeString(value[comma+1:])
}
