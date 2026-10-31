# Project 27: durable Go integration worker

Date: 1 November 2026. Status: approved by Dmitry on 1 November 2026.

One fresh read-only builder check on 1 November returned "nothing important left".
This is design review, not software verification. Dmitry subsequently approved
this written design; implementation planning is authorized.

## Intent and decisions

Build a bounded local portfolio demonstration of idiomatic Go integration work:
bounded concurrency, context propagation, HTTP failure decisions, durable recovery
and shutdown. All events and software are independently invented. This is not a
production system, a load benchmark or a claim of commercial Go tenure.

Dmitry accepted the recommended durable-worker approach after requesting the
smallest useful scope. The selected outline is a CLI, one Go worker with at most
two concurrent deliveries and three attempts per job, one owned HTTP receiver,
PostgreSQL, Docker Compose and locally captured OpenTelemetry traces. The detailed
protocol, sample and bounds below were assistant design calls included in the
written design Dmitry approved.

## Approaches

| Approach | Tradeoff | Decision |
|---|---|---|
| Durable local PostgreSQL queue and owned receiver | A little SQL/claim machinery; demonstrates real restart and response-loss recovery | Selected by Dmitry on 1 November |
| In-memory channel and HTTP fixture | Smaller teaching sample; loses queued/claimed work at exit | Rejected because durable recovery is a central learning goal |

## Scope and constraints

One event contract, one worker process, one receiver process and one PostgreSQL
instance with separate worker/receiver schemas. Only the receiver writes its
receipt/effect tables; the worker communicates with it over HTTP. One Go module
can provide CLI/worker and receiver commands. No framework or ORM is necessary.

Proposed local product location is `E:\Projects\go-integration-worker`, created
only after design/plan approval. Use Go's standard library (`net/http`, `context`,
`encoding/json`, `log/slog`, `testing`), pgx v5, SQL migrations and the OpenTelemetry
Go SDK/stdout trace exporter. Pin supported compatible patch versions, dependencies
and container digests during planning/preflight; do not install tooling now.

Non-goals: frontend, enqueue HTTP API, broker, gRPC, Kubernetes, cloud deployment,
multi-worker/HA operation, production authentication, external webhooks and paid
services. No provider model is involved. Avoid an observability stack: local trace
files and structured logs suffice. Claiming distributed exactly-once delivery,
production throughput or cancellation as rollback would make this project fail.

## Event and command surface

An event contains `event_id` (UUID), `sale_reference` (bounded synthetic string),
`currency` (AUD only) and `amount_minor` (positive integer, at most 100000000).
The event ID is the delivery/effect identity; a new ID is a new event, not replay.
There are no customer fields or real transactions. Reject unknown/missing fields,
oversized bodies (4 KiB), trailing JSON and invalid values before enqueue/send.

Serialize the validated typed event deterministically once and persist those
bytes and their SHA-256. All sends reuse them. The receiver validates the same
contract and computes identity from the same canonical representation, not raw
JSON whitespace. Same ID and payload returns the existing job/receipt; conflicting
reuse fails without changing the original. No edit-in-place or fresh-ID resend.

CLI commands: `enqueue <fixture>`, `worker`, `status [event-id]`, and
`reconcile <event-id>`. A separate `receiver` command owns the synthetic destination.
Reconciliation only reads a receiver receipt and records confirmed delivery; it
never sends an event or resets a retry budget. Receiver endpoints are
`POST /exports` and `GET /receipts/{event_id}` plus a simple readiness endpoint.
Do not add cancellation of individual business jobs; cancellation is Go context
and process-shutdown behavior.

## Durable queue and recovery

Persist jobs (immutable payload/hash, state, attempt count, due time, claim token,
expiry, receipt and last outcome) and append-only attempt reservations/outcomes.
States: `queued`, `processing`, `retry_wait`, `delivered`, `failed`, `uncertain`.
`failed` means a definitive rejection or exhausted confirmed no-effect failures;
`uncertain` means some attempt may have committed without a verified receipt.

Only two delivery goroutines exist. Each claims one due job immediately before
work, in a short transaction using row locking and `SKIP LOCKED`. Claim, random
ownership token, 10-second expiry and incremented attempt reservation commit
together. There is no unbounded in-memory prefetch queue and no open transaction
during HTTP. Database operations have a 1-second context deadline.

An attempt reservation counts against the three-attempt limit even if the worker
dies before the send. Report reservations and observed HTTP sends separately.
Complete/reschedule only while the same claim token owns `processing`; stale
completion cannot overwrite a recovered result. Persist any ambiguous history so
a later no-effect failure cannot erase it. Accept a successful response only if
it contains the matching ID, hash and stable receiver receipt.

Expired claims can be recovered after a killed worker has exited. Record the old
attempt as abandoned/possibly sent, then retry the same identity if budget remains.
An abandoned third reservation becomes `uncertain`, not silently retried. Startup
does not reset counts or force immediate expiry. One active worker process is the
supported topology; the harness confirms its predecessor exited before restart.
Multiple concurrent worker processes and fencing remote side effects are not claims.

## Receiver contract and ambiguous outcomes

In one PostgreSQL transaction, insert the identity/hash receipt and one synthetic
export-effect row, or read the original on duplicate identity. Concurrent exact
duplicates converge on one effect/receipt; conflicting payload gets 409 without
another effect. Keep deduplication records for the full demonstration; deleting
them while work remains would invalidate safe replay. Worker schemas cannot serve
as proof of receiver effects. Acceptance queries receiver tables independently.

201 means a new committed effect; 200 means the matching original receipt. 400,
409 and 422 are definitive no-new-effect rejections under this owned contract.
408, 429, 500, 502, 503 and 504 are retry candidates; this fixture emits these only
before any effect transaction, so they are confirmed no-effect responses under its
documented contract. A transport timeout, truncated
or mismatched success body, connection loss after send or unrecognized response
is ambiguous. Follow neither redirects nor another receiver URL supplied in data.
Ambiguous transport/malformed-success outcomes may retry within the unchanged
budget. Other unrecognized statuses stop as `uncertain` for inspection, with no
automatic resend. Validate readiness and configured receiver identity before the
sample; these rules are valid only for this controlled receiver.

Bounded same-ID replay after ambiguity is allowed only because this owned receiver
has the specified durable atomic deduplication contract. It does not establish a
generic safe policy for arbitrary webhooks. Canceling a context is never proof
that the receiver did not commit. After three reservations, any unresolved ambiguous
history becomes `uncertain`; otherwise unsuccessful retryable work becomes `failed`.

`reconcile` requests the receipt with a deadline. A matching ID/hash/receipt can
move a terminal failed/uncertain job to `delivered`, preserving attempt history.
A missing/unavailable/mismatched receipt leaves it unchanged and reports the reason.
404 alone does not prove that an earlier in-flight request can never commit. There
is no automatic or manual resend beyond the three reservations in this milestone.

## Time and shutdown bounds

Per HTTP request: 500 ms connect deadline, 2 seconds total including body reading;
bound response bodies to 4 KiB. Reuse a configured HTTP client. Retry delays are
100 ms then 200 ms, persisted as due times. For 429, honor valid Retry-After up to
1 second; larger values terminate automatic retry visibly rather than violating it.
These are demonstration defaults, not externally tuned reliability settings.

On graceful stop, stop new claims, allow at most 1 second for active work to finish,
then cancel its contexts. Wait at most another 4 seconds for bounded persistence,
HTTP/background goroutines and telemetry flush, then exit nonzero if shutdown failed.
Attempt final cancellation bookkeeping with a new bounded context; if storage is
unavailable, leave the lease recoverable. Never mark a possibly sent request rolled
back. Abrupt termination instead relies on expiry and preserved reservations.
Observe goroutine completion via owned task tracking and before/after counts, without
claiming a universal leak detector or stopping unrelated processes.

## Frozen local acceptance sample

Freeze fixture IDs/hashes, expected states/effects and fault settings before the
canonical run. One isolated case namespace per row; exactly twelve event IDs in
total. At most 36 worker attempt reservations, 38 delivery POSTs (including two
direct duplicate probes) and one additional conflicting POST, plus at most four receipt
lookups across this sample. A1-A8 is one bounded correctness sample, not statistical
reliability evidence. Software tests use their own disposable records.

| Group | Events | Required observation |
|---|---:|---|
| A1: normal and concurrency | 4 | Four delivered/effects; controlled receiver gate shows maximum in-flight exactly two, never more; context/trace IDs correlate |
| A2: duplicate identity | 1 | Exact enqueue returns original; conflicting enqueue fails; two deliberately simultaneous direct HTTP duplicate probes plus original worker send yield one stable receipt/effect; conflict HTTP request is separately a no-effect protocol check |
| A3: transient and rejection | 2 | One injected 503 then success (two reservations); separate 422 is terminal after one send and zero effects |
| A4: exhaustion | 1 | Three injected no-effect 503s, then failed; no fourth reservation/send |
| A5: graceful stop | 1 | Stop while a request is active; worker stops claiming, exits within five seconds, records finish/cancellation honestly; any expired recovery keeps same ID/budget |
| A6: actual process interruption | 1 | Kill actual worker after committed claim/before send at a deterministic barrier; preserve SQL evidence, restart after exit/expiry, recover same job with one effect; consumed abandoned reservation remains visible |
| A7: commit then lost response | 1 | Receiver commits receipt/effect then closes connection before response; next bounded same-ID send returns original receipt; exactly one effect |
| A8: terminal ambiguity/reconcile | 1 | Receiver commits once but drops all three POST responses; worker ends uncertain, survives restart without fourth send; explicit receipt reconciliation marks delivered with original receipt |

A2's two direct duplicate probes are protocol tests, not worker attempts. Its
conflicting request is an additional no-effect POST check with the same ID. Report
these three probes separately from worker sends. Expected final receiver effects:
ten (4 + 1 + 1 + 0 + 1 + 1 + 1 + 1 across A1-A8). Do not infer effects from enqueue
submissions or reservations. Freeze exact canonical request counts per case during
planning, rather than silently replacing expectations with observed values.

The graceful-stop fixture holds the first request at a receiver precommit gate.
Signal stop while it is held; observe cancellation and worker exit, then release
the receiver to commit despite the disconnected client. Restart the worker under
the same budget and recover the original receipt. Gate/receipt assertions distinguish
precommit from postcommit observations and demonstrate cancellation is not rollback.
Receiver faults exist only in explicit local test mode and are keyed to frozen IDs;
they are absent from event payloads. Distinguish injected status/drop behavior from
the real worker process kill, actual HTTP exceptions and actual PostgreSQL commits.

## Verification, evidence and cleanup

Run formatting/vet/build, meaningful Go unit tests and real PostgreSQL/HTTP
integration/acceptance checks. Run `go test -race` in a supported native or Linux
container environment and state which environment actually ran it. No mocked SQL
or simulated restart can replace the canonical process/transaction checks.

Keep a source/config/fixture-bound JSON result, command logs, timings, independent
job/attempt/effect snapshots and a sanitized trace example linking enqueue, attempt
and receiver work. State sample size, observed maximum concurrency, reservations,
sends, retry decisions and final unresolved work. Historical failed runs remain
distinct; never relabel them as canonical success. Trace export failure is visible
and does not roll back business results, but missing required trace evidence fails
the trace acceptance check. Include a short state/sequence diagram in the report.

Default namespace `p27`, loopback receiver port 8127 and PostgreSQL port 5527 after
checking availability; choose unused alternatives if occupied. Do not stop another
project. Compose resources use a dedicated project name and owned volumes. Do a
read-only CPU/RAM/disk preflight; no global runtime/security/PATH changes. Preserve
private raw evidence and database volumes, stop owned services, verify listener/
container absence, and report retained storage. A canonical sample has a five-minute
runtime deadline excluding first image/build preparation; exceeding it stops the
sample and reports incomplete evidence, rather than expanding the bound.

## Finish, owner decisions and builder discretion

The local milestone is runnable source, passing agreed checks/A1-A8, one fresh code
review with important findings resolved, measured report and verified cleanup. No
public repository, push, hosted CI, paid action, CV/profile work or other-project
change is included. Follow portfolio RULES.md and personal commit identity. Tracker
and handover stay current; design approval precedes implementation planning.

Planning estimate: roughly two to three focused implementation days plus review,
not a measured duration or fixed commitment. Builder may choose file/package layout,
exact SQL/migration implementation, precise compatible version pins, test helper
structure and unused local resource names. Builder cannot silently add a broker,
change identity/retry semantics, skip actual interruption, increase study scope or
publish. Any necessary scope change returns to the owner with a concrete reason.

Primary stack references: [Go release policy](https://go.dev/doc/devel/release),
[pgx](https://github.com/jackc/pgx), and
[OpenTelemetry Go exporters](https://opentelemetry.io/docs/languages/go/exporters/).
