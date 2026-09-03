package identity

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// TestLoginAsksForTOTPOnlyAfterThePasswordVerifies pins the two-step login
// contract the sign-in screen depends on: a correct password on a 2FA account
// answers ErrTOTPRequired (so the UI can ask for the code in a second step),
// that step is not recorded as a failed attempt, and a wrong code is still an
// ordinary invalid-credentials failure.
func TestLoginAsksForTOTPOnlyAfterThePasswordVerifies(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := identityTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const email, password = "totp@example.test", "uzun-ve-guvenli-parola"
	meta := RequestMeta{TraceID: "login-totp-test", IP: "127.0.0.1"}
	session, err := service.Setup(ctx, SetupInput{
		AdminName: "TOTP Yönetici", AdminEmail: email, Password: password,
		LegalName: "TOTP Firma AŞ", TradeName: "TOTP", EntityType: "LEGAL_ENTITY",
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	// Without 2FA the password alone is enough.
	if _, err = service.Login(ctx, email, password, "", meta); err != nil {
		t.Fatalf("login without 2FA: %v", err)
	}

	secret, _, err := service.BeginTOTP(ctx, session, meta)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTP(ctx, session, generateTOTP(secret, uint64(service.now().Unix()/30)), meta)
	if err != nil {
		t.Fatalf("confirm totp: %v", err)
	}

	failures := func() int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE NOT succeeded`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := failures()

	if _, err = service.Login(ctx, email, password, "", meta); !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("expected ErrTOTPRequired for a 2FA account without a code, got %v", err)
	}
	if after := failures(); after != before {
		t.Fatalf("the code prompt was recorded as a failed attempt: %d -> %d", before, after)
	}

	// A wrong password must not reveal that the account uses 2FA.
	if _, err = service.Login(ctx, email, "yanlis-parola-degeri", "", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for a wrong password, got %v", err)
	}
	if _, err = service.Login(ctx, email, password, "000000", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials for a wrong code, got %v", err)
	}
	if _, err = service.Login(ctx, email, password, generateTOTP(secret, uint64(service.now().Unix()/30)), meta); err != nil {
		t.Fatalf("login with a valid code: %v", err)
	}
	if _, err = service.Login(ctx, email, password, recoveryCodes[0], meta); err != nil {
		t.Fatalf("login with a recovery code: %v", err)
	}
}
