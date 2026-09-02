// Package timesheet builds deterministic attendance days from immutable inputs.
package timesheet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

type ScheduleDay struct{ PlannedMinutes int }
type Holiday struct {
	Date    time.Time
	HalfDay bool
}
type Day struct {
	Date                 string `json:"date"`
	Source               string `json:"source"`
	PlannedMinutes       int    `json:"planned_minutes"`
	WorkedMinutes        int    `json:"worked_minutes"`
	PaidLeaveMinutes     int    `json:"paid_leave_minutes"`
	UnpaidLeaveMinutes   int    `json:"unpaid_leave_minutes"`
	PublicHolidayMinutes int    `json:"public_holiday_minutes"`
	WeekRestMinutes      int    `json:"week_rest_minutes"`
}
type Input struct {
	Year           int
	Month          time.Month
	EmploymentFrom time.Time
	EmploymentTo   *time.Time
	Schedule       map[time.Weekday]ScheduleDay
	Holidays       []Holiday
	ExistingManual []Day
}
type Result struct {
	Days     []Day
	Checksum string
}

// Generate prefills GENERATED attendance days from the work schedule and any
// manually marked public holidays. MANUAL days (which is where leave is
// recorded, one day at a time, from the timesheet UI) are preserved untouched.
// It never fails on a missing public-holiday calendar — holidays are marked by
// hand in the timesheet.
func Generate(input Input) (Result, error) {
	manual := map[string]Day{}
	for _, day := range input.ExistingManual {
		if day.Source == "MANUAL" {
			manual[day.Date] = day
		}
	}
	holiday := map[string]Holiday{}
	for _, item := range input.Holidays {
		holiday[item.Date.Format("2006-01-02")] = item
	}
	// A non-workday within an assigned schedule is paid weekly rest for a
	// monthly-salaried employee, so it must count toward the paid/SGK day
	// total. Value it at a normal workday. With no schedule assigned we cannot
	// tell rest from work, so nothing is prefilled.
	restMinutes := 0
	for _, sd := range input.Schedule {
		if sd.PlannedMinutes > restMinutes {
			restMinutes = sd.PlannedMinutes
		}
	}
	last := time.Date(input.Year, input.Month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	days := make([]Day, 0, last)
	for number := 1; number <= last; number++ {
		date := time.Date(input.Year, input.Month, number, 0, 0, 0, 0, time.UTC)
		key := date.Format("2006-01-02")
		if existing, ok := manual[key]; ok {
			days = append(days, existing)
			continue
		}
		if date.Before(dayOnly(input.EmploymentFrom)) || (input.EmploymentTo != nil && date.After(dayOnly(*input.EmploymentTo))) {
			continue
		}
		planned := input.Schedule[date.Weekday()].PlannedMinutes
		day := Day{Date: key, Source: "GENERATED", PlannedMinutes: planned, WorkedMinutes: planned}
		if item, ok := holiday[key]; ok {
			minutes := planned
			if item.HalfDay {
				minutes /= 2
			}
			day.PublicHolidayMinutes = minutes
			day.WorkedMinutes = 0
		} else if planned == 0 && restMinutes > 0 {
			day.WeekRestMinutes = restMinutes
		}
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	payload, _ := json.Marshal(days)
	sum := sha256.Sum256(payload)
	return Result{Days: days, Checksum: hex.EncodeToString(sum[:])}, nil
}
func dayOnly(value time.Time) time.Time {
	y, m, d := value.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
