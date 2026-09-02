package identity

import "testing"

func TestLogoDataURIPattern(t *testing.T) {
	valid := []string{
		"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB",
		"data:image/jpeg;base64,/9j/4AAQSkZJRgABAQAAAQ==",
		"data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
		"data:image/webp;base64,UklGRg==",
	}
	for _, v := range valid {
		if !logoDataURIPattern.MatchString(v) {
			t.Errorf("expected %q to be accepted", v)
		}
	}

	invalid := []string{
		`data:image/png"><script>alert(1)</script>`,
		"data:image/png;base64,not valid base64 with spaces",
		"data:text/html;base64,PHNjcmlwdD4=",
		"https://evil.example/logo.png",
		"data:image/png,<svg onload=alert(1)>",
		"javascript:alert(1)",
	}
	for _, v := range invalid {
		if logoDataURIPattern.MatchString(v) {
			t.Errorf("expected %q to be rejected", v)
		}
	}
}
