# Security

## Supported versions

The latest tagged release. There are no long-lived release branches, so a fix ships in the next tag
rather than being backported.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting** on this repository (Security → Report a
vulnerability). It stays private until a fix ships.

Please do not open a public issue for a suspected vulnerability first.

## What is in scope

- **`Parse` input.** A crafted nettrace must not panic or loop forever. Memory is proportional to the input but
  not a small multiple of it: a compressed sample event is 2 bytes on the wire and a 48-byte `Sample` once decoded,
  and a stream of nothing else measured 554 times its size in total allocation (2,000,455 bytes in, 1,108,771,864
  allocated, 1,000,000 samples out). The caller therefore bounds the reader to the size it can afford to hold 600
  times over. Block, stack and payload counts are checked against the bytes that remain before they size an
  allocation. `FuzzParse` covers it. `Parse` reads the whole stream, so the caller bounds an untrusted reader.
- **IPC replies.** A hostile or broken peer on the diagnostics endpoint must not make `StartTracing` or `Stop` panic,
  allocate beyond one 64 KiB frame, or outlive its context.
- **Endpoint discovery.** On Unix only a socket (not a link to one) owned by the user who owns the target process is
  dialed, and the process's own `TMPDIR` is searched before the shared temp directory, so a socket another user
  plants in a sticky `/tmp` is never connected to. The bytes copied to the writer are capped by
  `Config.MaxStreamBytes` (`ErrStreamTooLarge`). The Windows pipe name carries no owner check.

## What is out of scope

- Which process to attach to. A diagnostics endpoint is a same-user, same-host channel: attaching to it lets the
  caller read that process's runtime events, which is the purpose.
- Memory use proportional to the size of a stream the caller chose to read.
- The content of the writer passed to `StartTracing`: the bytes are the runtime's, unparsed.
