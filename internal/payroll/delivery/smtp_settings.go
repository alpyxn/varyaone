package delivery

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/secrets"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrSMTPSettingsNotFound = errors.New("SMTP_SETTINGS_NOT_FOUND")
	ErrSMTPValidation       = errors.New("SMTP_SETTINGS_INVALID")
	ErrSMTPVersionConflict  = errors.New("SMTP_SETTINGS_VERSION_CONFLICT")
)

type SMTPSettingsService struct {
	pool        database.Querier
	box         *secrets.Box
	environment string
}

func NewSMTPSettingsService(pool database.Querier, masterKey []byte, environment string) (*SMTPSettingsService, error) {
	box, err := secrets.NewBox(masterKey)
	if err != nil {
		return nil, err
	}
	return &SMTPSettingsService{pool: pool, box: box, environment: environment}, nil
}

type SMTPSettings struct {
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	SecurityMode          string `json:"security_mode"`
	Username              string `json:"username"`
	FromEmail             string `json:"from_email"`
	FromName              string `json:"from_name"`
	ConnectTimeoutSeconds int    `json:"connect_timeout_seconds"`
	HasPassword           bool   `json:"has_password"`
	Version               int64  `json:"version"`
}
type SMTPSettingsInput struct {
	Host                  string  `json:"host"`
	Port                  int     `json:"port"`
	SecurityMode          string  `json:"security_mode"`
	Username              string  `json:"username"`
	Password              *string `json:"password,omitempty"`
	FromEmail             string  `json:"from_email"`
	FromName              string  `json:"from_name"`
	ConnectTimeoutSeconds int     `json:"connect_timeout_seconds"`
}
type SMTPTestResult struct {
	Connected     bool   `json:"connected"`
	Authenticated bool   `json:"authenticated"`
	SecurityMode  string `json:"security_mode"`
}

func (s *SMTPSettingsService) Get(ctx context.Context, session identity.Session) (SMTPSettings, error) {
	if !session.HasPermission("settings.email.manage") {
		return SMTPSettings{}, identity.ErrForbidden
	}
	var item SMTPSettings
	err := s.pool.QueryRow(ctx, `SELECT host,port,security_mode,username,from_email,from_name,connect_timeout_seconds,password_ciphertext IS NOT NULL,version FROM company_smtp_settings WHERE company_id=$1`, session.CurrentCompanyID).Scan(&item.Host, &item.Port, &item.SecurityMode, &item.Username, &item.FromEmail, &item.FromName, &item.ConnectTimeoutSeconds, &item.HasPassword, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPSettings{}, ErrSMTPSettingsNotFound
	}
	return item, err
}

func (s *SMTPSettingsService) Put(ctx context.Context, session identity.Session, version int64, input SMTPSettingsInput, meta identity.RequestMeta) (SMTPSettings, error) {
	if !session.HasPermission("settings.email.manage") {
		return SMTPSettings{}, identity.ErrForbidden
	}
	normalizeSMTPInput(&input)
	if err := validateSMTPInput(input, s.environment); err != nil {
		return SMTPSettings{}, err
	}
	var ciphertext []byte
	if input.Password != nil {
		plain := []byte(*input.Password)
		var err error
		ciphertext, err = s.box.Seal(session.CurrentCompanyID, "smtp_password", plain)
		for index := range plain {
			plain[index] = 0
		}
		if err != nil {
			return SMTPSettings{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SMTPSettings{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var item SMTPSettings
	if version == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO company_smtp_settings(company_id,host,port,security_mode,username,password_ciphertext,from_email,from_name,connect_timeout_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(company_id) DO NOTHING RETURNING host,port,security_mode,username,from_email,from_name,connect_timeout_seconds,password_ciphertext IS NOT NULL,version`, session.CurrentCompanyID, input.Host, input.Port, input.SecurityMode, input.Username, ciphertext, input.FromEmail, input.FromName, input.ConnectTimeoutSeconds).Scan(&item.Host, &item.Port, &item.SecurityMode, &item.Username, &item.FromEmail, &item.FromName, &item.ConnectTimeoutSeconds, &item.HasPassword, &item.Version)
	} else {
		err = tx.QueryRow(ctx, `UPDATE company_smtp_settings SET host=$3,port=$4,security_mode=$5,username=$6,password_ciphertext=CASE WHEN $7::bytea IS NULL THEN password_ciphertext ELSE $7 END,from_email=$8,from_name=$9,connect_timeout_seconds=$10,updated_at=now(),version=version+1 WHERE company_id=$1 AND version=$2 RETURNING host,port,security_mode,username,from_email,from_name,connect_timeout_seconds,password_ciphertext IS NOT NULL,version`, session.CurrentCompanyID, version, input.Host, input.Port, input.SecurityMode, input.Username, ciphertext, input.FromEmail, input.FromName, input.ConnectTimeoutSeconds).Scan(&item.Host, &item.Port, &item.SecurityMode, &item.Username, &item.FromEmail, &item.FromName, &item.ConnectTimeoutSeconds, &item.HasPassword, &item.Version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPSettings{}, ErrSMTPVersionConflict
	}
	if err != nil {
		return SMTPSettings{}, err
	}
	details, _ := json.Marshal(map[string]any{"security_mode": item.SecurityMode, "has_password": item.HasPassword})
	if _, err = tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,'SMTP_SETTINGS_UPDATED','company_smtp_settings',$4,$5,$6,$7)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return SMTPSettings{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,'settings.email.updated',1,$2,$3,$4)`, uuid.NewString(), session.CurrentCompanyID, meta.TraceID, details); err != nil {
		return SMTPSettings{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SMTPSettings{}, err
	}
	return item, nil
}

func (s *SMTPSettingsService) Test(ctx context.Context, session identity.Session) (SMTPTestResult, error) {
	if !session.HasPermission("settings.email.test") {
		return SMTPTestResult{}, identity.ErrForbidden
	}
	var settings SMTPSettings
	var ciphertext []byte
	err := s.pool.QueryRow(ctx, `SELECT host,port,security_mode,username,from_email,from_name,connect_timeout_seconds,password_ciphertext,version FROM company_smtp_settings WHERE company_id=$1`, session.CurrentCompanyID).Scan(&settings.Host, &settings.Port, &settings.SecurityMode, &settings.Username, &settings.FromEmail, &settings.FromName, &settings.ConnectTimeoutSeconds, &ciphertext, &settings.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPTestResult{}, ErrSMTPSettingsNotFound
	}
	if err != nil {
		return SMTPTestResult{}, err
	}
	password, err := s.box.Open(session.CurrentCompanyID, "smtp_password", ciphertext)
	if err != nil {
		return SMTPTestResult{}, errors.New("SMTP_SECRET_UNAVAILABLE")
	}
	defer func() {
		for i := range password {
			password[i] = 0
		}
	}()
	timeout := time.Duration(settings.ConnectTimeoutSeconds) * time.Second
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := testSMTP(testCtx, settings, string(password))
	if err != nil {
		return SMTPTestResult{}, errors.New("SMTP_TEST_FAILED")
	}
	return result, nil
}

func testSMTP(ctx context.Context, settings SMTPSettings, password string) (SMTPTestResult, error) {
	address := net.JoinHostPort(settings.Host, fmt.Sprintf("%d", settings.Port))
	dialer := net.Dialer{}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Timeout = time.Until(deadline)
		if dialer.Timeout <= 0 {
			return SMTPTestResult{}, context.DeadlineExceeded
		}
	}
	var client *smtp.Client
	if settings.SecurityMode == "TLS" {
		connection, err := tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return SMTPTestResult{}, err
		}
		client, err = smtp.NewClient(connection, settings.Host)
		if err != nil {
			_ = connection.Close()
			return SMTPTestResult{}, err
		}
	} else {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return SMTPTestResult{}, err
		}
		client, err = smtp.NewClient(connection, settings.Host)
		if err != nil {
			_ = connection.Close()
			return SMTPTestResult{}, err
		}
		if err = client.StartTLS(&tls.Config{ServerName: settings.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return SMTPTestResult{}, err
		}
	}
	defer client.Close()
	result := SMTPTestResult{Connected: true, SecurityMode: settings.SecurityMode}
	if settings.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.Username, password, settings.Host)); err != nil {
			return SMTPTestResult{}, err
		}
		result.Authenticated = true
	}
	if err := client.Quit(); err != nil {
		return SMTPTestResult{}, err
	}
	return result, nil
}

func normalizeSMTPInput(input *SMTPSettingsInput) {
	input.Host = strings.TrimSpace(input.Host)
	input.SecurityMode = strings.ToUpper(strings.TrimSpace(input.SecurityMode))
	input.Username = strings.TrimSpace(input.Username)
	input.FromEmail = strings.ToLower(strings.TrimSpace(input.FromEmail))
	input.FromName = strings.TrimSpace(input.FromName)
	if input.ConnectTimeoutSeconds == 0 {
		input.ConnectTimeoutSeconds = 10
	}
}
func validateSMTPInput(input SMTPSettingsInput, environment string) error {
	if input.Host == "" || input.Port < 1 || input.Port > 65535 || (input.SecurityMode != "TLS" && input.SecurityMode != "STARTTLS") || input.FromEmail == "" || !valid(input.FromEmail) || input.FromName == "" || input.ConnectTimeoutSeconds < 1 || input.ConnectTimeoutSeconds > 60 {
		return ErrSMTPValidation
	}
	if environment == "production" && input.SecurityMode == "" {
		return ErrSMTPValidation
	}
	if input.Password != nil && len(*input.Password) > 4096 {
		return ErrSMTPValidation
	}
	return nil
}
