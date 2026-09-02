// Package dbguard is a build-time safety net for company isolation.
//
// Every SQL statement that reads or writes a company-scoped table must constrain
// it by company_id. The database now also enforces this through row-level
// security (migration 000147), but the RLS policy is deliberately transparent
// until the GUC is set, so the application predicate remains the primary control
// on code paths that have not adopted database.WithCompany yet.
//
// Scan walks the Go source under internal/ and flags SQL string literals that
// touch a company-scoped table without mentioning company_id. Known, reviewed
// exceptions live in allowlist.txt; the test fails only on new findings.
package dbguard

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// Finding is one SQL literal that references a company-scoped table without a
// company_id reference.
type Finding struct {
	File   string // path relative to repo root
	Line   int
	Table  string
	Detail string // trimmed SQL snippet
}

// Key identifies a finding independently of its line number, so unrelated edits
// that shift the statement do not churn allowlist.txt. It is
// <file>:<table>:<sha256[:8] of the normalised SQL>.
func (f Finding) Key() string {
	sum := sha256.Sum256([]byte(f.Detail))
	return f.File + ":" + f.Table + ":" + hex.EncodeToString(sum[:])[:8]
}

var (
	sqlVerbRE    = regexp.MustCompile(`(?is)\b(FROM|JOIN|UPDATE|DELETE\s+FROM|INSERT\s+INTO)\s+(?:ONLY\s+)?([a-z_][a-z0-9_]*)`)
	companyRefRE = regexp.MustCompile(`(?i)company_id`)
	looksSQLRE   = regexp.MustCompile(`(?is)\b(SELECT|INSERT\s+INTO|UPDATE|DELETE\s+FROM|WITH)\b`)
)

// Scan parses every non-test .go file under root/internal and returns findings
// sorted by location.
func Scan(root string) ([]Finding, error) {
	scoped, err := migrations.CompanyScopedTables()
	if err != nil {
		return nil, err
	}
	scopedSet := make(map[string]struct{}, len(scoped))
	for _, name := range scoped {
		scopedSet[name] = struct{}{}
	}

	fset := token.NewFileSet()
	var findings []Finding

	internalDir := filepath.Join(root, "internal")
	walkErr := filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The guard package itself only contains matcher regexes, not queries.
		if strings.Contains(path, filepath.Join("platform", "dbguard")) {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}

		rel, _ := filepath.Rel(root, path)
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, uErr := strconv.Unquote(lit.Value)
			if uErr != nil {
				// Raw string with backticks unquotes fine; anything else skip.
				return true
			}
			if !looksSQLRE.MatchString(text) {
				return true
			}
			if hasGuardAllowComment(file, fset, lit) {
				return true
			}
			for _, m := range sqlVerbRE.FindAllStringSubmatch(text, -1) {
				table := strings.ToLower(m[2])
				if _, isScoped := scopedSet[table]; !isScoped {
					continue
				}
				if companyRefRE.MatchString(text) {
					continue
				}
				findings = append(findings, Finding{
					File:   filepath.ToSlash(rel),
					Line:   fset.Position(lit.Pos()).Line,
					Table:  table,
					Detail: snippet(text),
				})
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Table < findings[j].Table
	})
	return dedupe(findings), nil
}

func dedupe(in []Finding) []Finding {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, f := range in {
		if _, ok := seen[f.Key()]; ok {
			continue
		}
		seen[f.Key()] = struct{}{}
		out = append(out, f)
	}
	return out
}

// hasGuardAllowComment reports whether a `//dbguard:allow` comment sits on the
// line above or the same line as the literal.
func hasGuardAllowComment(file *ast.File, fset *token.FileSet, lit *ast.BasicLit) bool {
	litLine := fset.Position(lit.Pos()).Line
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.Contains(c.Text, "dbguard:allow") {
				continue
			}
			cl := fset.Position(c.Pos()).Line
			if cl == litLine || cl == litLine-1 {
				return true
			}
		}
	}
	return false
}

func snippet(sql string) string {
	s := strings.Join(strings.Fields(sql), " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
