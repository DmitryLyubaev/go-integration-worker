# Go integration worker

Bounded local portfolio demonstration, built from scratch with synthetic sales
exports. Go, PostgreSQL, HTTP and local OpenTelemetry traces. This is not a
production service or a throughput benchmark. No paid or cloud resources.

## Local setup

Windows uses the existing Ubuntu WSL2 Docker engine. Keep that distribution running
in an owned foreground session while commands execute; do not shut down shared WSL.
No global Go install is needed. On Linux, use the same pinned Compose images.

```powershell
./scripts/run.ps1 up -d
./scripts/run.ps1 exec -T tooling go test -p 1 ./...
```

The module/build caches and database use project-owned `p27-local` volumes.
PostgreSQL binds only to loopback port 5527. Roles and passwords are intentionally
public local-demo credentials, valid only for disposable synthetic data; do not
reuse them externally. Worker and receiver roles own separate schemas. Admin access
is restricted to local initialization, tests and independent evidence queries.

Use `./scripts/run.ps1 stop` to stop owned services while retaining evidence/data.
Do not prune shared Docker resources. Raw artifacts, logs, local environment and
worktrees are ignored. See the [measured local report](docs/local-report.md) and
[sanitized trace example](docs/trace-example.json).

## Commands and verification

Build the CLI inside tooling, then run `build/p27 enqueue /workspace/path/to/event.json`,
`build/p27 status [ID]`, `build/p27 worker`, or `build/p27 reconcile ID` through
`scripts/run.ps1 exec -T tooling`. Start `build/p27 receiver` in a separate owned
terminal. The receiver binds container-loopback 8127; the demonstration has no
browser interface or remote listener. Reconciliation reads a receipt, never resends.

```powershell
./scripts/check.ps1
# Canonical demonstration requires a clean committed source tree:
./scripts/demo.ps1
```

All checks require the real database and run race detection on Linux. Unit-only
`./scripts/check.ps1 -Profile Unit` checks event parsing, and is not full verification.
The demo creates a fresh owned database, runs twelve frozen event IDs once, preserves
timestamped evidence and stops owned containers in finally. A failed run remains
separate; the script does not automatically repeat it. Review the recorded result
before deciding whether a corrected run is warranted.

First measured run on 1 November 2026: A1-A8 passed, twelve IDs, twenty worker
reservations, nineteen worker POSTs plus three identity probes, ten receiver effects.
Runtime including container cleanup was 24.166 seconds; one correctness sample.
At-least-once sends and receiver-side atomic deduplication explain the result.
This is not a claim of exactly-once network delivery or generic webhook replay safety.
