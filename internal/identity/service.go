package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/modules"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

var (
	ErrAlreadySetup       = errors.New("instance setup is already complete")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLoginLimited       = errors.New("too many login attempts")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrForbidden          = errors.New("permission denied")
	ErrConflict           = errors.New("concurrent update conflict")
	ErrBaseCurrencyLocked = errors.New("company base currency is locked")
	ErrValidation         = errors.New("validation failed")
	ErrLastCompany        = errors.New("cannot delete the last remaining company")
)

// LoginRateLimitedError carries how long the caller must still wait before
// the login-attempt window (see Login) clears on its own; RetryAfter shrinks
// on every request rather than resetting to a fixed 15 minutes, since blocked
// attempts are never recorded as new failures.
type LoginRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *LoginRateLimitedError) Error() string        { return ErrLoginLimited.Error() }
func (e *LoginRateLimitedError) Is(target error) bool { return target == ErrLoginLimited }

const sessionLifetime = 12 * time.Hour

type defaultPartyGroup struct {
	code string
	name string
}

var defaultPartyGroups = []defaultPartyGroup{
	{code: "PERAKENDE", name: "Perakende Müşteriler"},
	{code: "TOPTAN", name: "Toptan Müşteriler"},
	{code: "BAYI", name: "Bayiler"},
	{code: "HIZMET_TED", name: "Hizmet Tedarikçileri"},
	{code: "MALZEME_TED", name: "Malzeme Tedarikçileri"},
}

type Service struct {
	pool      database.Querier
	secretBox *SecretBox
	now       func() time.Time
	// maintenanceDSN, when set, is an owner/superuser connection string used only
	// for destructive maintenance that must bypass RLS and the per-table
	// immutability triggers (currently DeleteCompany). Empty in tests and any
	// deployment that only configures the non-superuser serving connection.
	maintenanceDSN string
}

// Option customises a Service at construction time.
type Option func(*Service)

// WithMaintenanceDSN supplies the owner/superuser connection string DeleteCompany
// uses to purge a company past FK checks and immutability triggers.
func WithMaintenanceDSN(dsn string) Option {
	return func(s *Service) { s.maintenanceDSN = strings.TrimSpace(dsn) }
}

func NewService(pool database.Querier, masterKey []byte, opts ...Option) (*Service, error) {
	box, err := NewSecretBox(masterKey)
	if err != nil {
		return nil, err
	}
	s := &Service{pool: pool, secretBox: box, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

type SetupInput struct {
	AdminName      string   `json:"admin_name"`
	AdminEmail     string   `json:"admin_email"`
	Password       string   `json:"password"`
	LegalName      string   `json:"legal_name"`
	TradeName      string   `json:"trade_name"`
	EntityType     string   `json:"entity_type"`
	TaxNumber      string   `json:"tax_number"`
	SectorPackages []string `json:"sector_packages,omitempty"`
	// Modules lists the feature modules to enable. An empty list enables every
	// module; a non-empty list enables only the codes it contains.
	Modules []string `json:"modules,omitempty"`
}

type Company struct {
	ID                            string  `json:"id"`
	LegalName                     string  `json:"legal_name"`
	TradeName                     string  `json:"trade_name"`
	EntityType                    string  `json:"entity_type"`
	TaxNumber                     *string `json:"tax_number,omitempty"`
	BaseCurrency                  string  `json:"base_currency"`
	Timezone                      string  `json:"timezone"`
	DuplicatePartyTaxNumberPolicy string  `json:"duplicate_party_tax_number_policy,omitempty"`
	PartyCodePrefix               string  `json:"party_code_prefix,omitempty"`
	PartyCodeDigits               int     `json:"party_code_digits,omitempty"`
	Logo                          string  `json:"logo"`
	Version                       int64   `json:"version"`
}

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	TOTPEnabled bool   `json:"totp_enabled"`
}

type Session struct {
	ID               string    `json:"-"`
	Token            string    `json:"-"`
	CSRFToken        string    `json:"csrf_token"`
	User             User      `json:"user"`
	Companies        []Company `json:"companies"`
	CurrentCompanyID string    `json:"current_company_id,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	Permissions      []string  `json:"permissions"`
	Modules          []string  `json:"modules"`
	// IsInstanceOwner is true for the user who completed the one-time setup; only
	// they may create additional companies.
	IsInstanceOwner bool `json:"is_instance_owner"`
	IsAPIToken      bool `json:"-"`
}

func (s *Service) AuthenticateAPIToken(ctx context.Context, token string) (Session, error) {
	if !strings.HasPrefix(token, "vry_") {
		return Session{}, ErrUnauthenticated
	}
	ctx = database.WithoutConn(ctx)
	var result Session
	var scopes []string
	err := s.pool.QueryRow(ctx, `
		SELECT t.id,t.owner_user_id,u.email,u.display_name,u.totp_enabled_at IS NOT NULL,t.company_id,COALESCE(t.expires_at,now()+interval '100 years'),t.scopes
		FROM api_tokens t
		JOIN users u ON u.id=t.owner_user_id AND u.is_active
		JOIN companies c ON c.id=t.company_id AND c.is_active
		JOIN company_memberships m ON m.company_id=t.company_id AND m.user_id=t.owner_user_id AND m.is_active
		WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>now())`, tokenHash(token)).
		Scan(&result.ID, &result.User.ID, &result.User.Email, &result.User.DisplayName, &result.User.TOTPEnabled, &result.CurrentCompanyID, &result.ExpiresAt, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, err
	}
	ownerPermissions, err := s.permissions(ctx, result.User.ID, result.CurrentCompanyID)
	if err != nil {
		return Session{}, err
	}
	result.Permissions = intersectTokenPermissions(ownerPermissions, scopes)
	if result.Modules, err = s.activeModules(ctx, result.CurrentCompanyID); err != nil {
		return Session{}, err
	}
	result.IsAPIToken = true
	if result.HasPermission("organization.company.read") {
		company, companyErr := s.CurrentCompany(ctx, result)
		if companyErr != nil {
			return Session{}, companyErr
		}
		result.Companies = []Company{company}
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at=now() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<now()-interval '5 minutes')`, result.ID)
	return result, nil
}

func intersectTokenPermissions(ownerPermissions, scopes []string) []string {
	scopePermissions := map[string][]string{
		"organization:read":   {"organization.company.read"},
		"party:read":          {"party.read"},
		"party:ledger:read":   {"party.ledger.read"},
		"security:audit:read": {"security.audit.read"},
		"security:users:read": {"security.user.read"},
		"finance:read": {
			"finance.cash_account.read", "finance.bank_account.read",
			"finance.cash_movement.read", "finance.bank_movement.read",
			"finance.collection.read", "finance.payment.read", "finance.transfer.read",
		},
	}
	owner := map[string]struct{}{}
	for _, permission := range ownerPermissions {
		owner[permission] = struct{}{}
	}
	result := []string{}
	for _, scope := range scopes {
		permissions, known := scopePermissions[scope]
		if !known {
			continue
		}
		for _, permission := range permissions {
			if _, allowed := owner[permission]; allowed {
				result = append(result, permission)
			}
		}
	}
	return uniqueStrings(result)
}

func (s Session) HasPermission(permission string) bool {
	for _, item := range s.Permissions {
		if item == permission {
			return true
		}
	}
	return false
}

// HasModule reports whether the session's current company has the named feature
// module enabled. An empty module code is treated as core (always available).
func (s Session) HasModule(module string) bool {
	if module == "" {
		return true
	}
	for _, item := range s.Modules {
		if item == module {
			return true
		}
	}
	return false
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	PlainToken string     `json:"token,omitempty"`
}

type RequestMeta struct {
	TraceID        string
	IP             string
	UserAgent      string
	IdempotencyKey string
}

func (s *Service) SetupStatus(ctx context.Context) (bool, error) {
	var complete bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_setup WHERE singleton)`).Scan(&complete)
	return complete, err
}

func (s *Service) Setup(ctx context.Context, input SetupInput, meta RequestMeta) (Session, error) {
	input.AdminEmail = strings.ToLower(strings.TrimSpace(input.AdminEmail))
	input.AdminName = strings.TrimSpace(input.AdminName)
	input.LegalName = strings.TrimSpace(input.LegalName)
	input.TradeName = strings.TrimSpace(input.TradeName)
	input.EntityType = strings.TrimSpace(input.EntityType)
	address, addressErr := mail.ParseAddress(input.AdminEmail)
	if addressErr != nil || address.Address != input.AdminEmail || input.AdminName == "" || input.LegalName == "" || input.TradeName == "" {
		return Session{}, fmt.Errorf("%w: geçerli yönetici ve firma bilgileri gereklidir", ErrValidation)
	}
	if input.EntityType != "LEGAL_ENTITY" && input.EntityType != "SOLE_PROPRIETOR" {
		return Session{}, fmt.Errorf("%w: geçersiz firma türü", ErrValidation)
	}
	for _, code := range input.Modules {
		if !modules.Valid(code) {
			return Session{}, fmt.Errorf("%w: bilinmeyen modül %q", ErrValidation, code)
		}
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return Session{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	ids, err := makeIDs(3)
	if err != nil {
		return Session{}, err
	}
	userID, auditID, eventID := ids[0], ids[1], ids[2]

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, fmt.Errorf("begin setup: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(867_972_101)); err != nil {
		return Session{}, fmt.Errorf("lock setup: %w", err)
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_setup)`).Scan(&exists); err != nil {
		return Session{}, err
	}
	if exists {
		return Session{}, ErrAlreadySetup
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users (id,email,display_name,password_hash) VALUES ($1,$2,$3,$4)`,
		userID, input.AdminEmail, input.AdminName, passwordHash); err != nil {
		return Session{}, fmt.Errorf("apply setup: %w", err)
	}
	prov, err := provisionCompany(ctx, tx, userID, companyProvisionInput{
		LegalName:      input.LegalName,
		TradeName:      input.TradeName,
		EntityType:     input.EntityType,
		TaxNumber:      input.TaxNumber,
		SectorPackages: input.SectorPackages,
		Modules:        input.Modules,
	})
	if err != nil {
		return Session{}, err
	}
	companyID := prov.CompanyID
	postStatements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO security_audit_events (id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES ($1,$2,$3,'SETUP_COMPLETED','company',$2,$4,$5,$6,$7)`, []any{auditID, companyID, userID, jsonBytes(map[string]any{"branch_id": prov.BranchID, "warehouse_id": prov.WarehouseID, "transit_warehouse_id": prov.TransitWarehouseID}), meta.TraceID, meta.IP, meta.UserAgent}},
		{`INSERT INTO outbox_events (event_id,type,schema_version,company_id,trace_id,payload) VALUES ($1,'identity.setup.completed',1,$2,$3,$4)`, []any{eventID, companyID, meta.TraceID, jsonBytes(map[string]any{"user_id": userID})}},
		{`INSERT INTO instance_setup (completed_at,completed_by) VALUES (now(),$1)`, []any{userID}},
	}
	for _, statement := range postStatements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return Session{}, fmt.Errorf("apply setup: %w", err)
		}
	}
	session, err := s.createSession(ctx, tx, User{ID: userID, Email: input.AdminEmail, DisplayName: input.AdminName}, companyID, meta)
	if err != nil {
		return Session{}, err
	}
	// The user who completes setup is, by definition, the instance owner.
	session.IsInstanceOwner = true
	if err = tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit setup: %w", err)
	}
	return session, nil
}

type companyProvisionInput struct {
	LegalName      string
	TradeName      string
	EntityType     string
	TaxNumber      string
	SectorPackages []string
	Modules        []string
}

type companyProvisionResult struct {
	CompanyID          string
	BranchID           string
	WarehouseID        string
	TransitWarehouseID string
	RoleID             string
}

// provisionCompany creates a company and its full baseline inside the caller's
// serializable transaction: the Merkez branch, the Ana Depo and system transit
// warehouses, an all-permissions "Yönetici" role assigned to userID, default cari
// groups, the requested variant packages and per-module toggles. base_currency
// and timezone are left to their column defaults (TRY / Europe/Istanbul).
func provisionCompany(ctx context.Context, tx pgx.Tx, userID string, in companyProvisionInput) (companyProvisionResult, error) {
	ids, err := makeIDs(5)
	if err != nil {
		return companyProvisionResult{}, err
	}
	res := companyProvisionResult{
		CompanyID:          ids[0],
		BranchID:           ids[1],
		WarehouseID:        ids[2],
		TransitWarehouseID: ids[3],
		RoleID:             ids[4],
	}
	// The database guard only permits the system transit warehouse row while this
	// setting is on. Keeping it local to the serializable transaction prevents a
	// caller from creating another one through the public connection afterwards.
	if _, err = tx.Exec(ctx, `SELECT set_config('varyaone.allow_system_transit','on',true)`); err != nil {
		return companyProvisionResult{}, fmt.Errorf("allow setup transit warehouse: %w", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO companies (id,legal_name,trade_name,entity_type,tax_number) VALUES ($1,$2,$3,$4,$5)`, []any{res.CompanyID, in.LegalName, in.TradeName, in.EntityType, nullString(in.TaxNumber)}},
		{`INSERT INTO branches (id,company_id,code,name) VALUES ($1,$2,'MRK','Merkez')`, []any{res.BranchID, res.CompanyID}},
		{`INSERT INTO warehouses (id,company_id,branch_id,code,name) VALUES ($1,$2,$3,'ANA','Ana Depo')`, []any{res.WarehouseID, res.CompanyID, res.BranchID}},
		{`INSERT INTO warehouses (id,company_id,branch_id,code,name,warehouse_type,uses_locations,is_transit,is_system,is_active) VALUES ($1,$2,$3,'SYS-TRANSIT','Sistem Transit Deposu','TRANSIT',false,true,true,true)`, []any{res.TransitWarehouseID, res.CompanyID, res.BranchID}},
		{`INSERT INTO company_memberships (company_id,user_id) VALUES ($1,$2)`, []any{res.CompanyID, userID}},
		{`INSERT INTO roles (id,company_id,name,is_system) VALUES ($1,$2,'Yönetici',true)`, []any{res.RoleID, res.CompanyID}},
		{`INSERT INTO role_permissions (company_id,role_id,permission_code) SELECT $1,$2,code FROM permissions`, []any{res.CompanyID, res.RoleID}},
		{`INSERT INTO membership_roles (company_id,user_id,role_id) VALUES ($1,$2,$3)`, []any{res.CompanyID, userID, res.RoleID}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return companyProvisionResult{}, fmt.Errorf("apply setup: %w", err)
		}
	}
	if err = seedDefaultPartyGroups(ctx, tx, res.CompanyID); err != nil {
		return companyProvisionResult{}, fmt.Errorf("apply default cari groups: %w", err)
	}
	if err = seedVariantPackages(ctx, tx, res.CompanyID, in.SectorPackages); err != nil {
		return companyProvisionResult{}, fmt.Errorf("apply variant packages: %w", err)
	}
	enableAll := len(in.Modules) == 0
	selected := make(map[string]bool, len(in.Modules))
	for _, code := range in.Modules {
		selected[code] = true
	}
	for _, code := range modules.Codes() {
		if _, err = tx.Exec(ctx, `INSERT INTO company_modules(company_id,module_code,enabled,updated_by) VALUES($1,$2,$3,$4)`,
			res.CompanyID, code, enableAll || selected[code], userID); err != nil {
			return companyProvisionResult{}, fmt.Errorf("apply modules: %w", err)
		}
	}
	return res, nil
}

type CreateCompanyInput struct {
	LegalName      string   `json:"legal_name"`
	TradeName      string   `json:"trade_name"`
	EntityType     string   `json:"entity_type"`
	TaxNumber      string   `json:"tax_number"`
	SectorPackages []string `json:"sector_packages,omitempty"`
	Modules        []string `json:"modules,omitempty"`
}

// CreateCompany provisions an additional company for an already-signed-in user
// and switches their session to it. Only the instance owner (the user who
// completed the one-time setup) may call it.
func (s *Service) CreateCompany(ctx context.Context, session Session, input CreateCompanyInput, meta RequestMeta) (Session, error) {
	// The new company is, by definition, outside this request's RLS scope.
	ctx = database.WithoutConn(ctx)

	input.LegalName = strings.TrimSpace(input.LegalName)
	input.TradeName = strings.TrimSpace(input.TradeName)
	input.EntityType = strings.TrimSpace(input.EntityType)
	if input.LegalName == "" || input.TradeName == "" {
		return Session{}, fmt.Errorf("%w: resmî unvan ve ticari ad gereklidir", ErrValidation)
	}
	if input.EntityType != "LEGAL_ENTITY" && input.EntityType != "SOLE_PROPRIETOR" {
		return Session{}, fmt.Errorf("%w: geçersiz firma türü", ErrValidation)
	}
	for _, code := range input.Modules {
		if !modules.Valid(code) {
			return Session{}, fmt.Errorf("%w: bilinmeyen modül %q", ErrValidation, code)
		}
	}

	var isOwner bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_setup WHERE completed_by=$1)`, session.User.ID).Scan(&isOwner); err != nil {
		return Session{}, err
	}
	if !isOwner {
		return Session{}, ErrForbidden
	}

	auditID, err := newID()
	if err != nil {
		return Session{}, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Session{}, fmt.Errorf("begin create company: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	prov, err := provisionCompany(ctx, tx, session.User.ID, companyProvisionInput(input))
	if err != nil {
		return Session{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO security_audit_events (id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES ($1,$2,$3,'COMPANY_CREATED','company',$2,$4,$5,$6,$7)`,
		auditID, prov.CompanyID, session.User.ID, jsonBytes(map[string]any{"branch_id": prov.BranchID, "warehouse_id": prov.WarehouseID, "transit_warehouse_id": prov.TransitWarehouseID}), meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return Session{}, fmt.Errorf("apply create company: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET current_company_id=$1 WHERE id=$2`, prov.CompanyID, session.ID); err != nil {
		return Session{}, fmt.Errorf("switch session company: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit create company: %w", err)
	}
	return s.Authenticate(ctx, session.Token)
}

func (s *Service) Login(ctx context.Context, email, password, totpCode string, meta RequestMeta) (Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	emailHash, ipHash := identifierHash(email), identifierHash(meta.IP)
	var failures int
	var oldestFailure time.Time
	if err := s.pool.QueryRow(ctx, `SELECT count(*),COALESCE(min(attempted_at),now()) FROM login_attempts WHERE email_hash=$1 AND ip_hash=$2 AND NOT succeeded AND attempted_at > now() - interval '15 minutes'`, emailHash, ipHash).Scan(&failures, &oldestFailure); err != nil {
		return Session{}, err
	}
	if failures >= 5 {
		retryAfter := time.Until(oldestFailure.Add(15 * time.Minute))
		if retryAfter < 0 {
			retryAfter = 0
		}
		return Session{}, &LoginRateLimitedError{RetryAfter: retryAfter}
	}
	var user User
	var passwordHash string
	var totpCiphertext []byte
	err := s.pool.QueryRow(ctx, `SELECT id,email,display_name,password_hash,totp_secret_ciphertext,totp_enabled_at IS NOT NULL FROM users WHERE email=$1 AND is_active`, email).
		Scan(&user.ID, &user.Email, &user.DisplayName, &passwordHash, &totpCiphertext, &user.TOTPEnabled)
	valid := false
	if err == nil {
		valid, err = VerifyPassword(passwordHash, password)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Equalize the expensive path enough to avoid a trivial account enumeration oracle.
		_, _ = HashPassword("invalid-login-padding-value")
		err = nil
	}
	if err != nil {
		return Session{}, err
	}
	if valid && len(totpCiphertext) > 0 {
		secret, openErr := s.secretBox.Open(user.ID, "totp", totpCiphertext)
		valid = openErr == nil && VerifyTOTP(string(secret), totpCode, s.now())
		if openErr == nil && !valid {
			valid, err = s.consumeRecoveryCode(ctx, user.ID, totpCode, meta)
			if err != nil {
				return Session{}, err
			}
		}
	}
	if !valid {
		_, _ = s.pool.Exec(ctx, `INSERT INTO login_attempts(email_hash,ip_hash,succeeded) VALUES ($1,$2,false)`, emailHash, ipHash)
		_ = s.audit(ctx, "", "", "LOGIN_FAILURE", "user", "", map[string]any{"email_hash": fmt.Sprintf("%x", emailHash[:8])}, meta)
		return Session{}, ErrInvalidCredentials
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO login_attempts(email_hash,ip_hash,succeeded) VALUES ($1,$2,true)`, emailHash, ipHash)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var companyID string
	_ = tx.QueryRow(ctx, `SELECT company_id FROM company_memberships WHERE user_id=$1 AND is_active ORDER BY created_at LIMIT 1`, user.ID).Scan(&companyID)
	session, err := s.createSession(ctx, tx, user, companyID, meta)
	if err != nil {
		return Session{}, err
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, user.ID, "LOGIN_SUCCESS", "session", session.ID, nil, meta); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Service) consumeRecoveryCode(ctx context.Context, userID, code string, meta RequestMeta) (bool, error) {
	if strings.TrimSpace(code) == "" {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	result, err := tx.Exec(ctx, `UPDATE recovery_codes SET used_at=now() WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL`, userID, tokenHash(strings.TrimSpace(code)))
	if err != nil {
		return false, err
	}
	if result.RowsAffected() == 0 {
		return false, nil
	}
	var companyID string
	_ = tx.QueryRow(ctx, `SELECT company_id FROM company_memberships WHERE user_id=$1 AND is_active ORDER BY created_at LIMIT 1`, userID).Scan(&companyID)
	if err = insertAudit(ctx, tx, companyID, userID, "RECOVERY_CODE_USED", "user", userID, nil, meta); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrUnauthenticated
	}
	// Session hydration spans every company the user belongs to, so it must not
	// be filtered by a request's currently selected company.
	ctx = database.WithoutConn(ctx)
	var result Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.id,s.user_id,u.email,u.display_name,u.totp_enabled_at IS NOT NULL,COALESCE(s.current_company_id::text,''),s.expires_at
		FROM sessions s
		JOIN users u ON u.id=s.user_id
		LEFT JOIN company_memberships m ON m.company_id=s.current_company_id AND m.user_id=s.user_id AND m.is_active
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.is_active
		  AND (s.current_company_id IS NULL OR m.user_id IS NOT NULL)`, tokenHash(token)).
		Scan(&result.ID, &result.User.ID, &result.User.Email, &result.User.DisplayName, &result.User.TOTPEnabled, &result.CurrentCompanyID, &result.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, err
	}
	result.Companies, err = s.companies(ctx, result.User.ID)
	if err != nil {
		return Session{}, err
	}
	if result.CurrentCompanyID != "" {
		if result.Permissions, err = s.permissions(ctx, result.User.ID, result.CurrentCompanyID); err != nil {
			return Session{}, err
		}
		if result.Modules, err = s.activeModules(ctx, result.CurrentCompanyID); err != nil {
			return Session{}, err
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_setup WHERE completed_by=$1)`, result.User.ID).Scan(&result.IsInstanceOwner); err != nil {
		return Session{}, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at=now() WHERE id=$1 AND last_seen_at < now()-interval '5 minutes'`, result.ID)
	return result, err
}

func (s *Service) ValidateCSRF(ctx context.Context, sessionID, csrfToken string) bool {
	if csrfToken == "" {
		return false
	}
	var valid bool
	err := s.pool.QueryRow(ctx, `SELECT csrf_hash=$2 FROM sessions WHERE id=$1 AND revoked_at IS NULL AND expires_at>now()`, sessionID, tokenHash(csrfToken)).Scan(&valid)
	return err == nil && valid
}

// RotateCSRF replaces only the browser session's CSRF secret. It never
// changes business data and refuses API-token contexts, which do not own a
// browser session row.
func (s *Service) RotateCSRF(ctx context.Context, session Session) (string, error) {
	if session.IsAPIToken || strings.TrimSpace(session.ID) == "" {
		return "", ErrForbidden
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	var updated bool
	err = s.pool.QueryRow(ctx, `UPDATE sessions SET csrf_hash=$1 WHERE id=$2 AND revoked_at IS NULL AND expires_at>now() RETURNING true`, tokenHash(token), session.ID).Scan(&updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUnauthenticated
	}
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) SelectCompany(ctx context.Context, session Session, companyID string, meta RequestMeta) (Session, error) {
	// Switching companies checks a membership in a company that is, by
	// definition, not the one this request is currently scoped to.
	ctx = database.WithoutConn(ctx)
	var allowed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM company_memberships WHERE company_id=$1 AND user_id=$2 AND is_active)`, companyID, session.User.ID).Scan(&allowed); err != nil {
		return Session{}, err
	}
	if !allowed {
		return Session{}, ErrForbidden
	}
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET current_company_id=$1 WHERE id=$2`, companyID, session.ID); err != nil {
		return Session{}, err
	}
	_ = s.audit(ctx, companyID, session.User.ID, "COMPANY_SELECTED", "company", companyID, nil, meta)
	return s.Authenticate(ctx, session.Token)
}

func (s *Service) Logout(ctx context.Context, session Session, meta RequestMeta) error {
	result, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, session.ID)
	if err != nil {
		return err
	}
	if result.RowsAffected() > 0 {
		return s.audit(ctx, session.CurrentCompanyID, session.User.ID, "LOGOUT", "session", session.ID, nil, meta)
	}
	return nil
}

func (s *Service) BeginTOTP(ctx context.Context, session Session, meta RequestMeta) (string, string, error) {
	secret, err := NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	ciphertext, err := s.secretBox.Seal(session.User.ID, "totp", []byte(secret))
	if err != nil {
		return "", "", err
	}
	if _, err = s.pool.Exec(ctx, `UPDATE users SET totp_pending_ciphertext=$1,updated_at=now(),version=version+1 WHERE id=$2 AND is_active`, ciphertext, session.User.ID); err != nil {
		return "", "", err
	}
	_ = s.audit(ctx, session.CurrentCompanyID, session.User.ID, "TOTP_SETUP_STARTED", "user", session.User.ID, nil, meta)
	return secret, TOTPURI(secret, session.User.Email), nil
}

func (s *Service) ConfirmTOTP(ctx context.Context, session Session, code string, meta RequestMeta) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var ciphertext []byte
	if err = tx.QueryRow(ctx, `SELECT totp_pending_ciphertext FROM users WHERE id=$1 FOR UPDATE`, session.User.ID).Scan(&ciphertext); err != nil || len(ciphertext) == 0 {
		return nil, fmt.Errorf("%w: bekleyen TOTP kurulumu yok", ErrValidation)
	}
	secret, err := s.secretBox.Open(session.User.ID, "totp", ciphertext)
	if err != nil || !VerifyTOTP(string(secret), code, s.now()) {
		return nil, ErrInvalidCredentials
	}
	codes := make([]string, 8)
	if _, err = tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id=$1`, session.User.ID); err != nil {
		return nil, err
	}
	for index := range codes {
		codes[index], err = randomToken(9)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO recovery_codes(user_id,code_hash) VALUES($1,$2)`, session.User.ID, tokenHash(codes[index])); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET totp_secret_ciphertext=totp_pending_ciphertext,totp_pending_ciphertext=NULL,totp_enabled_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, session.User.ID); err != nil {
		return nil, err
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "TOTP_ENABLED", "user", session.User.ID, nil, meta); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) DisableTOTP(ctx context.Context, session Session, password string, meta RequestMeta) error {
	var passwordHash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 AND is_active`, session.User.ID).Scan(&passwordHash); err != nil {
		return err
	}
	valid, err := VerifyPassword(passwordHash, password)
	if err != nil {
		return err
	}
	if !valid {
		return ErrInvalidCredentials
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `UPDATE users SET totp_secret_ciphertext=NULL,totp_pending_ciphertext=NULL,totp_enabled_at=NULL,updated_at=now(),version=version+1 WHERE id=$1`, session.User.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id=$1`, session.User.ID); err != nil {
		return err
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "TOTP_DISABLED", "user", session.User.ID, nil, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var allowedTokenScopes = map[string]struct{}{
	"organization:read":   {},
	"party:read":          {},
	"party:ledger:read":   {},
	"security:audit:read": {},
	"security:users:read": {},
	"finance:read":        {},
}

func (s *Service) CreateAPIToken(ctx context.Context, session Session, name string, scopes []string, expiresAt *time.Time, meta RequestMeta) (APIToken, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.token.manage") {
		return APIToken{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	scopes = uniqueStrings(scopes)
	if name == "" || len(name) > 120 {
		return APIToken{}, fmt.Errorf("%w: token adı gereklidir", ErrValidation)
	}
	if expiresAt != nil && !expiresAt.After(s.now()) {
		return APIToken{}, fmt.Errorf("%w: token son kullanma tarihi gelecekte olmalıdır", ErrValidation)
	}
	for _, scope := range scopes {
		if _, allowed := allowedTokenScopes[scope]; !allowed {
			return APIToken{}, fmt.Errorf("%w: geçersiz token kapsamı", ErrValidation)
		}
	}
	id, err := newID()
	if err != nil {
		return APIToken{}, err
	}
	random, err := randomToken(32)
	if err != nil {
		return APIToken{}, err
	}
	plain := "vry_" + random
	prefix := plain[:12]
	var createdAt time.Time
	err = s.pool.QueryRow(ctx, `INSERT INTO api_tokens(id,company_id,owner_user_id,name,token_prefix,token_hash,scopes,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		id, session.CurrentCompanyID, session.User.ID, name, prefix, tokenHash(plain), scopes, expiresAt).Scan(&createdAt)
	if err != nil {
		return APIToken{}, err
	}
	_ = s.audit(ctx, session.CurrentCompanyID, session.User.ID, "API_TOKEN_CREATED", "api_token", id, map[string]any{"scopes": scopes}, meta)
	return APIToken{ID: id, Name: name, Prefix: prefix, Scopes: scopes, CreatedAt: createdAt, ExpiresAt: expiresAt, PlainToken: plain}, nil
}

func (s *Service) ListAPITokens(ctx context.Context, session Session) ([]APIToken, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.token.manage") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,name,token_prefix,scopes,created_at,expires_at,revoked_at FROM api_tokens WHERE company_id=$1 ORDER BY created_at DESC`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []APIToken{}
	for rows.Next() {
		var token APIToken
		if err := rows.Scan(&token.ID, &token.Name, &token.Prefix, &token.Scopes, &token.CreatedAt, &token.ExpiresAt, &token.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, token)
	}
	return result, rows.Err()
}

func (s *Service) RevokeAPIToken(ctx context.Context, session Session, tokenID string, meta RequestMeta) error {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.token.manage") {
		return ErrForbidden
	}
	result, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE company_id=$1 AND id=$2 AND revoked_at IS NULL`, session.CurrentCompanyID, tokenID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrForbidden
	}
	return s.audit(ctx, session.CurrentCompanyID, session.User.ID, "API_TOKEN_REVOKED", "api_token", tokenID, nil, meta)
}

func (s *Service) createSession(ctx context.Context, tx pgx.Tx, user User, companyID string, meta RequestMeta) (Session, error) {
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return Session{}, err
	}
	expires := s.now().UTC().Add(sessionLifetime)
	_, err = tx.Exec(ctx, `INSERT INTO sessions(id,token_hash,csrf_hash,user_id,current_company_id,expires_at,ip_hash,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, tokenHash(token), tokenHash(csrf), user.ID, nullString(companyID), expires, identifierHash(meta.IP), truncate(meta.UserAgent, 512))
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	companies, err := companiesTx(ctx, tx, user.ID)
	if err != nil {
		return Session{}, err
	}
	permissions := []string{}
	modules := []string{}
	if companyID != "" {
		if permissions, err = permissionsTx(ctx, tx, user.ID, companyID); err != nil {
			return Session{}, err
		}
		modules, err = activeModulesTx(ctx, tx, companyID)
	}
	return Session{ID: id, Token: token, CSRFToken: csrf, User: user, Companies: companies, CurrentCompanyID: companyID, ExpiresAt: expires, Permissions: permissions, Modules: modules}, err
}

func (s *Service) companies(ctx context.Context, userID string) ([]Company, error) {
	return scanCompanies(s.pool.Query(ctx, `SELECT c.id,c.legal_name,c.trade_name,c.entity_type,c.base_currency,c.timezone,c.version FROM companies c JOIN company_memberships m ON m.company_id=c.id WHERE m.user_id=$1 AND m.is_active AND c.is_active ORDER BY c.trade_name`, userID))
}

func companiesTx(ctx context.Context, tx pgx.Tx, userID string) ([]Company, error) {
	return scanCompanies(tx.Query(ctx, `SELECT c.id,c.legal_name,c.trade_name,c.entity_type,c.base_currency,c.timezone,c.version FROM companies c JOIN company_memberships m ON m.company_id=c.id WHERE m.user_id=$1 AND m.is_active AND c.is_active ORDER BY c.trade_name`, userID))
}

type rowsResult interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

func scanCompanies(rows rowsResult, err error) ([]Company, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	companies := []Company{}
	for rows.Next() {
		var company Company
		if err := rows.Scan(&company.ID, &company.LegalName, &company.TradeName, &company.EntityType, &company.BaseCurrency, &company.Timezone, &company.Version); err != nil {
			return nil, err
		}
		companies = append(companies, company)
	}
	return companies, rows.Err()
}

func (s *Service) permissions(ctx context.Context, userID, companyID string) ([]string, error) {
	return scanStrings(s.pool.Query(ctx, `SELECT DISTINCT rp.permission_code FROM company_memberships m JOIN membership_roles mr USING(company_id,user_id) JOIN role_permissions rp USING(company_id,role_id) WHERE m.company_id=$1 AND m.user_id=$2 AND m.is_active ORDER BY rp.permission_code`, companyID, userID))
}

func permissionsTx(ctx context.Context, tx pgx.Tx, userID, companyID string) ([]string, error) {
	return scanStrings(tx.Query(ctx, `SELECT DISTINCT rp.permission_code FROM company_memberships m JOIN membership_roles mr USING(company_id,user_id) JOIN role_permissions rp USING(company_id,role_id) WHERE m.company_id=$1 AND m.user_id=$2 AND m.is_active ORDER BY rp.permission_code`, companyID, userID))
}

func (s *Service) activeModules(ctx context.Context, companyID string) ([]string, error) {
	return scanStrings(s.pool.Query(ctx, `SELECT module_code FROM company_modules WHERE company_id=$1 AND enabled ORDER BY module_code`, companyID))
}

func activeModulesTx(ctx context.Context, tx pgx.Tx, companyID string) ([]string, error) {
	return scanStrings(tx.Query(ctx, `SELECT module_code FROM company_modules WHERE company_id=$1 AND enabled ORDER BY module_code`, companyID))
}

func scanStrings(rows rowsResult, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) audit(ctx context.Context, companyID, userID, eventType, entityType, entityID string, details map[string]any, meta RequestMeta) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, nullString(companyID), nullString(userID), eventType, nullString(entityType), nullString(entityID), jsonBytes(details), meta.TraceID, meta.IP, truncate(meta.UserAgent, 512))
	return err
}

func insertAudit(ctx context.Context, tx pgx.Tx, companyID, userID, eventType, entityType, entityID string, details map[string]any, meta RequestMeta) error {
	id, err := newID()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		id, nullString(companyID), nullString(userID), eventType, nullString(entityType), nullString(entityID), jsonBytes(details), meta.TraceID, meta.IP, truncate(meta.UserAgent, 512))
	return err
}

func makeIDs(count int) ([]string, error) {
	ids := make([]string, count)
	for index := range ids {
		id, err := newID()
		if err != nil {
			return nil, err
		}
		ids[index] = id
	}
	return ids, nil
}

func seedDefaultPartyGroups(ctx context.Context, tx pgx.Tx, companyID string) error {
	for _, group := range defaultPartyGroups {
		id, err := newID()
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO party_groups (id, company_id, code, name)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (company_id, code) DO NOTHING`, id, companyID, group.code, group.name); err != nil {
			return err
		}
	}
	return nil
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func jsonBytes(value any) []byte {
	if value == nil {
		return []byte("{}")
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func truncate(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length]
}
