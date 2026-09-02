package dataexchange

import (
	"fmt"
	"strings"
)

// Row is a normalized source row. Values are ordered exactly like Table.Headers.
type Row struct {
	Number int
	Values []string
}

// Table is the adapter-neutral tabular input. File readers should convert
// their records into this rectangular representation before calling the core.
type Table struct {
	Headers []string
	Rows    []Row
}

// NewTable trims cells, assigns source row numbers beginning at 2, pads short
// records with empty cells, and rejects records wider than the header.
func NewTable(headers []string, records [][]string) (Table, error) {
	if len(headers) == 0 {
		return Table{}, fmt.Errorf("%w: en az bir başlık gereklidir", ErrInvalidTable)
	}

	normalizedHeaders := make([]string, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for index, header := range headers {
		header = strings.TrimSpace(header)
		if header == "" {
			return Table{}, fmt.Errorf("%w: %d numaralı başlık boş", ErrInvalidTable, index+1)
		}
		key := normalizeHeader(header)
		if key == "" {
			return Table{}, fmt.Errorf("%w: %d numaralı başlık kullanılamaz", ErrInvalidTable, index+1)
		}
		if _, exists := seen[key]; exists {
			return Table{}, fmt.Errorf("%w: %d numaralı sütunda yinelenen başlık var", ErrInvalidTable, index+1)
		}
		seen[key] = struct{}{}
		normalizedHeaders[index] = header
	}

	rows := make([]Row, len(records))
	for rowIndex, record := range records {
		if len(record) > len(normalizedHeaders) {
			return Table{}, fmt.Errorf("%w: %d numaralı satırda başlıklardan fazla hücre var", ErrInvalidTable, rowIndex+2)
		}
		values := make([]string, len(normalizedHeaders))
		for columnIndex, value := range record {
			values[columnIndex] = strings.TrimSpace(value)
		}
		rows[rowIndex] = Row{Number: rowIndex + 2, Values: values}
	}

	return Table{Headers: normalizedHeaders, Rows: rows}, nil
}

func (t Table) validate() error {
	if len(t.Headers) == 0 {
		return fmt.Errorf("%w: en az bir başlık gereklidir", ErrInvalidTable)
	}
	seen := make(map[string]struct{}, len(t.Headers))
	for index, header := range t.Headers {
		if strings.TrimSpace(header) == "" {
			return fmt.Errorf("%w: %d numaralı başlık boş", ErrInvalidTable, index+1)
		}
		key := normalizeHeader(header)
		if key == "" {
			return fmt.Errorf("%w: %d numaralı başlık kullanılamaz", ErrInvalidTable, index+1)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: %d numaralı sütunda yinelenen başlık var", ErrInvalidTable, index+1)
		}
		seen[key] = struct{}{}
	}
	for rowIndex, row := range t.Rows {
		if len(row.Values) != len(t.Headers) {
			return fmt.Errorf("%w: %d numaralı satırın hücre sayısı başlıklarla uyuşmuyor", ErrInvalidTable, rowNumber(row, rowIndex))
		}
	}
	return nil
}

func rowNumber(row Row, index int) int {
	if row.Number > 0 {
		return row.Number
	}
	return index + 2
}
