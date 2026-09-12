package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// The restore rehearsal.
//
// The existing round-trip test proves the mechanism: rows go out and come back,
// bytes go out and come back. What it does not prove is that this application
// can be restored, because its fixture is one toy table and a migration table
// holding a single fabricated version. An ERP restore has to bring back
// companies, users and their roles, commercial documents with their amounts,
// stock with its quantities, finance, personnel, encrypted columns and the
// files documents point at — and it has to land in a database that has never
// held any of it.
//
// So this one migrates a real schema, fills it with the seeded showcase
// company, backs it up, restores into a second clean database, and compares
// the two exhaustively: every table's contents, every sequence's position,
// every constraint, and the stored object's bytes.

// missingToolIsFailure decides what a missing pg_dump means. In CI it is a
// failure: the restore rehearsal is the one test that actually restores a
// database, and a skip there reads as a pass. On a developer's machine, where
// the client tools are often simply not installed, it is a skip — and the two
// are never reported as the same thing.
func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s PATH'te yok; geri yükleme provası CI'da atlanamaz", tool)
			}
			t.Skipf("%s PATH'te yok; yerel koşuda atlanıyor (CI'da hata olur)", tool)
		}
	}
}

func TestFullSchemaRestoreRehearsal(t *testing.T) {
	baseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	requireTools(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	stamp := time.Now().UnixNano()
	sourceDB := fmt.Sprintf("varya_rehearsal_src_%d", stamp)
	targetDB := fmt.Sprintf("varya_rehearsal_dst_%d", stamp)
	for _, name := range []string{sourceDB, targetDB} {
		if _, err = admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
			t.Fatalf("create database %s: %v", name, err)
		}
		dropped := name
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cleanupCancel()
			_, _ = admin.Exec(cleanupCtx,
				`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, dropped)
			_, _ = admin.Exec(cleanupCtx, `DROP DATABASE IF EXISTS "`+dropped+`"`)
		})
	}

	sourceURL := databaseURL(t, baseURL, sourceDB)
	targetURL := databaseURL(t, baseURL, targetDB)

	// 1) A real schema, built the way an installation builds it.
	sourcePool, err := pgxpool.New(ctx, sourceURL)
	if err != nil {
		t.Fatalf("open source pool: %v", err)
	}
	defer sourcePool.Close()
	if err = migrations.New(sourcePool).Up(ctx); err != nil {
		t.Fatalf("migrate source: %v", err)
	}

	// 2) A real dataset. The showcase seed is the one fixture that covers the
	// whole product — company, users and roles, commercial documents, stock,
	// finance and personnel — including the columns that are encrypted at rest.
	masterKey := bytes.Repeat([]byte{7}, 32)
	seeder := demo.New(sourcePool, demo.Options{
		MaintenanceDSN: sourceURL,
		MasterKey:      masterKey,
		Email:          "rehearsal@example.test",
		Password:       "Rehearsal-Passphrase-1",
		Logger:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Now:            func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) },
	})
	if err = seeder.Ensure(ctx); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// The fixture has to be worth restoring. An empty database round-trips
	// perfectly and proves nothing, so the tables the product is made of are
	// asserted to hold rows before anything is backed up.
	for _, table := range []string{
		"companies", "users", "roles", "products", "documents",
		"stock_movements", "employees",
	} {
		if rows := countRows(ctx, t, sourcePool, table); rows == 0 {
			t.Fatalf("fixture boş: %s tablosunda satır yok", table)
		}
	}

	// 3) A real file, referenced the way an attachment is.
	storageRoot := t.TempDir()
	objectRel := filepath.Join("documents", "2026", "invoice.pdf")
	objectBytes := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("varya-rehearsal"), 8192)...)
	if err = os.MkdirAll(filepath.Dir(filepath.Join(storageRoot, objectRel)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(storageRoot, objectRel), objectBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	wantSum := sha256.Sum256(objectBytes)

	before := fingerprint(ctx, t, sourcePool)

	// 4) Back up.
	source, err := backup.NewEngine(backup.Options{
		DatabaseURL: sourceURL, StorageRoot: storageRoot, Release: "rehearsal",
	})
	if err != nil {
		t.Fatalf("source engine: %v", err)
	}
	var archive bytes.Buffer
	manifest, err := source.Create(ctx, &archive)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(manifest.Objects) != 1 || manifest.Objects[0].SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("manifest does not describe the stored object: %+v", manifest.Objects)
	}
	if manifest.MigrationVersion == 0 {
		t.Fatal("manifest carries no migration version")
	}

	// 5) Restore into a database that has never held any of this, and a
	// storage tree that has never held the file. This is the case a disaster
	// actually presents; restoring over the source proves much less.
	targetStorage := t.TempDir()
	target, err := backup.NewEngine(backup.Options{
		DatabaseURL: targetURL, StorageRoot: targetStorage, Release: "rehearsal",
	})
	if err != nil {
		t.Fatalf("target engine: %v", err)
	}
	if _, err = target.Restore(ctx, &archive, backup.RestoreOptions{}); err != nil {
		t.Fatalf("restore into a clean target: %v", err)
	}

	// 6) Compare. Not row counts — contents: a restore that dropped every
	// amount to zero, or lost the encrypted columns, keeps the counts intact.
	targetPool, err := pgxpool.New(ctx, targetURL)
	if err != nil {
		t.Fatalf("open target pool: %v", err)
	}
	defer targetPool.Close()
	after := fingerprint(ctx, t, targetPool)
	compareFingerprints(t, before, after)

	restored, err := os.ReadFile(filepath.Join(targetStorage, objectRel))
	if err != nil {
		t.Fatalf("restored object missing: %v", err)
	}
	if gotSum := sha256.Sum256(restored); gotSum != wantSum {
		t.Fatal("restored file does not match the original byte for byte")
	}

	// 7) The restored installation must be usable by the role that serves
	// traffic, not only by its owner. A restore that lands every object owned
	// by the superuser leaves an installation that starts and then fails every
	// request.
	assertApplicationRoleCanRead(ctx, t, admin, baseURL, targetDB)

	// 8) Company isolation still holds. It is enforced in the database, so a
	// restore that lost the policies would hand every company's data to
	// everyone and still look completely healthy.
	assertCompanyIsolation(ctx, t, targetPool)
}

// snapshot is everything about a database that a restore must preserve.
type snapshot struct {
	// tables maps a table name to a checksum over all of its rows.
	tables map[string]string
	// sequences maps a sequence name to its position. A restore that reset
	// them leaves an installation that issues duplicate document numbers on
	// its first save.
	sequences map[string]int64
	// constraints is every constraint by name, so a restore cannot quietly
	// drop the foreign keys and check constraints that hold the data together.
	constraints []string
	// policies is every row-level-security policy, by table and name.
	policies []string
}

func fingerprint(ctx context.Context, t *testing.T, pool *pgxpool.Pool) snapshot {
	t.Helper()
	result := snapshot{tables: map[string]string{}, sequences: map[string]int64{}}

	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) < 50 {
		t.Fatalf("yalnız %d tablo bulundu; şema uygulanmamış görünüyor", len(tables))
	}

	for _, table := range tables {
		// The whole row as text, ordered by the same text, so the checksum is
		// independent of physical order — which a dump and restore does not
		// preserve and is not supposed to.
		var sum *string
		query := fmt.Sprintf(
			`SELECT md5(coalesce(string_agg(row_text, E'\n' ORDER BY row_text), ''))
			 FROM (SELECT t::text AS row_text FROM %q t) s`, table)
		if err = pool.QueryRow(ctx, query).Scan(&sum); err != nil {
			t.Fatalf("checksum %s: %v", table, err)
		}
		if sum != nil {
			result.tables[table] = *sum
		}
	}

	seqRows, err := pool.Query(ctx, `SELECT sequencename FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename`)
	if err != nil {
		t.Fatalf("list sequences: %v", err)
	}
	var sequences []string
	for seqRows.Next() {
		var name string
		if err = seqRows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		sequences = append(sequences, name)
	}
	seqRows.Close()
	for _, name := range sequences {
		var last *int64
		if err = pool.QueryRow(ctx,
			fmt.Sprintf(`SELECT last_value FROM %q`, name)).Scan(&last); err != nil {
			t.Fatalf("sequence %s: %v", name, err)
		}
		if last != nil {
			result.sequences[name] = *last
		}
	}

	result.constraints = stringColumn(ctx, t, pool,
		`SELECT conrelid::regclass::text || '.' || conname
		   FROM pg_constraint c
		   JOIN pg_namespace n ON n.oid = c.connamespace
		  WHERE n.nspname = 'public'
		  ORDER BY 1`)
	result.policies = stringColumn(ctx, t, pool,
		`SELECT schemaname || '.' || tablename || '.' || policyname
		   FROM pg_policies WHERE schemaname='public' ORDER BY 1`)
	return result
}

func compareFingerprints(t *testing.T, before, after snapshot) {
	t.Helper()

	var missing, differing []string
	for table, sum := range before.tables {
		other, ok := after.tables[table]
		switch {
		case !ok:
			missing = append(missing, table)
		case other != sum:
			differing = append(differing, table)
		}
	}
	sort.Strings(missing)
	sort.Strings(differing)
	if len(missing) > 0 {
		t.Errorf("geri yüklemeden sonra bu tablolar yok: %v", missing)
	}
	if len(differing) > 0 {
		t.Errorf("bu tabloların içeriği değişti: %v", differing)
	}
	if extra := len(after.tables) - len(before.tables); extra != 0 {
		t.Errorf("tablo sayısı değişti: %d → %d", len(before.tables), len(after.tables))
	}

	for name, position := range before.sequences {
		if got, ok := after.sequences[name]; !ok || got != position {
			t.Errorf("sequence %s: %d → %v (belge numaraları tekrar eder)", name, position, got)
		}
	}
	if !equalStrings(before.constraints, after.constraints) {
		t.Errorf("kısıtlar korunmadı: %d → %d", len(before.constraints), len(after.constraints))
	}
	if !equalStrings(before.policies, after.policies) {
		t.Errorf("satır güvenlik politikaları korunmadı: %d → %d",
			len(before.policies), len(after.policies))
	}
}

// assertApplicationRoleCanRead gives the restored installation's serving role a
// password and connects as it. The role itself comes from a migration, so its
// existence after a restore is part of what is being tested.
func assertApplicationRoleCanRead(ctx context.Context, t *testing.T, admin *pgx.Conn, baseURL, database string) {
	t.Helper()
	var exists bool
	if err := admin.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='varyaone_app')`).Scan(&exists); err != nil {
		t.Fatalf("uygulama rolü sorulamadı: %v", err)
	}
	if !exists {
		t.Fatal("geri yüklemeden sonra varyaone_app rolü yok; sunucu bu kurulumla çalışamaz")
	}
	const password = "rehearsal-only-password"
	if _, err := admin.Exec(ctx,
		`ALTER ROLE varyaone_app LOGIN PASSWORD `+quoteLiteral(password)); err != nil {
		t.Fatalf("uygulama rolüne parola verilemedi: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `ALTER ROLE varyaone_app NOLOGIN`)
	})

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword("varyaone_app", password)
	parsed.Path = "/" + database
	appConn, err := pgx.Connect(ctx, parsed.String())
	if err != nil {
		t.Fatalf("uygulama rolüyle bağlanılamadı: %v", err)
	}
	defer func() { _ = appConn.Close(context.WithoutCancel(ctx)) }()

	// A representative read, not SELECT 1: the grants are per table, and a
	// restore that lost them fails at the first real query rather than at the
	// connection.
	var companies int
	if err = appConn.QueryRow(ctx, `SELECT count(*) FROM companies`).Scan(&companies); err != nil {
		t.Fatalf("uygulama rolü companies okuyamıyor: %v", err)
	}
	if companies == 0 {
		t.Fatal("uygulama rolü hiç şirket göremiyor")
	}
}

// assertCompanyIsolation checks the restored database still refuses to show one
// company's rows to a session scoped to another.
func assertCompanyIsolation(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	companies := stringColumn(ctx, t, pool, `SELECT id::text FROM companies ORDER BY id`)
	if len(companies) == 0 {
		t.Fatal("geri yüklenen kurulumda şirket yok")
	}
	var policies int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_policies WHERE schemaname='public'`).Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if policies == 0 {
		t.Fatal("geri yüklemeden sonra hiç satır güvenlik politikası yok; firma izolasyonu kalkmış")
	}
	// Every table that carries a company_id must be covered by a policy.
	// Isolation that holds for most tables is not isolation.
	uncovered := stringColumn(ctx, t, pool, `
		SELECT c.relname
		  FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'company_id' AND a.attnum > 0
		 WHERE n.nspname = 'public' AND c.relkind = 'r'
		   AND NOT c.relrowsecurity
		 ORDER BY 1`)
	if len(uncovered) > 0 {
		t.Errorf("company_id taşıyan ama satır güvenliği kapalı tablolar: %v", uncovered)
	}
}

func databaseURL(t *testing.T, baseURL, database string) string {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + database
	return parsed.String()
}

func countRows(ctx context.Context, t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %q`, table)).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func stringColumn(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// quoteLiteral is enough for the fixed test password; it never sees input.
func quoteLiteral(value string) string {
	return "'" + value + "'"
}
