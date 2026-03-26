package delivery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/session"
)

const (
	deliveryQueueDirName = "delivery-queue"
	deliveryFailedDir    = "failed"

	DeliveryStatusQueued    = "queued"
	DeliveryStatusSending   = "sending"
	DeliveryStatusSent      = "sent"
	DeliveryStatusFailed    = "failed"
	DeliveryStatusPartial   = "partial"
	DeliveryStatusCanceled  = "canceled"
	deliveryQueueVersion    = 1
	defaultReplayBatchSize  = 32
	defaultReplayRetryDelay = 5 * time.Second

	// deliveryQueuedStuckThreshold is the minimum age of a "queued" entry
	// before the replay worker considers it stuck and eligible for replay.
	// Entries younger than this are assumed to be transitioning to "sending"
	// by an active Deliver call. Without this guard, the replay worker can
	// race with an in-progress delivery and send the same message twice.
	deliveryQueuedStuckThreshold = 30 * time.Second

	// deliverySendingStuckThreshold is the minimum time since LastAttemptAt
	// for a "sending" entry before the replay worker retries it. This window
	// must be long enough to accommodate large file uploads that legitimately
	// take tens of seconds. Without this guard the replay worker can duplicate
	// an in-progress file delivery.
	deliverySendingStuckThreshold = 2 * time.Minute
)

// QueuedDelivery represents a queued outbound delivery entry.
type QueuedDelivery struct {
	Version       int                          `json:"version"`
	ID            string                       `json:"id"`
	Kind          core.ReplyKind               `json:"kind"`
	Target        core.DeliveryTarget          `json:"target"`
	Payload       core.ReplyPayload            `json:"payload"`
	Status        string                       `json:"status"`
	AttemptCount  int                          `json:"attemptCount,omitempty"`
	EnqueuedAt    time.Time                    `json:"enqueuedAt"`
	LastAttemptAt time.Time                    `json:"lastAttemptAt,omitempty"`
	NextAttemptAt time.Time                    `json:"nextAttemptAt,omitempty"`
	LastError     string                       `json:"lastError,omitempty"`
	LastMessageID string                       `json:"lastMessageId,omitempty"`
	Results       []core.ChannelDeliveryResult `json:"results,omitempty"`
}

func resolveDeliveryQueueDir(stateDir string) string {
	return filepath.Join(stateDir, deliveryQueueDirName)
}

func resolveDeliveryFailedDir(stateDir string) string {
	return filepath.Join(resolveDeliveryQueueDir(stateDir), deliveryFailedDir)
}

func ensureDeliveryQueueDirs(stateDir string) error {
	if err := os.MkdirAll(resolveDeliveryQueueDir(stateDir), 0o700); err != nil {
		return err
	}
	return os.MkdirAll(resolveDeliveryFailedDir(stateDir), 0o700)
}

// EnqueueDelivery creates and persists a new queued delivery entry.
func EnqueueDelivery(stateDir string, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) (QueuedDelivery, error) {
	if err := ensureDeliveryQueueDirs(stateDir); err != nil {
		return QueuedDelivery{}, err
	}
	id, err := session.RandomToken(12)
	if err != nil {
		return QueuedDelivery{}, err
	}
	entry := QueuedDelivery{
		Version:    deliveryQueueVersion,
		ID:         id,
		Kind:       kind,
		Target:     target,
		Payload:    payload,
		Status:     DeliveryStatusQueued,
		EnqueuedAt: time.Now().UTC(),
	}
	if err := WriteQueuedDelivery(stateDir, entry, false); err != nil {
		return QueuedDelivery{}, err
	}
	return entry, nil
}

func pendingDeliveryPath(stateDir string, id string) string {
	return filepath.Join(resolveDeliveryQueueDir(stateDir), id+".json")
}

func failedDeliveryPath(stateDir string, id string) string {
	return filepath.Join(resolveDeliveryFailedDir(stateDir), id+".json")
}

// WriteQueuedDelivery writes a queued delivery entry to disk.
func WriteQueuedDelivery(stateDir string, entry QueuedDelivery, failed bool) error {
	raw, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	path := pendingDeliveryPath(stateDir, entry.ID)
	if failed {
		path = failedDeliveryPath(stateDir, entry.ID)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadQueuedDelivery reads a single queued delivery entry from disk.
func ReadQueuedDelivery(path string) (QueuedDelivery, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return QueuedDelivery{}, err
	}
	var entry QueuedDelivery
	if err := json.Unmarshal(raw, &entry); err != nil {
		return QueuedDelivery{}, err
	}
	if entry.Version == 0 {
		entry.Version = deliveryQueueVersion
	}
	if strings.TrimSpace(entry.Status) == "" {
		entry.Status = DeliveryStatusQueued
	}
	return entry, nil
}

// AckDelivery removes all queue files for the given delivery ID.
func AckDelivery(stateDir string, id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	errA := os.Remove(pendingDeliveryPath(stateDir, id))
	errB := os.Remove(failedDeliveryPath(stateDir, id))
	if errA != nil && !os.IsNotExist(errA) {
		return errA
	}
	if errB != nil && !os.IsNotExist(errB) {
		return errB
	}
	return nil
}

// MarkDeliverySending transitions a queued delivery to the "sending" state.
func MarkDeliverySending(stateDir string, entry QueuedDelivery) error {
	entry.Status = DeliveryStatusSending
	entry.AttemptCount++
	entry.LastAttemptAt = time.Now().UTC()
	entry.LastError = ""
	entry.NextAttemptAt = time.Time{}
	_ = os.Remove(failedDeliveryPath(stateDir, entry.ID)) // best-effort cleanup
	return WriteQueuedDelivery(stateDir, entry, false)
}

// FailDelivery marks a delivery as failed and moves it to the failed directory.
func FailDelivery(stateDir string, entry QueuedDelivery, failure string, partial bool) error {
	entry.LastError = strings.TrimSpace(failure)
	entry.Status = DeliveryStatusFailed
	if partial {
		entry.Status = DeliveryStatusPartial
	}
	entry.NextAttemptAt = time.Now().UTC().Add(ComputeDeliveryRetryDelay(entry.AttemptCount))
	if err := WriteQueuedDelivery(stateDir, entry, true); err != nil {
		return err
	}
	if err := os.Remove(pendingDeliveryPath(stateDir, entry.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// CancelDelivery marks a delivery as canceled and moves it to the failed directory.
func CancelDelivery(stateDir string, entry QueuedDelivery) error {
	entry.Status = DeliveryStatusCanceled
	entry.NextAttemptAt = time.Time{}
	if err := WriteQueuedDelivery(stateDir, entry, true); err != nil {
		return err
	}
	if err := os.Remove(pendingDeliveryPath(stateDir, entry.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SentDelivery marks a delivery as sent and removes it from the queue.
func SentDelivery(stateDir string, entry QueuedDelivery, results []core.ChannelDeliveryResult) error {
	entry.Status = DeliveryStatusSent
	entry.Results = append([]core.ChannelDeliveryResult{}, results...)
	if len(results) > 0 {
		entry.LastMessageID = strings.TrimSpace(results[len(results)-1].MessageID)
	}
	if err := WriteQueuedDelivery(stateDir, entry, false); err != nil {
		return err
	}
	return AckDelivery(stateDir, entry.ID)
}

// ComputeDeliveryRetryDelay returns the backoff duration for a given attempt count.
func ComputeDeliveryRetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return defaultReplayRetryDelay
	}
	delay := defaultReplayRetryDelay
	for range attempt - 1 {
		delay *= 2
		if delay >= time.Minute {
			return time.Minute
		}
	}
	return delay
}

// LoadQueuedDeliveries returns all queued delivery entries, optionally including failed ones.
func LoadQueuedDeliveries(stateDir string, includeFailed bool) ([]QueuedDelivery, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, nil
	}
	dirs := []string{resolveDeliveryQueueDir(stateDir)}
	if includeFailed {
		dirs = append(dirs, resolveDeliveryFailedDir(stateDir))
	}
	var out []QueuedDelivery
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			item, err := ReadQueuedDelivery(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].EnqueuedAt.Before(out[j].EnqueuedAt)
	})
	return out, nil
}

// DueQueuedDeliveries returns deliveries that are due for retry on or before the given time.
func DueQueuedDeliveries(stateDir string, now time.Time, limit int) ([]QueuedDelivery, error) {
	if limit <= 0 {
		limit = defaultReplayBatchSize
	}
	items, err := LoadQueuedDeliveries(stateDir, true)
	if err != nil {
		return nil, err
	}
	due := make([]QueuedDelivery, 0, limit)
	for _, item := range items {
		if len(due) >= limit {
			break
		}
		switch item.Status {
		case DeliveryStatusQueued:
			// Only replay entries that have been queued long enough to be
			// considered stuck (i.e. not currently transitioning to "sending"
			// by an active Deliver call). This prevents a race where the replay
			// worker and the original delivery both send the same message.
			if item.EnqueuedAt.IsZero() || now.Sub(item.EnqueuedAt) >= deliveryQueuedStuckThreshold {
				due = append(due, item)
			}
		case DeliveryStatusSending:
			// Only replay entries where the last attempt started long enough
			// ago to be considered stuck. This prevents duplicate sends when
			// the replay worker fires during an in-progress file upload or
			// other slow delivery operation.
			lastAttempt := item.LastAttemptAt
			if lastAttempt.IsZero() {
				lastAttempt = item.EnqueuedAt
			}
			if lastAttempt.IsZero() || now.Sub(lastAttempt) >= deliverySendingStuckThreshold {
				due = append(due, item)
			}
		case DeliveryStatusFailed, DeliveryStatusPartial:
			if item.NextAttemptAt.IsZero() || !item.NextAttemptAt.After(now) {
				due = append(due, item)
			}
		}
	}
	return due, nil
}
