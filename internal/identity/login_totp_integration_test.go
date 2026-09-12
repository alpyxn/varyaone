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

	secret, _, err := service.BeginTOTP(ctx, session, "", meta)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTP(ctx, session, generateTOTP(secret, uint64(service.now().Unix()/30)), "", meta)
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
	validCode := generateTOTP(secret, uint64(service.now().Unix()/30))
	if _, err = service.Login(ctx, email, password, validCode, meta); err != nil {
		t.Fatalf("login with a valid code: %v", err)
	}
	// The same code must not work a second time inside its acceptance window:
	// VerifyTOTPStep's matched time step is recorded on the successful login
	// above, so a replay of the exact same 6 digits is now an ordinary
	// invalid-credentials failure rather than a second successful login.
	if _, err = service.Login(ctx, email, password, validCode, meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials replaying the same TOTP code, got %v", err)
	}
	// A -> B -> A: recording only "the last step used" (instead of requiring
	// the recorded step to strictly advance) would let A become valid again
	// the instant a later, different code B is accepted, since A != B passes
	// an equality check. The next step's code must log in, and A must still
	// be rejected afterwards.
	nextStepCode := generateTOTP(secret, uint64(service.now().Unix()/30)+1)
	if _, err = service.Login(ctx, email, password, nextStepCode, meta); err != nil {
		t.Fatalf("login with the next step's code: %v", err)
	}
	if _, err = service.Login(ctx, email, password, validCode, meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("A->B->A: expected ErrInvalidCredentials replaying A after B was accepted, got %v", err)
	}
	if _, err = service.Login(ctx, email, password, recoveryCodes[0], meta); err != nil {
		t.Fatalf("login with a recovery code: %v", err)
	}
}

// TestReplacingAnActiveTOTPFactorRequiresReauthentication pins a report
// finding: BeginTOTP used to hand out a fresh pending secret to any valid
// session regardless of whether TOTP was already enabled, and ConfirmTOTP
// only checked the new code — so a hijacked or left-open session token alone
// was enough to take over an account's second factor. BeginTOTP now requires
// the account password once TOTP is active, and ConfirmTOTP additionally
// requires a code from the *still-active* secret before letting the new one
// take over.
func TestReplacingAnActiveTOTPFactorRequiresReauthentication(t *testing.T) {
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
	service, err := NewService(pool, bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	const email, password = "totp-swap@example.test", "uzun-ve-guvenli-parola"
	meta := RequestMeta{TraceID: "totp-swap-test", IP: "127.0.0.1"}
	session, err := service.Setup(ctx, SetupInput{
		AdminName: "TOTP Yönetici", AdminEmail: email, Password: password,
		LegalName: "TOTP Swap AŞ", TradeName: "TOTP Swap", EntityType: "LEGAL_ENTITY",
	}, meta)
	if err != nil {
		t.Fatal(err)
	}

	// First-time setup needs neither a password nor an existing code.
	firstSecret, _, err := service.BeginTOTP(ctx, session, "", meta)
	if err != nil {
		t.Fatalf("first-time begin: %v", err)
	}
	if _, err = service.ConfirmTOTP(ctx, session, generateTOTP(firstSecret, uint64(service.now().Unix()/30)), "", meta); err != nil {
		t.Fatalf("first-time confirm: %v", err)
	}

	// Replacing the now-active factor with the wrong password is refused
	// before a new pending secret is even generated.
	if _, _, err = service.BeginTOTP(ctx, session, "yanlis-parola", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("begin with wrong password: got %v, want ErrInvalidCredentials", err)
	}

	secondSecret, _, err := service.BeginTOTP(ctx, session, password, meta)
	if err != nil {
		t.Fatalf("begin replacement with correct password: %v", err)
	}
	newCode := generateTOTP(secondSecret, uint64(service.now().Unix()/30))

	// Confirming without the still-active factor's code is refused, even
	// though the new secret's own code is correct.
	if _, err = service.ConfirmTOTP(ctx, session, newCode, "", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("confirm without current code: got %v, want ErrInvalidCredentials", err)
	}
	if _, err = service.ConfirmTOTP(ctx, session, newCode, "000000", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("confirm with wrong current code: got %v, want ErrInvalidCredentials", err)
	}

	currentCode := generateTOTP(firstSecret, uint64(service.now().Unix()/30))
	if _, err = service.ConfirmTOTP(ctx, session, newCode, currentCode, meta); err != nil {
		t.Fatalf("confirm replacement with both codes: %v", err)
	}

	// The swap actually took effect: the old secret's code no longer logs in,
	// the new one does.
	if _, err = service.Login(ctx, email, password, generateTOTP(firstSecret, uint64(service.now().Unix()/30)), meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login with the replaced secret: got %v, want ErrInvalidCredentials", err)
	}
	if _, err = service.Login(ctx, email, password, generateTOTP(secondSecret, uint64(service.now().Unix()/30)), meta); err != nil {
		t.Fatalf("login with the new secret: %v", err)
	}
}
