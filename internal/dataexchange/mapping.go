package dataexchange

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// FieldType is the provider-neutral representation used for one exchange
// field. Decimal values are transported as strings so adapters can preserve
// exact precision without going through floating point.
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeText    FieldType = FieldTypeString
	FieldTypeBoolean FieldType = "boolean"
	FieldTypeDecimal FieldType = "decimal"
	FieldTypeJSON    FieldType = "json"
	FieldTypeInteger FieldType = "integer"
)

// FieldSpec describes one canonical target field and its source-header
// aliases. Name is always a stable target field ID; Label is only the
// human-facing Turkish text used by mapping screens and exported headers.
type FieldSpec struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Type     FieldType `json:"type"`
	Aliases  []string  `json:"aliases,omitempty"`
	Required bool      `json:"required"`
	Example  string    `json:"example,omitempty"`
}

// MappingOptions contains explicit target-field to source-header assignments.
// Fields not listed here are resolved automatically.
type MappingOptions struct {
	Manual map[string]string
}

// MappingMethod records whether a column assignment was explicit or automatic.
type MappingMethod string

const (
	MappingMethodManual MappingMethod = "MANUAL"
	MappingMethodAuto   MappingMethod = "AUTO"
)

// ColumnMapping is one target-field to source-column assignment.
type ColumnMapping struct {
	Field        string        `json:"field"`
	SourceHeader string        `json:"source_header"`
	SourceIndex  int           `json:"source_index"`
	Method       MappingMethod `json:"method"`
}

// Mapping is an immutable-by-convention resolved column mapping.
type Mapping struct {
	SourceHeaders []string        `json:"source_headers"`
	Columns       []ColumnMapping `json:"columns"`
}

// MappingResult contains the resolved mapping and non-fatal mapping warnings.
// When an error is returned, Issues also contains the structured mapping errors.
type MappingResult struct {
	Mapping Mapping
	Issues  []Issue
}

// MappedRow contains canonical field values for one source row.
type MappedRow struct {
	RowNumber int
	Values    map[string]string
}

// MappingError reports one or more mapping failures without exposing source values.
type MappingError struct {
	Issues []Issue
}

func (e *MappingError) Error() string {
	if e == nil {
		return ErrInvalidMapping.Error()
	}
	return fmt.Sprintf("%s: %d issue(s)", ErrInvalidMapping, len(e.Issues))
}

func (e *MappingError) Unwrap() error { return ErrInvalidMapping }

// ResolveMapping resolves manual assignments first, then exact normalized
// header/alias matches. Required fields must resolve and one source column may
// not be assigned to multiple target fields.
func ResolveMapping(table Table, fields []FieldSpec, options MappingOptions) (MappingResult, error) {
	if err := table.validate(); err != nil {
		return MappingResult{}, err
	}
	if len(fields) == 0 {
		return MappingResult{}, &MappingError{Issues: []Issue{{
			Code:     "no_fields",
			Severity: SeverityError,
			Message:  "en az bir aktarım alanı gereklidir",
		}}}
	}

	canonicalFields := make(map[string]FieldSpec, len(fields))
	normalizedFields := make([]FieldSpec, len(fields))
	for index := range fields {
		field := fields[index]
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" || normalizeHeader(field.Name) == "" {
			return MappingResult{}, &MappingError{Issues: []Issue{{
				Field:    field.Name,
				Code:     "invalid_field",
				Severity: SeverityError,
				Message:  "aktarılacak alan adı boş veya kullanılamaz",
			}}}
		}
		field.Aliases = append([]string(nil), field.Aliases...)
		key := normalizeHeader(field.Name)
		if _, exists := canonicalFields[key]; exists {
			return MappingResult{}, &MappingError{Issues: []Issue{{
				Field:    field.Name,
				Code:     "duplicate_field",
				Severity: SeverityError,
				Message:  "aktarılacak alan birden fazla tanımlanmış",
			}}}
		}
		canonicalFields[key] = field
		normalizedFields[index] = field
	}

	headerIndexes := make(map[string]int, len(table.Headers))
	for index, header := range table.Headers {
		headerIndexes[normalizeHeader(header)] = index
	}

	manual, manualIssues := normalizeManualMappings(options.Manual, canonicalFields)
	issues := append([]Issue(nil), manualIssues...)
	mapping := Mapping{SourceHeaders: append([]string(nil), table.Headers...)}
	usedSources := make(map[int]string, len(fields))
	assignedFields := make(map[string]struct{}, len(fields))

	for _, field := range normalizedFields {
		fieldKey := normalizeHeader(field.Name)
		sourceKey, isManual := manual[fieldKey]
		if isManual {
			if sourceKey == "" {
				issues = append(issues, Issue{Field: field.Name, Code: "manual_source_empty", Severity: SeverityError, Message: "elle seçilen kaynak sütun boş"})
				continue
			}
			sourceIndex, exists := headerIndexes[sourceKey]
			if !exists {
				issues = append(issues, Issue{Field: field.Name, Code: "manual_source_missing", Severity: SeverityError, Message: "elle seçilen kaynak sütun bulunamadı"})
				continue
			}
			if previous, exists := usedSources[sourceIndex]; exists {
				issues = append(issues, Issue{Field: field.Name, Code: "source_column_reused", Severity: SeverityError, Message: fmt.Sprintf("kaynak sütun zaten %s alanına eşlenmiş", previous)})
				continue
			}
			mapping.Columns = append(mapping.Columns, ColumnMapping{
				Field: field.Name, SourceHeader: table.Headers[sourceIndex], SourceIndex: sourceIndex, Method: MappingMethodManual,
			})
			usedSources[sourceIndex] = field.Name
			assignedFields[fieldKey] = struct{}{}
			continue
		}

		candidates := matchingHeaders(table.Headers, field)
		if len(candidates) == 0 {
			severity := SeverityWarning
			code := "optional_field_unmapped"
			message := "isteğe bağlı aktarım alanı eşlenmedi"
			if field.Required {
				severity = SeverityError
				code = "required_field_unmapped"
				message = "zorunlu aktarım alanı eşlenmedi"
			}
			issues = append(issues, Issue{Field: field.Name, Code: code, Severity: severity, Message: message})
			continue
		}
		if len(candidates) > 1 {
			issues = append(issues, Issue{Field: field.Name, Code: "ambiguous_mapping", Severity: SeverityError, Message: "aktarılacak alanla birden fazla kaynak sütun eşleşiyor"})
			continue
		}
		sourceIndex := candidates[0]
		if previous, exists := usedSources[sourceIndex]; exists {
			issues = append(issues, Issue{Field: field.Name, Code: "source_column_reused", Severity: SeverityError, Message: fmt.Sprintf("kaynak sütun zaten %s alanına eşlenmiş", previous)})
			continue
		}
		mapping.Columns = append(mapping.Columns, ColumnMapping{
			Field: field.Name, SourceHeader: table.Headers[sourceIndex], SourceIndex: sourceIndex, Method: MappingMethodAuto,
		})
		usedSources[sourceIndex] = field.Name
		assignedFields[fieldKey] = struct{}{}
	}

	unknownManualFields := make([]string, 0)
	for fieldKey := range manual {
		if _, assigned := assignedFields[fieldKey]; !assigned {
			if _, known := canonicalFields[fieldKey]; known {
				continue
			}
			unknownManualFields = append(unknownManualFields, fieldKey)
		}
	}
	sort.Strings(unknownManualFields)
	for _, fieldKey := range unknownManualFields {
		issues = append(issues, Issue{Field: fieldKey, Code: "manual_target_unknown", Severity: SeverityError, Message: "elle seçilen aktarım alanı tanımlı değil"})
	}

	for index, header := range table.Headers {
		if _, used := usedSources[index]; !used {
			issues = append(issues, Issue{Field: header, Code: "unmapped_source_column", Severity: SeverityWarning, Message: "kaynak sütun eşlemede kullanılmıyor"})
		}
	}

	if hasError(issues) {
		return MappingResult{Mapping: mapping, Issues: issues}, &MappingError{Issues: copyIssues(issues)}
	}
	return MappingResult{Mapping: mapping, Issues: issues}, nil
}

func normalizeManualMappings(manual map[string]string, fields map[string]FieldSpec) (map[string]string, []Issue) {
	result := make(map[string]string, len(manual))
	if len(manual) == 0 {
		return result, nil
	}
	keys := make([]string, 0, len(manual))
	for key := range manual {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var issues []Issue
	for _, key := range keys {
		fieldKey := normalizeHeader(key)
		if fieldKey == "" {
			issues = append(issues, Issue{Code: "manual_target_empty", Severity: SeverityError, Message: "elle seçilen aktarım alanı boş"})
			continue
		}
		if _, exists := fields[fieldKey]; !exists {
			result[fieldKey] = normalizeHeader(manual[key])
			continue
		}
		if previous, exists := result[fieldKey]; exists && previous != normalizeHeader(manual[key]) {
			issues = append(issues, Issue{Field: key, Code: "manual_target_ambiguous", Severity: SeverityError, Message: "aktarılacak alan için birbiriyle çelişen elle eşlemeler var"})
			continue
		}
		result[fieldKey] = normalizeHeader(manual[key])
	}
	return result, issues
}

func matchingHeaders(headers []string, field FieldSpec) []int {
	accepted := map[string]struct{}{normalizeHeader(field.Name): {}}
	for _, alias := range field.Aliases {
		if key := normalizeHeader(alias); key != "" {
			accepted[key] = struct{}{}
		}
	}
	var matches []int
	for index, header := range headers {
		if _, exists := accepted[normalizeHeader(header)]; exists {
			matches = append(matches, index)
		}
	}
	return matches
}

// Lookup returns the source column index for a canonical target field.
func (m Mapping) Lookup(field string) (int, bool) {
	key := normalizeHeader(field)
	for _, column := range m.Columns {
		if normalizeHeader(column.Field) == key {
			return column.SourceIndex, true
		}
	}
	return 0, false
}

// MapRow projects a rectangular source row into canonical target fields.
func (m Mapping) MapRow(row Row) (MappedRow, error) {
	if len(m.SourceHeaders) == 0 || len(row.Values) != len(m.SourceHeaders) {
		return MappedRow{}, fmt.Errorf("%w: satır eşlemeyle uyumlu değil", ErrInvalidTable)
	}
	values := make(map[string]string, len(m.Columns))
	for _, column := range m.Columns {
		if column.SourceIndex < 0 || column.SourceIndex >= len(row.Values) {
			return MappedRow{}, fmt.Errorf("%w: eşleme kaynak sütun numarası aralık dışında", ErrInvalidMapping)
		}
		values[column.Field] = strings.TrimSpace(row.Values[column.SourceIndex])
	}
	return MappedRow{RowNumber: row.Number, Values: values}, nil
}

func normalizeHeader(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		switch character {
		case 'ç':
			character = 'c'
		case 'ğ':
			character = 'g'
		case 'ı':
			character = 'i'
		case 'ö':
			character = 'o'
		case 'ş':
			character = 's'
		case 'ü':
			character = 'u'
		}
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func hasError(issues []Issue) bool {
	for _, issue := range issues {
		if issue.IsError() {
			return true
		}
	}
	return false
}
