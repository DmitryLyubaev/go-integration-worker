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
worktrees are ignored. A final measured report will distinguish software checks
from actual process-recovery observations; no measured results are claimed yet.
