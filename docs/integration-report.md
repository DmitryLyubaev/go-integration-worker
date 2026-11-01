# Local integration closeout

Completed 1 November 2026, Brisbane, after Dmitry selected option 1.

Primary `main` fast-forwarded from `1e5190e` to reviewed branch head `e20fcb2`.
At this local integration checkpoint the product repository had no remote.

## Merged-checkout verification

The first primary build failed while obtaining optional Go VCS metadata: container
Git rejected the mounted Windows-owned `/workspace` repository. The failure log is
retained at `.local/checks/20261101T010217954Z/build.jsonl`.

A one-command `GOFLAGS=-buildvcs=false` probe passed and produced a binary identical
to the corrected measured executable. That setting is now scoped to the project's
Compose tooling environment. The acceptance manifest still records exact Git/source
identity. No global Git trust, ownership, runtime or security setting changed.

Fresh primary All-profile verification passed 84 Go test events, zero failures or
skipped tests, two PowerShell preparation-cleanup cases, formatting, module checksums,
vet, build and full Linux race detection. Receipt:
`.local/checks/20261101T010530629Z/summary.json`.

The primary worker binary SHA-256 is
`8f368d46d9f7a7e7f532375277f03615f92b9b4b099f806eb0c6890ec1d313e4`,
identical to the measured `a4c4136` executable. No canonical scenario was repeated
for integration. Both retained A1-A8 bundles passed read-only verification from the
primary checkout, with all 334 files unchanged by content and modification time.
Their original source identities, counts and timings remain historical evidence.

## Preservation and cleanup

All 593 ignored worktree files, 74,053,998 bytes, were copied and hash-verified in
`.local/integration/20261101T010314873Z/worktree-ignored` before cleanup. This includes
raw results, development/check/review logs and built executables. Earlier original
and corrected archives and complete source bundles remain in `.local/preserved/`.

Only the verified owned `.worktrees/local` checkout was removed, without force;
the fully merged `codex/local` branch was deleted. Primary main retains the source.
Both owned containers are stopped; the database and two cache volumes remain.
The temporary integration keepalive is cleaned up individually, without WSL shutdown.

The approved bounded local milestone is complete. At this checkpoint public source
publication, hosted CI, paid services and cloud deployment were uncommissioned.

## Public source publication follow-up

On 1 November 2026 Dmitry approved creating the public
[go-integration-worker repository](https://github.com/DmitryLyubaev/go-integration-worker)
and pushing the reviewed source, tests, safe reports and trace excerpts. The
pre-publication audit covered all 11 commits and 82 historical blobs through
`5591590`, checking selected credential/private-key/employer markers and forbidden
raw-evidence/environment/cache paths, with no selected findings. This was a targeted
audit, not a guarantee against every possible secret. Demo credentials are deliberately
public and documented in the README.

The publication follow-up changes documentation only. The measured source identities
and previous software verification remain as recorded above; the canonical scenarios
are not repeated for publication. Raw evidence, private archives, builds, caches and
actual `.env` files remain ignored. Hosted CI and cloud deployment are outside this
publication scope.
