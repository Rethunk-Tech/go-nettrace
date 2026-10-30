<h1 align="center">go-nettrace</h1>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27.3+-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

---

Reads .NET EventPipe NetTrace streams into resolved CPU-sample stacks and attaches to a running .NET process over its diagnostics IPC. Package `nettrace`, module `github.com/Rethunk-Tech/go-nettrace`.

`Parse` turns a NetTrace V4/V5 stream (what `dotnet-trace` and the runtime's diagnostics IPC write) into thread
samples whose stack frames are named from the trace's own JIT, loader and rundown events, so no symbol files are
needed. `StartTracing` starts such a stream from a running .NET process, with nothing but the standard library.

## Quick start

```bash
go get github.com/Rethunk-Tech/go-nettrace
```

A worked example and the contracts: [HUMANS.md](HUMANS.md).

## Features

- Frames resolve to method, declaring type and assembly by address.
- Samples carry a 64-bit thread ID and whether the thread was in managed code.
- Newer or unknown formats (NetTrace V6) are refused with `ErrUnsupportedVersion`, never guessed at.
- Finds a process's diagnostics socket or named pipe, including a containerised process's `TMPDIR` on Linux.
- Every call is bound to its `context.Context`, and `Stop` never outlives it.
- Standard library only.

## Documentation

| Doc | Audience |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Running and using the package |
| [AGENTS.md](AGENTS.md) | Internals, invariants, and why |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Process — commits, tests, docs |
| [SECURITY.md](SECURITY.md) | Vulnerability disclosure |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

## License

MIT — see [LICENSE](LICENSE). © 2026 Rethunk.Tech, LLC.
