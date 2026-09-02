package identity

import (
	"testing"
	"time"
)

func TestTOTPUsesRFC6238CompatibleWindow(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	now := time.Unix(1_700_000_000, 0)
	code := generateTOTP(secret, uint64(now.Unix()/30))
	if len(code) != 6 || !VerifyTOTP(secret, code, now) {
		t.Fatalf("generated TOTP %q was not accepted", code)
	}
	if VerifyTOTP(secret, code, now.Add(2*time.Minute)) {
		t.Fatal("expired TOTP was accepted")
	}
}

func TestTOTPMatchesRFC6238SHA1VectorTruncatedToSixDigits(t *testing.T) {
	const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got := generateTOTP(rfcSecret, uint64(time.Unix(59, 0).Unix()/30)); got != "287082" {
		t.Fatalf("got %q, want 287082", got)
	}
}
