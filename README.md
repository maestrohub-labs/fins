# fins

A Go client library for the Omron FINS protocol.

This is a MaestroHub fork of [`github.com/xiaotushaoxia/fins`](https://github.com/xiaotushaoxia/fins)
(itself derived from `github.com/l1va/gofins`, © 2018 l1va, MIT). See `NOTICE.md`
for the full attribution chain.

## Install

```
go get github.com/maestrohub-labs/fins@v0.1.0-mh.1
```

Requires Go 1.25 or later.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/maestrohub-labs/fins"
)

func main() {
	ctx := context.Background()

	local := fins.NewUDPAddress("", 0, 0, 2, 0)
	plc := fins.NewUDPAddress("192.168.1.100", 9600, 0, 1, 0)

	c, err := fins.NewUDPClient(ctx, local, plc)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	// Read 10 words from DM area, word-address 100.
	words, err := c.ReadWords(ctx, fins.AreaDMWord, 100, 10)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(words)

	// Large contiguous reads chunk automatically (1 FINS frame caps at
	// MaxWordsPerFrame words; batches just stitch them together).
	bulk, err := c.ReadWordsBatch(ctx, fins.AreaDMWord, 0, 5000)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(bulk)) // 5000

	// EM (Expansion Memory) banks 0..12.
	em0, _ := fins.EMBank(0, false)
	_, _ = c.ReadWords(ctx, em0, 0, 100)
}
```

## Features beyond upstream

- **`context.Context` on every public method** — cancellation and
  deadlines compose with your request handling. Calling the method with
  a pre-cancelled ctx short-circuits without any UDP traffic.
- **Typed `MemoryArea`** — compile-time check against passing a word
  area code where a bit area is required, and vice versa. Eighteen
  predefined `Area*` values plus `EMBank(bank, bitLevel)` for EM banks.
- **Batch helpers** — `ReadWordsBatch` / `ReadBytesBatch` chunk across
  FINS frames transparently.
- **`log/slog` integration** — inject a `*slog.Logger` via
  `SetSlogLogger`; internal log sites emit structured attrs
  (`area`, `addr`, `count`, `endCode`, …).
- **Terminal `Close()`** — once closed, a client cannot reopen; all
  subsequent calls return `ClientClosedError` promptly. In-flight
  reads unblock within milliseconds of `Close()`.
- **`Stats()`** — in-flight requests, lifetime requests, lifetime
  timeouts. Cheap to call, concurrent-safe, still callable after Close.
- **Simulator correctness** — the bundled `UDPServerSimulator` now
  indexes DM memory by FINS word / bit index (upstream used raw byte
  offsets, which happened to work only when reads and writes used the
  same addresses).

## Migrating from upstream

```go
// Upstream (github.com/xiaotushaoxia/fins):
c, _ := fins.NewUDPClient(local, plc)
words, _ := c.ReadWords(fins.MemoryAreaDMWord, 100, 10)
c.Close()

// MaestroHub fork:
ctx := context.Background()
c, _ := fins.NewUDPClient(ctx, local, plc)
words, _ := c.ReadWords(ctx, fins.AreaDMWord, 100, 10)
_ = c.Close()
```

Every `Read*`, `Write*`, `SetBit`, `ResetBit`, `ToggleBit`, and
`ReadClock` call gains a leading `ctx context.Context`, and the area
parameter becomes a `fins.MemoryArea` instead of a raw `byte`. The old
`MemoryArea*` byte constants are kept with `// Deprecated:` comments so
pattern-matching code keeps compiling.

## Remotes

This repo tracks both its fork base and its own history:

```
origin    git@github.com:maestrohub-labs/fins.git     (MaestroHub fork)
upstream  https://github.com/xiaotushaoxia/fins.git   (fork base, read-only)
```

## License

MIT — see `LICENSE` (preserves the upstream `© 2018 l1va` notice) and
`NOTICE.md` for full attribution.
