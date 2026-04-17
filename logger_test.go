package fins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogger(t *testing.T) {
	stdoutLoggerInstance.Printf("aa %d", 1)
}

// lockedBuffer is a concurrent-safe io.Writer wrapping a bytes.Buffer.
// Needed because slog.JSONHandler.Handle may be invoked from multiple
// goroutines (read loop, caller goroutine) in race tests.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (lb *lockedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.Write(p)
}

func (lb *lockedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.String()
}

// TestSetSlogLogger_Captured injects a JSON handler at Debug level and
// verifies that a read emits at least one structured record carrying the
// area attribute. Proves the injected logger is actually reached by
// internal code paths.
func TestSetSlogLogger_Captured(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	lb := &lockedBuffer{}
	h := slog.NewJSONHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})
	c.SetSlogLogger(slog.New(h))

	// Write a pattern and read it back to generate log events.
	err := c.WriteWords(context.Background(), AreaDMWord, 0, []uint16{1, 2, 3})
	assert.Nil(t, err)
	_, err = c.ReadWords(context.Background(), AreaDMWord, 0, 3)
	assert.Nil(t, err)

	out := lb.String()
	// At least one line should carry an area attribute set to "DM".
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.NotEmpty(t, lines, "expected at least one structured record")

	found := false
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["area"] == "DM" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected at least one slog record with area=\"DM\", got:\n%s", out)
}

// TestSetSlogLogger_Nil verifies nil argument does not panic and falls
// back to slog.Default.
func TestSetSlogLogger_Nil(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	assert.NotPanics(t, func() { c.SetSlogLogger(nil) })

	// A read should still work using slog.Default().
	err := c.WriteWords(context.Background(), AreaDMWord, 0, []uint16{1})
	assert.Nil(t, err)
}

// TestSetReadPacketErrorLogger_LegacyShim verifies the deprecated
// Printf-style setter still receives messages, routed via slog.
func TestSetReadPacketErrorLogger_LegacyShim(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	rec := &capturingLogger{}
	c.SetReadPacketErrorLogger(rec)
	// Emit a record ourselves via the package's shim path.
	c.printFinsPacketError("test %s %d", "legacy", 42)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Contains(t, rec.messages, "test legacy 42")
}

// TestDeprecated_Logger_NilArg verifies passing nil to the legacy setter
// resets to slog.Default() without panicking.
func TestSetReadPacketErrorLogger_Nil(t *testing.T) {
	var c commLogger
	assert.NotPanics(t, func() { c.SetReadPacketErrorLogger(nil) })
	assert.NotNil(t, c.logger())
}

// TestShowPacket_RoutesToSlog verifies printPacket emits Debug records
// with dir and bytes attributes when the flag is on.
func TestShowPacket_RoutesToSlog(t *testing.T) {
	lb := &lockedBuffer{}
	h := slog.NewJSONHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})
	var c commLogger
	c.SetSlogLogger(slog.New(h))
	c.SetShowPacket(true)

	c.printPacket("write", []byte{0x01, 0x02, 0x03})

	var rec map[string]any
	assert.Nil(t, json.Unmarshal([]byte(strings.TrimSpace(lb.String())), &rec))
	assert.Equal(t, "write", rec["dir"])
	assert.Equal(t, "01 02 03", rec["bytes"])
}

// capturingLogger is a test Logger that appends every Printf into a slice.
type capturingLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *capturingLogger) Printf(f string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, strings.TrimSpace(fmt.Sprintf(f, args...)))
}
