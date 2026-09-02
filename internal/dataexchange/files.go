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
type xlsxSharedStrings struct {
	Items []struct {
		Text string `xml:"t"`
	} `xml:"si"`
}

func readXLSX(payload []byte) (Table, error) {
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return Table{}, fmt.Errorf("%w: XLSX arşivi geçersiz", ErrInvalidTable)
	}
	files := map[string]*zip.File{}
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || len(file.Name) > 200 || strings.Contains(file.Name, "..") {
			continue
		}
		files[file.Name] = file
	}
	shared := []string{}
	if file := files["xl/sharedStrings.xml"]; file != nil {
		data, readErr := readZipMember(file, 64<<20)
		if readErr != nil {
			return Table{}, readErr
		}
		var parsed xlsxSharedStrings
		if err = xml.Unmarshal(data, &parsed); err != nil {
			return Table{}, fmt.Errorf("%w: XLSX ortak metinleri geçersiz", ErrInvalidTable)
		}
		for _, item := range parsed.Items {
			shared = append(shared, item.Text)
		}
	}
	sheet := files["xl/worksheets/sheet1.xml"]
	if sheet == nil {
		return Table{}, fmt.Errorf("%w: ilk XLSX çalışma sayfası bulunamadı", ErrInvalidTable)
	}
	data, err := readZipMember(sheet, 128<<20)
	if err != nil {
		return Table{}, err
	}
	var parsed xlsxSheet
	if err = xml.Unmarshal(data, &parsed); err != nil {
		return Table{}, fmt.Errorf("%w: XLSX çalışma sayfası geçersiz", ErrInvalidTable)
	}
	if len(parsed.Rows) == 0 {
		return Table{}, fmt.Errorf("%w: XLSX çalışma sayfası boş", ErrInvalidTable)
	}
	width := 0
	records := make([][]string, len(parsed.Rows))
	for rowIndex, row := range parsed.Rows {
		values := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			index := columnIndex(cell.Ref)
			if index < 0 || index >= maxTableCells {
				return Table{}, fmt.Errorf("%w: XLSX hücre başvurusu geçersiz", ErrInvalidTable)
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
				if parseErr != nil || sharedIndex < 0 || sharedIndex >= len(shared) {
					return Table{}, fmt.Errorf("%w: XLSX ortak metin dizini geçersiz", ErrInvalidTable)
				}
				value = shared[sharedIndex]
			}
			values[index] = value
		}
		if len(values) > width {
			width = len(values)
		}
		records[rowIndex] = values
	}
	if width == 0 || int64(width)*int64(len(records)) > maxTableCells {
		return Table{}, fmt.Errorf("%w: XLSX tablosu çok büyük", ErrInvalidTable)
	}
	return NewTable(records[0], records[1:])
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
