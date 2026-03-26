package task

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"

	"github.com/robfig/cron/v3"
)

// NormalizeTaskSchedule fills in missing schedule fields on a TaskRecord.
func NormalizeTaskSchedule(record *core.TaskRecord, now time.Time) error {
	if record == nil {
		return nil
	}
	if record.ScheduleKind == "" {
		switch {
		case !record.ScheduleAt.IsZero():
			record.ScheduleKind = core.TaskScheduleAt
		case record.ScheduleEveryMs > 0:
			record.ScheduleKind = core.TaskScheduleEvery
		case strings.TrimSpace(record.ScheduleExpr) != "":
			record.ScheduleKind = core.TaskScheduleCron
		case record.IntervalSeconds > 0:
			record.ScheduleKind = core.TaskScheduleEvery
			record.ScheduleEveryMs = int64(record.IntervalSeconds) * int64(time.Second/time.Millisecond)
			if record.ScheduleAnchorMs <= 0 {
				record.ScheduleAnchorMs = now.UTC().UnixMilli()
			}
		default:
			record.ScheduleKind = core.TaskScheduleAt
			record.ScheduleAt = nonZeroTime(record.NextRunAt, now)
		}
	}
	if record.ScheduleKind == core.TaskScheduleEvery {
		if record.ScheduleEveryMs <= 0 && record.IntervalSeconds > 0 {
			record.ScheduleEveryMs = int64(record.IntervalSeconds) * int64(time.Second/time.Millisecond)
		}
		if record.ScheduleEveryMs <= 0 {
			return fmt.Errorf("every schedule requires scheduleEveryMs")
		}
		if record.ScheduleAnchorMs <= 0 {
			anchor := now.UTC().UnixMilli()
			if !record.CreatedAt.IsZero() {
				anchor = record.CreatedAt.UTC().UnixMilli()
			}
			record.ScheduleAnchorMs = anchor
		}
		record.IntervalSeconds = int(record.ScheduleEveryMs / int64(time.Second/time.Millisecond))
	}
	if record.ScheduleKind == core.TaskScheduleCron && strings.TrimSpace(record.ScheduleExpr) == "" {
		return fmt.Errorf("cron schedule requires scheduleExpr")
	}
	if record.ScheduleKind == core.TaskScheduleAt && record.ScheduleAt.IsZero() {
		record.ScheduleAt = nonZeroTime(record.NextRunAt, now)
	}
	return nil
}

// ComputeTaskNextRunAt computes the next run time for a task.
func ComputeTaskNextRunAt(record core.TaskRecord, now time.Time) (time.Time, error) {
	now = now.UTC()
	switch record.ScheduleKind {
	case "", core.TaskScheduleAt:
		if record.ScheduleAt.IsZero() {
			return time.Time{}, nil
		}
		if !record.ScheduleAt.After(now) {
			return time.Time{}, nil
		}
		return record.ScheduleAt.UTC(), nil
	case core.TaskScheduleEvery:
		everyMs := record.ScheduleEveryMs
		if everyMs <= 0 && record.IntervalSeconds > 0 {
			everyMs = int64(record.IntervalSeconds) * int64(time.Second/time.Millisecond)
		}
		if everyMs <= 0 {
			return time.Time{}, fmt.Errorf("every schedule requires scheduleEveryMs")
		}
		anchor := record.ScheduleAnchorMs
		if anchor <= 0 {
			anchor = now.UnixMilli()
		}
		if now.UnixMilli() < anchor {
			return time.UnixMilli(anchor).UTC(), nil
		}
		elapsed := now.UnixMilli() - anchor
		steps := (elapsed + everyMs - 1) / everyMs
		if steps < 1 {
			steps = 1
		}
		nextMs := anchor + steps*everyMs
		if nextMs <= now.UnixMilli() {
			nextMs += everyMs
		}
		return time.UnixMilli(nextMs).UTC(), nil
	case core.TaskScheduleCron:
		schedule, err := ParseCronSchedule(record.ScheduleExpr, record.ScheduleTZ)
		if err != nil {
			return time.Time{}, err
		}
		next := schedule.Next(now)
		if next.IsZero() {
			return time.Time{}, nil
		}
		if record.ScheduleStaggerMs > 0 {
			offset := stableCronOffsetMs(record.ID, record.ScheduleStaggerMs)
			next = next.Add(time.Duration(offset) * time.Millisecond)
			if !next.After(now) {
				next = next.Add(time.Second)
			}
		}
		return next.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported task schedule kind %q", record.ScheduleKind)
	}
}

// ParseCronSchedule parses a cron expression with optional timezone.
func ParseCronSchedule(expr, tz string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron expr is required")
	}
	var parser cron.Parser
	fields := len(strings.Fields(expr))
	switch fields {
	case 5:
		parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	case 6:
		parser = cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	default:
		return nil, fmt.Errorf("unsupported cron expr field count %d", fields)
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, err
	}
	location := time.UTC
	if trimmed := strings.TrimSpace(tz); trimmed != "" {
		if loaded, loadErr := time.LoadLocation(trimmed); loadErr == nil {
			location = loaded
		} else {
			return nil, loadErr
		}
	}
	return cronScheduleWithLocation{schedule: schedule, location: location}, nil
}

type cronScheduleWithLocation struct {
	schedule cron.Schedule
	location *time.Location
}

func (c cronScheduleWithLocation) Next(t time.Time) time.Time {
	return c.schedule.Next(t.In(c.location))
}

func stableCronOffsetMs(id string, staggerMs int64) int64 {
	if staggerMs <= 1 {
		return 0
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(id)))
	return int64(binary.BigEndian.Uint32(sum[:4]) % uint32(staggerMs))
}
