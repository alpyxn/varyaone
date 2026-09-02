package identity

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ActorContext is the explicit authorization and trace context for an
// externally initiated command.  Command handlers must derive it from an
// authenticated Session; callers must not manufacture an empty actor to skip
// scope checks.
type ActorContext struct {
	UserID         string
	CompanyID      string
	BranchID       string
	WarehouseID    string
	TraceID        string
	IdempotencyKey string
}

// InternalCommandContext is reserved for trusted application workers.  It is
// intentionally separate from ActorContext so a missing user cannot silently
// turn an HTTP command into an internal command.
type InternalCommandContext struct {
	Name      string
	CompanyID string
	TraceID   string
}

var ErrInvalidActorContext = errors.New("invalid actor context")

func (s Session) ActorContext(meta RequestMeta) (ActorContext, error) {
	actor := ActorContext{
		UserID:    strings.TrimSpace(s.User.ID),
		CompanyID: strings.TrimSpace(s.CurrentCompanyID),
		TraceID:   strings.TrimSpace(meta.TraceID),
	}
	if err := actor.Validate(); err != nil {
		return ActorContext{}, err
	}
	return actor, nil
}

func (a ActorContext) Validate() error {
	if !validUUID(a.UserID) || !validUUID(a.CompanyID) {
		return fmt.Errorf("%w: authenticated user and company are required", ErrInvalidActorContext)
	}
	return nil
}

func (a ActorContext) RequireIdempotency() error {
	if strings.TrimSpace(a.IdempotencyKey) == "" {
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidActorContext)
	}
	return nil
}

func (c InternalCommandContext) Validate() error {
	if strings.TrimSpace(c.Name) == "" || !validUUID(strings.TrimSpace(c.CompanyID)) {
		return fmt.Errorf("%w: internal command name and company are required", ErrInvalidActorContext)
	}
	return nil
}

// ValidateExternalActor is the common guard for service methods that still
// accept Session for compatibility with the existing module boundaries.
func ValidateExternalActor(session Session) error {
	_, err := session.ActorContext(RequestMeta{})
	return err
}

func validUUID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	_, err := uuid.Parse(value)
	return err == nil
}
