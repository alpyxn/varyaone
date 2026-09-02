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

// TestSystemRoleIsLocked verifies the "Yönetici" system role cannot have its
// permissions edited, and that its permission set stays intact afterwards.
func TestSystemRoleIsLocked(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := identityTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Setup(ctx, SetupInput{
		AdminName: "Test Yönetici", AdminEmail: "admin@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Birinci Firma AŞ", TradeName: "Birinci", EntityType: "LEGAL_ENTITY",
	}, RequestMeta{TraceID: "t", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	roles, err := service.ListRoles(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	var sysRole *Role
	for i := range roles {
		if roles[i].IsSystem {
			sysRole = &roles[i]
			break
		}
	}
	if sysRole == nil {
		t.Fatal("no system role found after setup")
	}
	before := len(sysRole.Permissions)
	if before == 0 {
		t.Fatal("system role has no permissions")
	}

	_, err = service.UpdateRole(ctx, session, sysRole.ID, "Hacklendi",
		[]string{"party.read"}, sysRole.Version, RequestMeta{TraceID: "t"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpdateRole on system role = %v, want ErrForbidden", err)
	}

	roles, err = service.ListRoles(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if r.IsSystem {
			if r.Name != sysRole.Name {
				t.Fatalf("system role name changed to %q", r.Name)
			}
			if len(r.Permissions) != before {
				t.Fatalf("system role permission count changed: %d -> %d", before, len(r.Permissions))
			}
		}
	}
}
