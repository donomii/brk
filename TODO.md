# TODO

Remaining roadmap items, roughly ordered by payoff.

## Features

- [x] Large-message fragmentation and reassembly, so callers stop caring about datagram size limits.
- [ ] Opt-in per-peer ordered delivery (hold-back queue) for FIFO delivery without switching to TCP.

## Repo

- [ ] Windows runner in the CI matrix; UDP socket behavior differs enough to be worth testing.
- [ ] Terminal recording (GIF or asciinema) of the lossy demo embedded in the README; visible retransmission is the strongest pitch.
