package timesheet

import (
	"testing"
	"time"
)

func TestGeneratePreservesManualAndIsDeterministic(t *testing.T) {
	input := Input{Year: 2026, Month: time.February, EmploymentFrom: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Schedule: map[time.Weekday]ScheduleDay{time.Monday: {PlannedMinutes: 480}}, ExistingManual: []Day{{Date: "2026-02-02", Source: "MANUAL", WorkedMinutes: 300}}}
	first, err := Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := Generate(input)
	if first.Checksum != second.Checksum {
		t.Fatal("checksum changed")
	}
	foundManual := false
	for _, day := range first.Days {
		if day.Date == "2026-02-02" && day.Source == "MANUAL" && day.WorkedMinutes == 300 {
			foundManual = true
		}
	}
	if !foundManual {
		t.Fatalf("manual day overwritten: %#v", first.Days)
	}
}

func TestGenerateWithoutCalendarSucceeds(t *testing.T) {
	res, err := Generate(Input{Year: 2026, Month: time.January,
		EmploymentFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Schedule:       map[time.Weekday]ScheduleDay{time.Monday: {PlannedMinutes: 480}}})
	if err != nil {
		t.Fatalf("error=%v", err)
	}
	if len(res.Days) == 0 {
		t.Fatal("expected generated days without a holiday calendar")
	}
}
