# TODO

Remaining roadmap items, roughly ordered by payoff.

## Features

- [x] Large-message fragmentation and reassembly, so callers stop caring about datagram size limits.
- [x] Reject a fragmented send before dispatch unless the pending queue can hold its complete fragment group.
- [x] Opt-in per-peer ordered delivery (hold-back queue) for FIFO delivery without switching to TCP.

## Repo

- [x] Windows runner in the CI matrix; UDP socket behavior differs enough to be worth testing.
- [x] Terminal recording (GIF or asciinema) of the lossy demo embedded in the README; visible retransmission is the strongest pitch.
- [x] Native deterministic fuzz targets for the wire and STUN parser boundaries, covering valid round trips plus arbitrary, truncated, length-corrupt, and unknown-field input without panics or unbounded allocation.
