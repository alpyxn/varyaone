// Package exchange owns company-scoped exchange-rate retrieval and exposes a
// small resolver for document services. It deliberately stores the fetched
// source and date so a document can be audited without contacting a provider.
package exchange

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

const (
	SourceAuto = "AUTO"
	SourceTCMB = "TCMB"
	SourceECB  = "ECB"

	tcmbURL = "https://www.tcmb.gov.tr/kurlar/today.xml"
	ecbURL  = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
)

var (
	ErrRateUnavailable   = errors.New("exchange rate unavailable")
	ErrInvalidSource     = errors.New("invalid exchange-rate source")
	errRefreshInProgress = errors.New("exchange rate refresh in progress")
)

type Service struct {
	pool   database.Querier
	clock  func() time.Time
	client *http.Client
}

type Settings struct {
	CompanyID            string     `json:"company_id"`
	SourcePreference     string     `json:"source_preference"`
	RefreshIntervalHours int        `json:"refresh_interval_hours"`
	LastAttemptAt        *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt        *time.Time `json:"last_success_at,omitempty"`
	LastRateDate         *string    `json:"last_rate_date,omitempty"`
	LastSource           string     `json:"last_source,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	Version              int64      `json:"version"`
}

type Rate struct {
	CompanyID    string    `json:"company_id"`
	CurrencyCode string    `json:"currency_code"`
	BaseCurrency string    `json:"base_currency"`
	RateToBase   string    `json:"rate_to_base"`
	RateDate     string    `json:"rate_date"`
	Source       string    `json:"source"`
	SourceURL    string    `json:"source_url"`
	FetchedAt    time.Time `json:"fetched_at"`
}

type Dashboard struct {
	BaseCurrency string   `json:"base_currency"`
	Settings     Settings `json:"settings"`
	Items        []Rate   `json:"items"`
}

type SettingsInput struct {
	SourcePreference     string `json:"source_preference"`
	RefreshIntervalHours int    `json:"refresh_interval_hours"`
}

func NewService(pool database.Querier) *Service {
	return &Service{
		pool:   pool,
		clock:  time.Now,
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func (s *Service) GetDashboard(ctx context.Context, session identity.Session) (Dashboard, error) {
	if !canRead(session) {
		return Dashboard{}, identity.ErrForbidden
	}
	refreshErr := s.refreshIfDue(ctx, session.CurrentCompanyID)
	result, err := s.dashboard(ctx, session.CurrentCompanyID)
	if err != nil {
		return Dashboard{}, err
	}
	if refreshErr != nil && !errors.Is(refreshErr, errRefreshInProgress) && len(result.Items) == 0 {
		return Dashboard{}, refreshErr
	}
	return result, nil
}

func (s *Service) UpdateSettings(ctx context.Context, session identity.Session, input SettingsInput) (Settings, error) {
	if !canManage(session) {
		return Settings{}, identity.ErrForbidden
	}
	input.SourcePreference = strings.ToUpper(strings.TrimSpace(input.SourcePreference))
	if input.SourcePreference == "" {
		input.SourcePreference = SourceAuto
	}
	if !validSource(input.SourcePreference) {
		return Settings{}, fmt.Errorf("%w: kur kaynağı geçersiz", identity.ErrValidation)
	}
	if input.RefreshIntervalHours == 0 {
		input.RefreshIntervalHours = 6
	}
	if input.RefreshIntervalHours < 1 || input.RefreshIntervalHours > 72 {
		return Settings{}, fmt.Errorf("%w: yenileme aralığı 1 ile 72 saat arasında olmalıdır", identity.ErrValidation)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO exchange_rate_settings(company_id,source_preference,refresh_interval_hours)
		VALUES($1,$2,$3)
		ON CONFLICT (company_id) DO UPDATE SET
		 source_preference=excluded.source_preference,
		 refresh_interval_hours=excluded.refresh_interval_hours,
		 last_attempt_at=CASE WHEN exchange_rate_settings.source_preference <> excluded.source_preference OR exchange_rate_settings.refresh_interval_hours <> excluded.refresh_interval_hours THEN NULL ELSE exchange_rate_settings.last_attempt_at END,
		 last_success_at=CASE WHEN exchange_rate_settings.source_preference <> excluded.source_preference OR exchange_rate_settings.refresh_interval_hours <> excluded.refresh_interval_hours THEN NULL ELSE exchange_rate_settings.last_success_at END,
		 last_error='',
		 updated_at=now(), version=exchange_rate_settings.version+1`,
		session.CurrentCompanyID, input.SourcePreference, input.RefreshIntervalHours)
	if err != nil {
		return Settings{}, err
	}
	return s.settings(ctx, session.CurrentCompanyID)
}

func (s *Service) RefreshNow(ctx context.Context, session identity.Session) (Dashboard, error) {
	if !canManage(session) {
		return Dashboard{}, identity.ErrForbidden
	}
	if err := s.refreshCompany(ctx, session.CurrentCompanyID, true); err != nil && !errors.Is(err, errRefreshInProgress) {
		return Dashboard{}, err
	}
	return s.dashboard(ctx, session.CurrentCompanyID)
}

// ResolveRate returns the authoritative current rate. A missing or stale rate
// triggers an immediate provider fetch, which keeps first-use documents safe
// without relying on the worker having already run.
func (s *Service) ResolveRate(ctx context.Context, companyID, currencyCode string, on time.Time) (string, error) {
	currencyCode = strings.ToUpper(strings.TrimSpace(currencyCode))
	if companyID == "" || currencyCode == "" {
		return "", fmt.Errorf("%w: firma ve para birimi gereklidir", identity.ErrValidation)
	}
	base, err := s.companyBaseCurrency(ctx, companyID)
	if err != nil {
		return "", err
	}
	if currencyCode == base {
		return "1", nil
	}
	refreshErr := s.refreshIfDue(ctx, companyID)
	if refreshErr != nil && !errors.Is(refreshErr, errRefreshInProgress) {
		// A provider outage must not silently turn 400 TRY into 400 USD. Fail
		// closed for new documents; cached values remain visible in settings.
		return "", refreshErr
	}
	var rate string
	err = s.pool.QueryRow(ctx, `
		SELECT rate_to_base::text
		  FROM exchange_rates
		 WHERE company_id=$1 AND currency_code=$2 AND rate_date <= $3::date
		 ORDER BY rate_date DESC, fetched_at DESC
		 LIMIT 1`, companyID, currencyCode, dateOnly(on)).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		if errors.Is(refreshErr, errRefreshInProgress) {
			if rate, waitErr := s.waitForRate(ctx, companyID, currencyCode, on); waitErr == nil {
				return documentRate(rate)
			}
		}
		return "", fmt.Errorf("%w: %s kuru bulunamadı", ErrRateUnavailable, currencyCode)
	}
	if err != nil {
		return "", err
	}
	return documentRate(rate)
}

// RefreshDue is called by the worker. It intentionally continues after a
// single company/provider failure; the next cycle can retry without blocking
// outbox processing for every company.
func (s *Service) RefreshDue(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM companies WHERE is_active ORDER BY id`)
	if err != nil {
		return err
	}
	companyIDs := make([]string, 0)
	for rows.Next() {
		var companyID string
		if err := rows.Scan(&companyID); err != nil {
			rows.Close()
			return err
		}
		companyIDs = append(companyIDs, companyID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	// Drain the company rows before running per-company refreshes so a
	// request-pinned connection is never asked to serve a nested query.
	var firstErr error
	for _, companyID := range companyIDs {
		if err := s.refreshIfDue(ctx, companyID); err != nil && !errors.Is(err, errRefreshInProgress) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Service) dashboard(ctx context.Context, companyID string) (Dashboard, error) {
	base, err := s.companyBaseCurrency(ctx, companyID)
	if err != nil {
		return Dashboard{}, err
	}
	settings, err := s.settings(ctx, companyID)
	if err != nil {
		return Dashboard{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT company_id::text,currency_code,$2,rate_to_base::text,rate_date::text,
		       source_code,source_url,fetched_at
		  FROM (
			SELECT DISTINCT ON (currency_code)
			       company_id,currency_code,rate_to_base,rate_date,source_code,source_url,fetched_at
			  FROM exchange_rates
			 WHERE company_id=$1
			 ORDER BY currency_code,rate_date DESC,fetched_at DESC,source_code
		  ) latest
		 ORDER BY currency_code`, companyID, base)
	if err != nil {
		return Dashboard{}, err
	}
	defer rows.Close()
	items := make([]Rate, 0)
	for rows.Next() {
		var item Rate
		if err := rows.Scan(&item.CompanyID, &item.CurrencyCode, &item.BaseCurrency, &item.RateToBase, &item.RateDate, &item.Source, &item.SourceURL, &item.FetchedAt); err != nil {
			return Dashboard{}, err
		}
		// rate_to_base is numeric(38,18); handing eighteen fraction digits to a
		// screen that prefills a tahsilat/ödeme rate field would post a value
		// the money parser rejects ("kur geçersiz"). Publish the same canonical
		// form document posting uses.
		if canonical, rateErr := documentRate(item.RateToBase); rateErr == nil {
			item.RateToBase = canonical
		}
		items = append(items, item)
	}
	return Dashboard{BaseCurrency: base, Settings: settings, Items: items}, rows.Err()
}

func (s *Service) settings(ctx context.Context, companyID string) (Settings, error) {
	var result Settings
	err := s.pool.QueryRow(ctx, `
		SELECT company_id::text,source_preference,refresh_interval_hours,last_attempt_at,
		       last_success_at,last_rate_date::text,COALESCE(last_source,''),last_error,version
		  FROM exchange_rate_settings WHERE company_id=$1`, companyID).
		Scan(&result.CompanyID, &result.SourcePreference, &result.RefreshIntervalHours, &result.LastAttemptAt,
			&result.LastSuccessAt, &result.LastRateDate, &result.LastSource, &result.LastError, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{CompanyID: companyID, SourcePreference: SourceAuto, RefreshIntervalHours: 6, Version: 1}, nil
	}
	return result, err
}

func (s *Service) refreshIfDue(ctx context.Context, companyID string) error {
	settings, err := s.settings(ctx, companyID)
	if err != nil {
		return err
	}
	if settings.LastSuccessAt != nil && s.clock().UTC().Before(settings.LastSuccessAt.UTC().Add(time.Duration(settings.RefreshIntervalHours)*time.Hour)) {
		complete, err := s.hasAllActiveRates(ctx, companyID)
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
	}
	return s.refreshCompany(ctx, companyID, false)
}

// A successful refresh that stored only the base currency is not complete.
// This check also heals caches created by an older provider parser without
// waiting for the regular six-hour refresh window.
func (s *Service) hasAllActiveRates(ctx context.Context, companyID string) (bool, error) {
	var complete bool
	err := s.pool.QueryRow(ctx, `
		SELECT NOT EXISTS (
			SELECT 1
			  FROM pricing_currencies c
			 WHERE c.company_id=$1
			   AND c.is_active
			   AND c.code <> (SELECT base_currency FROM companies WHERE id=$1)
			   AND NOT EXISTS (
					SELECT 1
					  FROM exchange_rates r
					 WHERE r.company_id=c.company_id
					   AND r.currency_code=c.code
					   AND r.rate_date <= CURRENT_DATE
				)
		)`, companyID).Scan(&complete)
	return complete, err
}

func (s *Service) refreshCompany(ctx context.Context, companyID string, force bool) error {
	settings, err := s.settings(ctx, companyID)
	if err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO exchange_rate_settings(company_id,source_preference,refresh_interval_hours,last_error)
		VALUES($1,$2,$3,'')
		ON CONFLICT (company_id) DO NOTHING`,
		companyID, settings.SourcePreference, settings.RefreshIntervalHours); err != nil {
		return err
	}
	var claimed bool
	if err := s.pool.QueryRow(ctx, `
		UPDATE exchange_rate_settings
		   SET last_attempt_at=now(),last_error='',updated_at=now()
		 WHERE company_id=$1
		   AND (
				($2::boolean AND (
					last_attempt_at IS NULL
					OR last_error <> ''
					OR last_attempt_at <= COALESCE(last_success_at, last_attempt_at - interval '2 minutes')
					OR last_attempt_at < now()-interval '1 minute'
				))
				OR (NOT $2::boolean AND (last_attempt_at IS NULL OR last_attempt_at < now()-interval '1 minute'))
			)
		 RETURNING true`, companyID, force).Scan(&claimed); errors.Is(err, pgx.ErrNoRows) {
		// Another request/worker is already fetching this company's rate.
		return errRefreshInProgress
	} else if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	fetched, fetchErr := s.fetchPreferred(ctx, settings.SourcePreference)
	if fetchErr != nil {
		s.markFailure(ctx, companyID, fetchErr)
		return fetchErr
	}
	base, err := s.companyBaseCurrency(ctx, companyID)
	if err != nil {
		s.markFailure(ctx, companyID, err)
		return err
	}
	currencies, err := s.activeCurrencies(ctx, companyID)
	if err != nil {
		s.markFailure(ctx, companyID, err)
		return err
	}
	baseValue, ok := fetched.Rates[base]
	if !ok {
		s.markFailure(ctx, companyID, fmt.Errorf("%w: temel para birimi kaynakta yok", ErrRateUnavailable))
		return fmt.Errorf("%w: temel para birimi kaynakta yok", ErrRateUnavailable)
	}
	type row struct{ currency, rate string }
	values := make([]row, 0, len(currencies))
	for _, currency := range currencies {
		quote, exists := fetched.Rates[currency]
		if !exists || quote.Sign() <= 0 {
			continue
		}
		// Provider maps are expressed as source-base units per source quote
		// unit. Our persisted convention is the same: one document currency
		// unit equals this many company-base units.
		values = append(values, row{currency: currency, rate: new(big.Rat).Quo(quote, baseValue).FloatString(18)})
	}
	if len(values) == 0 {
		err := fmt.Errorf("%w: desteklenen para birimi bulunamadı", ErrRateUnavailable)
		s.markFailure(ctx, companyID, err)
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rateDate := fetched.Date.Format("2006-01-02")
	for _, value := range values {
		if _, err := tx.Exec(ctx, `
			INSERT INTO exchange_rates(company_id,currency_code,rate_date,rate_to_base,source_code,source_url,fetched_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,now(),now())
			ON CONFLICT (company_id,currency_code,rate_date,source_code) DO UPDATE SET
			 rate_to_base=excluded.rate_to_base,source_url=excluded.source_url,
			 fetched_at=excluded.fetched_at,updated_at=now()`,
			companyID, value.currency, rateDate, value.rate, fetched.Source, fetched.URL); err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			s.markFailure(ctx, companyID, err)
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE exchange_rate_settings
		   SET last_success_at=now(),last_rate_date=$2::date,last_source=$3,last_error='',updated_at=now(),version=version+1
		 WHERE company_id=$1`, companyID, rateDate, fetched.Source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) markFailure(ctx context.Context, companyID string, cause error) {
	message := "kur sağlayıcısından veri alınamadı"
	if cause != nil {
		message = message + ": " + cause.Error()
	}
	if len(message) > 500 {
		message = message[:500]
	}
	_, _ = s.pool.Exec(ctx, `UPDATE exchange_rate_settings SET last_error=$2,updated_at=now() WHERE company_id=$1`, companyID, message)
}

func (s *Service) waitForRate(ctx context.Context, companyID, currencyCode string, on time.Time) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for attempt := 0; attempt < 48; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
		var rate string
		err := s.pool.QueryRow(ctx, `
			SELECT rate_to_base::text
			  FROM exchange_rates
			 WHERE company_id=$1 AND currency_code=$2 AND rate_date <= $3::date
			 ORDER BY rate_date DESC, fetched_at DESC
			 LIMIT 1`, companyID, currencyCode, dateOnly(on)).Scan(&rate)
		if err == nil {
			return rate, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	return "", errRefreshInProgress
}

func (s *Service) companyBaseCurrency(ctx context.Context, companyID string) (string, error) {
	var base string
	err := s.pool.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1 AND is_active`, companyID).Scan(&base)
	return strings.ToUpper(strings.TrimSpace(base)), err
}

func (s *Service) activeCurrencies(ctx context.Context, companyID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT code FROM pricing_currencies WHERE company_id=$1 AND is_active ORDER BY code`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		result = append(result, strings.ToUpper(code))
	}
	return result, rows.Err()
}

type fetchedRates struct {
	Source string
	URL    string
	Date   time.Time
	Rates  map[string]*big.Rat
}

func (s *Service) fetchPreferred(ctx context.Context, preference string) (fetchedRates, error) {
	if preference == SourceTCMB {
		return s.fetchTCMB(ctx)
	}
	if preference == SourceECB {
		return s.fetchECB(ctx)
	}
	tcmb, tcmbErr := s.fetchTCMB(ctx)
	if tcmbErr == nil {
		return tcmb, nil
	}
	ecb, ecbErr := s.fetchECB(ctx)
	if ecbErr == nil {
		return ecb, nil
	}
	return fetchedRates{}, fmt.Errorf("%w: TCMB ve ECB kaynakları kullanılamadı", ErrRateUnavailable)
}

type tcmbDocument struct {
	Date  string `xml:"Tarih,attr"`
	Items []struct {
		Code    string `xml:"CurrencyCode,attr"`
		Unit    int    `xml:"Unit"`
		Buying  string `xml:"ForexBuying"`
		Selling string `xml:"ForexSelling"`
	} `xml:"Currency"`
}

func (s *Service) fetchTCMB(ctx context.Context) (fetchedRates, error) {
	var document tcmbDocument
	if err := s.fetchContext(ctx, tcmbURL, &document); err != nil {
		return fetchedRates{}, err
	}
	date, err := parseProviderDate(document.Date)
	if err != nil {
		return fetchedRates{}, err
	}
	rates := map[string]*big.Rat{"TRY": big.NewRat(1, 1)}
	for _, item := range document.Items {
		value := strings.TrimSpace(item.Selling)
		if value == "" {
			value = strings.TrimSpace(item.Buying)
		}
		value = strings.ReplaceAll(value, ",", ".")
		rat, ok := new(big.Rat).SetString(value)
		if !ok || rat.Sign() <= 0 || item.Unit < 1 {
			continue
		}
		rates[strings.ToUpper(strings.TrimSpace(item.Code))] = new(big.Rat).Quo(rat, big.NewRat(int64(item.Unit), 1))
	}
	return fetchedRates{Source: SourceTCMB, URL: tcmbURL, Date: date, Rates: rates}, nil
}

func (s *Service) fetchContext(ctx context.Context, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/xml,text/xml;q=0.9")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	return xml.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target)
}

type ecbDocument struct {
	Days []struct {
		Date  string `xml:"time,attr"`
		Rates []struct {
			Code  string `xml:"currency,attr"`
			Value string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

func (s *Service) fetchECB(ctx context.Context) (fetchedRates, error) {
	var document ecbDocument
	if err := s.fetchContext(ctx, ecbURL, &document); err != nil {
		return fetchedRates{}, err
	}
	date := s.clock().UTC().Truncate(24 * time.Hour)
	if len(document.Days) > 0 {
		if parsed, parseErr := parseProviderDate(document.Days[0].Date); parseErr == nil {
			date = parsed
		}
	}
	if len(document.Days) == 0 {
		return fetchedRates{}, fmt.Errorf("ECB response contains no rates")
	}
	rates := ecbRates(document)
	if len(rates) == 1 {
		return fetchedRates{}, fmt.Errorf("ECB response contains no rates")
	}
	return fetchedRates{Source: SourceECB, URL: ecbURL, Date: date, Rates: rates}, nil
}

// ecbRates converts an ECB daily document into our internal convention:
// provider-base (EUR) units per one foreign unit. ECB itself quotes the
// inverse (foreign units per one EUR, e.g. 1 EUR = 1.08 USD), so every quote
// is inverted here to match the TCMB parser and refreshCompany's math.
func ecbRates(document ecbDocument) map[string]*big.Rat {
	rates := map[string]*big.Rat{"EUR": big.NewRat(1, 1)}
	if len(document.Days) == 0 {
		return rates
	}
	for _, item := range document.Days[0].Rates {
		rat, ok := new(big.Rat).SetString(strings.TrimSpace(item.Value))
		if ok && rat.Sign() > 0 {
			rates[strings.ToUpper(strings.TrimSpace(item.Code))] = new(big.Rat).Quo(big.NewRat(1, 1), rat)
		}
	}
	return rates
}

func parseProviderDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"02.01.2006", "2006-01-02", "2006-01-02T15:04:05Z07:00"} {
		if result, err := time.Parse(layout, value); err == nil {
			return result.UTC().Truncate(24 * time.Hour), nil
		}
	}
	return time.Time{}, fmt.Errorf("provider date is invalid")
}

func dateOnly(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return value.UTC().Format("2006-01-02")
}

func validSource(value string) bool {
	return value == SourceAuto || value == SourceTCMB || value == SourceECB
}

// documentRate adapts the provider's high precision value to the eight-place
// exchange_rate column used by documents. The provider rate remains stored at
// eighteen places; this is only the explicit document snapshot boundary.
func documentRate(value string) (string, error) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || rat.Sign() <= 0 {
		return "", fmt.Errorf("%w: kur değeri geçersiz", ErrRateUnavailable)
	}
	formatted := strings.TrimRight(strings.TrimRight(rat.FloatString(8), "0"), ".")
	if formatted == "" || formatted == "0" {
		return "", fmt.Errorf("%w: kur değeri geçersiz", ErrRateUnavailable)
	}
	return formatted, nil
}

func canRead(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && (session.HasPermission("pricing.read") || session.HasPermission("pricing.manage"))
}

func canManage(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission("pricing.manage")
}
