# Changelog

All notable changes to this fork are recorded here. Dates are UTC.

The format is loosely based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [SemVer](https://semver.org/spec/v2.0.0.html)
with a `-mh.N` pre-release suffix to disambiguate from any future upstream
tags.

## [v0.1.0-mh.3] — 2026-04-20

### Fixed

- `SetByteOrder(binary.LittleEndian)` no longer panics when called after
  `NewUDPClient`. The client's internal byte-order field was an
  `atomic.Value`, which locks in the first concrete type it sees;
  `NewUDPClient` primed it with `binary.BigEndian` (concrete type
  `binary.bigEndian`), so any later store of `binary.LittleEndian`
  (concrete type `binary.littleEndian`) panicked with
  `sync/atomic: store of inconsistently typed value into Value`.
  The field is now `atomic.Pointer[binary.ByteOrder]`, which is
  type-safe for interface values. No API changes.

## [v0.1.0-mh.2] — 2026-04-17

### Fixed

- `ReadBits` no longer panics with an index-out-of-range when the PLC
  returns a response whose data section is shorter than the requested
  bit count. Added a length guard that returns `ResponseLengthError`
  instead. Reachable in practice on misbehaving firmware, packet
  corruption, or when `SetIgnoreErrorCodes` is configured and the PLC
  returns an ignored error code with a truncated payload.
  `ReadBytes` already had this check; `ReadBits` did not.

## [v0.1.0-mh.1] — 2026-04-17

First MaestroHub release. Fork base: `github.com/xiaotushaoxia/fins`
at tag `v0.0.2` (commit `dbb9952`).

### Breaking changes

- Every public method on `UDPClient` takes `ctx context.Context` as its
  first argument, including `NewUDPClient`. Cancelling the caller's ctx
  unblocks the call promptly and returns `ctx.Err()`; cancelling the ctx
  passed to `NewUDPClient` tears the client down (same effect as `Close()`).
- Read/Write/Bit methods take a typed `MemoryArea` instead of a raw
  `byte`. Eighteen predefined `Area*` values cover the full Omron FINS
  area code set; use `EMBank(bank, bitLevel)` for Expansion Memory banks
  (0..12). The old `MemoryArea*` byte constants are preserved with
  `// Deprecated:` comments for source-level compatibility.
- `Close()` is now terminal and returns `error` (always `nil` today).
  Calls after `Close()` return `ClientClosedError` without attempting
  to re-dial — upstream's reopen-after-close behaviour was a latent bug.

### Added

- `ReadWordsBatch` / `ReadBytesBatch` — transparent chunking across
  FINS frames (per-frame cap exposed as `MaxWordsPerFrame = 999`).
  Requests that overflow the uint16 address space are rejected up
  front rather than silently wrapping.
- `SetSlogLogger(*slog.Logger)` — injectable structured logging.
  Internal log sites emit at Debug (reads, writes, ignored end codes,
  packet dumps) and Warn (unknown end codes, socket errors). A nil
  argument resets to `slog.Default()`. `SetReadPacketErrorLogger` is
  preserved as a deprecated shim that routes the legacy Printf-style
  Logger through slog.
- `Stats() Stats` — three lightweight counters: `InFlightRequests`,
  `LifetimeRequests`, `LifetimeTimeouts`. Safe to call concurrently
  and after Close.
- `EMBank(bank int, bitLevel bool) (MemoryArea, error)` — typed
  access to EM banks 0..12 (bit codes 0x20..0x2C, word codes 0xA0..0xAC).

### Fixed

- `UDPServerSimulator` now indexes DM memory by FINS word/bit index
  rather than by raw byte offset, matching the protocol. Upstream's
  own tests only worked because reads and writes happened to use
  matching (mis-)addresses; batch reads at non-zero offsets exposed
  the mismatch.
- `handleReadError` no longer concatenates `err.Error()` into a
  printf format string (would crash on `%` in the message).

### Notes

- `SetTimeoutMs` is preserved as the fallback timeout when the caller's
  ctx has no deadline. When both are set, the tighter of the two wins.
- Package name remains `fins`; import path is now
  `github.com/maestrohub-labs/fins`.
- Breaking-change migration: see README "Migrating from upstream".
