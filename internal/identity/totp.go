package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // TOTP interoperability requires HMAC-SHA1 by default (RFC 6238).
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func NewTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

func TOTPURI(secret, email string) string {
	values := url.Values{"secret": {secret}, "issuer": {"Varya One"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape("Varya One:"+email) + "?" + values.Encode()
}

func VerifyTOTP(secret, code string, now time.Time) bool {
	_, ok := VerifyTOTPStep(secret, code, now)
	return ok
}

// VerifyTOTPStep is VerifyTOTP plus the matched 30-second time step, so a
// caller (Login) can atomically record it as spent and reject a second use of
// the same code within its acceptance window.
func VerifyTOTPStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	base := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		step := base + offset
		if generateTOTP(secret, uint64(step)) == code {
			return step, true
		}
	}
	return 0, false
}

func generateTOTP(secret string, counter uint64) string {
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, decoded)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
