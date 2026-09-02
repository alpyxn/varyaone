// Package schedule contains effective work-schedule rules.
package schedule

import (
	"errors"
	"time"
)

var ErrInvalidScheduleDay = errors.New("INVALID_WORK_SCHEDULE_DAY")

type Day struct {
	Weekday        time.Weekday
	IsWorkday      bool
	StartsAt       time.Duration
	EndsAt         time.Duration
	EndsNextDay    bool
	BreakMinutes   int
	PlannedMinutes int
}

func (d Day) Validate() error {
	if !d.IsWorkday {
		if d.PlannedMinutes != 0 || d.EndsNextDay {
			return ErrInvalidScheduleDay
		}
		return nil
	}
	if d.StartsAt < 0 || d.StartsAt >= 24*time.Hour || d.EndsAt < 0 || d.EndsAt >= 24*time.Hour || d.BreakMinutes < 0 {
		return ErrInvalidScheduleDay
	}
	duration := d.EndsAt - d.StartsAt
	if d.EndsNextDay {
		if d.EndsAt > d.StartsAt {
			return ErrInvalidScheduleDay
		}
		duration += 24 * time.Hour
	} else if duration <= 0 {
		return ErrInvalidScheduleDay
	}
	minutes := int(duration/time.Minute) - d.BreakMinutes
	if minutes <= 0 || minutes != d.PlannedMinutes {
		return ErrInvalidScheduleDay
	}
	return nil
}
