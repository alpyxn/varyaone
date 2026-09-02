package email

import "testing"

func TestRenderText(t *testing.T) {
	vars := map[string]string{"ad_soyad": "Ayşe Yılmaz", "donem": "Ağustos 2026"}
	got := RenderText("Sayın {{ad_soyad}}, {{donem}} dönemi. {{eksik}}", vars)
	want := "Sayın Ayşe Yılmaz, Ağustos 2026 dönemi. {{eksik}}"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if RenderText("{{ ad_soyad }}", vars) != "Ayşe Yılmaz" {
		t.Fatalf("whitespace-padded placeholder not resolved")
	}
}

func TestValidAddress(t *testing.T) {
	cases := map[string]bool{
		"a@b.com":     true,
		"A@B.CO":      true,
		"nope":        false,
		"x <x@y.com>": false,
		" a@b.com ":   true,
	}
	for in, want := range cases {
		if got := ValidAddress(in); got != want {
			t.Errorf("ValidAddress(%q)=%v want %v", in, got, want)
		}
	}
}

func TestValidateTemplate(t *testing.T) {
	if err := validateTemplate(TemplateInput{Name: "", Scope: "GENERIC", Subject: "x"}); err == nil {
		t.Error("expected error for empty name")
	}
	if err := validateTemplate(TemplateInput{Name: "n", Scope: "BAD", Subject: "x"}); err == nil {
		t.Error("expected error for bad scope")
	}
	if err := validateTemplate(TemplateInput{Name: "n", Scope: "GENERIC"}); err == nil {
		t.Error("expected error when subject and body both empty")
	}
	if err := validateTemplate(TemplateInput{Name: "n", Scope: "PAYROLL_PAYSLIP", Body: "b"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
