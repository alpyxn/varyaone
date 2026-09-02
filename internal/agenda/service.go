// Package agenda backs the topbar calendar: a per-user, per-company list of
// personal reminder events. It follows the internal/dashboard pattern — raw
// pgx queries, no sqlc, hand-rolled JSON models — but stores one row per event
// instead of a jsonb blob so events can be created and deleted individually.
package agenda

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

const (
	maxTitleLen     = 200
	maxRemindMin    = 10080 // one week
	maxListedEvents = 500
)

var (
	timePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// Service is the data gateway for personal agenda events.
type Service struct{ pool database.Querier }

// NewService wires the agenda service to a database pool.
func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

// Event is one personal reminder attached to a calendar day. Time is an empty
// string for all-day entries. RemindMinutes is how many minutes before the
// event the bell should ring (0 = at the event time).
type Event struct {
	ID            string     `json:"id"`
	Date          string     `json:"date"`
	Time          string     `json:"time"`
	Title         string     `json:"title"`
	RemindMinutes int        `json:"remind_minutes"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	Version       int64      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Input is the create payload for a new event.
type Input struct {
	Date          string `json:"date"`
	Time          string `json:"time"`
	Title         string `json:"title"`
	RemindMinutes int    `json:"remind_minutes"`
}

func owner(session identity.Session) error {
	if session.CurrentCompanyID == "" || session.User.ID == "" {
		return identity.ErrForbidden
	}
	return nil
}

// eventColumns is the shared projection for every row returned to the client.
const eventColumns = `id::text, to_char(event_date,'YYYY-MM-DD'), event_time, title,
	remind_minutes, notified_at, completed_at, version, created_at`

type scannable interface {
	Scan(dest ...any) error
}

func scanEvent(row scannable) (Event, error) {
	var e Event
	err := row.Scan(&e.ID, &e.Date, &e.Time, &e.Title, &e.RemindMinutes,
		&e.NotifiedAt, &e.CompletedAt, &e.Version, &e.CreatedAt)
	return e, err
}

// List returns the caller's events from yesterday onwards, oldest first.
func (s *Service) List(ctx context.Context, session identity.Session) ([]Event, error) {
	if err := owner(session); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+eventColumns+`
		FROM user_agenda_events
		WHERE company_id=$1 AND user_id=$2 AND event_date >= current_date - INTERVAL '1 day'
		ORDER BY event_date, event_time, created_at
		LIMIT $3`,
		session.CurrentCompanyID, session.User.ID, maxListedEvents)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0, 32)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// Create validates and inserts a new event for the caller.
func (s *Service) Create(ctx context.Context, session identity.Session, in Input) (Event, error) {
	if err := owner(session); err != nil {
		return Event{}, err
	}
	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return Event{}, fmt.Errorf("%w: geçersiz tarih", identity.ErrValidation)
	}
	clock := strings.TrimSpace(in.Time)
	if clock != "" && !timePattern.MatchString(clock) {
		return Event{}, fmt.Errorf("%w: geçersiz saat", identity.ErrValidation)
	}
	title := strings.TrimSpace(in.Title)
	if title == "" || len([]rune(title)) > maxTitleLen {
		return Event{}, fmt.Errorf("%w: başlık 1-%d karakter olmalıdır", identity.ErrValidation, maxTitleLen)
	}
	if in.RemindMinutes < 0 || in.RemindMinutes > maxRemindMin {
		return Event{}, fmt.Errorf("%w: geçersiz hatırlatma süresi", identity.ErrValidation)
	}

	return scanEvent(s.pool.QueryRow(ctx, `
		INSERT INTO user_agenda_events(company_id,user_id,event_date,event_time,title,remind_minutes)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING `+eventColumns,
		session.CurrentCompanyID, session.User.ID, in.Date, clock, title, in.RemindMinutes))
}

// SetCompleted marks an event done (or clears that flag). A missing row is
// reported as forbidden so ids cannot be probed.
func (s *Service) SetCompleted(ctx context.Context, session identity.Session, id string, completed bool) (Event, error) {
	if err := owner(session); err != nil {
		return Event{}, err
	}
	id = strings.TrimSpace(id)
	if !uuidPattern.MatchString(id) {
		return Event{}, identity.ErrForbidden
	}
	e, err := scanEvent(s.pool.QueryRow(ctx, `
		UPDATE user_agenda_events
		SET completed_at = CASE WHEN $4 THEN now() ELSE NULL END,
		    updated_at = now(), version = version + 1
		WHERE id=$1 AND company_id=$2 AND user_id=$3
		RETURNING `+eventColumns,
		id, session.CurrentCompanyID, session.User.ID, completed))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, identity.ErrForbidden
	}
	return e, err
}

// Delete removes one of the caller's events. Missing rows are reported as a
// forbidden access so callers cannot probe other users' ids.
func (s *Service) Delete(ctx context.Context, session identity.Session, id string) error {
	if err := owner(session); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if !uuidPattern.MatchString(id) {
		return identity.ErrForbidden
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM user_agenda_events WHERE id=$1 AND company_id=$2 AND user_id=$3`,
		id, session.CurrentCompanyID, session.User.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrForbidden
	}
	return nil
}

// MarkNotified stamps notified_at on the given events so their reminder is
// never replayed. Ids that are not the caller's, or already stamped, are
// silently ignored.
func (s *Service) MarkNotified(ctx context.Context, session identity.Session, ids []string) error {
	if err := owner(session); err != nil {
		return err
	}
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); uuidPattern.MatchString(id) {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE user_agenda_events
		SET notified_at=now(), updated_at=now(), version=version+1
		WHERE id = ANY($1::uuid[]) AND company_id=$2 AND user_id=$3 AND notified_at IS NULL`,
		cleaned, session.CurrentCompanyID, session.User.ID)
	return err
}
