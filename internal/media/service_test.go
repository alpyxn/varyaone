package media

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSafeFilenameNeverBecomesAStoragePath(t *testing.T) {
	got := safeFilename("../../müşteri\x00-belgesi.pdf")
	if strings.Contains(got, "/") || strings.ContainsRune(got, '\x00') || got == "" {
		t.Fatalf("unsafe filename returned: %q", got)
	}
	if len(safeFilename(strings.Repeat("x", 300))) != 255 {
		t.Fatal("filename was not bounded")
	}
}

func TestAttachmentKindAndExtensionValidation(t *testing.T) {
	for _, valid := range []string{"GENERAL", "CERTIFICATE_2026", "TECH.PDF"} {
		if !validKind(valid) {
			t.Fatalf("expected %q to be a valid attachment kind", valid)
		}
	}
	for _, invalid := range []string{"", "general", "A/B", "A!"} {
		if validKind(invalid) {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
	if extensionForContentType("application/pdf") != ".bin" {
		t.Fatal("unknown content type should use a neutral extension")
	}
}

func TestReadBoundedHonoursContextAndLimit(t *testing.T) {
	if _, err := readBounded(context.Background(), bytes.NewBufferString("12345"), 4); err == nil {
		t.Fatal("expected size limit error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readBounded(ctx, bytes.NewBufferString("x"), 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
