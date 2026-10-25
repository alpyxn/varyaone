package dataexchange

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/money"
)

const maxTableCells = 2_000_000

// ReadTable accepts CSV and the common XLSX worksheet shape without loading
// arbitrary archive members. Callers still enforce the upload byte limit in
// storage/API code.
func ReadTable(reader io.Reader, filename string) (Table, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, (256<<20)+1))
	if err != nil {
		return Table{}, err
	}
	if len(payload) > 256<<20 {
		return Table{}, fmt.Errorf("%w: dosya çok büyük", ErrInvalidTable)
	}
	if len(payload) == 0 {
		return Table{}, fmt.Errorf("%w: dosya boş", ErrInvalidTable)
	}
	if strings.EqualFold(filepath.Ext(filename), ".xlsx") || bytes.HasPrefix(payload, []byte("PK\x03\x04")) {
		return readXLSX(payload)
	}
	return readCSV(payload)
}

func readCSV(payload []byte) (Table, error) {
	reader := csv.NewReader(bytes.NewReader(payload))
	records, err := reader.ReadAll()
	if err != nil {
		return Table{}, fmt.Errorf("%w: CSV okunamadı: %v", ErrInvalidTable, err)
	}
	if len(records) < 1 {
		return Table{}, fmt.Errorf("%w: CSV başlığı bulunamadı", ErrInvalidTable)
	}
	if len(records[0]) == 0 || len(records)*len(records[0]) > maxTableCells {
		return Table{}, fmt.Errorf("%w: CSV tablosu çok büyük", ErrInvalidTable)
	}
	return NewTable(records[0], records[1:])
}

type xlsxCell struct {
	Ref  string `xml:"r,attr"`
	Type string `xml:"t,attr"`
	V    string `xml:"v"`
	Text string `xml:"is>t"`
}

type xlsxRow struct {
	Cells []xlsxCell `xml:"c"`
}
type xlsxSheet struct {
	Rows []xlsxRow `xml:"sheetData>row"`
}

// xlsxSharedStrings reads plain strings (<si><t>) and rich-text strings, whose
// text is split over formatting runs (<si><r><t>). Phonetic hints (<rPh>) are
// not part of the cell text and are ignored.
type xlsxSharedStrings struct {
	Items []struct {
		Text string `xml:"t"`
		Runs []struct {
			Text string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type xlsxWorkbook struct {
	Sheets []struct {
		Name string `xml:"name,attr"`
		ID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sheets>sheet"`
}

type xlsxRelationships struct {
	Items []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type xlsxSheetRef struct{ name, path string }

type xlsxArchive struct {
	files  map[string]*zip.File
	shared []string
	sheets []xlsxSheetRef
}

// NamedTable is one worksheet of a workbook.
type NamedTable struct {
	Name  string
	Table Table
}

func openXLSX(payload []byte) (xlsxArchive, error) {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return xlsxArchive{}, fmt.Errorf("%w: XLSX arşivi geçersiz", ErrInvalidTable)
	}
	result := xlsxArchive{files: map[string]*zip.File{}}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || len(file.Name) > 200 || strings.Contains(file.Name, "..") {
			continue
		}
		result.files[file.Name] = file
	}
	if file := result.files["xl/sharedStrings.xml"]; file != nil {
		data, readErr := readZipMember(file, 64<<20)
		if readErr != nil {
			return xlsxArchive{}, readErr
		}
		var parsed xlsxSharedStrings
		if err = xml.Unmarshal(data, &parsed); err != nil {
			return xlsxArchive{}, fmt.Errorf("%w: XLSX ortak metinleri geçersiz", ErrInvalidTable)
		}
		for _, item := range parsed.Items {
			text := item.Text
			for _, run := range item.Runs {
				text += run.Text
			}
			result.shared = append(result.shared, text)
		}
	}
	result.sheets = workbookSheets(result.files)
	return result, nil
}

// workbookSheets lists worksheets in the order the workbook shows them. The
// part names (sheet1.xml, sheet2.xml …) need not follow that order, so the
// workbook and its relationships are read; a workbook without them falls back
// to sheet1.xml.
func workbookSheets(files map[string]*zip.File) []xlsxSheetRef {
	fallback := []xlsxSheetRef{{"Sayfa1", "xl/worksheets/sheet1.xml"}}
	book, rels := files["xl/workbook.xml"], files["xl/_rels/workbook.xml.rels"]
	if book == nil || rels == nil {
		return fallback
	}
	bookData, err := readZipMember(book, 8<<20)
	if err != nil {
		return fallback
	}
	relData, err := readZipMember(rels, 8<<20)
	if err != nil {
		return fallback
	}
	var workbook xlsxWorkbook
	var relationships xlsxRelationships
	if xml.Unmarshal(bookData, &workbook) != nil || xml.Unmarshal(relData, &relationships) != nil {
		return fallback
	}
	targets := map[string]string{}
	for _, rel := range relationships.Items {
		if strings.HasPrefix(rel.Target, "/") {
			targets[rel.ID] = strings.TrimPrefix(rel.Target, "/")
		} else {
			targets[rel.ID] = "xl/" + rel.Target
		}
	}
	sheets := []xlsxSheetRef{}
	for _, sheet := range workbook.Sheets {
		if target, ok := targets[sheet.ID]; ok && files[target] != nil {
			sheets = append(sheets, xlsxSheetRef{sheet.Name, target})
		}
	}
	if len(sheets) == 0 {
		return fallback
	}
	return sheets
}

func (a xlsxArchive) records(path string) ([][]string, error) {
	sheet := a.files[path]
	if sheet == nil {
		return nil, fmt.Errorf("%w: ilk XLSX çalışma sayfası bulunamadı", ErrInvalidTable)
	}
	data, err := readZipMember(sheet, 128<<20)
	if err != nil {
		return nil, err
	}
	var parsed xlsxSheet
	if err = xml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("%w: XLSX çalışma sayfası geçersiz", ErrInvalidTable)
	}
	width := 0
	records := make([][]string, len(parsed.Rows))
	for rowIndex, row := range parsed.Rows {
		values := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			index := columnIndex(cell.Ref)
			if index < 0 || index >= maxTableCells {
				return nil, fmt.Errorf("%w: XLSX hücre başvurusu geçersiz", ErrInvalidTable)
			}
			for len(values) <= index {
				values = append(values, "")
			}
			value := cell.V
			if cell.Type == "inlineStr" {
				value = cell.Text
			}
			if cell.Type == "s" {
				sharedIndex, parseErr := strconv.Atoi(cell.V)
				if parseErr != nil || sharedIndex < 0 || sharedIndex >= len(a.shared) {
					return nil, fmt.Errorf("%w: XLSX ortak metin dizini geçersiz", ErrInvalidTable)
				}
				value = a.shared[sharedIndex]
			}
			values[index] = value
		}
		if len(values) > width {
			width = len(values)
		}
		records[rowIndex] = values
	}
	if int64(width)*int64(len(records)) > maxTableCells {
		return nil, fmt.Errorf("%w: XLSX tablosu çok büyük", ErrInvalidTable)
	}
	return records, nil
}

func readXLSX(payload []byte) (Table, error) {
	archive, err := openXLSX(payload)
	if err != nil {
		return Table{}, err
	}
	records, err := archive.records(archive.sheets[0].path)
	if err != nil {
		return Table{}, err
	}
	width := 0
	for _, record := range records {
		if len(record) > width {
			width = len(record)
		}
	}
	if len(records) == 0 || width == 0 {
		return Table{}, fmt.Errorf("%w: XLSX çalışma sayfası boş", ErrInvalidTable)
	}
	return NewTable(records[0], records[1:])
}

// ReadWorkbook returns every non-empty worksheet of an XLSX file (a CSV file
// is a single sheet) for callers that let the user pick the sheet. It is
// lenient about headers typed by hand: the first non-empty row is the header,
// a blank heading becomes "Sütun N", a repeated one gets " (2)", and cells past
// the last heading get a heading of their own.
func ReadWorkbook(reader io.Reader, filename string) ([]NamedTable, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, (256<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > 256<<20 {
		return nil, fmt.Errorf("%w: dosya çok büyük", ErrInvalidTable)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: dosya boş", ErrInvalidTable)
	}
	if !strings.EqualFold(filepath.Ext(filename), ".xlsx") && !bytes.HasPrefix(payload, []byte("PK\x03\x04")) {
		csvReader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(payload, []byte("\xef\xbb\xbf"))))
		csvReader.FieldsPerRecord = -1
		csvReader.LazyQuotes = true
		records, err := csvReader.ReadAll()
		if err != nil {
			return nil, fmt.Errorf("%w: CSV okunamadı", ErrInvalidTable)
		}
		table, ok := lenientTable(records)
		if !ok {
			return nil, fmt.Errorf("%w: dosya boş", ErrInvalidTable)
		}
		return []NamedTable{{Name: "CSV", Table: table}}, nil
	}
	archive, err := openXLSX(payload)
	if err != nil {
		return nil, err
	}
	sheets := []NamedTable{}
	for _, sheet := range archive.sheets {
		records, err := archive.records(sheet.path)
		if err != nil {
			return nil, err
		}
		if table, ok := lenientTable(records); ok {
			sheets = append(sheets, NamedTable{Name: sheet.name, Table: table})
		}
	}
	if len(sheets) == 0 {
		return nil, fmt.Errorf("%w: dosyada dolu çalışma sayfası yok", ErrInvalidTable)
	}
	return sheets, nil
}

func lenientTable(records [][]string) (Table, bool) {
	blank := func(record []string) bool {
		for _, value := range record {
			if strings.TrimSpace(value) != "" {
				return false
			}
		}
		return true
	}
	for len(records) > 0 && blank(records[0]) {
		records = records[1:]
	}
	if len(records) == 0 {
		return Table{}, false
	}
	width := 0
	for _, record := range records {
		if len(record) > width {
			width = len(record)
		}
	}
	headers := make([]string, width)
	seen := map[string]bool{}
	for index := range headers {
		header := ""
		if index < len(records[0]) {
			header = strings.TrimSpace(records[0][index])
		}
		if normalizeHeader(header) == "" {
			header = "Sütun " + strconv.Itoa(index+1)
		}
		unique := header
		for n := 2; seen[normalizeHeader(unique)]; n++ {
			unique = header + " (" + strconv.Itoa(n) + ")"
		}
		seen[normalizeHeader(unique)] = true
		headers[index] = unique
	}
	rows := make([][]string, 0, len(records)-1)
	for _, record := range records[1:] {
		if !blank(record) {
			rows = append(rows, record)
		}
	}
	table, err := NewTable(headers, rows)
	return table, err == nil
}

func readZipMember(file *zip.File, max int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: XLSX arşiv öğesi açılamadı", ErrInvalidTable)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, fmt.Errorf("%w: XLSX arşiv öğesi çok büyük", ErrInvalidTable)
	}
	return data, nil
}

func columnIndex(ref string) int {
	letters := strings.TrimRight(strings.ToUpper(ref), "0123456789")
	if letters == "" {
		return -1
	}
	index := 0
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return -1
		}
		index = index*26 + int(letter-'A'+1)
	}
	return index - 1
}

func WriteCSV(writer io.Writer, table Table) error {
	if err := table.validate(); err != nil {
		return err
	}
	encoder := csv.NewWriter(writer)
	if err := encoder.Write(table.Headers); err != nil {
		return err
	}
	for _, row := range table.Rows {
		if err := encoder.Write(row.Values); err != nil {
			return err
		}
	}
	encoder.Flush()
	return encoder.Error()
}

// WriteXLSX writes a deliberately small first-sheet workbook using inline
// strings. It is enough for ERP exports and avoids a provider-specific SDK.
func WriteXLSX(writer io.Writer, table Table) error {
	return WriteXLSXWithOptions(writer, table, XLSXOptions{})
}

// XLSXOptions lets financial exports use a domain-specific worksheet name and
// real numeric cells. NumericColumn indexes are zero-based; headers remain text.
type XLSXOptions struct {
	SheetName      string
	NumericColumns map[int]bool
}

func WriteXLSXWithOptions(writer io.Writer, table Table, options XLSXOptions) error {
	if err := table.validate(); err != nil {
		return err
	}
	sheetName := strings.TrimSpace(options.SheetName)
	if sheetName == "" {
		sheetName = "Aktarım"
	}
	if len([]rune(sheetName)) > 31 || strings.ContainsAny(sheetName, `[]:*?/\`) {
		return fmt.Errorf("%w: çalışma sayfası adı geçersiz", ErrInvalidTable)
	}
	for column := range options.NumericColumns {
		if column < 0 || column >= len(table.Headers) {
			return fmt.Errorf("%w: sayısal sütun indeksi geçersiz", ErrInvalidTable)
		}
	}
	archive := zip.NewWriter(writer)
	entries := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="` + xmlEscape(sheetName) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	rows := append([][]string{table.Headers}, func() [][]string {
		result := make([][]string, len(table.Rows))
		for i, row := range table.Rows {
			result[i] = row.Values
		}
		return result
	}()...)
	for rowIndex, row := range rows {
		sheet.WriteString(`<row r="` + strconv.Itoa(rowIndex+1) + `">`)
		for column, value := range row {
			cellRef := excelRef(column, rowIndex+1)
			if rowIndex > 0 && options.NumericColumns[column] && value != "" {
				decimal, err := money.ParseDecimal(value, 18)
				if err != nil {
					_ = archive.Close()
					return fmt.Errorf("%w: %s hücresindeki sayısal değer geçersiz", ErrInvalidTable, cellRef)
				}
				sheet.WriteString(`<c r="` + cellRef + `"><v>` + decimal.String() + `</v></c>`)
			} else {
				sheet.WriteString(`<c r="` + cellRef + `" t="inlineStr"><is><t>` + xmlEscape(value) + `</t></is></c>`)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	entries["xl/worksheets/sheet1.xml"] = sheet.String()
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		file, err := archive.Create(key)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(file, entries[key]); err != nil {
			return err
		}
	}
	return archive.Close()
}

func excelRef(column, row int) string {
	result := ""
	for column >= 0 {
		result = string(rune('A'+column%26)) + result
		column = column/26 - 1
	}
	return result + strconv.Itoa(row)
}
func xmlEscape(value string) string {
	var builder strings.Builder
	_ = xml.EscapeText(&builder, []byte(value))
	return builder.String()
}
