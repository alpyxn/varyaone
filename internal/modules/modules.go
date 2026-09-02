// Package modules is the registry for Varya One's activatable feature modules.
//
// A module groups a set of domain packages, HTTP routes and navigation entries
// that can be switched on or off per company at runtime (see the
// company_modules table, migration 000133). Core capabilities - identity,
// settings, dashboard, currency rates, e-mail - are never modules; they are
// always available.
package modules

// Code identifies a module. Values are stable and stored verbatim in the
// database, so they must never change once shipped.
type Code = string

const (
	// HR covers employees, leave, schedule, timesheet, advances and payroll.
	HR Code = "hr"
	// PreAccounting covers cari, stock, sales, purchasing, finance and reports.
	PreAccounting Code = "preaccounting"
	// FixedAsset covers the fixed-asset register and assignments.
	FixedAsset Code = "fixed_asset"
)

// Definition is a catalog entry describing a module to operators.
type Definition struct {
	Code        Code   `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Catalog is the ordered list of every module the build knows about. Adding a
// module here plus a company_modules backfill migration is all that is needed
// to make it selectable.
var Catalog = []Definition{
	{Code: PreAccounting, Name: "Ön Muhasebe", Description: "Cari, stok, satış, alış, banka & kasa ve raporlar."},
	{Code: HR, Name: "İnsan Kaynakları", Description: "Çalışanlar, izin, çalışma planı, puantaj, avanslar ve bordro."},
	{Code: FixedAsset, Name: "Sabit Kıymetler", Description: "Sabit kıymet kartları, kategoriler ve zimmet takibi."},
}

// Valid reports whether code names a known module.
func Valid(code string) bool {
	for _, definition := range Catalog {
		if definition.Code == code {
			return true
		}
	}
	return false
}

// Codes returns every module code in catalog order.
func Codes() []string {
	codes := make([]string, len(Catalog))
	for i, definition := range Catalog {
		codes[i] = definition.Code
	}
	return codes
}
