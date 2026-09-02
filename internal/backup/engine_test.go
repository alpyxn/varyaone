package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestEngineRoundTrip exercises Create -> destructive wipe -> Restore against a
// throwaway database and a local storage tree, asserting that both table rows
// and stored object bytes come back byte-for-byte.
func TestEngineRoundTrip(t *testing.T) {
	baseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dbName := fmt.Sprintf("varya_backup_test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err = admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, dbName)
		_, _ = admin.Exec(cleanupCtx, `DROP DATABASE IF EXISTS "`+dbName+`"`)
		_ = admin.Close(cleanupCtx)
	})

	parsed, _ := url.Parse(baseURL)
	parsed.Path = "/" + dbName
	targetURL := parsed.String()

	seed, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatalf("connect target: %v", err)
	}
	mustExec(ctx, t, seed, `CREATE TABLE platform_schema_migrations (version bigint PRIMARY KEY, name text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`)
	mustExec(ctx, t, seed, `INSERT INTO platform_schema_migrations(version,name) VALUES (1,'baseline')`)
	mustExec(ctx, t, seed, `CREATE TABLE widget (id int PRIMARY KEY, label text NOT NULL)`)
	mustExec(ctx, t, seed, `INSERT INTO widget(id,label) VALUES (1,'alpha'),(2,'beta'),(3,'çğş')`)
	_ = seed.Close(ctx)

	storageRoot := t.TempDir()
	objectRel := filepath.Join("media", "img", "sample.bin")
	objectBytes := bytes.Repeat([]byte("varya"), 4096)
	if err = os.MkdirAll(filepath.Join(storageRoot, "media", "img"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(storageRoot, objectRel), objectBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	engine, err := NewEngine(Options{DatabaseURL: targetURL, StorageRoot: storageRoot, Release: "test"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	var archive bytes.Buffer
	manifest, err := engine.Create(ctx, &archive)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if manifest.MigrationVersion != 1 || len(manifest.Objects) != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	wantSum := sha256.Sum256(objectBytes)
	if manifest.Objects[0].SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("object checksum mismatch in manifest")
	}

	// Destructive wipe: drop the data table and delete the stored object.
	wipe, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(ctx, t, wipe, `DROP TABLE widget`)
	_ = wipe.Close(ctx)
	if err = os.Remove(filepath.Join(storageRoot, objectRel)); err != nil {
		t.Fatal(err)
	}

	if _, err = engine.Restore(ctx, &archive, RestoreOptions{}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	check, err := pgx.Connect(ctx, targetURL)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close(ctx)
	var count int
	if err = check.QueryRow(ctx, `SELECT count(*) FROM widget`).Scan(&count); err != nil {
		t.Fatalf("widget query after restore: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 widget rows after restore, got %d", count)
	}

	restored, err := os.ReadFile(filepath.Join(storageRoot, objectRel))
	if err != nil {
		t.Fatalf("read restored object: %v", err)
	}
	if !bytes.Equal(restored, objectBytes) {
		t.Fatalf("restored object bytes differ from original")
	}
}

func mustExec(ctx context.Context, t *testing.T, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
