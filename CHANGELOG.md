# Changelog

Notable changes to `go-nettrace`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `Parse` reads a NetTrace V4/V5 stream into CPU samples with each stack frame resolved to method, type and
  assembly from the trace's own JIT, loader and rundown events; other formats are refused with `ErrNotNettrace` or
  `ErrUnsupportedVersion`.
- `StartTracing` and `Session.Stop` start and stop an EventPipe session on a running .NET process over its
  diagnostics IPC (Unix socket or Windows named pipe, container-aware on Linux), streaming the nettrace bytes into a
  caller-supplied `io.Writer`; a refused request is a `*RuntimeError`.

[Unreleased]: https://github.com/Rethunk-Tech/go-nettrace/commits/main
