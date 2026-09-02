package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type setupVariantOption struct {
	code, name, shortCode string
	position              int
}

type setupVariantDefinition struct {
	code, name string
	position   int
	options    map[string]setupVariantOption
}

// seedVariantPackages copies the selected immutable package catalog into the
// new company. It intentionally runs inside the serializable setup transaction
// so a failed package selection cannot leave a partially seeded company.
func seedVariantPackages(ctx context.Context, tx pgx.Tx, companyID string, requested []string) error {
	packages := make([]string, 0, len(requested))
	seenPackages := map[string]bool{}
	for _, raw := range requested {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" || seenPackages[code] {
			continue
		}
		seenPackages[code] = true
		packages = append(packages, code)
	}
	if len(packages) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT d.definition_code,d.definition_name,d.position,o.option_code,o.option_name,o.short_code,o.position
		FROM variant_definition_package_definitions d
		JOIN variant_definition_package_options o ON o.package_code=d.package_code AND o.definition_code=d.definition_code
		WHERE d.package_code = ANY($1::text[])
		ORDER BY d.definition_code,d.position,o.position,o.option_code`, packages)
	if err != nil {
		return err
	}
	defs := map[string]*setupVariantDefinition{}
	for rows.Next() {
		var dc, dn, oc, on, sc string
		var dp, op int
		if err = rows.Scan(&dc, &dn, &dp, &oc, &on, &sc, &op); err != nil {
			rows.Close()
			return err
		}
		def := defs[dc]
		if def == nil {
			def = &setupVariantDefinition{code: dc, name: dn, position: dp, options: map[string]setupVariantOption{}}
			defs[dc] = def
		} else if def.name != dn {
			rows.Close()
			return fmt.Errorf("%w: varyant tanımı çakışması: %s", ErrValidation, dc)
		}
		if old, ok := def.options[oc]; ok && (old.name != on || old.shortCode != sc) {
			rows.Close()
			return fmt.Errorf("%w: varyant seçeneği çakışması: %s/%s", ErrValidation, dc, oc)
		}
		def.options[oc] = setupVariantOption{code: oc, name: on, shortCode: sc, position: op}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(defs) == 0 {
		return fmt.Errorf("%w: seçilen varyant paketi bulunamadı", ErrValidation)
	}
	for _, def := range defs {
		definitionID := newUUID()
		if _, err = tx.Exec(ctx, `INSERT INTO variant_definitions(id,company_id,code,name) VALUES($1,$2,$3,$4)`, definitionID, companyID, def.code, def.name); err != nil {
			return err
		}
		for _, option := range def.options {
			if _, err = tx.Exec(ctx, `INSERT INTO variant_definition_options(id,company_id,definition_id,code,name,short_code,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, newUUID(), companyID, definitionID, option.code, option.name, option.shortCode, option.position); err != nil {
				return err
			}
		}
	}
	return nil
}

func newUUID() string {
	return uuid.NewString()
}
