// Package delivery manages outbound message delivery including
// the delivery queue, replay mechanism, reply dispatching,
// block reply pipeline, and outbound hooks/payload formatting.
//
// Replies are dispatched through a pipeline that supports
// queueing, deduplication, hook processing, and retry on failure.
package delivery
