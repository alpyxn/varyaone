// Package crm owns the pre-sale side of the customer relationship: leads that
// are not yet a cari, the calls made to them, and the notes and follow-up tasks
// kept on leads and cari cards alike.
package crm

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrLeadNotFound     = errors.New("CRM_LEAD_NOT_FOUND")
	ErrActivityNotFound = errors.New("CRM_ACTIVITY_NOT_FOUND")
	ErrPartyNotFound    = errors.New("CRM_PARTY_NOT_FOUND")
	ErrDoNotCall        = errors.New("CRM_LEAD_DO_NOT_CALL")
	ErrLeadConverted    = errors.New("CRM_LEAD_CONVERTED")
	ErrOwnerNotMember   = errors.New("CRM_OWNER_NOT_MEMBER")
)

// Lead statuses. CONVERTED is reached only by turning the lead into a cari.
const (
	StatusNew          = "NEW"
	StatusContacted    = "CONTACTED"
	StatusQualified    = "QUALIFIED"
	StatusDisqualified = "DISQUALIFIED"
	StatusConverted    = "CONVERTED"
)

// editableStatuses are the states a user may set directly on a lead card.
var editableStatuses = map[string]bool{
	StatusNew: true, StatusContacted: true, StatusQualified: true, StatusDisqualified: true,
}

// Call outcomes, stored verbatim in crm_activities.call_outcome.
const (
	OutcomeNoAnswer      = "NO_ANSWER"
	OutcomeBusy          = "BUSY"
	OutcomeWrongNumber   = "WRONG_NUMBER"
	OutcomeVoicemail     = "VOICEMAIL"
	OutcomeCallback      = "CALLBACK"
	OutcomeNotInterested = "NOT_INTERESTED"
	OutcomeInterested    = "INTERESTED"
	OutcomeMeetingSet    = "MEETING_SET"
	OutcomeDoNotCall     = "DO_NOT_CALL"
)

var callOutcomes = map[string]bool{
	OutcomeNoAnswer: true, OutcomeBusy: true, OutcomeWrongNumber: true, OutcomeVoicemail: true,
	OutcomeCallback: true, OutcomeNotInterested: true, OutcomeInterested: true,
	OutcomeMeetingSet: true, OutcomeDoNotCall: true,
}

// ValidOutcome reports whether outcome is a known call result.
func ValidOutcome(outcome string) bool { return callOutcomes[outcome] }

// Reached reports whether the outcome means somebody actually answered.
func Reached(outcome string) bool {
	switch outcome {
	case OutcomeCallback, OutcomeNotInterested, OutcomeInterested, OutcomeMeetingSet, OutcomeDoNotCall:
		return true
	}
	return false
}

// LeadCallState is the part of a lead a call result changes.
type LeadCallState struct {
	Status        string
	DoNotCall     bool
	CallAttempts  int
	LastCallAt    *time.Time
	LastOutcome   *string
	NextActionAt  *time.Time
	DoNotCallNote string
}

// ApplyCall returns the lead state after a call with the given outcome. A
// callback outcome needs callbackAt, which becomes the lead's next action.
// Calling a lead marked do-not-call, or one already converted to a cari, is
// refused: a converted lead's history continues on the cari card.
func ApplyCall(lead LeadCallState, outcome string, at time.Time, callbackAt *time.Time) (LeadCallState, error) {
	if !ValidOutcome(outcome) {
		return LeadCallState{}, errInvalid("geçersiz arama sonucu")
	}
	if lead.DoNotCall {
		return LeadCallState{}, ErrDoNotCall
	}
	if lead.Status == StatusConverted {
		return LeadCallState{}, ErrLeadConverted
	}
	if outcome == OutcomeCallback && callbackAt == nil {
		return LeadCallState{}, errInvalid("geri arama için tarih ve saat seçin")
	}
	lead.CallAttempts++
	lead.LastCallAt = &at
	lead.LastOutcome = &outcome
	switch outcome {
	case OutcomeCallback:
		lead.NextActionAt = callbackAt
		if lead.Status == StatusNew {
			lead.Status = StatusContacted
		}
	case OutcomeInterested, OutcomeMeetingSet:
		lead.Status = StatusQualified
		lead.NextActionAt = callbackAt
	case OutcomeNotInterested:
		lead.Status = StatusDisqualified
		lead.NextActionAt = nil
	case OutcomeDoNotCall:
		lead.Status = StatusDisqualified
		lead.DoNotCall = true
		lead.NextActionAt = nil
	default:
		// Nobody answered (or the number is wrong): the lead keeps its status
		// and is simply tried again later.
		if callbackAt != nil {
			lead.NextActionAt = callbackAt
		}
	}
	return lead, nil
}

// NormalizePhone reduces a phone number to digits for duplicate detection.
// Turkish numbers written in any common form ("0532 123 45 67", "+90 532...",
// "5321234567") collapse to the same 905XXXXXXXXX value; anything else keeps
// its digits as written.
func NormalizePhone(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	switch {
	case strings.HasPrefix(digits, "00"):
		digits = digits[2:]
	case len(digits) == 11 && strings.HasPrefix(digits, "0"):
		digits = "90" + digits[1:]
	case len(digits) == 10 && !strings.HasPrefix(digits, "0"):
		digits = "90" + digits
	}
	if len(digits) > 15 {
		digits = digits[:15]
	}
	return digits
}

// Call-list item states.
const (
	ItemPending  = "PENDING"
	ItemCallback = "CALLBACK"
	ItemDone     = "DONE"
	ItemSkipped  = "SKIPPED"
)

// NextItemState decides where a call-list item goes after a call. attempts is
// the count including this call. Nobody answering puts the lead back in the
// queue after retry (or at the callback time the caller picked) until the
// list's attempt limit is reached; any conversation closes the item.
func NextItemState(outcome string, attempts, maxAttempts int, retry time.Duration, now time.Time, callbackAt *time.Time) (string, time.Time) {
	switch outcome {
	case OutcomeCallback:
		if callbackAt != nil {
			return ItemCallback, *callbackAt
		}
		return ItemCallback, now.Add(retry)
	case OutcomeNoAnswer, OutcomeBusy, OutcomeVoicemail:
		if attempts >= maxAttempts {
			return ItemDone, now
		}
		if callbackAt != nil {
			return ItemPending, *callbackAt
		}
		return ItemPending, now.Add(retry)
	default:
		return ItemDone, now
	}
}
