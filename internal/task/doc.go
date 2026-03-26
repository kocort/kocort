// Package task provides the scheduled task system including cron-based
// task scheduling, subagent task orchestration, and task lifecycle
// management.
//
// Future home of:
//   - Task scheduler (cron_schedule.go)
//   - Subagent spawn/orchestration (subagent_*.go)
//   - Task registry and lifecycle
//   - Active run queue management
//   - Followup queue management
//
// Design: Tasks are scheduled via cron expressions or event triggers
// and execute as agent runs with configurable concurrency and queuing.
package task
