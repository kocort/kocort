package infra

import (
	"strings"
	"testing"
	"time"

	"github.com/kocort/kocort/internal/core"
)

func TestFormatUserTime_24h(t *testing.T) {
	// Wednesday, March 21, 2026, 14:30
	tm := time.Date(2026, time.March, 21, 14, 30, 0, 0, time.UTC)
	got := FormatUserTime(tm, false)
	expect := "Saturday, March 21st, 2026 — 14:30"
	if got != expect {
		t.Fatalf("got %q, want %q", got, expect)
	}
}

func TestFormatUserTime_12h(t *testing.T) {
	tm := time.Date(2026, time.March, 21, 14, 30, 0, 0, time.UTC)
	got := FormatUserTime(tm, true)
	if !strings.Contains(got, "2:30 PM") {
		t.Fatalf("expected 12h format with PM, got %q", got)
	}
}

func TestFormatUserTime_Midnight(t *testing.T) {
	tm := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	got24 := FormatUserTime(tm, false)
	if !strings.Contains(got24, "00:00") {
		t.Fatalf("expected 00:00, got %q", got24)
	}
	got12 := FormatUserTime(tm, true)
	if !strings.Contains(got12, "12:00 AM") {
		t.Fatalf("expected 12:00 AM, got %q", got12)
	}
}

func TestFormatUserTime_Noon(t *testing.T) {
	tm := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)
	got12 := FormatUserTime(tm, true)
	if !strings.Contains(got12, "12:00 PM") {
		t.Fatalf("expected 12:00 PM, got %q", got12)
	}
}

func TestOrdinalSuffix(t *testing.T) {
	tests := []struct {
		day    int
		suffix string
	}{
		{1, "st"}, {2, "nd"}, {3, "rd"}, {4, "th"},
		{10, "th"}, {11, "th"}, {12, "th"}, {13, "th"},
		{14, "th"}, {20, "th"}, {21, "st"}, {22, "nd"},
		{23, "rd"}, {24, "th"}, {30, "th"}, {31, "st"},
	}
	for _, tc := range tests {
		got := ordinalSuffix(tc.day)
		if got != tc.suffix {
			t.Errorf("ordinalSuffix(%d) = %q, want %q", tc.day, got, tc.suffix)
		}
	}
}

func TestFormatUserTimeInTimezone(t *testing.T) {
	// Just verify it doesn't panic and returns non-empty.
	got := FormatUserTimeInTimezone("America/New_York", false)
	if got == "" {
		t.Fatal("expected non-empty")
	}
	if !strings.Contains(got, "—") {
		t.Fatalf("missing em-dash separator: %q", got)
	}

	// Invalid timezone should fallback to UTC.
	got2 := FormatUserTimeInTimezone("Invalid/Zone", false)
	if got2 == "" {
		t.Fatal("expected non-empty for invalid zone")
	}
}

func TestBuildTimePromptSection_IncludesFormattedTime(t *testing.T) {
	// Requires a timezone to produce output.
	section := BuildTimePromptSection(
		core.AgentIdentity{UserTimezone: "UTC"},
		core.AgentRunRequest{},
	)
	if !strings.Contains(section, "## Current Date & Time") {
		t.Fatal("missing header")
	}
	if !strings.Contains(section, "Time zone: UTC") {
		t.Fatal("missing timezone")
	}
	// Should contain formatted time with em-dash.
	if !strings.Contains(section, "—") {
		t.Fatalf("missing formatted time: %q", section)
	}
}
