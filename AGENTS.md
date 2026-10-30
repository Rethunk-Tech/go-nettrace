# AGENTS.md

Package `nettrace` reads .NET EventPipe NetTrace V4/V5 streams into resolved CPU samples, and drives the runtime's
Diagnostic IPC to start and stop a session that produces such a stream. Profilers, launchers and supervisors import
it. It imports only the standard library, and no consumer package.

## Gate

`gate` is the command. Each override in `.gate.toml` is there because a runner
cannot resolve that check from PATH. The `windows-latest` test job is the only place
Windows-only behaviour runs.
`TestLive` runs on both legs and is the only test of the Windows diagnostics endpoint end to end; `TestPipeDisconnectIsEOF`
covers how `transport_windows.go` reads a pipe the runtime has disconnected. The fake-socket tests in `ipc_test.go` are
Unix only. Spin starts its work only when `TestLive` writes a line after the session starts, and the session stops
only after Spin prints its result: a loaded 2-vCPU runner can spend longer than any fixed sleep starting the runtime.

## Shape

- `Parse` reads only what the format specifies. There is no scanner for damaged or unknown layouts: a guessed event
  header returns wrong data that looks right. A stream that is not V4/V5 with the Trace object first is
  `ErrUnsupportedVersion`.
- Events are recognised by (provider, event ID), never by name or payload bytes. The runtime and rundown providers
  number their loader events differently (154 is AssemblyLoad on one, ModuleDCEnd on the other), so one switch on the
  ID alone fills the module and assembly maps with each other's entries.
- A count read from the stream is checked against the bytes that remain before anything is allocated.
- The high bit of an uncompressed event's MetadataId is the IsSorted flag and is masked. The advance to the next
  event is aligned to 4 bytes from the event size, which holds whether a writer counts its padding inside EventSize
  or after it.
- Samples on one stack share one `[]Frame`: a startup trace has about a million samples and few distinct stacks.
- `ThreadID` is 64-bit as in the format; narrowing merges distinct IDs.
- A session writes to the caller's `io.Writer` and creates no file. `Stop` runs the stop once, every caller waits
  under its own context, and it returns only after the copy goroutine exits, so the writer is untouched afterwards.
- Each IPC frame is one `Write`: a runtime that has read the whole frame may hang up, and a second write would fail.
  Only a `0xFF/0x00` reply is a success; `0xFF/0xFF` is a `*RuntimeError`.
- Rundown is on by default: methods JIT-compiled before the session started resolve only from the rundown events.
- The Windows pipe is opened with `os.OpenFile` and a busy pipe is retried until the context ends. A read that fails
  with `ERROR_PIPE_NOT_CONNECTED` is the end of the stream: the runtime ends a session by flushing and disconnecting.

## Files

| File | Holds |
| --- | --- |
| `doc.go` | Package comment |
| `trace.go` | Public trace types, `Parse`, sample extraction |
| `container.go` | FastSerialization header, Trace object and block framing |
| `events.go` | Event headers (compressed and uncompressed), metadata records, stack blocks |
| `runtime.go` | Loader and JIT event decoding, frame resolution, integer helpers |
| `session.go` | `Config`, `Provider`, `StartTracing`, `Session.Stop` |
| `wire.go` | IPC framing, `CollectTracing2` payload, replies, `RuntimeError` |
| `endpoint.go` | Diagnostics endpoint discovery and dialing |
| `transport_unix.go` | Unix socket dial |
| `transport_windows.go` | Named-pipe open with busy retry |
