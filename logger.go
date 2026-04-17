package fins

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
)

// Logger is the deprecated Printf-style logger interface kept for
// source-level compatibility with upstream code that calls
// SetReadPacketErrorLogger.
//
// Deprecated: pass an *slog.Logger to SetSlogLogger instead.
type Logger interface {
	Printf(string, ...any) // auto new line
}

var stdoutLoggerInstance = &stdoutLogger{}

type stdoutLogger struct{}

func (stdoutLogger) Printf(f string, args ...any) {
	fmt.Println(fmt.Sprintf(f, args...))
}

// commLogger holds the active slog.Logger shared by UDPClient and UDPServer.
// It is the only logging surface in the package; every log call funnels
// through logger() and then into the active *slog.Logger.
type commLogger struct {
	slogLogger atomic.Pointer[slog.Logger]
	showPacket atomic.Bool
}

// SetSlogLogger installs the structured logger used by this client or
// server. A nil argument resets to slog.Default().
func (c *commLogger) SetSlogLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	c.slogLogger.Store(l)
}

// SetReadPacketErrorLogger installs a legacy Printf-style logger. Messages
// are routed through slog and forwarded to the provided Logger.
//
// Deprecated: use SetSlogLogger.
func (c *commLogger) SetReadPacketErrorLogger(l Logger) {
	if l == nil {
		c.slogLogger.Store(slog.Default())
		return
	}
	c.slogLogger.Store(slog.New(&legacyLoggerHandler{inner: l}))
}

// SetShowPacket toggles Debug-level packet-dump logging.
func (c *commLogger) SetShowPacket(show bool) {
	c.showPacket.Store(show)
}

// logger returns the active *slog.Logger, falling back to slog.Default().
func (c *commLogger) logger() *slog.Logger {
	if l := c.slogLogger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// printFinsPacketError is a Printf-style shim for Warn-level packet-handling
// errors. Kept for upstream-parity call sites; new code should prefer
// c.logger().Warn(...) with structured attrs.
func (c *commLogger) printFinsPacketError(f string, args ...any) {
	c.logger().Warn(fmt.Sprintf(f, args...))
}

// printPacket emits a Debug-level structured packet dump when the
// showPacket toggle is enabled.
func (c *commLogger) printPacket(rw string, p []byte) {
	if !c.showPacket.Load() {
		return
	}
	c.logger().Debug("fins packet",
		slog.String("dir", rw),
		slog.String("bytes", fmt.Sprintf("% X", p)),
	)
}

// legacyLoggerHandler adapts a Printf-style Logger into an slog.Handler.
// Attributes are appended to the message as space-separated key=value pairs.
// Log levels and groups are not distinguished — the shim preserves the
// information content but not the structure a real slog backend would.
type legacyLoggerHandler struct {
	inner Logger
	attrs []slog.Attr
}

func (h *legacyLoggerHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *legacyLoggerHandler) Handle(_ context.Context, r slog.Record) error {
	var sb strings.Builder
	sb.WriteString(r.Message)
	for _, a := range h.attrs {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
	}
	r.Attrs(func(a slog.Attr) bool {
		sb.WriteString(" ")
		sb.WriteString(a.Key)
		sb.WriteString("=")
		sb.WriteString(a.Value.String())
		return true
	})
	h.inner.Printf("%s", sb.String())
	return nil
}

func (h *legacyLoggerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	newAttrs = append(newAttrs, h.attrs...)
	newAttrs = append(newAttrs, attrs...)
	return &legacyLoggerHandler{inner: h.inner, attrs: newAttrs}
}

func (h *legacyLoggerHandler) WithGroup(_ string) slog.Handler {
	return h
}
