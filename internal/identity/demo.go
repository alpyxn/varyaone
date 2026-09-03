package identity

// Demo provisioning is the only place in identity that creates a company with a
// pinned identifier and purges one without a signed-in owner. Both are gated on
// companies.is_demo (migration 000151): a company that is not flagged as demo
// can never be purged here, whatever the caller passes in. That single check is
// what keeps `varyaone demo reset` from touching a real installation.

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// ErrNotDemoCompany is returned when demo tooling is pointed at a company that
// is not flagged as disposable demo data.
var ErrNotDemoCompany = errors.New("company is not a demo company")

// DemoCompanyInput describes the fixed identity of the shared demo company. The
// identifiers are pinned by the caller so a reseed recreates the same company
// and the same user, and links into the demo keep working across resets.
type DemoCompanyInput struct {
	CompanyID   string
	UserID      string
	Email       string
	DisplayName string
	Password    string
	LegalName   string
	TradeName   string
}

func (in DemoCompanyInput) validate() error {
	if !validUUID(in.CompanyID) || !validUUID(in.UserID) {
		return fmt.Errorf("%w: demo company and user identifiers must be UUIDs", ErrValidation)
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("%w: geçerli demo kullanıcı e-postası gereklidir", ErrValidation)
	}
	if strings.TrimSpace(in.DisplayName) == "" || strings.TrimSpace(in.LegalName) == "" || strings.TrimSpace(in.TradeName) == "" {
		return fmt.Errorf("%w: demo kullanıcı ve firma adları gereklidir", ErrValidation)
	}
	return nil
}

// DemoCompanyExists reports whether the demo company is already provisioned. A
// company that exists under this identifier but is not flagged as demo is
// reported as an error rather than silently adopted.
func (s *Service) DemoCompanyExists(ctx context.Context, companyID string) (bool, error) {
	ctx = database.WithoutConn(ctx)
	var isDemo bool
	switch err := s.pool.QueryRow(ctx, `SELECT is_demo FROM companies WHERE id=$1`, companyID).Scan(&isDemo); {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	case !isDemo:
		return false, fmt.Errorf("%w: %s", ErrNotDemoCompany, companyID)
	}
	return true, nil
}

// ProvisionDemoCompany creates the demo user and the demo company with the
// pinned identifiers and returns a signed-in session for the demo user. It is
// the demo counterpart of Setup: it completes instance setup when that has not
// happened yet, so the demo deployment never shows the setup wizard.
//
// It refuses to run when a company already exists under the pinned identifier;
// callers reseed by purging first (see PurgeDemoCompany).
func (s *Service) ProvisionDemoCompany(ctx context.Context, in DemoCompanyInput, meta RequestMeta) (Session, error) {
	if err := in.validate(); err != nil {
		return Session{}, err
	}
	ctx = database.WithoutConn(ctx)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	passwordHash, err := HashPassword(in.Password)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, fmt.Errorf("begin demo provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The same advisory lock Setup takes: demo provisioning and the setup wizard
	// both write instance_setup and must not interleave.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(867_972_101)); err != nil {
		return Session{}, fmt.Errorf("lock demo provisioning: %w", err)
	}
	var companyExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE id=$1)`, in.CompanyID).Scan(&companyExists); err != nil {
		return Session{}, err
	}
	if companyExists {
		return Session{}, fmt.Errorf("%w: demo firması zaten mevcut", ErrValidation)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO users (id,email,display_name,password_hash) VALUES ($1,$2,$3,$4)
		ON CONFLICT (id) DO UPDATE SET email=EXCLUDED.email,display_name=EXCLUDED.display_name,password_hash=EXCLUDED.password_hash,is_active=true`,
		in.UserID, email, strings.TrimSpace(in.DisplayName), passwordHash); err != nil {
		return Session{}, fmt.Errorf("apply demo user: %w", err)
	}
	prov, err := provisionCompany(ctx, tx, in.UserID, companyProvisionInput{
		CompanyID:  in.CompanyID,
		LegalName:  strings.TrimSpace(in.LegalName),
		TradeName:  strings.TrimSpace(in.TradeName),
		EntityType: "LEGAL_ENTITY",
	})
	if err != nil {
		return Session{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE companies SET is_demo=true WHERE id=$1`, prov.CompanyID); err != nil {
		return Session{}, fmt.Errorf("flag demo company: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO instance_setup (completed_at,completed_by) VALUES (now(),$1) ON CONFLICT (singleton) DO NOTHING`, in.UserID); err != nil {
		return Session{}, fmt.Errorf("complete demo setup: %w", err)
	}
	if err = insertAudit(ctx, tx, prov.CompanyID, in.UserID, "DEMO_COMPANY_PROVISIONED", "company", prov.CompanyID,
		map[string]any{"branch_id": prov.BranchID, "warehouse_id": prov.WarehouseID}, meta); err != nil {
		return Session{}, err
	}
	session, err := s.createSession(ctx, tx, User{ID: in.UserID, Email: email, DisplayName: strings.TrimSpace(in.DisplayName)}, prov.CompanyID, meta)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit demo provisioning: %w", err)
	}
	session.IsInstanceOwner = true
	return session, nil
}

// PurgeDemoCompany deletes the demo company and every row scoped to it. Unlike
// DeleteCompany it needs no signed-in owner and no fallback company, because the
// caller recreates the company immediately afterwards - but it runs only when
// the target is flagged is_demo.
func (s *Service) PurgeDemoCompany(ctx context.Context, companyID string) error {
	ctx = database.WithoutConn(ctx)
	companyID = strings.TrimSpace(companyID)
	if !validUUID(companyID) {
		return fmt.Errorf("%w: firma kimliği gereklidir", ErrValidation)
	}
	if s.maintenanceDSN == "" {
		return fmt.Errorf("%w: demo sıfırlama bu kurulumda yapılandırılmamış", ErrValidation)
	}
	// Purging touches rows the RLS policies hide and the immutability triggers
	// protect, so it runs on the owner connection rather than the serving pool.
	conn, err := pgx.Connect(ctx, s.maintenanceDSN)
	if err != nil {
		return fmt.Errorf("open maintenance connection: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var isDemo bool
	switch err = tx.QueryRow(ctx, `SELECT is_demo FROM companies WHERE id=$1 FOR UPDATE`, companyID).Scan(&isDemo); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil // nothing to purge; provisioning will create it
	case err != nil:
		return err
	case !isDemo:
		return fmt.Errorf("%w: %s", ErrNotDemoCompany, companyID)
	}
	if err = purgeCompanyRows(ctx, tx, companyID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// StartDemoSession issues a signed-in session for the demo user without a
// password. It exists so a visitor reaches the showcase in one click rather
// than being handed shared credentials to type.
//
// It is not a password bypass: it only ever works for a company flagged is_demo
// and only for a user who is a member of it, and the route that calls it is
// mounted only when the installation runs in demo mode.
func (s *Service) StartDemoSession(ctx context.Context, companyID, userID string, meta RequestMeta) (Session, error) {
	ctx = database.WithoutConn(ctx)
	companyID = strings.TrimSpace(companyID)
	userID = strings.TrimSpace(userID)
	if !validUUID(companyID) || !validUUID(userID) {
		return Session{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var user User
	var isDemo bool
	switch err = tx.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.totp_enabled_at IS NOT NULL,c.is_demo
		FROM users u
		JOIN company_memberships m ON m.user_id=u.id AND m.is_active
		JOIN companies c ON c.id=m.company_id AND c.is_active
		WHERE u.id=$1 AND u.is_active AND c.id=$2`, userID, companyID).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.TOTPEnabled, &isDemo); {
	case errors.Is(err, pgx.ErrNoRows):
		return Session{}, ErrForbidden
	case err != nil:
		return Session{}, err
	case !isDemo:
		return Session{}, fmt.Errorf("%w: %s", ErrNotDemoCompany, companyID)
	}
	session, err := s.createSession(ctx, tx, user, companyID, meta)
	if err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return session, nil
}

// ReconcileDemoUser brings the existing demo account in line with the
// configured credentials. The login screen shows those credentials filled in,
// so if an operator changes VARYAONE_DEMO_EMAIL or VARYAONE_DEMO_PASSWORD after
// the company was built, the form would otherwise offer a password the stored
// account no longer has - and every visitor would be told their credentials are
// wrong.
func (s *Service) ReconcileDemoUser(ctx context.Context, in DemoCompanyInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	ctx = database.WithoutConn(ctx)
	var isDemo bool
	switch err := s.pool.QueryRow(ctx, `SELECT is_demo FROM companies WHERE id=$1`, in.CompanyID).Scan(&isDemo); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	case !isDemo:
		return fmt.Errorf("%w: %s", ErrNotDemoCompany, in.CompanyID)
	}
	passwordHash, err := HashPassword(in.Password)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE users SET email=$2,display_name=$3,password_hash=$4,is_active=true,updated_at=now()
		WHERE id=$1 AND (email<>$2 OR password_hash<>$4 OR NOT is_active)`,
		in.UserID, strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.DisplayName), passwordHash)
	if err != nil {
		return fmt.Errorf("reconcile demo user: %w", err)
	}
	return nil
}
