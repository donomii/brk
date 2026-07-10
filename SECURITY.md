# Security policy

## Supported versions

Security fixes are applied to the latest tagged release.

## Reporting a vulnerability

Please report vulnerabilities privately through [GitHub security advisories](https://github.com/donomii/brk/security/advisories/new). Include affected versions, reproduction details, impact, and any suggested mitigation.

Do not open a public issue for an undisclosed vulnerability. You can expect an initial response within seven days.

## Security scope

HMAC-SHA256 authentication protects version 1 packet integrity and possession of a shared key. It does not encrypt payloads. STUN, hole punching, and keepalives do not replace payload encryption, rendezvous authentication, or TURN relaying.
