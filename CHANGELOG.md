# Changelog

Notable changes to `go-nettrace`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `Parse` reads a NetTrace V4/V5 stream into CPU samples with each stack frame resolved to method, type and
  assembly from the trace's own JIT, loader and rundown events; other formats are refused with `ErrNotNettrace` or
  `ErrUnsupportedVersion`.

[Unreleased]: https://github.com/Rethunk-Tech/go-nettrace/commits/main
