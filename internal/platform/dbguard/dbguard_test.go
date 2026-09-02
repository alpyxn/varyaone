package dbguard

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func itoa(n int) string { return strconv.Itoa(n) }

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		// Fall back to walking up for a go.mod.
		dir, _ := os.Getwd()
		for {
			if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				t.Fatalf("cannot locate repo root: %v", err)
			}
			dir = parent
		}
	}
	return strings.TrimSpace(string(out))
}

func loadAllowlist(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	f, err := os.Open(filepath.Join(root, "internal", "platform", "dbguard", "allowlist.txt"))
	if err != nil {
		t.Fatalf("open allowlist: %v", err)
	}
	defer f.Close()
	allow := map[string]struct{}{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// format: <file>:<line>:<table>  <optional whitespace + reason>
		key := strings.Fields(line)[0]
		allow[key] = struct{}{}
	}
	return allow
}

func TestNoUnscopedCompanyQueries(t *testing.T) {
	root := repoRoot(t)
	findings, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	allow := loadAllowlist(t, root)

	var unexpected []Finding
	for _, f := range findings {
		if _, ok := allow[f.Key()]; ok {
			continue
		}
		unexpected = append(unexpected, f)
	}
	if len(unexpected) > 0 {
		var b strings.Builder
		b.WriteString("new SQL statements touch a company-scoped table without a company_id predicate.\n")
		b.WriteString("Add the predicate, or if the scope is genuinely enforced elsewhere add a\n")
		b.WriteString("`//dbguard:allow <reason>` comment on the line, or an allowlist.txt entry:\n\n")
		for _, f := range unexpected {
			b.WriteString("  ")
			b.WriteString(f.File)
			b.WriteString(":")
			b.WriteString(itoa(f.Line))
			b.WriteString("  (allowlist key: ")
			b.WriteString(f.Key())
			b.WriteString(")\n      ")
			b.WriteString(f.Detail)
			b.WriteString("\n")
		}
		t.Fatal(b.String())
	}

	// Keep the allowlist honest: entries that no longer match a finding should be
	// pruned so it cannot mask a regression later.
	live := map[string]struct{}{}
	for _, f := range findings {
		live[f.Key()] = struct{}{}
	}
	for key := range allow {
		if _, ok := live[key]; !ok {
			t.Errorf("stale allowlist entry (no matching finding, please remove): %s", key)
		}
	}
}
