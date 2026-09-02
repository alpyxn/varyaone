package fixedasset

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/codeseq"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound       = errors.New("FIXED_ASSET_NOT_FOUND")
	ErrAssignNotFound = errors.New("FIXED_ASSET_ASSIGNMENT_NOT_FOUND")
	ErrEmployeeGone   = errors.New("FIXED_ASSET_EMPLOYEE_NOT_FOUND")
)

// editableStatuses are the states a user may set directly on an asset card.
// ASSIGNED is reached only through an assignment and left only through a return.
var editableStatuses = map[string]bool{"AVAILABLE": true, "MAINTENANCE": true, "RETIRED": true}

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Record struct {
	ID           string      `json:"id"`
	AssetCode    string      `json:"asset_code"`
	Name         string      `json:"name"`
	Category     string      `json:"category"`
	SerialNumber *string     `json:"serial_number,omitempty"`
	Description  string      `json:"description"`
	Status       string      `json:"status"`
	AssignedTo   *AssignedTo `json:"assigned_to,omitempty"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
	Version      int64       `json:"version"`
}

type AssignedTo struct {
	AssignmentID string    `json:"assignment_id"`
	EmployeeID   string    `json:"employee_id"`
	EmployeeCode string    `json:"employee_code"`
	EmployeeName string    `json:"employee_name"`
	AssignedAt   time.Time `json:"assigned_at"`
}

type Input struct {
	AssetCode    string `json:"asset_code"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	SerialNumber string `json:"serial_number"`
	Description  string `json:"description"`
	Status       string `json:"status"`
}

type ListResult struct {
	Items      []Record `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

type AssignmentRecord struct {
	ID             string     `json:"id"`
	AssetID        string     `json:"asset_id"`
	AssetCode      string     `json:"asset_code"`
	AssetName      string     `json:"asset_name"`
	EmployeeID     string     `json:"employee_id"`
	EmployeeCode   string     `json:"employee_code"`
	EmployeeName   string     `json:"employee_name"`
	AssignedAt     time.Time  `json:"assigned_at"`
	ReturnedAt     *time.Time `json:"returned_at,omitempty"`
	AssignmentNote string     `json:"assignment_note"`
	ReturnNote     *string    `json:"return_note,omitempty"`
}

type AssignInput struct {
	EmployeeID string `json:"employee_id"`
	AssignedAt string `json:"assigned_at"`
	Note       string `json:"note"`
}

type ReturnInput struct {
	ReturnedAt string `json:"returned_at"`
	Note       string `json:"note"`
}

const assetSelect = `SELECT a.id::text,a.asset_code,a.name,a.category,a.serial_number,a.description,a.status,
 act.id::text,act.employee_id::text,e.employee_code,e.first_name||' '||e.last_name,act.assigned_at,
 a.created_at,a.updated_at,a.version
 FROM fixed_assets a
 LEFT JOIN LATERAL(SELECT id,employee_id,assigned_at FROM asset_assignments x
   WHERE x.company_id=a.company_id AND x.asset_id=a.id AND x.returned_at IS NULL) act ON true
 LEFT JOIN employees e ON e.company_id=a.company_id AND e.id=act.employee_id `

func (s *Service) List(ctx context.Context, session identity.Session, query, status, category, cursor string, limit int) (ListResult, error) {
	if !session.HasPermission("fixed_asset.read") {
		return ListResult{}, identity.ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cursorCode, cursorID := decodeCursor(cursor)
	rows, err := s.pool.Query(ctx, assetSelect+` WHERE a.company_id=$1
 AND ($2='' OR a.status=$2)
 AND ($3='' OR a.category ILIKE '%'||$3||'%')
 AND ($4='' OR a.asset_code ILIKE '%'||$4||'%' OR a.name ILIKE '%'||$4||'%' OR a.serial_number ILIKE '%'||$4||'%')
 AND ($5='' OR (a.asset_code,a.id) > ($5,NULLIF($6,'')::uuid))
 ORDER BY a.asset_code,a.id LIMIT $7`,
		session.CurrentCompanyID, strings.ToUpper(strings.TrimSpace(status)), strings.TrimSpace(category),
		strings.TrimSpace(query), cursorCode, cursorID, limit+1)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	result := ListResult{Items: []Record{}}
	for rows.Next() {
		item, err := scanRecord(rows)
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return ListResult{}, err
	}
	if len(result.Items) > limit {
		last := result.Items[limit-1]
		result.Items = result.Items[:limit]
		result.NextCursor = encodeCursor(last.AssetCode, last.ID)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string) (Record, error) {
	if !session.HasPermission("fixed_asset.read") {
		return Record{}, identity.ErrForbidden
	}
	item, err := scanRecord(s.pool.QueryRow(ctx, assetSelect+` WHERE a.company_id=$1 AND a.id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return item, err
}

func (s *Service) Create(ctx context.Context, session identity.Session, input Input, meta identity.RequestMeta) (Record, error) {
	if !session.HasPermission("fixed_asset.edit") {
		return Record{}, identity.ErrForbidden
	}
	normalize(&input)
	if input.Status == "" {
		input.Status = "AVAILABLE"
	}
	if err := validate(input, true); err != nil {
		return Record{}, err
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if input.AssetCode == "" {
		if input.AssetCode, err = codeseq.Next(ctx, tx, session.CurrentCompanyID, "fixed_assets", "asset_code", "SK"); err != nil {
			return Record{}, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO fixed_assets(id,company_id,asset_code,name,category,serial_number,description,status)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)`,
		id, session.CurrentCompanyID, input.AssetCode, input.Name, input.Category, input.SerialNumber, input.Description, input.Status)
	if err != nil {
		return Record{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_CREATED", "fixed_asset.created", id, map[string]any{"asset_code": input.AssetCode}); err != nil {
		return Record{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) Update(ctx context.Context, session identity.Session, id string, version int64, input Input, meta identity.RequestMeta) (Record, error) {
	if !session.HasPermission("fixed_asset.edit") {
		return Record{}, identity.ErrForbidden
	}
	normalize(&input)
	if err := validate(input, false); err != nil {
		return Record{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var hasActive bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM asset_assignments x WHERE x.company_id=f.company_id AND x.asset_id=f.id AND x.returned_at IS NULL)
 FROM fixed_assets f WHERE f.company_id=$1 AND f.id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id).Scan(&hasActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	// A currently-assigned asset keeps its ASSIGNED status; the card cannot be
	// moved to another state until the assignment is returned.
	if hasActive && input.Status != "ASSIGNED" {
		return Record{}, fmt.Errorf("%w: zimmetli sabit kıymetin durumu iade alınmadan değiştirilemez", identity.ErrValidation)
	}
	if !hasActive && input.Status == "ASSIGNED" {
		return Record{}, fmt.Errorf("%w: ASSIGNED durumu yalnızca zimmetleme ile atanır", identity.ErrValidation)
	}

	tag, err := tx.Exec(ctx, `UPDATE fixed_assets SET asset_code=$4,name=$5,category=$6,serial_number=NULLIF($7,''),description=$8,status=$9,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND version=$3`,
		session.CurrentCompanyID, id, version, input.AssetCode, input.Name, input.Category, input.SerialNumber, input.Description, input.Status)
	if err != nil {
		return Record{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Record{}, identity.ErrConflict
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_UPDATED", "fixed_asset.updated", id, map[string]any{"asset_code": input.AssetCode, "status": input.Status}); err != nil {
		return Record{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return s.Get(ctx, session, id)
}

func (s *Service) Assign(ctx context.Context, session identity.Session, assetID string, input AssignInput, meta identity.RequestMeta) (Record, error) {
	if !session.HasPermission("fixed_asset.assign") {
		return Record{}, identity.ErrForbidden
	}
	input.EmployeeID = strings.TrimSpace(input.EmployeeID)
	input.Note = strings.TrimSpace(input.Note)
	assignedAt, err := parseTime(input.AssignedAt)
	if err != nil {
		return Record{}, fmt.Errorf("%w: zimmet tarihi geçersiz", identity.ErrValidation)
	}
	if input.EmployeeID == "" {
		return Record{}, fmt.Errorf("%w: çalışan zorunlu", identity.ErrValidation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var status string
	var version int64
	err = tx.QueryRow(ctx, `SELECT status,version FROM fixed_assets WHERE company_id=$1 AND id=NULLIF($2,'')::uuid FOR UPDATE`, session.CurrentCompanyID, assetID).Scan(&status, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	var employeeExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE company_id=$1 AND id=NULLIF($2,'')::uuid)`, session.CurrentCompanyID, input.EmployeeID).Scan(&employeeExists); err != nil {
		return Record{}, err
	}
	if !employeeExists {
		return Record{}, ErrEmployeeGone
	}

	var hasActive bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM asset_assignments WHERE company_id=$1 AND asset_id=NULLIF($2,'')::uuid AND returned_at IS NULL)`, session.CurrentCompanyID, assetID).Scan(&hasActive); err != nil {
		return Record{}, err
	}
	// Pure domain rule check before we touch the database.
	domainAsset := Asset{ID: assetID, CompanyID: session.CurrentCompanyID, Status: status, Version: version}
	if _, _, derr := Assign(domainAsset, hasActive, Assignment{ID: "x", CompanyID: session.CurrentCompanyID, AssetID: assetID, EmployeeID: input.EmployeeID, AssignedAt: assignedAt}); derr != nil {
		return Record{}, derr
	}

	actorSnapshot, _ := json.Marshal(map[string]string{
		"user_id": session.User.ID, "email": session.User.Email, "display_name": session.User.DisplayName,
	})
	assignmentID := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO asset_assignments(id,company_id,asset_id,employee_id,assigned_at,assignment_note,assigned_by,actor_snapshot)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8)`,
		assignmentID, session.CurrentCompanyID, assetID, input.EmployeeID, assignedAt, input.Note, session.User.ID, actorSnapshot)
	if err != nil {
		return Record{}, mapConstraint(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE fixed_assets SET status='ASSIGNED',updated_at=now(),version=version+1 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, assetID); err != nil {
		return Record{}, err
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_ASSIGNED", "fixed_asset.assigned", assetID, map[string]any{"assignment_id": assignmentID, "employee_id": input.EmployeeID}); err != nil {
		return Record{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return s.Get(ctx, session, assetID)
}

func (s *Service) Return(ctx context.Context, session identity.Session, assetID, assignmentID string, input ReturnInput, meta identity.RequestMeta) (Record, error) {
	if !session.HasPermission("fixed_asset.assign") {
		return Record{}, identity.ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	returnedAt, err := parseTime(input.ReturnedAt)
	if err != nil {
		return Record{}, fmt.Errorf("%w: iade tarihi geçersiz", identity.ErrValidation)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var assignedAt time.Time
	var alreadyReturned bool
	err = tx.QueryRow(ctx, `SELECT assigned_at,returned_at IS NOT NULL FROM asset_assignments
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND asset_id=NULLIF($3,'')::uuid FOR UPDATE`,
		session.CurrentCompanyID, assignmentID, assetID).Scan(&assignedAt, &alreadyReturned)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrAssignNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if _, _, derr := Return(Asset{ID: assetID, CompanyID: session.CurrentCompanyID, Status: "ASSIGNED"},
		Assignment{ID: assignmentID, AssignedAt: assignedAt, ReturnedAt: returnedFlag(alreadyReturned)}, returnedAt); derr != nil {
		return Record{}, derr
	}

	tag, err := tx.Exec(ctx, `UPDATE asset_assignments SET returned_at=$4,return_note=$5,returned_by=$6
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND asset_id=NULLIF($3,'')::uuid AND returned_at IS NULL`,
		session.CurrentCompanyID, assignmentID, assetID, returnedAt, nullString(input.Note), session.User.ID)
	if err != nil {
		return Record{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Record{}, ErrAssignmentReturned
	}
	if _, err = tx.Exec(ctx, `UPDATE fixed_assets SET status='AVAILABLE',updated_at=now(),version=version+1 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND status='ASSIGNED'`, session.CurrentCompanyID, assetID); err != nil {
		return Record{}, err
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_RETURNED", "fixed_asset.returned", assetID, map[string]any{"assignment_id": assignmentID}); err != nil {
		return Record{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Record{}, err
	}
	return s.Get(ctx, session, assetID)
}

func (s *Service) ListAssignments(ctx context.Context, session identity.Session, assetID, employeeID string, limit int) ([]AssignmentRecord, error) {
	if !session.HasPermission("fixed_asset.read") {
		return nil, identity.ErrForbidden
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT act.id::text,act.asset_id::text,a.asset_code,a.name,act.employee_id::text,e.employee_code,e.first_name||' '||e.last_name,
 act.assigned_at,act.returned_at,act.assignment_note,act.return_note
 FROM asset_assignments act
 JOIN fixed_assets a ON a.company_id=act.company_id AND a.id=act.asset_id
 JOIN employees e ON e.company_id=act.company_id AND e.id=act.employee_id
 WHERE act.company_id=$1 AND ($2='' OR act.asset_id=NULLIF($2,'')::uuid) AND ($3='' OR act.employee_id=NULLIF($3,'')::uuid)
 ORDER BY act.assigned_at DESC,act.id DESC LIMIT $4`,
		session.CurrentCompanyID, strings.TrimSpace(assetID), strings.TrimSpace(employeeID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AssignmentRecord{}
	for rows.Next() {
		var a AssignmentRecord
		if err := rows.Scan(&a.ID, &a.AssetID, &a.AssetCode, &a.AssetName, &a.EmployeeID, &a.EmployeeCode, &a.EmployeeName,
			&a.AssignedAt, &a.ReturnedAt, &a.AssignmentNote, &a.ReturnNote); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (Record, error) {
	var r Record
	var assignmentID, employeeID, employeeCode, employeeName *string
	var assignedAt *time.Time
	if err := row.Scan(&r.ID, &r.AssetCode, &r.Name, &r.Category, &r.SerialNumber, &r.Description, &r.Status,
		&assignmentID, &employeeID, &employeeCode, &employeeName, &assignedAt,
		&r.CreatedAt, &r.UpdatedAt, &r.Version); err != nil {
		return Record{}, err
	}
	if assignmentID != nil && employeeID != nil && assignedAt != nil {
		r.AssignedTo = &AssignedTo{
			AssignmentID: *assignmentID, EmployeeID: *employeeID,
			EmployeeCode: derefString(employeeCode), EmployeeName: derefString(employeeName), AssignedAt: *assignedAt,
		}
	}
	return r, nil
}

func normalize(input *Input) {
	input.AssetCode = strings.TrimSpace(input.AssetCode)
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.TrimSpace(input.Category)
	input.SerialNumber = strings.TrimSpace(input.SerialNumber)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.ToUpper(strings.TrimSpace(input.Status))
}

func validate(input Input, isCreate bool) error {
	// On create the asset code may be blank; the service generates one.
	if (!isCreate && input.AssetCode == "") || input.Name == "" || input.Category == "" {
		return fmt.Errorf("%w: sabit kıymet kartı alanları geçersiz", identity.ErrValidation)
	}
	if isCreate {
		if !editableStatuses[input.Status] {
			return fmt.Errorf("%w: geçersiz sabit kıymet durumu", identity.ErrValidation)
		}
		return nil
	}
	if input.Status != "ASSIGNED" && !editableStatuses[input.Status] {
		return fmt.Errorf("%w: geçersiz sabit kıymet durumu", identity.ErrValidation)
	}
	return nil
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "asset_code"):
		return fmt.Errorf("%w: sabit kıymet kodu zaten kullanımda", identity.ErrValidation)
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "serial"):
		return fmt.Errorf("%w: seri numarası zaten kullanımda", identity.ErrValidation)
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "one_active"):
		return ErrActiveAssignmentExists
	case pgErr.Code == "55000":
		return ErrAssignmentReturned
	case pgErr.Code == "23503":
		return ErrEmployeeGone
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, assetID string, details map[string]any) error {
	detailBytes, _ := json.Marshal(details)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'fixed_asset',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, assetID, detailBytes, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"asset_id": assetID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errors.New("invalid time")
}

func returnedFlag(alreadyReturned bool) *time.Time {
	if !alreadyReturned {
		return nil
	}
	t := time.Unix(0, 0)
	return &t
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func encodeCursor(code, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(code + "\x00" + id))
}

func decodeCursor(cursor string) (string, string) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ""
	}
	parts := strings.SplitN(string(raw), "\x00", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
