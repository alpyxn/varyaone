package identity

import (
	"errors"
	"testing"
)

func TestActorContextRejectsMissingActor(t *testing.T) {
	_, err := (Session{CurrentCompanyID: "not-a-user"}).ActorContext(RequestMeta{})
	if !errors.Is(err, ErrInvalidActorContext) {
		t.Fatalf("expected invalid actor context, got %v", err)
	}
}

func TestActorContextRequiresUUIDUserAndCompany(t *testing.T) {
	actor := ActorContext{UserID: "00000000-0000-0000-0000-000000000001", CompanyID: "00000000-0000-0000-0000-000000000002"}
	if err := actor.Validate(); err != nil {
		t.Fatalf("expected valid actor, got %v", err)
	}
	if err := (ActorContext{UserID: actor.UserID}).Validate(); !errors.Is(err, ErrInvalidActorContext) {
		t.Fatalf("expected missing company rejection, got %v", err)
	}
}

func TestInternalCommandContextIsExplicit(t *testing.T) {
	valid := InternalCommandContext{Name: "outbox.worker", CompanyID: "00000000-0000-0000-0000-000000000002"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid internal context, got %v", err)
	}
	if err := (InternalCommandContext{CompanyID: valid.CompanyID}).Validate(); !errors.Is(err, ErrInvalidActorContext) {
		t.Fatalf("expected missing worker name rejection, got %v", err)
	}
}
