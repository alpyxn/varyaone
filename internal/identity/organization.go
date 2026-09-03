package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/modules"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// logoDataURIPattern accepts only a well-formed base64 image data URI — the one
// shape the browser uploader ever produces (FileReader.readAsDataURL). The logo
// is later echoed into a `src="..."` attribute of the print/PDF window, so a
// looser check (bare "data:image/" prefix) would let a value like
// `data:image/png"><script>...` break out of that attribute.
var logoDataURIPattern = regexp.MustCompile(`^data:image/[a-zA-Z0-9.+-]+;base64,[A-Za-z0-9+/]+=*$`)

// ModuleState reports a single feature module and whether it is enabled for the
// current company.
type ModuleState struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Version     int64  `json:"version"`
}

// ListModules returns every catalog module with its activation state for the
// session's current company. Rows missing from company_modules (e.g. a module
// shipped after setup) are reported as enabled with version 0.
func (s *Service) ListModules(ctx context.Context, session Session) ([]ModuleState, error) {
	if session.CurrentCompanyID == "" ||
		(!session.HasPermission("organization.company.read") && !session.HasPermission("organization.module.manage")) {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT module_code,enabled,version FROM company_modules WHERE company_id=$1`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type record struct {
		enabled bool
		version int64
	}
	stored := map[string]record{}
	for rows.Next() {
		var code string
		var rec record
		if err := rows.Scan(&code, &rec.enabled, &rec.version); err != nil {
			return nil, err
		}
		stored[code] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]ModuleState, 0, len(modules.Catalog))
	for _, definition := range modules.Catalog {
		rec, ok := stored[definition.Code]
		state := ModuleState{Code: definition.Code, Name: definition.Name, Description: definition.Description, Enabled: true}
		if ok {
			state.Enabled = rec.enabled
			state.Version = rec.version
		}
		result = append(result, state)
	}
	return result, nil
}

// SetModule enables or disables a feature module for the session's current
// company. expectedVersion guards against a concurrent change; pass 0 to create
// the row for a module that has no state yet.
func (s *Service) SetModule(ctx context.Context, session Session, code string, enabled bool, expectedVersion int64, meta RequestMeta) (ModuleState, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("organization.module.manage") {
		return ModuleState{}, ErrForbidden
	}
	if !modules.Valid(code) {
		return ModuleState{}, fmt.Errorf("%w: bilinmeyen modül", ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ModuleState{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var version int64
	if expectedVersion < 1 {
		err = tx.QueryRow(ctx, `
			INSERT INTO company_modules(company_id,module_code,enabled,updated_by)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(company_id,module_code) DO NOTHING
			RETURNING version`, session.CurrentCompanyID, code, enabled, session.User.ID).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ModuleState{}, ErrConflict
		}
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE company_modules SET enabled=$3,updated_at=now(),updated_by=$4,version=version+1
			WHERE company_id=$1 AND module_code=$2 AND version=$5
			RETURNING version`, session.CurrentCompanyID, code, enabled, session.User.ID, expectedVersion).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return ModuleState{}, ErrConflict
		}
	}
	if err != nil {
		return ModuleState{}, err
	}
	// entity_id is a uuid column, so the module code lives in details instead.
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "MODULE_TOGGLED", "module", "", map[string]any{"module": code, "enabled": enabled}, meta); err != nil {
		return ModuleState{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ModuleState{}, err
	}
	definition := modules.Catalog[0]
	for _, item := range modules.Catalog {
		if item.Code == code {
			definition = item
		}
	}
	return ModuleState{Code: code, Name: definition.Name, Description: definition.Description, Enabled: enabled, Version: version}, nil
}

type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IsSystem    bool     `json:"is_system"`
	Version     int64    `json:"version"`
	Permissions []string `json:"permissions"`
}

type Member struct {
	User     User     `json:"user"`
	IsActive bool     `json:"is_active"`
	Version  int64    `json:"version"`
	Roles    []string `json:"role_ids"`
}

type MemberInput struct {
	Email        string   `json:"email"`
	DisplayName  string   `json:"display_name"`
	Password     string   `json:"password"`
	RoleIDs      []string `json:"role_ids"`
	BranchIDs    []string `json:"branch_ids"`
	WarehouseIDs []string `json:"warehouse_ids"`
}

type CompanyInput struct {
	LegalName                     string `json:"legal_name"`
	TradeName                     string `json:"trade_name"`
	EntityType                    string `json:"entity_type"`
	TaxNumber                     string `json:"tax_number"`
	BaseCurrency                  string `json:"base_currency"`
	Timezone                      string `json:"timezone"`
	DuplicatePartyTaxNumberPolicy string `json:"duplicate_party_tax_number_policy"`
	PartyCodePrefix               string `json:"party_code_prefix"`
	PartyCodeDigits               int    `json:"party_code_digits"`
	Logo                          string `json:"logo"`
}

func (s *Service) CurrentCompany(ctx context.Context, session Session) (Company, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("organization.company.read") {
		return Company{}, ErrForbidden
	}
	var company Company
	err := s.pool.QueryRow(ctx, `SELECT id,legal_name,trade_name,entity_type,tax_number,base_currency,timezone,duplicate_party_tax_number_policy,party_code_prefix,party_code_digits,logo,version FROM companies WHERE id=$1 AND is_active`, session.CurrentCompanyID).
		Scan(&company.ID, &company.LegalName, &company.TradeName, &company.EntityType, &company.TaxNumber, &company.BaseCurrency, &company.Timezone, &company.DuplicatePartyTaxNumberPolicy, &company.PartyCodePrefix, &company.PartyCodeDigits, &company.Logo, &company.Version)
	return company, err
}

func (s *Service) UpdateCompany(ctx context.Context, session Session, input CompanyInput, expectedVersion int64, meta RequestMeta) (Company, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("organization.company.edit") {
		return Company{}, ErrForbidden
	}
	// Phase 0.1 clients do not know the Phase 0.2 cari settings yet. Omitted fields
	// retain their current values instead of silently resetting company policy.
	if strings.TrimSpace(input.DuplicatePartyTaxNumberPolicy) == "" || strings.TrimSpace(input.PartyCodePrefix) == "" || input.PartyCodeDigits == 0 {
		var currentPolicy, currentPrefix string
		var currentDigits int
		if err := s.pool.QueryRow(ctx, `SELECT duplicate_party_tax_number_policy,party_code_prefix,party_code_digits FROM companies WHERE id=$1 AND is_active`, session.CurrentCompanyID).Scan(&currentPolicy, &currentPrefix, &currentDigits); err != nil {
			return Company{}, err
		}
		if strings.TrimSpace(input.DuplicatePartyTaxNumberPolicy) == "" {
			input.DuplicatePartyTaxNumberPolicy = currentPolicy
		}
		if strings.TrimSpace(input.PartyCodePrefix) == "" {
			input.PartyCodePrefix = currentPrefix
		}
		if input.PartyCodeDigits == 0 {
			input.PartyCodeDigits = currentDigits
		}
	}
	input.LegalName = strings.TrimSpace(input.LegalName)
	input.TradeName = strings.TrimSpace(input.TradeName)
	input.EntityType = strings.TrimSpace(input.EntityType)
	input.BaseCurrency = strings.ToUpper(strings.TrimSpace(input.BaseCurrency))
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.DuplicatePartyTaxNumberPolicy = strings.ToUpper(strings.TrimSpace(input.DuplicatePartyTaxNumberPolicy))
	input.PartyCodePrefix = strings.ToUpper(strings.TrimSpace(input.PartyCodePrefix))
	if input.LegalName == "" || input.TradeName == "" || len(input.BaseCurrency) != 3 || expectedVersion < 1 {
		return Company{}, fmt.Errorf("%w: firma unvanı, ticari ad, para birimi ve If-Match gereklidir", ErrValidation)
	}
	if input.EntityType != "LEGAL_ENTITY" && input.EntityType != "SOLE_PROPRIETOR" {
		return Company{}, fmt.Errorf("%w: geçersiz firma türü", ErrValidation)
	}
	if input.DuplicatePartyTaxNumberPolicy != "ALLOW" && input.DuplicatePartyTaxNumberPolicy != "WARN" && input.DuplicatePartyTaxNumberPolicy != "BLOCK" {
		return Company{}, fmt.Errorf("%w: geçersiz mükerrer vergi numarası politikası", ErrValidation)
	}
	if input.PartyCodePrefix == "" || len(input.PartyCodePrefix) > 8 || input.PartyCodeDigits < 3 || input.PartyCodeDigits > 12 {
		return Company{}, fmt.Errorf("%w: cari kod öneki ve basamak sayısı geçersiz", ErrValidation)
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return Company{}, fmt.Errorf("%w: geçersiz saat dilimi", ErrValidation)
	}
	input.Logo = strings.TrimSpace(input.Logo)
	if input.Logo != "" && (!logoDataURIPattern.MatchString(input.Logo) || len(input.Logo) > 700000) {
		return Company{}, fmt.Errorf("%w: logo bir görsel olmalı ve 500 KB'ı aşmamalıdır", ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Company{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var company Company
	err = tx.QueryRow(ctx, `UPDATE companies SET legal_name=$1,trade_name=$2,entity_type=$3,tax_number=$4,base_currency=$5,timezone=$6,duplicate_party_tax_number_policy=$7,party_code_prefix=$8,party_code_digits=$9,logo=$12,updated_at=now(),version=version+1 WHERE id=$10 AND version=$11 AND is_active RETURNING id,legal_name,trade_name,entity_type,tax_number,base_currency,timezone,duplicate_party_tax_number_policy,party_code_prefix,party_code_digits,logo,version`,
		input.LegalName, input.TradeName, input.EntityType, nullString(input.TaxNumber), input.BaseCurrency, input.Timezone, input.DuplicatePartyTaxNumberPolicy, input.PartyCodePrefix, input.PartyCodeDigits, session.CurrentCompanyID, expectedVersion, input.Logo).
		Scan(&company.ID, &company.LegalName, &company.TradeName, &company.EntityType, &company.TaxNumber, &company.BaseCurrency, &company.Timezone, &company.DuplicatePartyTaxNumberPolicy, &company.PartyCodePrefix, &company.PartyCodeDigits, &company.Logo, &company.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Company{}, ErrConflict
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "company_base_currency_locked" {
		return Company{}, fmt.Errorf("%w: posted geçmiş bulunan şirketin temel para birimi değiştirilemez", ErrBaseCurrencyLocked)
	}
	if err != nil {
		return Company{}, err
	}
	if err = insertAudit(ctx, tx, company.ID, session.User.ID, "COMPANY_UPDATED", "company", company.ID, map[string]any{"version": company.Version}, meta); err != nil {
		return Company{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Company{}, err
	}
	return company, nil
}

// DeleteCompany permanently removes a company and every row scoped to it, then
// switches the caller's session to another company they belong to. Only the
// instance owner (the user who completed the one-time setup) may call it, and the
// caller must confirm by typing the company's exact trade name. The last company
// a user has access to can never be deleted.
func (s *Service) DeleteCompany(ctx context.Context, session Session, companyID, confirmName string, meta RequestMeta) (Session, error) {
	// The target company is, by definition, outside this request's RLS scope.
	ctx = database.WithoutConn(ctx)
	companyID = strings.TrimSpace(companyID)
	confirmName = strings.TrimSpace(confirmName)
	if companyID == "" {
		return Session{}, fmt.Errorf("%w: firma kimliği gereklidir", ErrValidation)
	}

	var isOwner bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM instance_setup WHERE completed_by=$1)`, session.User.ID).Scan(&isOwner); err != nil {
		return Session{}, err
	}
	if !isOwner {
		return Session{}, ErrForbidden
	}
	if s.maintenanceDSN == "" {
		return Session{}, fmt.Errorf("%w: firma silme bu kurulumda yapılandırılmamış", ErrValidation)
	}

	// Purging a company means deleting rows the RLS policies hide and the
	// per-table immutability triggers protect (posted stock, ledger entries, the
	// system transit warehouse). That is only possible on the owner/superuser
	// connection with session_replication_role switched to replica, so this runs
	// on a dedicated maintenance connection rather than the serving pool.
	conn, err := pgx.Connect(ctx, s.maintenanceDSN)
	if err != nil {
		return Session{}, fmt.Errorf("open maintenance connection: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var tradeName string
	switch err = tx.QueryRow(ctx, `SELECT trade_name FROM companies WHERE id=$1 FOR UPDATE`, companyID).Scan(&tradeName); {
	case errors.Is(err, pgx.ErrNoRows):
		return Session{}, fmt.Errorf("%w: firma bulunamadı", ErrValidation)
	case err != nil:
		return Session{}, err
	}
	if !strings.EqualFold(confirmName, tradeName) {
		return Session{}, fmt.Errorf("%w: onaylamak için firmanın ticari adını (%s) tam olarak yazın", ErrValidation, tradeName)
	}

	// The caller must have another company to fall back to. This is also what
	// enforces that the last remaining company can never be deleted.
	var fallbackID string
	err = tx.QueryRow(ctx, `
		SELECT c.id FROM companies c
		JOIN company_memberships m ON m.company_id=c.id
		WHERE m.user_id=$1 AND m.is_active AND c.is_active AND c.id<>$2
		ORDER BY c.trade_name LIMIT 1`, session.User.ID, companyID).Scan(&fallbackID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrLastCompany
	}
	if err != nil {
		return Session{}, err
	}

	if err = purgeCompanyRows(ctx, tx, companyID); err != nil {
		return Session{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET current_company_id=$1 WHERE id=$2`, fallbackID, session.ID); err != nil {
		return Session{}, err
	}
	if err = insertAudit(ctx, tx, fallbackID, session.User.ID, "COMPANY_DELETED", "company", companyID, map[string]any{"trade_name": tradeName}, meta); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Session{}, err
	}
	return s.Authenticate(ctx, session.Token)
}

// purgeCompanyRows deletes every row scoped to companyID and the company itself,
// inside the caller's transaction on an owner/superuser connection.
//
// Every base table carrying a company_id column is company-scoped data that must
// go. Reading that list from the catalogue also covers the per-document-type line
// tables that are created dynamically outside the migration DDL.
func purgeCompanyRows(ctx context.Context, tx pgx.Tx, companyID string) error {
	scopedTables, err := scanStrings(tx.Query(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema=c.table_schema AND t.table_name=c.table_name
		WHERE c.table_schema='public' AND c.column_name='company_id'
		  AND t.table_type='BASE TABLE'
		ORDER BY c.table_name`))
	if err != nil {
		return err
	}

	// replica mode disables FK enforcement and every user trigger for this
	// transaction, so the purge below can run in a single pass in any order.
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		return fmt.Errorf("enable maintenance mode: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE sessions SET current_company_id=NULL WHERE current_company_id=$1`, companyID); err != nil {
		return err
	}
	for _, table := range scopedTables {
		if _, err = tx.Exec(ctx, `DELETE FROM `+pgx.Identifier{table}.Sanitize()+` WHERE company_id=$1`, companyID); err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM companies WHERE id=$1`, companyID); err != nil {
		return fmt.Errorf("delete company: %w", err)
	}
	return nil
}

func (s *Service) ListPermissions(ctx context.Context, session Session) ([]string, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.user.read") {
		return nil, ErrForbidden
	}
	return scanStrings(s.pool.Query(ctx, `SELECT code FROM permissions ORDER BY code`))
}

func (s *Service) ListRoles(ctx context.Context, session Session) ([]Role, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.user.read") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,r.name,r.is_system,r.version,
		       COALESCE(array_agg(rp.permission_code ORDER BY rp.permission_code) FILTER (WHERE rp.permission_code IS NOT NULL),'{}')
		FROM roles r LEFT JOIN role_permissions rp ON rp.company_id=r.company_id AND rp.role_id=r.id
		WHERE r.company_id=$1 GROUP BY r.id,r.name,r.is_system,r.version ORDER BY r.name`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []Role{}
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Name, &role.IsSystem, &role.Version, &role.Permissions); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Service) CreateRole(ctx context.Context, session Session, name string, permissions []string, meta RequestMeta) (Role, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.role.manage") {
		return Role{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	permissions = uniqueStrings(permissions)
	if name == "" || len(name) > 120 || len(permissions) == 0 {
		return Role{}, fmt.Errorf("%w: rol adı ve en az bir yetki gereklidir", ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = validatePermissions(ctx, tx, permissions); err != nil {
		return Role{}, err
	}
	id, err := newID()
	if err != nil {
		return Role{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO roles(id,company_id,name) VALUES($1,$2,$3)`, id, session.CurrentCompanyID, name); err != nil {
		return Role{}, err
	}
	for _, permission := range permissions {
		if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(company_id,role_id,permission_code) VALUES($1,$2,$3)`, session.CurrentCompanyID, id, permission); err != nil {
			return Role{}, err
		}
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "ROLE_CREATED", "role", id, map[string]any{"permissions": permissions}, meta); err != nil {
		return Role{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Role{}, err
	}
	return Role{ID: id, Name: name, Version: 1, Permissions: permissions}, nil
}

func (s *Service) UpdateRole(ctx context.Context, session Session, roleID, name string, permissions []string, expectedVersion int64, meta RequestMeta) (Role, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.role.manage") {
		return Role{}, ErrForbidden
	}
	name = strings.TrimSpace(name)
	permissions = uniqueStrings(permissions)
	if expectedVersion < 1 || name == "" || len(permissions) == 0 {
		return Role{}, fmt.Errorf("%w: geçerli If-Match sürümü, rol adı ve yetkiler gereklidir", ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The system administrator role ("Yönetici") is locked: its permission set
	// is the full catalogue and is kept complete by migrations, so allowing an
	// edit here would only ever remove access an operator relies on. Reject it
	// explicitly rather than letting the `NOT is_system` filter below surface a
	// misleading version-conflict error.
	var isSystem bool
	switch err = tx.QueryRow(ctx, `SELECT is_system FROM roles WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, roleID).Scan(&isSystem); {
	case errors.Is(err, pgx.ErrNoRows):
		return Role{}, fmt.Errorf("%w: rol bulunamadı", ErrValidation)
	case err != nil:
		return Role{}, err
	case isSystem:
		return Role{}, fmt.Errorf("%w: sistem rolü (Yönetici) düzenlenemez", ErrForbidden)
	}

	if err = validatePermissions(ctx, tx, permissions); err != nil {
		return Role{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE roles SET name=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4 AND NOT is_system`, name, session.CurrentCompanyID, roleID, expectedVersion)
	if err != nil {
		return Role{}, err
	}
	if result.RowsAffected() == 0 {
		return Role{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM role_permissions WHERE company_id=$1 AND role_id=$2`, session.CurrentCompanyID, roleID); err != nil {
		return Role{}, err
	}
	for _, permission := range permissions {
		if _, err = tx.Exec(ctx, `INSERT INTO role_permissions(company_id,role_id,permission_code) VALUES($1,$2,$3)`, session.CurrentCompanyID, roleID, permission); err != nil {
			return Role{}, err
		}
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "ROLE_UPDATED", "role", roleID, map[string]any{"version": expectedVersion + 1, "permissions": permissions}, meta); err != nil {
		return Role{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Role{}, err
	}
	return Role{ID: roleID, Name: name, Version: expectedVersion + 1, Permissions: permissions}, nil
}

func (s *Service) ListMembers(ctx context.Context, session Session) ([]Member, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.user.read") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id,u.email,u.display_name,m.is_active,m.version,
		       COALESCE(array_agg(mr.role_id::text ORDER BY mr.role_id) FILTER (WHERE mr.role_id IS NOT NULL),'{}')
		FROM company_memberships m JOIN users u ON u.id=m.user_id
		LEFT JOIN membership_roles mr ON mr.company_id=m.company_id AND mr.user_id=m.user_id
		WHERE m.company_id=$1 GROUP BY u.id,u.email,u.display_name,m.is_active,m.version ORDER BY u.display_name`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.User.ID, &member.User.Email, &member.User.DisplayName, &member.IsActive, &member.Version, &member.Roles); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *Service) AddMember(ctx context.Context, session Session, input MemberInput, meta RequestMeta) (Member, error) {
	if session.CurrentCompanyID == "" || !session.HasPermission("security.user.manage") {
		return Member{}, ErrForbidden
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.RoleIDs = uniqueStrings(input.RoleIDs)
	input.BranchIDs = uniqueStrings(input.BranchIDs)
	input.WarehouseIDs = uniqueStrings(input.WarehouseIDs)
	address, addressErr := mail.ParseAddress(input.Email)
	if addressErr != nil || address.Address != input.Email || len(input.RoleIDs) == 0 {
		return Member{}, fmt.Errorf("%w: geçerli e-posta ve en az bir rol gereklidir", ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Member{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var user User
	err = tx.QueryRow(ctx, `SELECT id,email,display_name FROM users WHERE email=$1`, input.Email).Scan(&user.ID, &user.Email, &user.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.DisplayName == "" {
			return Member{}, fmt.Errorf("%w: yeni kullanıcı için ad soyad gereklidir", ErrValidation)
		}
		passwordHash, hashErr := HashPassword(input.Password)
		if hashErr != nil {
			return Member{}, fmt.Errorf("%w: %v", ErrValidation, hashErr)
		}
		user.ID, err = newID()
		if err != nil {
			return Member{}, err
		}
		user.Email, user.DisplayName = input.Email, input.DisplayName
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,$2,$3,$4)`, user.ID, user.Email, user.DisplayName, passwordHash); err != nil {
			return Member{}, err
		}
	} else if err != nil {
		return Member{}, err
	}
	var membershipVersion int64
	if err = tx.QueryRow(ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2) ON CONFLICT(company_id,user_id) DO UPDATE SET is_active=true,updated_at=now(),version=company_memberships.version+1 RETURNING version`, session.CurrentCompanyID, user.ID).Scan(&membershipVersion); err != nil {
		return Member{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM membership_roles WHERE company_id=$1 AND user_id=$2`, session.CurrentCompanyID, user.ID); err != nil {
		return Member{}, err
	}
	for _, roleID := range input.RoleIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO membership_roles(company_id,user_id,role_id) VALUES($1,$2,$3)`, session.CurrentCompanyID, user.ID, roleID); err != nil {
			return Member{}, fmt.Errorf("%w: rol seçimi bu firmaya ait değil", ErrValidation)
		}
	}
	if err = replaceMemberScopes(ctx, tx, session.CurrentCompanyID, user.ID, input.BranchIDs, input.WarehouseIDs); err != nil {
		return Member{}, err
	}
	if err = insertAudit(ctx, tx, session.CurrentCompanyID, session.User.ID, "MEMBER_SAVED", "user", user.ID, map[string]any{"role_ids": input.RoleIDs}, meta); err != nil {
		return Member{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Member{}, err
	}
	return Member{User: user, IsActive: true, Version: membershipVersion, Roles: input.RoleIDs}, nil
}

func validatePermissions(ctx context.Context, tx pgx.Tx, permissions []string) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE code=ANY($1::text[])`, permissions).Scan(&count); err != nil {
		return err
	}
	if count != len(permissions) {
		return fmt.Errorf("%w: bilinmeyen yetki değeri", ErrValidation)
	}
	return nil
}

func replaceMemberScopes(ctx context.Context, tx pgx.Tx, companyID, userID string, branchIDs, warehouseIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM membership_branch_scopes WHERE company_id=$1 AND user_id=$2`, companyID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM membership_warehouse_scopes WHERE company_id=$1 AND user_id=$2`, companyID, userID); err != nil {
		return err
	}
	for _, branchID := range branchIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, companyID, userID, branchID); err != nil {
			return fmt.Errorf("%w: şube kapsamı bu firmaya ait değil", ErrValidation)
		}
	}
	for _, warehouseID := range warehouseIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO membership_warehouse_scopes(company_id,user_id,warehouse_id) VALUES($1,$2,$3)`, companyID, userID, warehouseID); err != nil {
			return fmt.Errorf("%w: depo kapsamı bu firmaya ait değil", ErrValidation)
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
