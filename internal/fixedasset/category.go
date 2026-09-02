package fixedasset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrCategoryNotFound = errors.New("FIXED_ASSET_CATEGORY_NOT_FOUND")

type Category struct {
	ID          string     `json:"id"`
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsSystem    bool       `json:"is_system"`
	IsActive    bool       `json:"is_active"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Version     int64      `json:"version"`
}

type CategoryInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Service) ListCategories(ctx context.Context, session identity.Session, includeArchived bool) ([]Category, error) {
	if !session.HasPermission("fixed_asset.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,name,description,is_system,archived_at,created_at,version
 FROM fixed_asset_categories WHERE company_id=$1 AND ($2 OR archived_at IS NULL)
 ORDER BY archived_at IS NOT NULL,name`, session.CurrentCompanyID, includeArchived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Code, &c.Name, &c.Description, &c.IsSystem, &c.ArchivedAt, &c.CreatedAt, &c.Version); err != nil {
			return nil, err
		}
		c.IsActive = c.ArchivedAt == nil
		items = append(items, c)
	}
	return items, rows.Err()
}

func normalizeCategory(input *CategoryInput) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
}

func (s *Service) CreateCategory(ctx context.Context, session identity.Session, input CategoryInput, meta identity.RequestMeta) (Category, error) {
	if !session.HasPermission("fixed_asset.edit") {
		return Category{}, identity.ErrForbidden
	}
	normalizeCategory(&input)
	if input.Name == "" {
		return Category{}, fmt.Errorf("%w: kategori adı zorunlu", identity.ErrValidation)
	}
	if input.Code == "" {
		input.Code = slugCode(input.Name)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Category{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO fixed_asset_categories(id,company_id,code,name,description,is_system)
 VALUES($1,$2,$3,$4,$5,false)`, id, session.CurrentCompanyID, input.Code, input.Name, input.Description)
	if err != nil {
		return Category{}, mapCategoryConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_CATEGORY_CREATED", "fixed_asset_category.created", id, map[string]any{"code": input.Code}); err != nil {
		return Category{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Category{}, err
	}
	return s.getCategory(ctx, session.CurrentCompanyID, id)
}

func (s *Service) UpdateCategory(ctx context.Context, session identity.Session, id string, version int64, input CategoryInput, meta identity.RequestMeta) (Category, error) {
	if !session.HasPermission("fixed_asset.edit") {
		return Category{}, identity.ErrForbidden
	}
	normalizeCategory(&input)
	if input.Name == "" {
		return Category{}, fmt.Errorf("%w: kategori adı zorunlu", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Category{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// System categories may be renamed but their code is immutable.
	tag, err := tx.Exec(ctx, `UPDATE fixed_asset_categories
 SET name=$4,description=$5,code=CASE WHEN is_system OR $6='' THEN code ELSE $6 END,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND version=$3`,
		session.CurrentCompanyID, id, version, input.Name, input.Description, input.Code)
	if err != nil {
		return Category{}, mapCategoryConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		if _, gErr := s.getCategory(ctx, session.CurrentCompanyID, id); errors.Is(gErr, ErrCategoryNotFound) {
			return Category{}, ErrCategoryNotFound
		}
		return Category{}, identity.ErrConflict
	}
	if err = writeEvent(ctx, tx, session, meta, "FIXED_ASSET_CATEGORY_UPDATED", "fixed_asset_category.updated", id, map[string]any{"name": input.Name}); err != nil {
		return Category{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Category{}, err
	}
	return s.getCategory(ctx, session.CurrentCompanyID, id)
}

// SetCategoryActive toggles a category between active and passive. Passive
// categories are simply no longer offered in pickers; existing asset cards keep
// their free-text category value.
func (s *Service) SetCategoryActive(ctx context.Context, session identity.Session, id string, active bool, meta identity.RequestMeta) (Category, error) {
	if !session.HasPermission("fixed_asset.edit") {
		return Category{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Category{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE fixed_asset_categories
 SET archived_at=CASE WHEN $3 THEN NULL ELSE now() END,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, id, active)
	if err != nil {
		return Category{}, err
	}
	if tag.RowsAffected() == 0 {
		return Category{}, ErrCategoryNotFound
	}
	event := "FIXED_ASSET_CATEGORY_DEACTIVATED"
	if active {
		event = "FIXED_ASSET_CATEGORY_ACTIVATED"
	}
	if err = writeEvent(ctx, tx, session, meta, event, "fixed_asset_category.status_changed", id, map[string]any{"active": active}); err != nil {
		return Category{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Category{}, err
	}
	return s.getCategory(ctx, session.CurrentCompanyID, id)
}

func (s *Service) getCategory(ctx context.Context, companyID, id string) (Category, error) {
	var c Category
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,description,is_system,archived_at,created_at,version
 FROM fixed_asset_categories WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, companyID, id).
		Scan(&c.ID, &c.Code, &c.Name, &c.Description, &c.IsSystem, &c.ArchivedAt, &c.CreatedAt, &c.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	c.IsActive = c.ArchivedAt == nil
	return c, err
}

func mapCategoryConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: bu kategori kodu zaten kullanımda", identity.ErrValidation)
	}
	return err
}

func slugCode(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '/':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "KATEGORI_" + uuid.NewString()[:8]
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
