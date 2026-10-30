# HUMANS.md

Run and use `go-nettrace`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
go get github.com/Rethunk-Tech/go-nettrace
```

Read a trace file and print the hottest innermost method:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/Rethunk-Tech/go-nettrace"
)

func main() {
	f, err := os.Open("trace.nettrace")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	trace, err := nettrace.Parse(f)
	if err != nil {
		log.Fatal(err)
	}
	counts := map[string]int{}
	for _, sample := range trace.Samples {
		for _, frame := range sample.Frames {
			if frame.Method != "" {
				counts[frame.Type+"."+frame.Method]++
				break
			}
		}
	}
	best, most := "", 0
	for name, n := range counts {
		if n > most {
			best, most = name, n
		}
	}
	fmt.Println(best, most)
}
```

Sample a running .NET process into a buffer, then read it back:

```go
var buf bytes.Buffer
session, err := nettrace.StartTracing(ctx, pid, nettrace.Config{}, &buf)
if err != nil {
	return err // errors.Is(err, nettrace.ErrNoEndpoint): the process has no endpoint yet, retry
}
time.Sleep(5 * time.Second)
if err := session.Stop(ctx); err != nil {
	return err
}
trace, err := nettrace.Parse(&buf)
```

## Contracts

| Behaviour | Contract |
| --- | --- |
| `Parse` | Reads the whole stream into memory; wrap untrusted input in `io.LimitReader`. `ErrNotNettrace` without the magic, `ErrUnsupportedVersion` for V6 or a Trace object needing a newer reader, `ErrNoEvents` for a stream with no blocks. |
| `Trace.Samples` | Ordered by timestamp, in nanoseconds. Samples on one stack share one `Frames` slice: do not modify it. |
| `Frame` | An address no method-load event covers keeps its `IP` with empty names. |
| `Sample.Managed` | False for a thread in native code. |
| `Config` | A zero field selects its default: sample profiler plus runtime loader and JIT events, 256 MB buffer, verbose level, rundown on. |
| `StartTracing` | Wraps `ErrNoEndpoint` (and `os.ErrNotExist`) while the process has no endpoint; a refused request is a `*RuntimeError`. Context cancellation before the reply returns `ctx.Err()`. |
| `Session.Stop` | Idempotent and safe from several goroutines. Never outlives its context. Nothing reaches the writer after it returns. |
| Writer | Written from a goroutine until the stream ends; the package creates no files. |
| Platforms | Unix socket on Linux and macOS, named pipe on Windows. Only the Linux container lookup (`/proc/<pid>/root`) is Linux specific. |
| Formats | NetTrace V4/V5 only. The runtime streams V4 over IPC. |

## Requirements

Go 1.27.3 or newer, plus `lefthook` and `gitleaks` on PATH if you want the git hooks.

```bash
lefthook install
```

That is per clone; git does not commit hooks. Pre-commit scans staged changes for secrets, and
pre-push runs `gate`.

## Verify

```bash
gate
```
