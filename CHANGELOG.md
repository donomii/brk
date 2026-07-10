# Changelog

## Unreleased

- Scoped duplicate suppression to source endpoints and validated acknowledgement endpoints.
- Made retry-cache updates atomic with acknowledgement handling.
- Delayed acknowledgements until new messages enter the application queue.
- Enforced the existing 32,768-byte encoded packet limit and added oversized-packet diagnostics.
- Rejected invalid retry messages before caching them.
- Copied `SendMessage` payloads and closed incoming queues during shutdown.
- Added local STUN, peer-collision, retry-race, packet-boundary, ownership, and lifecycle tests.
- Added context-managed UDP and retry servers with `Close`, `Done`, `LocalAddress`, and stats.
- Added configurable retry behavior through `RetryConfig`.
- Added duplicate-cache expiry and max retry attempts.
- Added optional STUN external-address discovery.
- Added localhost and lossy retry demos.
- Added Go module metadata, tests, race-test coverage, and GitHub Actions.

## v0.1.0

- Recommended first release tag for the retry-config, stats, context-server, STUN, and demo API set.
