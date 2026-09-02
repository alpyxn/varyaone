package email

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/secrets"
	"github.com/google/uuid"
)

// ErrNothingToSend means no recipient passed validation.
var ErrNothingToSend = errors.New("EMAIL_NOTHING_TO_SEND")

// Recipient is one addressee with its own placeholder values.
type Recipient struct {
	Email     string            `json:"email"`
	Name      string            `json:"name"`
	Variables map[string]string `json:"variables"`
}

// SendRequest is the payload for both Preview and Send.
type SendRequest struct {
	Subject     string      `json:"subject"`
	Body        string      `json:"body"`
	TemplateID  string      `json:"template_id"`
	ContextType string      `json:"context_type"`
	ContextID   string      `json:"context_id"`
	Recipients  []Recipient `json:"recipients"`
}

// RecipientStatus is a per-recipient classification for the preview.
type RecipientStatus struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Status string `json:"status"` // ready | missing | invalid | duplicate
}

type PreviewResult struct {
	Subject    string            `json:"subject"`
	Body       string            `json:"body"`
	Recipients []RecipientStatus `json:"recipients"`
	ReadyCount int               `json:"ready_count"`
	// SampleSubject / SampleBody are rendered for the first ready recipient.
	SampleSubject string `json:"sample_subject"`
	SampleBody    string `json:"sample_body"`
}

type SendResult struct {
	MessageID string        `json:"message_id"`
	Status    string        `json:"status"`
	Sent      int           `json:"sent"`
	Failed    int           `json:"failed"`
	Skipped   int           `json:"skipped"`
	Preview   PreviewResult `json:"preview"`
}

type ComposeService struct {
	pool      database.Querier
	box       *secrets.Box
	templates *TemplateService
}

func NewComposeService(pool database.Querier, masterKey []byte) (*ComposeService, error) {
	box, err := secrets.NewBox(masterKey)
	if err != nil {
		return nil, err
	}
	return &ComposeService{pool: pool, box: box, templates: NewTemplateService(pool)}, nil
}

// resolveText picks the effective subject/body: an explicit value wins, otherwise
// the referenced template's text is used.
func (s *ComposeService) resolveText(ctx context.Context, companyID string, req SendRequest) (subject, body string, err error) {
	subject, body = req.Subject, req.Body
	if req.TemplateID != "" && (strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "") {
		tpl, gErr := s.templates.get(ctx, companyID, req.TemplateID)
		if gErr != nil && !errors.Is(gErr, ErrTemplateNotFound) {
			return "", "", gErr
		}
		if strings.TrimSpace(subject) == "" {
			subject = tpl.Subject
		}
		if strings.TrimSpace(body) == "" {
			body = tpl.Body
		}
	}
	return subject, body, nil
}

func classify(recipients []Recipient) []RecipientStatus {
	counts := map[string]int{}
	for _, r := range recipients {
		e := strings.ToLower(strings.TrimSpace(r.Email))
		if e != "" {
			counts[e]++
		}
	}
	out := make([]RecipientStatus, 0, len(recipients))
	for _, r := range recipients {
		e := strings.ToLower(strings.TrimSpace(r.Email))
		status := "ready"
		switch {
		case e == "":
			status = "missing"
		case !ValidAddress(e):
			status = "invalid"
		case counts[e] > 1:
			status = "duplicate"
		}
		out = append(out, RecipientStatus{Email: r.Email, Name: r.Name, Status: status})
	}
	return out
}

func (s *ComposeService) Preview(ctx context.Context, session identity.Session, req SendRequest) (PreviewResult, error) {
	if !session.HasPermission("communication.email.send") {
		return PreviewResult{}, identity.ErrForbidden
	}
	subject, body, err := s.resolveText(ctx, session.CurrentCompanyID, req)
	if err != nil {
		return PreviewResult{}, err
	}
	statuses := classify(req.Recipients)
	result := PreviewResult{Subject: subject, Body: body, Recipients: statuses}
	for i, st := range statuses {
		if st.Status == "ready" {
			result.ReadyCount++
			if result.SampleSubject == "" && result.SampleBody == "" {
				result.SampleSubject = RenderText(subject, req.Recipients[i].Variables)
				result.SampleBody = RenderText(body, req.Recipients[i].Variables)
			}
		}
	}
	return result, nil
}

func (s *ComposeService) Send(ctx context.Context, session identity.Session, req SendRequest, meta identity.RequestMeta) (SendResult, error) {
	if !session.HasPermission("communication.email.send") {
		return SendResult{}, identity.ErrForbidden
	}
	cfg, password, err := LoadSMTP(ctx, s.pool, s.box, session.CurrentCompanyID)
	if err != nil {
		return SendResult{}, err
	}
	subject, body, err := s.resolveText(ctx, session.CurrentCompanyID, req)
	if err != nil {
		return SendResult{}, err
	}
	preview, err := s.Preview(ctx, session, req)
	if err != nil {
		return SendResult{}, err
	}
	if preview.ReadyCount == 0 {
		return SendResult{}, ErrNothingToSend
	}

	contextType := strings.TrimSpace(req.ContextType)
	if contextType == "" {
		contextType = "GENERIC"
	}
	messageID := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO email_messages(id,company_id,context_type,context_id,template_id,subject,body,status,requested_by)
 VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,'RUNNING',$8)`,
		messageID, session.CurrentCompanyID, contextType, strings.TrimSpace(req.ContextID), req.TemplateID, subject, body, session.User.ID); err != nil {
		return SendResult{}, err
	}

	result := SendResult{MessageID: messageID, Preview: preview}
	for i, st := range preview.Recipients {
		if st.Status != "ready" {
			if st.Status == "missing" {
				result.Skipped++
			} else {
				result.Failed++
			}
			continue
		}
		rcpt := req.Recipients[i]
		msg := Message{
			To:       strings.ToLower(strings.TrimSpace(rcpt.Email)),
			ToName:   rcpt.Name,
			Subject:  RenderText(subject, rcpt.Variables),
			BodyText: RenderText(body, rcpt.Variables),
		}
		code, outcome := Send(cfg, password, msg)
		status, errCode := DeliveryStatus(outcome)
		if _, err = tx.Exec(ctx, `INSERT INTO email_message_recipients(company_id,message_id,recipient,recipient_name,status,smtp_response_code,error_code,sent_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,CASE WHEN $5='SENT' THEN now() ELSE NULL END)`,
			session.CurrentCompanyID, messageID, msg.To, msg.ToName, status, nullInt(code), nullString(errCode)); err != nil {
			return SendResult{}, err
		}
		if status == "SENT" {
			result.Sent++
		} else {
			result.Failed++
		}
	}

	batchStatus := "COMPLETED"
	if result.Sent == 0 {
		batchStatus = "FAILED"
	}
	if _, err = tx.Exec(ctx, `UPDATE email_messages SET status=$2,sent_count=$3,failed_count=$4,skipped_count=$5,completed_at=now()
 WHERE company_id=$1 AND id=$6`,
		session.CurrentCompanyID, batchStatus, result.Sent, result.Failed, result.Skipped, messageID); err != nil {
		return SendResult{}, err
	}
	details, _ := json.Marshal(map[string]any{"message_id": messageID, "sent": result.Sent, "failed": result.Failed})
	if _, err = tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,'EMAIL_MESSAGE_SENT','email_message',$4,$5,$6,$7,$8)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, messageID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return SendResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,'email_message.sent',1,$2,$3,$4)`,
		uuid.NewString(), session.CurrentCompanyID, meta.TraceID, details); err != nil {
		return SendResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SendResult{}, err
	}
	result.Status = batchStatus
	return result, nil
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
