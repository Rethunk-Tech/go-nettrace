# Security

## Supported versions

The latest tagged release. There are no long-lived release branches, so a fix ships in the next tag
rather than being backported.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting** on this repository (Security → Report a
vulnerability). It stays private until a fix ships.

Please do not open a public issue for a suspected vulnerability first.

## What is in scope

- **`Parse` input.** A crafted nettrace must not panic, loop forever or allocate more than a small multiple of its
  own size. Block, stack and payload counts are checked against the bytes that remain before they size an
  allocation. `FuzzParse` covers it. `Parse` reads the whole stream, so the caller bounds an untrusted reader.
- **IPC replies.** A hostile or broken peer on the diagnostics endpoint must not make `StartTracing` or `Stop` panic,
  allocate beyond one 64 KiB frame, or outlive its context.

## What is out of scope

- Which process to attach to. A diagnostics endpoint is a same-user, same-host channel: attaching to it lets the
  caller read that process's runtime events, which is the purpose.
- Memory use proportional to the size of a stream the caller chose to read.
- The content of the writer passed to `StartTracing`: the bytes are the runtime's, unparsed.
