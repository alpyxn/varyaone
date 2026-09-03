package timesheet

import (
	"testing"
	"time"
)

func weekdaySchedule() map[time.Weekday]ScheduleDay {
	return map[time.Weekday]ScheduleDay{
		time.Monday: {480}, time.Tuesday: {480}, time.Wednesday: {480},
		time.Thursday: {480}, time.Friday: {480},
	}
}

func paidDayCount(days []Day) int {
	paid := 0
	for _, d := range days {
		if d.WorkedMinutes > 0 || d.PaidLeaveMinutes > 0 || d.PublicHolidayMinutes > 0 || d.WeekRestMinutes > 0 {
			paid++
		}
	}
	return paid
}

// A public holiday landing on a weekly-rest day used to produce a day with every
// bucket at zero, which payroll reads as unrecorded and prorates the wage down.
func TestGeneratedHolidayOnRestDayStaysPaid(t *testing.T) {
	res, err := Generate(Input{
		Year: 2026, Month: time.January,
		EmploymentFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Schedule:       weekdaySchedule(),
		Holidays:       []Holiday{{Date: time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)}}, // a Sunday
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := paidDayCount(res.Days); got != 31 {
		t.Fatalf("paid days = %d, want 31 (every day of a fully employed month is paid)", got)
	}
}

// With no schedule assigned the generator must still value weekly rest, so a
// month never comes back with zero paid days.
func TestGeneratedMonthIsNeverEntirelyUnpaid(t *testing.T) {
	res, err := Generate(Input{
		Year: 2026, Month: time.April,
		EmploymentFrom: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := paidDayCount(res.Days); got != 30 {
		t.Fatalf("paid days = %d, want 30", got)
	}
}

func TestWeekRestDayCarriesPaidMinutes(t *testing.T) {
	d := bucketsForKind("WEEK_REST", 480, "")
	if d.WeekRestMinutes != 480 {
		t.Fatalf("week rest minutes = %d, want 480: a rest day with no minutes reads as unrecorded to payroll", d.WeekRestMinutes)
	}
}

func TestHalfDayHolidayKeepsTheWorkedHalf(t *testing.T) {
	res, _ := Generate(Input{
		Year: 2026, Month: time.January,
		EmploymentFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Schedule:       weekdaySchedule(),
		Holidays:       []Holiday{{Date: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), HalfDay: true}}, // a Friday
	})
	for _, d := range res.Days {
		if d.Date != "2026-01-02" {
			continue
		}
		if d.PublicHolidayMinutes != 240 || d.WorkedMinutes != 240 {
			t.Fatalf("half-day holiday = %+v, want 240 worked + 240 holiday", d)
		}
		return
	}
	t.Fatal("half-day holiday not generated")
}
