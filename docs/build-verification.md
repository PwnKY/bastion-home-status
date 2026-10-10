# Preview build verification

The application and rebuilt Caddy use Go 1.26.9. Builds are generic Linux/amd64 artifacts, use `-trimpath`, and contain no deployment configuration, credentials, database or logs. Caddy is v2.11.7 with x/net v0.61.0 and build tags `nobadger,nomysql,nopgx`.

## Known scanner discrepancy: GO-2026-5932

The binary-mode scanner reported the unmaintained `golang.org/x/crypto/openpgp` family against Caddy's x/crypto module. A module being present does not establish that all its packages are present or reachable.

Verification with the exact Linux target and build tags:

- `go list -deps -tags=nobadger,nomysql,nopgx github.com/caddyserver/caddy/v2/cmd/caddy` contains no OpenPGP package. This package graph is included as `packages.txt` in the Caddy artifact. The release builder fails if this changes.
- Source-mode govulncheck v1.8.0 against that target reports zero affected calls and zero vulnerable imported packages, with one finding only in the module graph.
- The rebuilt binary contains no OpenPGP package symbol strings. Its embedded build information confirms the selected Go, Caddy and dependency versions.
- Both application binaries report no vulnerabilities in binary mode.

**Verdict: FALSE POSITIVE for reachable OpenPGP usage in this specific Caddy build.** Reachability gate fails: there is no linked package implementing the alleged sink, so attacker-controlled HTTP/TLS input cannot reach it. No triggering PoC or security impact is established; cryptographic parameter, bounds and concurrency analysis of an absent package is not applicable. This is not a claim that OpenPGP is safe, nor a blanket exception for other builds. The binary scanner warning is retained, not suppressed. Package or build changes require rechecking.

Reproduce source verification with a native govulncheck executable, using `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, in an isolated module pinned to the versions above. Running `go run` with those environment variables on Windows would instead try to execute a Linux tool.

## Test boundaries

The Linux CI job runs Go unit tests, vet, race, vulnerability checks, frontend unit tests, production build and isolated Chrome checks. Windows race tests currently fail to load their runtime (`0xc0000139`); they are not reported as passing. Browser checks use generated local fixtures, never household devices or a personal Chrome profile. Passing CI is not a substitute for deployment or real-service acceptance tests.
