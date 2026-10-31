# Go Integration Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Deliver the approved bounded local Go worker demonstration with durable recovery and source-bound evidence.

**Architecture:** One Go module provides a CLI/worker and an owned HTTP receiver. Separate PostgreSQL schemas persist queue attempts and atomic receiver effects/receipts. Two delivery goroutines use bounded context-aware HTTP; local OpenTelemetry capture supplies tracing evidence.

**Tech Stack:** Go standard library, pgx v5, PostgreSQL, Docker Compose, OpenTelemetry Go SDK/stdout exporter, Go tests including race detection on supported Linux.

**Spec:** [Approved design](../specs/2026-11-01-go-integration-worker-design.md), approved by Dmitry on 1 November 2026. Dmitry subsequently approved this plan with recommended Native execution on 1 November.

## Global Constraints

- One event contract, one worker process, one receiver process and one PostgreSQL instance with separate worker/receiver schemas.
- Only two delivery goroutines exist; three attempt reservations per job, including abandoned pre-send reservations. No reset at restart.
- Claim expiry 10 seconds; database operation deadline 1 second; connect deadline 500 ms; HTTP total deadline 2 seconds; request/response bounds 4 KiB.
- Retry delays 100 ms then 200 ms; valid Retry-After up to 1 second is honored, larger values stop automatic retry visibly.
- Shutdown: stop claims, drain 1 second, cancel, finish persistence/task join/telemetry within 4 more seconds; failed shutdown exits nonzero.
- Twelve canonical event IDs; at most 36 worker reservations, 38 delivery POSTs and one conflicting POST, four receipt lookups. Canonical runtime bound five minutes, excluding first preparation/build.
- Expected canonical effects ten; no frontend, broker, external receiver, cloud, public publication, paid action or CV/profile work.
- Default namespace p27, loopback ports 8127/5527 after checking availability. Preserve evidence/owned volumes; stop and verify only owned services.
- Personal commits: Dmitry Lyubaev <Dmitry.Lyubaev@gmail.com>. Keep credentials/raw evidence ignored; no global runtime/PATH/security changes.

## Review Focus

- Ambiguous history followed by confirmed no-effect failure must remain uncertain on exhaustion: Task 3 regression.
- Cancellation after a request leaves the worker cannot imply receiver rollback: Tasks 4 and 6 cancellation/late-commit checks.
- Duplicate JSON fields, equivalent UUID spelling and integer overflow must not create inconsistent identities: Task 1 boundary tests.
- HTTP library transparent replay/redirects must not consume unrecorded sends or contact another receiver: Task 4 wire-count tests.
- Receipt reconciliation racing with stale completion must preserve terminal identity and attempt history: Task 5 transaction regression.

## Execution preflight and version policy

Read-only observation on 1 November: product directory absent; Go/Docker not on
Windows PATH or their usual Program Files paths; existing Ubuntu WSL2 stopped;
6.6 GiB RAM and 76.88 GiB E: space free; no listener found on 8127/5527. These are
observations, not evidence that Linux tooling or a Docker daemon is available.

At execution, use the worktree skill to initialize the owned local product repo
and isolated codex/ branch/worktree. Inspect the existing Ubuntu toolchain/engine
before choosing native versus Linux-container commands. Prefer existing Docker
and a pinned Linux Go build/test image with gcc for race checks. If needed, a
verified portable Go archive stays project-local; do not install a machine-wide
runtime or Docker/WSL service automatically. If no usable engine exists, report
the concrete prerequisite rather than switching the approved architecture.

Planning candidates checked against primary sources on 1 November: Go 1.27.3,
pgx v5.11.0, OpenTelemetry stable trace SDK/exporter v1.47.0, PostgreSQL 18.6.
Resolve actual matching official images/modules and immutable digests in Task 1;
prove compatibility with build/tests and record the effective versions. Do not
invent a digest or silently use latest. If a candidate is unavailable, choose a
supported compatible patch under the design's builder discretion and record why.
Set GOTOOLCHAIN=local in checks; constrain owned container CPU/memory sensibly.

Primary references: [Go releases](https://go.dev/doc/devel/release),
[pgx release](https://github.com/jackc/pgx/releases/tag/v5.11.0),
[OpenTelemetry release](https://github.com/open-telemetry/opentelemetry-go/releases/tag/v1.47.0),
[PostgreSQL version policy](https://www.postgresql.org/support/versioning/).

## File map and command conventions

Paths below are relative to the future product repo `E:\Projects\go-integration-worker`
or its isolated worktree. Keep each implementation file focused; corresponding
`*_test.go` files sit beside it.

| Files | Responsibility |
|---|---|
| go.mod, go.sum, Dockerfile, compose.yaml, .gitignore, .env.example | Pinned module/runtime, isolated local resources, ignored evidence/secrets |
| internal/domain/event.go, outcome.go | Validated canonical identity, receipt and delivery outcome types |
| internal/queue/enqueue.go, claim.go, complete.go, reconcile.go | Durable job transitions and immutable attempt history |
| internal/receiver/store.go, http.go, faults.go | Atomic receipt/effect transaction, bounded HTTP, explicit test faults |
| internal/delivery/client.go, worker.go, shutdown.go | HTTP policy, two goroutines, recovery and bounded shutdown |
| internal/telemetry/trace.go, internal/cli/run.go | Trace propagation/capture and command dispatch |
| cmd/p27/main.go, migrations/001_init.sql | Signal wiring and schema/role initialization |
| internal/testutil/postgres.go | Explicit real-DB test setup with owned schema names |
| cmd/p27-accept/main.go, internal/acceptance/{runner,scenarios,evidence}.go | Actual process/HTTP/SQL canonical harness and readback |
| fixtures/canonical.json, scripts/{check,demo}.ps1, README.md, docs/local-report.md | Frozen expectations, reproducible commands and measured safe report |

Signatures below are the shared interfaces. Additional private helpers are the
builder's choice. Module path: `github.com/DmitryLyubaev/go-integration-worker`;
this is a local module identifier, not authorization to publish.

Focused test commands run in the selected Go environment at product root. Real
database checks require an explicit `P27_TEST_DSN`, fail if absent when selected,
and use owned disposable schemas; unit-only runs are never called full verification.
`scripts/check.ps1` runs unit/build/vet plus real-DB/race profiles and retains logs.
`scripts/demo.ps1` prepares/builds before starting the five-minute canonical clock.

### Task 1: canonical event identity and durable enqueue

**Files:** runtime/module/config files, migrations/001_init.sql,
internal/domain/{event,outcome}.go, internal/queue/enqueue.go, internal/testutil/postgres.go,
their tests and README setup section.

**Interfaces:** `domain.Event{EventID, SaleReference, Currency string; AmountMinor int64}`;
`domain.Payload{Event Event; Bytes []byte; Hash string}`;
`domain.Decode(io.Reader) (Payload,error)`;
`queue.New(*pgxpool.Pool) *Store`;
`(*Store).Enqueue(context.Context, domain.Payload) (Job,bool,error)`.
`domain.Receipt{EventID, PayloadHash, ReceiptID string}` is defined here for stored
jobs; Task 2 consumes it. `queue.Job{Payload domain.Payload; State string;
AttemptCount int; Ambiguous bool; ClaimToken string; LeaseUntil time.Time;
Receipt *domain.Receipt}`;
`ErrConflict` identifies conflicting enqueue without leaking credentials.

- [x] Inspect prerequisites and shared-host resources, initialize isolated local repo/worktree with personal identity, select/pin compatible runtime/module/image versions. Record exact commands, versions/digests and preparation time; do not start a canonical sample.
- [x] Write `TestDecodeCanonicalAndBounds` with reordered/whitespace JSON yielding identical bytes/hash; UUID uppercase normalized lowercase; sale reference 1-64 ASCII letters/digits/underscore/hyphen; AUD and amount 1..100000000 only. Assert duplicate/unknown/missing fields, fractional/exponent money, overflow, trailing JSON and 4097-byte input fail.

  ```go
  payload, err := domain.Decode(strings.NewReader(`{"event_id":"00000000-0000-0000-0000-000000000001","sale_reference":"A1_1","currency":"AUD","amount_minor":1000}`))
  if err != nil || payload.Event.AmountMinor != 1000 || len(payload.Hash) != 64 { t.Fatalf("invalid canonical payload: %v", err) }
  _, err = domain.Decode(strings.NewReader(strings.Repeat(" ", 4097)))
  if err == nil { t.Fatal("oversized input accepted") }
  ```
- [x] Run `go test ./internal/domain -run TestDecodeCanonicalAndBounds -count=1`; observe missing implementation/assertion failures, then implement `Decode` with a strict bounded parser and deterministic typed serialization. Re-run and require PASS.
- [x] Write `TestEnqueueIdentityRealDB`: exact duplicate returns same job/created=false, conflict leaves original bytes untouched; concurrent exact inserts produce one job. Run `go test ./internal/queue -run TestEnqueueIdentityRealDB -count=1` against actual PostgreSQL; observe behavioral RED.
- [x] Implement migrations and `Enqueue`; require real-DB GREEN. Initialization creates worker and receiver roles/schema grants and explicit readiness; worker role cannot write receiver tables. Keep admin connection confined to setup/tests.
- [x] Run `go vet ./internal/domain ./internal/queue` and focused tests, record evidence, commit `feat: add canonical events and durable enqueue`.

### Task 2: atomic owned receiver and receipt protocol

**Files:** internal/domain/outcome.go, internal/receiver/{store,http,faults}.go,
receiver tests; extend migrations only for receiver tables/grants.

**Interfaces:** consumes `domain.Receipt` and `domain.Payload` from Task 1;
`receiver.NewStore(*pgxpool.Pool) *Store`;
`(*Store).Apply(context.Context,domain.Payload) (domain.Receipt,bool,error)`;
`(*Store).Receipt(context.Context,string) (domain.Receipt,error)`;
`receiver.NewHandler(*Store,FaultController) http.Handler`.
FaultController is explicit local-test configuration keyed by event ID, never payload input.

- [x] Write `TestConcurrentApplyRealDB` with simultaneous new exact requests asserting one effect and the same receipt; conflicting reuse changes nothing. Force a transaction error between inserts and assert both receipt/effect absent.
- [x] Run `go test ./internal/receiver -run 'TestConcurrentApplyRealDB|TestApplyAtomicRollback' -count=1`; observe RED, implement atomic unique-key/transaction logic, rerun GREEN.
- [x] Write `TestHTTPContract` asserting POST 201/200/409, validation rejection, bounded request bodies and matching GET receipt/404. `GET /ready` verifies database access and constant protocol marker `p27-receiver-v1`; other methods return 405 with Allow.
- [x] Run `go test ./internal/receiver -run TestHTTPContract -count=1`; observe RED, implement bounded handlers and server read/header/write/idle timeouts, rerun GREEN.
- [x] Write `TestFaultsRequireTestMode` and `TestCommitThenCloseRealHTTP`: fault settings rejected outside explicit test mode; 503/422 injections precede effects; after commit a real HTTP connection is closed before response, SQL still has one receipt/effect. Observe RED, implement faults/barriers, require GREEN.
- [x] Verify worker/receiver role isolation against real SQL, run receiver tests/race where supported, commit `feat: add atomic synthetic receiver receipts`.

### Task 3: bounded claim ownership, history and recovery

**Files:** internal/queue/{claim,complete}.go and tests; domain/outcome.go.

**Interfaces:** `domain.Outcome{Kind OutcomeKind; Ambiguous bool; RetryAfter time.Duration; Receipt *Receipt; Reason string}`;
kinds Success/Permanent/Retryable/Unrecognized/Canceled;
`queue.Claim{Job Job; Token string; Attempt int}`;
`(*Store).ClaimDue(context.Context) (*Claim,error)` (nil when none);
`(*Store).Complete(context.Context,Claim,domain.Outcome) error`;
`(*Store).RecoverExpired(context.Context) (int,error)`.
Attempt history is immutable reservation/outcome records; timing uses database clock.

- [x] Write `TestClaimReservationsRealDB` asserting exclusive claims, reservation committed before return, 10-second expiry, no open transaction during send, no fourth slot and completion rejected for the wrong token.
- [x] Run `go test ./internal/queue -run TestClaimReservationsRealDB -count=1`; observe RED, implement bounded claim/conditional completion, rerun GREEN.
- [x] Write `TestRecoveryAndStickyAmbiguityRealDB`: abandoned reservation remains counted; expired third claim becomes uncertain; ambiguous first attempt followed by no-effect 503 exhaustion remains uncertain. Permanent no-effect rejection is failed; matching receipt is delivered. Use fixture timestamps only in software tests, not canonical recovery evidence.
- [x] Run the named regression RED, implement recovery and append-only outcomes/monotonic ambiguity; rerun GREEN. Preserve stale-token failure behavior through recovery.
- [x] Cover persisted retry due times 100/200 ms, no claims from terminal/delivered rows, and 1-second database deadlines using real blocked queries. Run all queue tests, commit `feat: persist bounded claims and restart recovery`.

### Task 4: HTTP delivery, two goroutines and shutdown

**Files:** internal/delivery/{client,worker,shutdown}.go and focused tests.

**Interfaces:** `delivery.NewClient(baseURL string) (*Client,error)`;
`(*Client).Send(context.Context,domain.Payload) domain.Outcome`;
`(*Client).Receipt(context.Context,string) (domain.Receipt,error)`;
`delivery.Config{Concurrency int; DrainTimeout, ShutdownTimeout time.Duration}`;
`delivery.Run(context.Context,*queue.Store,*Client,Config) error`.
An internal optional test barrier runs after committed claim/before send only in test mode.

- [x] Write `TestHTTPClassificationAndBounds`: match ID/hash/nonempty receipt; reject oversized/truncated/mismatched success; classify 400/409/422 definitive, 408/429/500/502/503/504 retryable no-effect under owned contract, other statuses uncertain/stop. Assert 500 ms connect and 2-second total bounds; Retry-After integer/date at <=1 second honored and larger values stop visibly.
- [x] Run `go test ./internal/delivery -run TestHTTPClassificationAndBounds -count=1`; observe RED, implement reusable client and classifications, rerun GREEN.
- [x] Write `TestWireSendCountAndRedirectDenied`: count wire requests on a reused connection closed around a POST, require one wire POST per reservation; do not attach an Idempotency-Key header or GetBody behavior that enables transparent net/http replay. Cross-host redirects receive zero requests. Observe RED, implement exact transport policy, rerun GREEN.
- [x] Write `TestTwoWorkersCancelAndJoin`: a controlled HTTP gate observes exactly two active sends across four jobs; no third claim/send; stopping prevents new claims, cancels contexts after one-second drain, joins owned goroutines and finishes within five seconds. Test final bookkeeping failure leaves recoverable leases, not delivered/rolled-back claims.
- [x] Observe RED with `go test ./internal/delivery -run TestTwoWorkersCancelAndJoin -count=1`; implement two fixed goroutines and bounded cleanup context, rerun GREEN. Receiver fixture may commit after cancellation; assert outcome is ambiguous.
- [x] Run `go test -race ./internal/delivery ./internal/queue ./internal/receiver` in supported Linux with real DB, record actual environment, commit `feat: deliver with bounded retries and context shutdown`.

### Task 5: runnable CLI, reconciliation and local traces

**Files:** cmd/p27/main.go, internal/cli/run.go, internal/queue/reconcile.go,
internal/telemetry/trace.go and tests; scripts/check.ps1, README instructions.

**Interfaces:** `cli.Run(context.Context,[]string,io.Writer,io.Writer) int`;
`(*Store).Status(context.Context,string) ([]Job,error)` (empty ID lists jobs);
`(*Store).Reconcile(context.Context,string,domain.Receipt) error`;
`telemetry.Start(context.Context,io.Writer) (func(context.Context) error,error)`.
Support enqueue, worker, receiver, status and reconcile only. Native signal wiring
maps interrupt/SIGTERM to the worker stop context; container tests use actual SIGTERM.

- [x] Write `TestCLIEnqueueStatusAndExitCodes`: valid enqueue/duplicate/status succeed, conflict/invalid command/missing runtime configuration fail clearly without dumping a DSN. Test actual built executable, not merely direct dispatch.
- [x] Observe RED; implement dispatch and process wiring with explicit bounded config, signal handling and structured output, rerun GREEN.
- [x] Write `TestReconcileReadOnlyAndRaceRealDB`: matching receipt updates failed/uncertain only, preserving all attempt records; unavailable/404/hash mismatch leaves unchanged; stale completion cannot overwrite reconciled delivery. Count HTTP POSTs before/after to prove zero resends.
- [x] Run reconciliation test RED, implement read-only lookup and conditional SQL transition, rerun GREEN.
- [x] Write `TestTraceLinkAndFlush`: persist/link enqueue trace identity across restart, propagate W3C traceparent to receiver, capture attempt/receiver spans with event/attempt/outcome attributes and no secrets. Export/flush failure is reported within shutdown bound and never rewrites business outcome. Observe RED, implement minimal SDK/manual spans and local capture, rerun GREEN.
- [x] Implement check.ps1 profiles for format/vet/build/unit/real-DB/race with fail-on-required-missing-prerequisite and retained output. Require clean formatting and all selected checks PASS, commit `feat: expose worker CLI reconciliation and trace capture`.

### Task 6: frozen actual-process acceptance and measured report

**Files:** fixtures/canonical.json, cmd/p27-accept/main.go,
internal/acceptance/{runner,scenarios,evidence}.go and tests, scripts/demo.ps1,
docs/local-report.md and README; copy approved design/plan into product docs.

**Interfaces:** `acceptance.Run(context.Context,Config) (Result,error)` starts only
owned compiled processes/services; `acceptance.Verify(Result) error` checks independently
defined fixture expectations. Config includes binary/fixture/output paths and owned
DSNs/namespace; Result binds source tree/config/fixture hashes, commands/timing,
snapshots, trace evidence and cleanup. Keep runner tests on separate test data.

Freeze canonical UUIDs with last 12 hex digits 000000000001..000000000012 in order
A1(1-4), A2(5), A3(6-7), A4(8), A5(9), A6(10), A7(11), A8(12).
Use fixed sale references matching group/ordinal, AUD and amount_minor=1000.
Conflicting probes change amount to 1001 under the same ID.

| Group | Worker reservations | Worker POSTs | Direct probes | Final effects |
|---|---:|---:|---:|---:|
| A1 | 4 | 4 | 0 | 4 |
| A2 | 1 | 1 | 2 exact plus 1 conflicting POST | 1 |
| A3 | 3 | 3 | 0 | 1 |
| A4 | 3 | 3 | 0 | 0 |
| A5 | 2 | 2 | 0 | 1 |
| A6 | 2 | 1 | 0 | 1 |
| A7 | 2 | 2 | 0 | 1 |
| A8 | 3 | 3 | 1 receipt GET | 1 |
| Total | 20 | 19 | 3 POST, 1 GET | 10 |

- [x] Write `TestVerifyRejectsMissingAndChangedEvidence`: missing case/process-exit proof, altered fixture/hash/effect count, absent required trace or absent cleanup observations fails. Unit tests exercise verification only, never stand in for runtime evidence. Observe RED, implement narrow verifier, rerun GREEN.
- [x] Implement fixture and deterministic scenario barriers: A1 two in-flight; A2 duplicates; A3 503->success and 422; A4 three 503; A5 stop/cancel then release late commit and restart; A6 real KILL after committed claim before send, observe process exit then wait genuine 10-second expiry; A7 actual postcommit socket close; A8 all three responses lost, restart proves no fourth POST, receipt reconciliation. No automatic whole-study rerun.
- [x] Implement demo.ps1 preparation/readiness, exact-source builds, private timestamped output folder and a five-minute runtime watchdog including owned cleanup. Receiver protocol marker/SQL readiness must match before cases; unexpected source changes/counts/failure stop the sample and preserve partial results. Separate preparation from canonical time.
- [x] Run `scripts/check.ps1` with all required profiles; require format/vet/build/unit/real-DB/race success with no hidden skipped required check. Commit executable/fixtures before canonical execution; do not manufacture measured report figures.
- [x] Run `scripts/demo.ps1` once against that clean committed source. Require A1-A8, exactly 12 IDs/20 reservations/19 worker POSTs/3 protocol POSTs/1 GET/10 effects, no fourth attempt, max worker concurrency two, shutdown <=5 seconds and complete sample <=300 seconds. Compare independent receiver SQL and process/wire logs, not just worker states.
- [x] Stop owned processes/containers in finally, preserve volumes/raw evidence, verify listener/container absence. If sample fails, report actual outcome; any corrected run gets a new source-bound directory and explicit reason, retaining original failure.
- [x] Write dated safe report with counts/timings/limits, trace example and state/sequence diagram. Request one fresh whole-branch code review; reproduce/fix Important findings in one correction pass with relevant tests. Re-run only checks/acceptance whose evidence changed, clearly preserving original sample/source.
- [x] Commit source/report closeout, update portfolio tracker/handover with exact verified state and deferred minors. Present local integration choice under finishing-branch skill; preserve evidence before retiring any worktree. Do not push/create a public repository or hosted CI.

## Plan self-review and execution handoff

Self-review completed: design sections map to Tasks 1-6; interfaces/types match;
all five Review Focus cases have owning regression steps; A1-A8 counts total
12 IDs, 20 reservations, 22 total POSTs and ten effects; prerequisites/version
resolution stay separate from software/runtime success. The plan adds no product
subsystem or paid/public milestone. Checkboxes describe future work, not results.

Recommend **Native** execution: six tightly connected tasks, one implementer and
one final independent review keep this local correctness demonstration proportionate.
Subagent-driven execution is an alternative with separate implementer/reviewer gates
per task, at additional context/review cost. Owner approved Native execution;
executing-plans governs task-by-task work and the final independent review.

## Final local checkpoint

All six implementation tasks, the single review correction pass and corrected-source
A1-A8 verification are complete. Source a4c4136 passed84 Go test events, two
PowerShell cleanup cases and full Linux race checks. Both raw runs are retained;
source/report closeout awaits only the owner's integration choice. No public action.
