# Contributing to brk

Thanks for improving `brk`.

## Development

Use Go 1.22 or newer. The standard library is the only runtime dependency.

```sh
./build.sh
./test.sh
./demo.sh
```

Keep changes focused and format Go files with `go fmt ./...`. Coverage must be deterministic and run without Internet access. Update `SPEC.md` when public behavior, packet formats, configuration, or APIs change.

## Compatibility

The legacy packet decoder is retained for compatibility. Changes to protocol version 1 must reject malformed input, preserve the 32,768-byte encoded-packet limit, and document the wire contract in `SPEC.md`.

## Pull requests

Describe the user-visible result, include coverage for new behavior, and confirm the build, race detector, demos, and package documentation succeed locally.
