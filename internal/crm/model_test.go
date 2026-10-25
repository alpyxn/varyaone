package crm

import (
	"errors"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"0532 123 45 67":     "905321234567",
		"+90 (532) 123-4567": "905321234567",
		"5321234567":         "905321234567",
		"0090 532 123 4567":  "905321234567",
		"905321234567":       "905321234567",
		"0212 555 00 11":     "902125550011",
		"+49 30 1234567":     "49301234567",
		"":                   "",
		"dahili 12":          "12",
	}
	for raw, want := range cases {
		if got := NormalizePhone(raw); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestApplyCallOutcomes(t *testing.T) {
	at := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	later := at.Add(48 * time.Hour)
	cases := []struct {
		name       string
		status     string
		outcome    string
		callback   *time.Time
		wantStatus string
		wantDNC    bool
		wantNext   *time.Time
	}{
		{"no answer keeps new", StatusNew, OutcomeNoAnswer, nil, StatusNew, false, nil},
		{"busy keeps contacted", StatusContacted, OutcomeBusy, nil, StatusContacted, false, nil},
		{"no answer can schedule retry", StatusNew, OutcomeVoicemail, &later, StatusNew, false, &later},
		{"callback contacts", StatusNew, OutcomeCallback, &later, StatusContacted, false, &later},
		{"callback keeps qualified", StatusQualified, OutcomeCallback, &later, StatusQualified, false, &later},
		{"interested qualifies", StatusContacted, OutcomeInterested, nil, StatusQualified, false, nil},
		{"meeting qualifies", StatusNew, OutcomeMeetingSet, &later, StatusQualified, false, &later},
		{"not interested disqualifies", StatusContacted, OutcomeNotInterested, nil, StatusDisqualified, false, nil},
		{"do not call", StatusNew, OutcomeDoNotCall, nil, StatusDisqualified, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyCall(LeadCallState{Status: tc.status, CallAttempts: 2}, tc.outcome, at, tc.callback)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.wantStatus || got.DoNotCall != tc.wantDNC {
				t.Fatalf("status/dnc = %s/%v, want %s/%v", got.Status, got.DoNotCall, tc.wantStatus, tc.wantDNC)
			}
			if got.CallAttempts != 3 || got.LastCallAt == nil || !got.LastCallAt.Equal(at) || *got.LastOutcome != tc.outcome {
				t.Fatalf("call summary not recorded: %+v", got)
			}
			if (got.NextActionAt == nil) != (tc.wantNext == nil) || (got.NextActionAt != nil && !got.NextActionAt.Equal(*tc.wantNext)) {
				t.Fatalf("next action = %v, want %v", got.NextActionAt, tc.wantNext)
			}
		})
	}
}

func TestApplyCallRefusals(t *testing.T) {
	at := time.Now()
	if _, err := ApplyCall(LeadCallState{Status: StatusNew, DoNotCall: true}, OutcomeNoAnswer, at, nil); !errors.Is(err, ErrDoNotCall) {
		t.Fatalf("do-not-call lead: %v", err)
	}
	if _, err := ApplyCall(LeadCallState{Status: StatusConverted}, OutcomeInterested, at, nil); !errors.Is(err, ErrLeadConverted) {
		t.Fatalf("converted lead: %v", err)
	}
	if _, err := ApplyCall(LeadCallState{Status: StatusNew}, OutcomeCallback, at, nil); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("callback without time: %v", err)
	}
	if _, err := ApplyCall(LeadCallState{Status: StatusNew}, "HUNG_UP", at, nil); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("unknown outcome: %v", err)
	}
}

func TestCheckOwnerChange(t *testing.T) {
	rep := identity.Session{Permissions: []string{"crm.edit"}}
	rep.User.ID = "u1"
	manager := identity.Session{Permissions: []string{"crm.edit", "crm.manage"}}
	manager.User.ID = "m1"
	other := "u2"
	mine := "u1"
	if err := checkOwnerChange(rep, nil, "u1"); err != nil {
		t.Errorf("rep claiming an unassigned lead: %v", err)
	}
	if err := checkOwnerChange(rep, &mine, "u1"); err != nil {
		t.Errorf("rep keeping own lead: %v", err)
	}
	if err := checkOwnerChange(rep, nil, "u2"); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("rep assigning to a colleague: %v", err)
	}
	if err := checkOwnerChange(rep, &other, "u1"); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("rep taking a colleague's lead: %v", err)
	}
	if err := checkOwnerChange(manager, &other, "u1"); err != nil {
		t.Errorf("manager reassigning: %v", err)
	}
}

func TestNextItemState(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	retry := 2 * time.Hour
	later := now.Add(24 * time.Hour)
	cases := []struct {
		name      string
		outcome   string
		attempts  int
		callback  *time.Time
		wantState string
		wantAt    time.Time
	}{
		{"no answer retries", OutcomeNoAnswer, 1, nil, ItemPending, now.Add(retry)},
		{"busy at chosen time", OutcomeBusy, 2, &later, ItemPending, later},
		{"voicemail exhausts", OutcomeVoicemail, 3, nil, ItemDone, now},
		{"callback waits", OutcomeCallback, 1, &later, ItemCallback, later},
		{"interested closes", OutcomeInterested, 1, nil, ItemDone, now},
		{"wrong number closes", OutcomeWrongNumber, 1, nil, ItemDone, now},
		{"do not call closes", OutcomeDoNotCall, 1, nil, ItemDone, now},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, at := NextItemState(tc.outcome, tc.attempts, 3, retry, now, tc.callback)
			if state != tc.wantState || !at.Equal(tc.wantAt) {
				t.Fatalf("got %s at %v, want %s at %v", state, at, tc.wantState, tc.wantAt)
			}
		})
	}
}
