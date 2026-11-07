# Changelog

Notable changes to `go-nettrace`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-11-07

### Fixed

- `StartTracing` dials only a socket owned by the target process's user, not a link or other file, and searches the process's own `TMPDIR` before the shared temp directory.
- `Config.MaxStreamBytes` (default 1 GiB) caps the stream copied to the writer; more is `ErrStreamTooLarge`.
- `Session.Stop` runs the shared stop under its own 30 second timeout instead of the first caller's context, so one caller's cancellation no longer fails another's.
- `Parse` appends events into one slice and sizes `Samples` once, cutting total allocation for a stream of minimal sample events from 554 to 235 times its size.
- SECURITY.md states `Parse`'s measured memory instead of a small multiple.

## [0.1.0] - 2026-10-29

### Added

- `Parse` reads a NetTrace V4/V5 stream into CPU samples with each stack frame resolved to method, type and
  assembly from the trace's own JIT, loader and rundown events; other formats are refused with `ErrNotNettrace` or
  `ErrUnsupportedVersion`.
- `StartTracing` and `Session.Stop` start and stop an EventPipe session on a running .NET process over its
  diagnostics IPC (Unix socket or Windows named pipe, container-aware on Linux), streaming the nettrace bytes into a
  caller-supplied `io.Writer`; a refused request is a `*RuntimeError`.

[Unreleased]: https://github.com/Rethunk-Tech/go-nettrace/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Rethunk-Tech/go-nettrace/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Rethunk-Tech/go-nettrace/releases/tag/v0.1.0
