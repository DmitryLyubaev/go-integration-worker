# Execution record

Native execution, approved by Dmitry on 1 November 2026. Primary initialized at
`1e5190e`; implementation branch `codex/local` in `.worktrees/local`. No remote
repository or public action. The private per-plan ledger has detailed command logs.

| Task | Checkpoint | Evidence |
|---|---|---|
| 1 | cc8e901 | Event parser/enqueue tests observed RED then GREEN, actual PostgreSQL and vet |
| 2 | cd75748 | Receiver transaction/HTTP/fault tests RED then GREEN, Linux race |
| 3 | 73f949d | Claims, stale ownership, sticky ambiguity, genuine blocked-query deadlines RED then GREEN |
| 4 | f9d8e4e | HTTP classification/wire counts, bounded concurrency/cancel/join RED then GREEN, full suite/race |
| 5 | f47a217 | Built CLI, read-only reconcile, durable traces and listen policy RED then GREEN; 57 passing test events |
| 6 executable | 5ab629f | Verifier mutation/actual TERM/KILL helper tests RED then GREEN; 69 passing test events and full race |
| 6 canonical | 20261031T224036695Z | First A1-A8 passed, 20 reservations/19 worker POSTs/3 probe POSTs/1 GET/10 effects; 24.166 seconds including container cleanup |

All software check events above are separate from the twelve-event actual sample.
The canonical source is immutable historical evidence; later report edits do not
turn it into a run against a newer source. Original setup/test failures are retained.

## Rulings

- Native worktree creation rejected the approved product path because it was outside
  this chat's registered workspace roots. Use a manual Git worktree after verifying
  its owned directory is ignored. Cost: app attachment convenience only.
- Define shared domain type interfaces in Task 1, before their behavior is used in
  later tasks, so each task compiles independently. Cost: none to product scope.
- After earlier ambiguity, a definitive no-new-effect rejection still ends uncertain
  unless a receipt resolves it. The prior request may already have committed. Cost:
  conservative manual receipt inspection, without extra resends.
- Use a context-aware BeforeSend callback for the approved claim barrier, wired only
  by explicit CLI local-test mode, instead of mixing file-gate policy into delivery.
  Cost: one controlled callback interface, no normal send-policy change.
- Reserve one second of the four-second post-drain shutdown window for trace flush;
  CLI gives worker join three seconds. Cost: a slightly smaller worker-join allowance,
  preserving the overall five-second bound.
- Treat the one whole-branch review as the common final review after Task 6 runtime
  and report verification, without a second Task 6 review. Cost: none; one fresh review
  remains mandatory before local integration.

## Development corrections

The Windows/WSL wrapper needed forward-slash path conversion and direct argv
execution. Setup services were observed stopped between foreground WSL commands;
an owned foreground session kept the existing distribution usable thereafter.
No global installation or WSL/security configuration was changed.

The HTTP cancellation test fixture initially waited for disconnect without consuming
the request body. Consuming it let the server observe cancellation and terminate.
This was a fixture correction, not evidence of business cancellation as rollback.

CLI repeat tests exposed pgx Config.ConnString retaining the original parsed URI
after fields changed. The test helper now serializes new database/role URIs explicitly.
The owned setup database contains an earlier software fixture; canonical acceptance
used a separate fresh database, so that fixture is excluded from actual results.

PowerShell formatting checks also required an initialized empty log file, and prose
line endings were normalized to LF. These corrections did not require a canonical
repeat. Dmitry subsequently selected local integration; see the
[integration closeout](integration-report.md) for merged-source checks and cleanup.

## Single independent review and correction pass

The fresh reviewer examined `1e5190e..6f4bedc` and reported two Important findings
and one Minor: error-body reads overriding status/header decisions, preparation
failures bypassing cleanup, and positive Retry-After overflow. The last was
regraded Important because it permits early sends that the agreed cap must stop.
All three were reproduced and fixed in one pass; no second review was dispatched.

`TestStatusHeadersSurviveInvalidBodies` observed eight RED failures for oversized/
truncated 401/302/422/429 bodies, then GREEN. Real PostgreSQL/HTTP
`TestHeaderDecisionsStopFurtherSendsRealDB` observed five RED three-send outcomes,
then GREEN with one reservation/one send for each stop condition, including overflow.
The PowerShell controlled-runtime regression observed preparation leaving the
runtime running, then both partial-up and failed-build cases passed with stopped
state and a failure cleanup receipt. It makes no actual Docker call.

All-profile verification passed 84 Go test events, zero failures/skips, two separate
PowerShell cleanup cases, format/modules/vet/build and full Linux race checks.
Corrected source `a4c4136` passed the unchanged frozen A1-A8 sample in run
`20261031T225855490Z`: the same twelve IDs, twenty reservations, nineteen worker
POSTs, three protocol POSTs, one receipt GET and ten effects. Runtime15.735s with
process cleanup,24.272s with containers; prep4.563s separate, max graceful stop
1.018s rounded up. Both containers stopped; three volumes retained. Both raw
bundles passed read-only verification with334 files unchanged by content/mtime.
Original canonical evidence is preserved separately with source/check logs.

Additional rulings:

- Treat positive Retry-After overflow as Important, alongside lost header limits;
  it has the same forbidden-early-send effect. Cost: stricter decimal interpretation.
- Leave multi-worker fencing/HA/load scalability excluded, as agreed. Cost: multiple
  active workers and production throughput are unsupported.
- Leave production authentication, rotation, external webhooks and cloud operations
  excluded. Cost: this local synthetic service must not be repurposed as production.
- Leave blocking pipes/remote trace filesystems outside the regular local-file setup.
  Cost: the observed shutdown bound does not cover those destinations.
- Run the same frozen sample once on corrected source because shared HTTP policy and
  cleanup code changed. Cost: one additional bounded correctness run, without tuning
  cases or replacing the original result.

Deferred minors: none after regrading the overflow behavior and fixing it. The
reviewer's excluded behaviors are recorded above rather than silently dropped.
