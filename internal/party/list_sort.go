package party

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// These names match scanParty's projection, including computed contact and
// balance fields. Only allowlisted expressions can enter an ORDER BY clause.
const partyListColumns = `id,code,kind,is_customer,is_supplier,display_name,legal_name,trade_name,first_name,last_name,tax_number,identity_number,tax_office,tax_office_id,default_currency,payment_term_id,price_list_id,default_discount_rate,sales_rep_user_id,credit_limit,risk_limit,risk_policy,phone,email,city,address_summary,contact_summary,group_summary,tag_summary,custom_field_summary,payment_term_name,sales_rep_name,balance,balance_currency,is_active,created_at,updated_at,version`

type partySort struct {
	Field, Direction, Expression string
	Numeric                      bool
}
type partySortCursor struct {
	Sort   string   `json:"sort"`
	Values []string `json:"values"`
	ID     string   `json:"id"`
}

var sortNumber = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func parsePartySort(value string) ([]partySort, error) {
	var result []partySort
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		pair := strings.Split(strings.TrimSpace(part), ":")
		if len(pair) != 2 || (pair[1] != "asc" && pair[1] != "desc") || seen[pair[0]] {
			return nil, fmt.Errorf("%w: Cari sıralama bilgisi geçersiz.", identity.ErrValidation)
		}
		item := partySort{Field: pair[0], Direction: pair[1]}
		switch item.Field {
		case "code", "display_name", "legal_name", "first_name", "last_name", "tax_number", "identity_number", "tax_office", "phone", "email", "city", "address_summary", "contact_summary", "group_summary", "tag_summary", "custom_field_summary", "default_currency":
			item.Expression = "lower(COALESCE(p." + item.Field + "::text,''))"
		case "trade_name":
			item.Expression = "lower(COALESCE(NULLIF(p.trade_name,''),p.display_name,''))"
		case "kind":
			item.Expression = "CASE p.kind WHEN 'PERSON' THEN 'Kişi' ELSE 'Kurum' END"
		case "roles":
			item.Expression = "CASE WHEN p.is_customer AND p.is_supplier THEN 'Müşteri + Tedarikçi' WHEN p.is_customer THEN 'Müşteri' ELSE 'Tedarikçi' END"
		case "risk_policy":
			item.Expression = "CASE p.risk_policy WHEN 'ALLOW' THEN 'İzin ver' WHEN 'BLOCK' THEN 'Engelle' ELSE 'Uyar' END"
		case "status":
			item.Expression = "CASE WHEN p.is_active THEN 'Aktif' ELSE 'Pasif' END"
		case "payment_term":
			item.Expression = "lower(COALESCE(NULLIF(p.payment_term_name,''),p.payment_term_id::text,'Peşin'))"
		case "price_list":
			item.Expression = "COALESCE(p.price_list_id::text,'')"
		case "sales_rep":
			item.Expression = "lower(COALESCE(NULLIF(p.sales_rep_name,''),p.sales_rep_user_id::text,''))"
		case "default_discount_rate", "credit_limit", "risk_limit", "balance", "version":
			item.Expression = "COALESCE(p." + item.Field + "::numeric,0)"
			item.Numeric = true
		case "created_at", "updated_at":
			item.Expression = "to_char(p." + item.Field + " AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS.US')"
		default:
			return nil, fmt.Errorf("%w: Bu cari sütunu sıralanamaz.", identity.ErrValidation)
		}
		seen[item.Field] = true
		result = append(result, item)
	}
	return result, nil
}

func sortedPartyQuery(base string, args []any, sort, cursor string, limit int) (string, []any, []partySort, error) {
	order, err := parsePartySort(sort)
	if err != nil {
		return "", nil, nil, err
	}
	normalized := make([]string, len(order))
	for i, item := range order {
		normalized[i] = item.Field + ":" + item.Direction
	}
	sort = strings.Join(normalized, ",")
	var after partySortCursor
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(raw, &after) != nil || after.Sort != sort || len(after.Values) != len(order) || uuid.Validate(after.ID) != nil {
			return "", nil, nil, fmt.Errorf("%w: Sayfalama bilgisi sıralamayla eşleşmiyor. Listeyi yenileyin.", identity.ErrValidation)
		}
		for i, item := range order {
			if item.Numeric && !sortNumber.MatchString(after.Values[i]) {
				return "", nil, nil, fmt.Errorf("%w: Sayfalama değeri geçersiz. Listeyi yenileyin.", identity.ErrValidation)
			}
		}
	}
	query := "WITH party_list (" + partyListColumns + ") AS (" + base + ") SELECT p.*"
	ordering := []string{}
	for _, item := range order {
		query += ", (" + item.Expression + ")::text"
		ordering = append(ordering, item.Expression+" "+strings.ToUpper(item.Direction))
	}
	query += " FROM party_list p"
	if cursor != "" {
		equal := []string{}
		alternatives := []string{}
		for i, item := range order {
			args = append(args, after.Values[i])
			param := fmt.Sprintf("$%d::text", len(args))
			if item.Numeric {
				param = fmt.Sprintf("$%d::numeric", len(args))
			}
			op := ">"
			if item.Direction == "desc" {
				op = "<"
			}
			alternatives = append(alternatives, "("+strings.Join(append(append([]string{}, equal...), item.Expression+op+param), " AND ")+")")
			equal = append(equal, item.Expression+"="+param)
		}
		args = append(args, after.ID)
		alternatives = append(alternatives, "("+strings.Join(append(equal, fmt.Sprintf("p.id>$%d::uuid", len(args))), " AND ")+")")
		query += " WHERE " + strings.Join(alternatives, " OR ")
	}
	ordering = append(ordering, "p.id ASC")
	args = append(args, limit+1)
	query += " ORDER BY " + strings.Join(ordering, ",") + fmt.Sprintf(" LIMIT $%d", len(args))
	return query, args, order, nil
}

// Append cursor values to scanParty without changing the shared row decoder.
type partySortScanner struct {
	scanner
	values []string
}

func (s partySortScanner) Scan(dest ...any) error {
	for i := range s.values {
		dest = append(dest, &s.values[i])
	}
	return s.scanner.Scan(dest...)
}

func (s *Service) listSorted(ctx context.Context, base string, args []any, sort, cursor string, limit int) (ListResult, error) {
	query, args, order, err := sortedPartyQuery(base, args, sort, cursor, limit)
	if err != nil {
		return ListResult{}, err
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	result := ListResult{Items: []Party{}}
	var last partySortCursor
	fields := make([]string, len(order))
	for i, item := range order {
		fields[i] = item.Field + ":" + item.Direction
	}
	for rows.Next() {
		values := make([]string, len(order))
		item, err := scanParty(partySortScanner{rows, values})
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, item)
		if len(result.Items) == limit {
			last = partySortCursor{Sort: strings.Join(fields, ","), Values: values, ID: item.ID}
		}
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		raw, _ := json.Marshal(last)
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}
