// Package email provides reusable e-mail templates and a generic SMTP sender
// shared by the payroll payslip delivery flow and the standalone e-mail composer.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/secrets"
	"github.com/jackc/pgx/v5"
)

// ErrSMTPNotConfigured is returned when a company has no SMTP settings row.
var ErrSMTPNotConfigured = errors.New("SMTP_SETTINGS_NOT_FOUND")

// SMTPConfig is the resolved connection configuration for one company.
type SMTPConfig struct {
	Host         string
	Port         int
	SecurityMode string
	Username     string
	FromEmail    string
	FromName     string
	Timeout      time.Duration
}

// Attachment is a single file attached to every recipient's message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is one outbound e-mail.
type Message struct {
	To          string
	ToName      string
	Subject     string
	BodyText    string
	Attachments []Attachment
}

// Outcome classifies the result of a single send attempt.
type Outcome string

const (
	Sent             Outcome = "SENT"
	StatusUnknown    Outcome = "DELIVERY_STATUS_UNKNOWN"
	Retry            Outcome = "RETRY"
	PermanentFailure Outcome = "PERMANENT_FAILURE"
)

// DeliveryStatus maps an Outcome to the persisted status + error code columns.
func DeliveryStatus(outcome Outcome) (status, errCode string) {
	switch outcome {
	case Sent:
		return "SENT", ""
	case StatusUnknown:
		return "DELIVERY_STATUS_UNKNOWN", "DELIVERY_STATUS_UNKNOWN"
	case Retry:
		return "FAILED_PERMANENT", "SMTP_RETRYABLE"
	default:
		return "FAILED_PERMANENT", "SMTP_PERMANENT"
	}
}

// LoadSMTP reads and decrypts the company SMTP settings. The returned password
// is a plain string; callers should not log it.
func LoadSMTP(ctx context.Context, pool database.Querier, box *secrets.Box, companyID string) (SMTPConfig, string, error) {
	var cfg SMTPConfig
	var ciphertext []byte
	var timeoutSeconds int
	err := pool.QueryRow(ctx, `SELECT host,port,security_mode,username,from_email,from_name,connect_timeout_seconds,password_ciphertext
 FROM company_smtp_settings WHERE company_id=$1`, companyID).
		Scan(&cfg.Host, &cfg.Port, &cfg.SecurityMode, &cfg.Username, &cfg.FromEmail, &cfg.FromName, &timeoutSeconds, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return SMTPConfig{}, "", ErrSMTPNotConfigured
	}
	if err != nil {
		return SMTPConfig{}, "", err
	}
	cfg.Timeout = time.Duration(timeoutSeconds) * time.Second
	password := ""
	if len(ciphertext) > 0 {
		plain, err := box.Open(companyID, "smtp_password", ciphertext)
		if err != nil {
			return SMTPConfig{}, "", err
		}
		password = string(plain)
		for i := range plain {
			plain[i] = 0
		}
	}
	return cfg, password, nil
}

// Send delivers one message. It returns the observed SMTP response code (0 when
// unknown) and an Outcome classification.
func Send(cfg SMTPConfig, password string, msg Message) (int, Outcome) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var client *smtp.Client
	var err error
	if cfg.SecurityMode == "TLS" {
		conn, derr := tls.Dial("tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if derr != nil {
			return 0, Retry
		}
		client, err = smtp.NewClient(conn, cfg.Host)
	} else {
		client, err = smtp.Dial(addr)
		if err == nil {
			err = client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		}
	}
	if err != nil || client == nil {
		return 0, Retry
	}
	defer client.Close()

	if cfg.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", cfg.Username, password, cfg.Host)); err != nil {
			return 535, PermanentFailure
		}
	}
	if err = client.Mail(cfg.FromEmail); err != nil {
		return 0, Retry
	}
	if err = client.Rcpt(msg.To); err != nil {
		return 550, PermanentFailure
	}
	wc, err := client.Data()
	if err != nil {
		return 0, Retry
	}
	if _, err = wc.Write(buildMIME(cfg, msg)); err != nil {
		_ = wc.Close()
		return 0, StatusUnknown
	}
	if err = wc.Close(); err != nil {
		return 0, StatusUnknown
	}
	_ = client.Quit()
	return 250, Sent
}

func buildMIME(cfg SMTPConfig, msg Message) []byte {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := &bytes.Buffer{}
	fmt.Fprintf(header, "From: %s <%s>\r\n", encodeHeader(cfg.FromName), cfg.FromEmail)
	if msg.ToName != "" {
		fmt.Fprintf(header, "To: %s <%s>\r\n", encodeHeader(msg.ToName), msg.To)
	} else {
		fmt.Fprintf(header, "To: %s\r\n", msg.To)
	}
	fmt.Fprintf(header, "Subject: %s\r\n", encodeHeader(msg.Subject))
	fmt.Fprintf(header, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(header, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(header, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", writer.Boundary())

	body := msg.BodyText
	if strings.TrimSpace(body) == "" {
		body = " "
	}
	textPart, _ := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}})
	_, _ = textPart.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"))

	for _, att := range msg.Attachments {
		contentType := att.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		part, _ := writer.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {contentType},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(att.Filename))},
		})
		encoded := base64.StdEncoding.EncodeToString(att.Data)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			_, _ = part.Write([]byte(encoded[i:end] + "\r\n"))
		}
	}
	_ = writer.Close()

	return append(header.Bytes(), buf.Bytes()...)
}

func encodeHeader(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	return mime.QEncoding.Encode("utf-8", value)
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "ek"
	}
	return strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return '-'
		}
		return r
	}, name)
}

// ValidAddress reports whether value is a single, plain e-mail address.
func ValidAddress(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}
