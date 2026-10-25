package crm

import (
	"context"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// maxImportRows bounds one lead import. A list larger than this is better
// split: the whole batch is validated and written in one transaction.
const maxImportRows = 5000

// importFields are the lead fields a spreadsheet column can be mapped to.
var importFields = []string{"display_name", "contact_name", "contact_title", "phone", "phone2", "email", "city", "district",
	"address", "website", "sector", "source", "priority", "status", "notes"}

// headerHints maps normalised column headings people commonly use to a field.
var headerHints = map[string]string{
	"firma": "display_name", "firmaadi": "display_name", "firmaunvani": "display_name", "unvan": "display_name",
	"sirket": "display_name", "sirketadi": "display_name", "ad": "display_name", "adi": "display_name",
	"musteri": "display_name", "musteriadi": "display_name", "adsoyad": "display_name", "isim": "display_name", "name": "display_name", "company": "display_name",
	"yetkili": "contact_name", "yetkiliadi": "contact_name", "ilgilikisi": "contact_name", "kisi": "contact_name", "contact": "contact_name",
	"gorev": "contact_title", "pozisyon": "contact_title", "unvani": "contact_title", "title": "contact_title",
	"telefon": "phone", "tel": "phone", "telefonno": "phone", "gsm": "phone", "cep": "phone", "ceptelefonu": "phone", "phone": "phone", "mobile": "phone",
	"telefon1": "phone",
	"telefon2": "phone2", "tel2": "phone2", "ikincitelefon": "phone2", "sabittelefon": "phone2",
	"yetkilidanisman": "contact_name", "danisman": "contact_name",
	"eposta": "email", "email": "email", "mail": "email", "epostaadresi": "email",
	"il": "city", "sehir": "city", "city": "city",
	"ilce": "district", "district": "district", "bolge": "district",
	"sektor": "sector", "sector": "sector", "faaliyetalani": "sector",
	"kaynak": "source", "source": "source",
	"not": "notes", "notlar": "notes", "aciklama": "notes", "notes": "notes",
	"adres": "address", "address": "address", "acikadres": "address",
	"websitesi": "website", "web": "website", "website": "website", "site": "website", "internetsitesi": "website",
	"oncelik": "priority", "priority": "priority", "segment": "priority",
	"status": "status", "aramadurumu": "status", "aramasonucu": "status",
}

// importStatuses turns the status words people write in their own lists into
// a lead status; "aranmasın" also sets the do-not-call mark. Anything else is
// refused for that row so a typo never silently re-opens a lead.
var importStatuses = map[string]string{
	"": StatusNew, "yeni": StatusNew, "new": StatusNew, "aranmadi": StatusNew, "ulasilamadi": StatusNew,
	"gorusuldu": StatusContacted, "contacted": StatusContacted, "sonraaranacak": StatusContacted,
	"ilgili": StatusQualified, "ilgileniyor": StatusQualified, "qualified": StatusQualified, "gorusulduilgili": StatusQualified,
	"elendi": StatusDisqualified, "ilgisiz": StatusDisqualified, "ilgilenmiyor": StatusDisqualified,
	"disqualified": StatusDisqualified, "gorusulduilgisiz": StatusDisqualified,
	"aranmasin": "DO_NOT_CALL",
}

// ImportPreview is a parsed file: its headers, every row, and a suggested
// header→field mapping (index into Headers, -1 when unmapped).
type ImportPreview struct {
	Sheets  []ImportSheet  `json:"sheets"`
	Sheet   string         `json:"sheet"`
	Headers []string       `json:"headers"`
	Rows    [][]string     `json:"rows"`
	Mapping map[string]int `json:"mapping"`
	Total   int            `json:"total"`
}

type ImportSheet struct {
	Name string `json:"name"`
	Rows int    `json:"rows"`
}

type ImportRow struct {
	Row          int    `json:"row"`
	DisplayName  string `json:"display_name"`
	ContactName  string `json:"contact_name"`
	ContactTitle string `json:"contact_title"`
	Phone        string `json:"phone"`
	Phone2       string `json:"phone2"`
	Email        string `json:"email"`
	City         string `json:"city"`
	District     string `json:"district"`
	Sector       string `json:"sector"`
	Source       string `json:"source"`
	Notes        string `json:"notes"`
	Address      string `json:"address"`
	Website      string `json:"website"`
	Priority     string `json:"priority"`
	Status       string `json:"status"`
}

type ImportInput struct {
	Rows        []ImportRow `json:"rows"`
	OwnerUserID string      `json:"owner_user_id"`
	Source      string      `json:"source"`
	CallListID  string      `json:"call_list_id"`
	// AllowDuplicates imports rows whose phone already belongs to a lead.
	// Numbers marked do-not-call are never imported again.
	AllowDuplicates bool `json:"allow_duplicates"`
}

type ImportIssue struct {
	Row     int    `json:"row"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type ImportResult struct {
	Created     int           `json:"created"`
	AddedToList int           `json:"added_to_list"`
	Duplicates  []ImportIssue `json:"duplicates"`
	DoNotCall   []ImportIssue `json:"do_not_call"`
	Invalid     []ImportIssue `json:"invalid"`
}

// PreviewImport parses a CSV or XLSX file and suggests a column mapping. A
// workbook's sheets are all listed; sheetName picks one, otherwise the first
// sheet that looks like a lead list (a name column and a phone or e-mail
// column) is used, so a summary sheet in front of the data is skipped.
func (s *Service) PreviewImport(session identity.Session, file io.Reader, filename, sheetName string) (ImportPreview, error) {
	if !session.HasPermission("crm.edit") {
		return ImportPreview{}, identity.ErrForbidden
	}
	sheets, err := dataexchange.ReadWorkbook(file, filename)
	if err != nil {
		return ImportPreview{}, errInvalid("dosya okunamadı; CSV ya da XLSX yükleyin")
	}
	preview := ImportPreview{Sheets: make([]ImportSheet, 0, len(sheets))}
	chosen := -1
	for index, sheet := range sheets {
		preview.Sheets = append(preview.Sheets, ImportSheet{Name: sheet.Name, Rows: len(sheet.Table.Rows)})
		if sheetName != "" && sheet.Name == sheetName {
			chosen = index
		}
	}
	if sheetName != "" && chosen < 0 {
		return ImportPreview{}, errInvalid("dosyada bu adda bir sayfa yok")
	}
	if chosen < 0 {
		for index, sheet := range sheets {
			mapping := suggestMapping(sheet.Table.Headers)
			if mapping["display_name"] >= 0 && (mapping["phone"] >= 0 || mapping["email"] >= 0) && len(sheet.Table.Rows) > 0 {
				chosen = index
				break
			}
		}
	}
	if chosen < 0 {
		chosen = 0
	}
	table := sheets[chosen].Table
	if len(table.Rows) > maxImportRows {
		return ImportPreview{}, errInvalid("bir sayfada en fazla 5000 satır olabilir")
	}
	preview.Sheet = sheets[chosen].Name
	preview.Headers = table.Headers
	preview.Total = len(table.Rows)
	preview.Rows = make([][]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		preview.Rows = append(preview.Rows, row.Values)
	}
	preview.Mapping = suggestMapping(table.Headers)
	return preview, nil
}

func suggestMapping(headers []string) map[string]int {
	mapping := make(map[string]int, len(importFields))
	for _, field := range importFields {
		mapping[field] = -1
	}
	for index, header := range headers {
		if field, ok := headerHints[headerKey(header)]; ok && mapping[field] == -1 {
			mapping[field] = index
		}
	}
	return mapping
}

// headerKey folds a heading to lower-case ASCII letters and digits, so
// "E-Posta Adresi" and "eposta adresi" match the same hint.
func headerKey(header string) string {
	replacer := strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "İ", "i", "ö", "o", "ş", "s", "ü", "u", "Ç", "c", "Ğ", "g", "Ö", "o", "Ş", "s", "Ü", "u", "I", "i")
	folded := strings.ToLower(replacer.Replace(header))
	var b strings.Builder
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ImportLeads validates every row, drops numbers already known (and always
// those marked do-not-call), and creates the rest in one transaction,
// optionally queueing them on a call list.
func (s *Service) ImportLeads(ctx context.Context, session identity.Session, input ImportInput, meta identity.RequestMeta) (ImportResult, error) {
	if !session.HasPermission("crm.edit") {
		return ImportResult{}, identity.ErrForbidden
	}
	if len(input.Rows) == 0 {
		return ImportResult{}, errInvalid("aktarılacak satır yok")
	}
	if len(input.Rows) > maxImportRows {
		return ImportResult{}, errInvalid("bir seferde en fazla 5000 satır aktarılabilir")
	}
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.Source = strings.TrimSpace(input.Source)
	input.CallListID = strings.TrimSpace(input.CallListID)
	if input.OwnerUserID == "" && !session.HasPermission("crm.manage") {
		input.OwnerUserID = session.User.ID
	}
	if err := checkOwnerChange(session, nil, input.OwnerUserID); err != nil {
		return ImportResult{}, err
	}
	if input.OwnerUserID != "" {
		if _, err := uuid.Parse(input.OwnerUserID); err != nil {
			return ImportResult{}, ErrOwnerNotMember
		}
	}
	if input.CallListID != "" && !session.HasPermission("crm.manage") {
		return ImportResult{}, identity.ErrForbidden
	}

	result := ImportResult{Duplicates: []ImportIssue{}, DoNotCall: []ImportIssue{}, Invalid: []ImportIssue{}}
	type candidate struct {
		row        ImportRow
		input      LeadInput
		normalized string
		doNotCall  bool
	}
	candidates := make([]candidate, 0, len(input.Rows))
	seen := map[string]int{}
	phones := []string{}
	for _, row := range input.Rows {
		status, known := importStatuses[headerKey(row.Status)]
		if !known {
			result.Invalid = append(result.Invalid, ImportIssue{Row: row.Row, Name: strings.TrimSpace(row.DisplayName),
				Message: "durum anlaşılamadı: " + strings.TrimSpace(row.Status) + " (Yeni, Görüşüldü, İlgili, Elendi ya da Aranmasın yazın)"})
			continue
		}
		doNotCall := status == "DO_NOT_CALL"
		if doNotCall {
			status = StatusDisqualified
		}
		lead := LeadInput{
			DisplayName: row.DisplayName, ContactName: row.ContactName, ContactTitle: row.ContactTitle,
			Phone: row.Phone, Phone2: row.Phone2, Email: row.Email, City: row.City, District: row.District,
			Sector: row.Sector, Source: row.Source, Notes: row.Notes, Status: status, OwnerUserID: input.OwnerUserID,
			Address: row.Address, Website: row.Website, Priority: row.Priority,
		}
		normalizeLead(&lead)
		// "A - Çok sıcak" or "a2" both mean A.
		if lead.Priority != "" {
			lead.Priority = strings.ToUpper(string([]rune(lead.Priority)[0]))
		}
		if lead.Source == "" {
			lead.Source = input.Source
		}
		if err := validateLead(lead); err != nil {
			result.Invalid = append(result.Invalid, ImportIssue{Row: row.Row, Name: lead.DisplayName, Message: strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")})
			continue
		}
		normalized := NormalizePhone(lead.Phone)
		if normalized != "" {
			if first, ok := seen[normalized]; ok {
				result.Duplicates = append(result.Duplicates, ImportIssue{Row: row.Row, Name: lead.DisplayName, Message: "aynı numara dosyada " + strconv.Itoa(first) + ". satırda da var"})
				continue
			}
			seen[normalized] = row.Row
			phones = append(phones, normalized)
		}
		candidates = append(candidates, candidate{row: row, input: lead, normalized: normalized, doNotCall: doNotCall})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var listID string
	if input.CallListID != "" {
		list, err := s.getCallList(ctx, tx, session.CurrentCompanyID, input.CallListID)
		if err != nil {
			return ImportResult{}, err
		}
		listID = list.ID
		if _, err = tx.Exec(ctx, `SELECT 1 FROM crm_call_lists WHERE company_id=$1 AND id=$2::uuid FOR UPDATE`, session.CurrentCompanyID, listID); err != nil {
			return ImportResult{}, err
		}
	}

	known := map[string]bool{} // normalised phone → do-not-call
	if len(phones) > 0 {
		rows, err := tx.Query(ctx, `SELECT phone_normalized,bool_or(do_not_call) FROM crm_leads
 WHERE company_id=$1 AND phone_normalized=ANY($2::text[]) GROUP BY phone_normalized`, session.CurrentCompanyID, phones)
		if err != nil {
			return ImportResult{}, err
		}
		for rows.Next() {
			var phone string
			var dnc bool
			if err := rows.Scan(&phone, &dnc); err != nil {
				rows.Close()
				return ImportResult{}, err
			}
			known[phone] = dnc
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return ImportResult{}, err
		}
	}

	var position int64
	if listID != "" {
		if err = tx.QueryRow(ctx, `SELECT COALESCE(max(position),0) FROM crm_call_list_items WHERE company_id=$1 AND list_id=$2::uuid`,
			session.CurrentCompanyID, listID).Scan(&position); err != nil {
			return ImportResult{}, err
		}
	}
	for _, c := range candidates {
		if dnc, ok := known[c.normalized]; ok {
			if dnc {
				result.DoNotCall = append(result.DoNotCall, ImportIssue{Row: c.row.Row, Name: c.input.DisplayName, Message: "bu numara aranmak istemiyor"})
				continue
			}
			if !input.AllowDuplicates {
				result.Duplicates = append(result.Duplicates, ImportIssue{Row: c.row.Row, Name: c.input.DisplayName, Message: "bu numara başka bir adayda kayıtlı"})
				continue
			}
		}
		id := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO crm_leads(id,company_id,display_name,contact_name,contact_title,phone,phone_normalized,phone2,
 email,city,district,sector,source,notes,status,owner_user_id,created_by,address,website,priority,
 do_not_call,do_not_call_at,do_not_call_reason)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16,'')::uuid,$17,$18,$19,$20,
 $21,CASE WHEN $21 THEN now() END,CASE WHEN $21 THEN 'İçe aktarılan listede aranmasın işaretliydi' ELSE '' END)`,
			id, session.CurrentCompanyID, c.input.DisplayName, c.input.ContactName, c.input.ContactTitle, c.input.Phone, c.normalized,
			c.input.Phone2, c.input.Email, c.input.City, c.input.District, c.input.Sector, c.input.Source, c.input.Notes,
			c.input.Status, input.OwnerUserID, session.User.ID, c.input.Address, c.input.Website, c.input.Priority,
			c.doNotCall); err != nil {
			return ImportResult{}, mapConstraint(err)
		}
		result.Created++
		// Only leads still worth calling go on the list.
		if listID != "" && !c.doNotCall && c.input.Status != StatusDisqualified {
			position++
			if _, err = tx.Exec(ctx, `INSERT INTO crm_call_list_items(id,company_id,list_id,lead_id,position) VALUES($1,$2,$3::uuid,$4::uuid,$5)`,
				uuid.NewString(), session.CurrentCompanyID, listID, id, position); err != nil {
				return ImportResult{}, mapConstraint(err)
			}
			result.AddedToList++
		}
	}
	if err = writeAudit(ctx, tx, session, meta, "CRM_LEADS_IMPORTED", "crm_lead", "", map[string]any{
		"created": result.Created, "duplicates": len(result.Duplicates), "do_not_call": len(result.DoNotCall), "invalid": len(result.Invalid),
	}); err != nil {
		return ImportResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}
