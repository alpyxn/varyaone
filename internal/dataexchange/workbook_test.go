package dataexchange

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// testWorkbook builds an XLSX whose visible sheet order ("Özet", "Liste")
// differs from its part names, with a rich-text cell and a repeated heading,
// the way hand-made cold-call lists look.
func testWorkbook(t *testing.T) []byte {
	t.Helper()
	parts := map[string]string{
		"xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="Özet" sheetId="1" r:id="rId7"/><sheet name="Liste" sheetId="2" r:id="rId3"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId3" Target="worksheets/sheet1.xml"/><Relationship Id="rId7" Target="/xl/worksheets/sheet2.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<si><t>Firma Adı</t></si><si><t>Durum</t></si><si><r><t>Ege </t></r><r><rPr><b/></rPr><t>Ambalaj</t></r><rPh><t>x</t></rPh></si><si><t>Özet bilgisi</t></si></sst>`,
		// sheet1.xml is the second visible sheet ("Liste").
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>
<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>1</v></c></row>
<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="inlineStr"><is><t>Aktif</t></is></c><c r="C2" t="inlineStr"><is><t>İlgilenmiyor</t></is></c><c r="D2" t="inlineStr"><is><t>fazla</t></is></c></row>
<row r="3"></row>
</sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>
<row r="1"><c r="A1" t="s"><v>3</v></c></row></sheetData></worksheet>`,
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range parts {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestReadWorkbookFollowsSheetOrderAndToleratesHandMadeHeaders(t *testing.T) {
	sheets, err := ReadWorkbook(bytes.NewReader(testWorkbook(t)), "liste.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 2 || sheets[0].Name != "Özet" || sheets[1].Name != "Liste" {
		t.Fatalf("sheets: %+v", sheets)
	}
	list := sheets[1].Table
	if got := strings.Join(list.Headers, "|"); got != "Firma Adı|Durum|Durum (2)|Sütun 4" {
		t.Fatalf("headers: %s", got)
	}
	if len(list.Rows) != 1 {
		t.Fatalf("blank rows must be dropped: %d rows", len(list.Rows))
	}
	if got := strings.Join(list.Rows[0].Values, "|"); got != "Ege Ambalaj|Aktif|İlgilenmiyor|fazla" {
		t.Fatalf("row: %s", got)
	}
}

func TestReadTableUsesFirstVisibleSheet(t *testing.T) {
	table, err := ReadTable(bytes.NewReader(testWorkbook(t)), "liste.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Headers) != 1 || table.Headers[0] != "Özet bilgisi" {
		t.Fatalf("first visible sheet not read: %v", table.Headers)
	}
}

func TestReadWorkbookCSV(t *testing.T) {
	sheets, err := ReadWorkbook(strings.NewReader("\xef\xbb\xbfFirma,Telefon,\nAlfa,0532 000 00 01,x\n"), "a.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 1 || strings.Join(sheets[0].Table.Headers, "|") != "Firma|Telefon|Sütun 3" {
		t.Fatalf("csv: %+v", sheets)
	}
}
