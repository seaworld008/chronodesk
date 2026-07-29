package services

import (
	"errors"
	"testing"
	"time"

	"gongdan-system/internal/models"
)

func standardWorkingHours() *models.WorkingHours {
	day := models.TimeRange{Start: "09:00", End: "18:00"}
	return &models.WorkingHours{
		Monday: day, Tuesday: day, Wednesday: day, Thursday: day, Friday: day,
		Timezone: "Asia/Shanghai",
	}
}

func TestAddWorkingTimeUsesConfiguredWindowsAcrossWeekend(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.July, 31, 17, 30, 0, 0, location) // Friday

	deadline, err := (&AutomationService{}).addWorkingTime(
		start,
		2*time.Hour,
		standardWorkingHours(),
		true,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.August, 3, 10, 30, 0, 0, location)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %s, want %s", deadline, want)
	}
}

func TestAddWorkingTimeStartsAtNextWindowAndSkipsConfiguredHoliday(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	hours := standardWorkingHours()
	hours.Holidays = []string{"2026-08-03"}
	start := time.Date(2026, time.August, 3, 8, 0, 0, 0, location)

	deadline, err := (&AutomationService{}).addWorkingTime(
		start,
		time.Hour,
		hours,
		true,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.August, 4, 10, 0, 0, 0, location)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %s, want %s", deadline, want)
	}
}

func TestAddWorkingTimeUsesBusinessTimezoneAndPreservesCallerLocation(t *testing.T) {
	start := time.Date(2026, time.August, 2, 23, 30, 0, 0, time.UTC)
	deadline, err := (&AutomationService{}).addWorkingTime(
		start,
		time.Hour,
		standardWorkingHours(),
		true,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.August, 3, 2, 0, 0, 0, time.UTC)
	if deadline.Location() != time.UTC || !deadline.Equal(want) {
		t.Fatalf("deadline = %s (%s), want %s UTC", deadline, deadline.Location(), want)
	}
}

func TestAddWorkingTimeRejectsInvalidOrEmptySchedules(t *testing.T) {
	tests := []struct {
		name  string
		hours *models.WorkingHours
	}{
		{
			name: "invalid clock",
			hours: &models.WorkingHours{
				Monday: models.TimeRange{Start: "9am", End: "18:00"},
			},
		},
		{
			name: "reverse interval",
			hours: &models.WorkingHours{
				Monday: models.TimeRange{Start: "18:00", End: "09:00"},
			},
		},
		{
			name: "weekend excluded leaves no interval",
			hours: &models.WorkingHours{
				Saturday: models.TimeRange{Start: "09:00", End: "18:00"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&AutomationService{}).addWorkingTime(
				time.Now(),
				time.Hour,
				test.hours,
				true,
				false,
			)
			if !errors.Is(err, ErrInvalidWorkingHours) {
				t.Fatalf("error = %v, want ErrInvalidWorkingHours", err)
			}
		})
	}
}
