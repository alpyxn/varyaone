package migrations

import (
	_ "embed"
	"regexp"
	"sort"
)

var (
	lineCommentRE = regexp.MustCompile(`--[^\n]*`)
	createTableRE = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s*\(`)
	dropTableRE   = regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	companyColRE  = regexp.MustCompile(`(?i)^\s*company_id\s`)
	addCompanyRE  = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s+ADD\s+(?:COLUMN\s+)?(?:IF\s+NOT\s+EXISTS\s+)?company_id\s`)
	dropCompanyRE = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-z_][a-z0-9_]*)\s+DROP\s+(?:COLUMN\s+)?(?:IF\s+EXISTS\s+)?company_id\b`)
)

//go:embed app_role.sql
var appRoleSQL string

// AppRoleSQL returns the idempotent creation + privilege grants for the
// non-superuser varyaone_app role. It is the same content as the app_role
// migration (TestDeployAppRoleScriptMatchesMigration enforces that) but shipped as a
// standalone script because it must also run where migrations do not re-run:
// after pg_restore (grants stripped by --no-privileges) and by deploy.sh
// together with `ALTER ROLE varyaone_app LOGIN PASSWORD ...`.
func AppRoleSQL() string { return appRoleSQL }

// CompanyScopedTables returns, in deterministic order, every table in the current
// migration set that carries a company_id column and still exists (accounting for
// later DROP TABLE / DROP COLUMN statements).
//
// It is the single source of truth for row-level-security coverage checks and the
// static SQL guard, so a new company-scoped table cannot silently skip isolation.
func CompanyScopedTables() ([]string, error) {
	items, err := load()
	if err != nil {
		return nil, err
	}
	scoped := map[string]bool{}
	for _, item := range items {
		sql := lineCommentRE.ReplaceAllString(item.SQL, "")
		for _, m := range createTableRE.FindAllStringSubmatchIndex(sql, -1) {
			name := sql[m[2]:m[3]]
			body := balancedParenBody(sql, m[1]-1)
			scoped[name] = scoped[name] || bodyHasCompanyColumn(body)
		}
		for _, m := range addCompanyRE.FindAllStringSubmatch(sql, -1) {
			scoped[m[1]] = true
		}
		for _, m := range dropCompanyRE.FindAllStringSubmatch(sql, -1) {
			scoped[m[1]] = false
		}
		for _, m := range dropTableRE.FindAllStringSubmatch(sql, -1) {
			delete(scoped, m[1])
		}
	}
	out := make([]string, 0, len(scoped))
	for name, ok := range scoped {
		if ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// balancedParenBody returns the text between the parenthesis that opens at
// openIdx and its matching close parenthesis.
func balancedParenBody(sql string, openIdx int) string {
	depth := 0
	for j := openIdx; j < len(sql); j++ {
		switch sql[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return sql[openIdx+1 : j]
			}
		}
	}
	return ""
}

func bodyHasCompanyColumn(body string) bool {
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				if companyColRE.MatchString(body[start:i]) {
					return true
				}
				start = i + 1
			}
		}
	}
	return companyColRE.MatchString(body[start:])
}
