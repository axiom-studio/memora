# Security policy

## Supported versions

Memora Core is pre-1.0. Security fixes are applied to:

| Version | Supported |
|---------|-----------|
| `main` (HEAD) | ✅ |
| Latest tagged minor (e.g. `v0.1.x`) | ✅ |
| Previous minor | best-effort backport for critical CVEs only |
| Anything older | ❌ |

Once we cut `v1.0`, the LTS branch (every 4th minor — see PRD #297 §10)
gets a 12-month security-fix window.

## Reporting a vulnerability

**Do NOT open public GitHub issues for security findings.**

Email **security@axiomstudio.ai** with:

- A description of the vulnerability.
- Steps to reproduce, including any required configuration.
- An assessment of the impact (data loss, RCE, privilege escalation,
  denial of service, information disclosure).
- Optional: proposed remediation or proof-of-concept.

We aim to respond within **3 business days** with an acknowledgement and
a remediation timeline.

## Disclosure process

- Reports are triaged by the Memora maintainers.
- We aim to ship a fix in **30 days** for high-severity findings, **60
  days** for medium, **90 days** for low — measured from triage to released
  fix on `main`.
- Once a fix is released, we publish a [GitHub Security Advisory](https://github.com/axiom-studio/memora/security/advisories)
  with details and CVE assignment when applicable.
- Reporters are credited in the advisory unless they request anonymity.

## In-scope

- The `memora-core` server binary and its adapters.
- The `memora-cli` binary.
- The bundled MCP server.
- Schema migrations.
- CI workflows and release artifacts (binaries, Docker image, Helm chart).

## Out of scope

- Third-party adapter implementations (report to the adapter author).
- Deployment misconfigurations on the operator's end (multi-tenant flag
  misuse, exposed admin keys, weak TLS settings — these are documented
  in the Operations guide).
- DoS against an unauthenticated endpoint when authentication is required
  by spec.
- Findings in pre-release / unreleased code that ships within 7 days
  of the report (we'll already fix it in the release).

## Encryption

If you prefer to encrypt your report, our PGP key is published at
[https://axiomstudio.ai/.well-known/pgp-key.asc](https://axiomstudio.ai/.well-known/pgp-key.asc)
(placeholder — final key gets published with v0.1).
