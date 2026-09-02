package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// TestAnnounceInstallAndFeedbackIntegration runs the two surviving collector
// calls against a freshly migrated schema: the once-per-install ping and a
// feedback submission. It also pins that nothing identifying is sent.
func TestAnnounceInstallAndFeedbackIntegration(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool := pulseTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	companyID := "20000000-0000-4000-8000-000000000001"
	if _, err := pool.Exec(ctx,
		`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency,timezone)
		 VALUES($1,'Gizli Sirket A.S.','Gizli','LEGAL_ENTITY','TRY','Europe/Istanbul')`,
		companyID); err != nil {
		t.Fatalf("seed company: %v", err)
	}

	var installCalls, feedbackCalls int
	var installBody, feedbackBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/pulse/v1/install":
			installCalls++
			_ = json.Unmarshal(raw, &installBody)
		case "/pulse/v1/feedback":
			feedbackCalls++
			_ = json.Unmarshal(raw, &feedbackBody)
		default:
			t.Errorf("collector received unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	svc := NewService(pool, config.Config{Release: "test-1.0"})
	svc.endpoint = server.URL
	svc.key = "test-key"

	// AnnounceInstall is pending (not done) until instance setup exists.
	if done, err := svc.AnnounceInstall(ctx); err != nil || done {
		t.Fatalf("AnnounceInstall (pre-setup): done=%v err=%v", done, err)
	}
	if installCalls != 0 {
		t.Fatalf("announced install before setup completed")
	}
	userID := "30000000-0000-4000-8000-000000000001"
	if _, err := pool.Exec(ctx,
		`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'a@b.co','A','x')`, userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance_setup(singleton,completed_at,completed_by) VALUES(true,now(),$1)`, userID); err != nil {
		t.Fatalf("seed instance_setup: %v", err)
	}
	if done, err := svc.AnnounceInstall(ctx); err != nil || !done {
		t.Fatalf("AnnounceInstall: done=%v err=%v", done, err)
	}
	if done, err := svc.AnnounceInstall(ctx); err != nil || !done {
		t.Fatalf("AnnounceInstall (repeat): done=%v err=%v", done, err)
	}
	if installCalls != 1 {
		t.Fatalf("install announced %d times, want exactly 1", installCalls)
	}
	for k := range installBody {
		switch k {
		case "install_id", "app_version", "setup_at":
		default:
			t.Errorf("unexpected field %q in install payload", k)
		}
	}

	if err := svc.SendFeedback(ctx, FeedbackInput{
		Category: "bug", Message: "bir sorun var", CompanyID: companyID,
	}); err != nil {
		t.Fatalf("SendFeedback: %v", err)
	}
	if feedbackCalls != 1 {
		t.Fatalf("feedback sent %d times, want 1", feedbackCalls)
	}
	if feedbackBody["company_id"] != companyID || feedbackBody["category"] != "bug" {
		t.Fatalf("unexpected feedback payload: %+v", feedbackBody)
	}
	// The company's real name must never appear anywhere in what we sent.
	blob, _ := json.Marshal(feedbackBody)
	if strings.Contains(string(blob), "Gizli") {
		t.Fatalf("feedback payload leaked the company name: %s", blob)
	}
}

func pulseTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_pulse_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool
}
