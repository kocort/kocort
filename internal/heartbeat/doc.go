// Package heartbeat implements the autonomous heartbeat scheduler that
// enables agents to wake periodically for proactive behaviors.
//
// Future home of:
//   - HeartbeatRunner and lifecycle management
//   - Heartbeat event dispatch
//   - Wake condition evaluation
//   - Heartbeat delivery pipeline
//
// Design: The heartbeat system runs as a background goroutine,
// evaluating wake conditions on a configurable schedule and
// triggering agent runs when conditions are met.
package heartbeat
