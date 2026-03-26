package infra

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Formatted User Time — Human-readable date/time for system prompts
//
// Produces format: "Wednesday, March 21st, 2026 — 14:30"
// with ordinal day suffixes (1st, 2nd, 3rd, 4th...) and 24h time.
//

// ---------------------------------------------------------------------------

// FormatUserTime formats a time value into a human-readable string for
// inclusion in agent system prompts.
//
// Output format: "Wednesday, March 21st, 2026 — 14:30"
//
// If use12h is true, uses 12-hour format: "Wednesday, March 21st, 2026 — 2:30 PM"
func FormatUserTime(t time.Time, use12h bool) string {
	weekday := t.Weekday().String()
	month := t.Month().String()
	day := t.Day()
	year := t.Year()
	dayStr := fmt.Sprintf("%d%s", day, ordinalSuffix(day))

	var timeStr string
	if use12h {
		hour := t.Hour()
		period := "AM"
		if hour >= 12 {
			period = "PM"
		}
		if hour == 0 {
			hour = 12
		} else if hour > 12 {
			hour -= 12
		}
		timeStr = fmt.Sprintf("%d:%02d %s", hour, t.Minute(), period)
	} else {
		timeStr = fmt.Sprintf("%02d:%02d", t.Hour(), t.Minute())
	}

	return fmt.Sprintf("%s, %s %s, %d — %s", weekday, month, dayStr, year, timeStr)
}

// ordinalSuffix returns the English ordinal suffix for a day number.
// 1→"st", 2→"nd", 3→"rd", 4-20→"th", 21→"st", 22→"nd", 23→"rd", 24-30→"th", 31→"st"
func ordinalSuffix(day int) string {
	if day >= 11 && day <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
	}
}

// FormatUserTimeInTimezone formats the current time in the given IANA timezone.
// Falls back to UTC if the timezone is invalid.
func FormatUserTimeInTimezone(timezone string, use12h bool) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return FormatUserTime(time.Now().In(loc), use12h)
}
