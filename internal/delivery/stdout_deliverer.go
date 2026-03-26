package delivery

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/core"
)

// StdoutDeliverer writes reply text to an io.Writer (defaults to stdout).
type StdoutDeliverer struct {
	Writer io.Writer
	mu     sync.Mutex
}

// Deliver writes the reply text to the configured writer.
func (d *StdoutDeliverer) Deliver(_ context.Context, kind core.ReplyKind, payload core.ReplyPayload, _ core.DeliveryTarget) error {
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return nil
	}
	writer := d.Writer
	if writer == nil {
		writer = os.Stdout
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := fmt.Fprintln(writer, text)
	return err
}
