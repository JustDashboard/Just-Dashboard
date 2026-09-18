# Deployment third pass: review, defects and gap closure

**Date:** 2026-09-16 UTC (third pass). **Baseline:** `patch/0.6.7` at `d1389e7` plus the uncommitted
first- and second-pass work recorded in [`implementation-progress.md`](implementation-progress.md).

This pass re-reads the deployment subsystem end to end (routes, engine, activation, notifications,
observers, previews, runtime owner, backups gate, blueprint adapter and the workspace UI), re-verifies
the inherited worktree, records the defects it found, re-maps the product against Coolify, Dokploy
and Vercel, and executes the gap closure below. A row is **Done** only when its verification evidence
exists in this checkout.

## 1. Baseline verification

| Check | Result |
| --- | --- |
| `go build ./...`, `go vet ./...` (Go 1.26.8) | Pass |
| `go test -count=1 ./...` | Pass, 33 packages |

## 2. Defects found in this pass

Severity: **P0** breaks a safety or correctness promise; **P1** impairs ordinary operation; **P2** quality.

| ID | Severity | Defect | Fix |
| --- | --- | --- | --- |
| D10 | P1 | `POST /deploy/{id}/environments/{env}/runs` accepted every operation the enqueue switch knows, so a `service.control` principal could request `preview_create`, `preview_update`, `preview_remove` or `scheduled` on the production environment. The lifecycle-only operations run the full release path under a wrong label and can bypass preview admission semantics. | The manual route accepts only `deploy`, `redeploy`, `restart` and `force_build`; anything else is `400`. Test. |
| D11 | P1 | A scheduled chain (`waitForScheduledRun`) treated a `rolled_back` deployment as a passed step, so a `deploy → restart` chain restarted the *old* release after the new one had failed. | `rolled_back` is a failure of the chain step; only `succeeded` passes. Test. |
| D12 | P2 | Approving a preview with `deploy: true` enqueued `preview_update` even when the approval had just created the preview environment. Run history and notifications said "update" for a first deployment. | The approval handler uses the `created` result to choose `preview_create`. |
| D13 | P2 | A blueprint's default **backup** automation preset was translated into a schedule step with an empty configuration that fails every night as `invalid_plan` once blueprint deployment is possible. | A preset with a backup step and no linked job is created **paused**, so it is visible on the Automations page to be completed; restart/update presets stay enabled. Test. |
| D14 | P2 | Slack message text interpolated titles and field values without escaping `&`, `<`, `>`; a project name containing them broke the link markup. Discord embed titles have no length cap although Discord refuses more than 256 characters. | mrkdwn escaping and title truncation. Tests. |
| D15 | P2 | Notification deduplication was check-then-send. Two observers finishing the same run concurrently (a worker exit racing a cancellation) could both pass `NotificationDelivered` and post twice. | The attempt row is reserved (`UNIQUE(channel_id, run_id, event, attempt)`) before delivery and updated afterwards; a lost reservation skips the send. Test. |
| D16 | P1 | **Found by the real-backend e2e once blueprints could deploy:** preflight blocked every plan whose managed named volume did not exist yet (`storage_unavailable`, "Docker volume was not found"). Docker creates a named volume on first mount, so no blueprint with data storage could be deployed on a fresh host, and any hand-written plan with a new managed volume had the same problem. | The dependency observer distinguishes *missing* from *uninspectable*; a missing volume the deployment owns passes as `storage_pending_creation`. Linked or uninspectable volumes still block. Test. |
| D17 | P2 | **Introduced and caught in this pass:** labelling a preview approval's run `preview_create` broke approval idempotency, because the retry saw an existing preview and asked for `preview_update` under the same key. | The approval handler looks the run up by idempotency key before choosing an operation (`RunByIdempotencyKey`). Existing test. |

Security review of this pass: the manual-run route (D10) was the only authorization-relevant finding.
Webhook signature handling, preview admission, variable reveal, the exec console (terminal capability,
audited Docker exec route), the preview frame CSP and notification credential sealing were re-read and
hold. Notification targets remain administrator-configured outbound URLs (documented trust boundary).

## 3. Competitive position after the second pass

| Capability | Just Dashboard | Coolify | Dokploy | Vercel | Gap |
| --- | --- | --- | --- | --- | --- |
| One-click services | Catalogue of 17 reviewed blueprints, **deployment refused** | 280+ templates | Template catalogue | Marketplace | **Largest gap.** Closed in this pass for the non-game, file-free subset (F10). |
| Database backups | File archives, SQLite consistent snapshots, coverage manifests, isolated restore proof | Engine-aware dumps to S3 with retention | Volume/DB backups to S3 | n/a | Native dumps were missing although `dbx` already ships them for the Databases page. Closed (F11). |
| Deployment analytics | None | None | None | Analytics (paid) | Success rate, durations, frequency and recovery time per project. Added (F12). |
| Notification reliability | Single attempt per event | Single attempt | Single attempt | Retries | Bounded retries with backoff. Added (F13). |
| Health-gated zero-downtime, frozen rollback, failure diagnostics, resource limits, console, commit statuses, private polling | Present (second pass) | Parity or behind | Parity or behind | n/a | Ahead or parity. |
| Multi-environment promotion, GitHub App PR comments, multi-server, teams | Absent | Partial / present | Present | Present | Remain next phases (R11, R12, R14, R15). |

## 4. Work executed in this pass

| Item | Scope | Verification |
| --- | --- | --- |
| D10–D15 | As above. | Unit tests per defect; full backend suite. |
| **F10 blueprint deployment** | Detection resolves the blueprint image to an immutable digest; the source identity carries the image reference, the reviewed `id@version` and the render digest. Input values become variable values and generated secrets are produced at commit with the blueprint's declared length. Materialization gives an image release its empty private workspace. Execution validates the immutable identity. Queue admission, planning and the catalogue answer per blueprint: game profiles, blueprints that ship config files or downloaded artifacts stay refused with a specific reason. Memory/CPU come from the blueprint's resources. | Unit tests for render mapping, commit generation, admission and preview-only refusal; browser test drives the wizard from the server's rendered plan; the real-backend e2e deploys the Redis blueprint and verifies the digest, readiness, limit, volume and generated password. |
| **F11 native database dumps in backups** | A backup job may name saved database connections. Each run dumps them through the Databases owner's native path (`pg_dump`, `mysqldump`, `mongodump`, Redis `--rdb`, or the built-in driver dump) into `database-NNNN/` inside the archive; the manifest records connection, engine, database, method, file and digest. The deployment gate accepts native-dump coverage for a linked database before falling back to filesystem coverage. A run exposes **Restore database** into the saved connection (typed confirmation, destructive capability). Backups UI selects connections; deployment settings show coverage. | Unit tests for manifest/gate; live Postgres fixture (`JD_DEPLOY_LIVE=1`) dumps, archives, restores to a second database and reads the canary. |
| **F12 deployment insights** | `GET /deploy/{id}/insights`: runs, success rate, median and p95 durations, deployments per week, mean time to recovery, current failure streak, last 30 days series. Deployments tab panel. | Store tests over synthetic runs; browser test. |
| **F13 notification retries** | Failed deliveries schedule `next_attempt_at` (1 min, 5 min, 30 min); a sweeper on the automation scheduler retries up to three attempts and records each. | Unit test with a failing then succeeding endpoint. |
| Docs | `implementation.md`, `backup-coverage.md`, `notifications.md`, `README.md`, `CONTRIBUTING.md`, this ledger. | Reviewed against the diff. |

## 5. Evidence

| Check | Result |
| --- | --- |
| `go build ./...`, `go vet ./...` (Go 1.26.8) | Pass |
| `go test -count=1 ./...` | Pass, 33 packages |
| `go test -race -p 1` on deploy, api, backups, store, dockerx, proxysvc (serially) | Pass, no `DATA RACE` (deploy 234 s, api 344 s) |
| `bun run lint`, `bunx tsc --noEmit`, `bun run build`, `bun test src` (29) | Pass |
| Browser suite (production build, all specs) | 110 passed in 4.8 minutes, including the three new deployment/backup tests |
| `JD_DEPLOY_LIVE=1 … -run TestLiveBackupDumpsAndRestoresAPostgresDatabase` | Pass on the local Docker daemon: real PostgreSQL provisioned, canary dumped (built-in dump, `pg_dump` absent on this host), row deleted live, restored into `drill`, live database untouched, mismatched confirmation refused. |
| `scripts/e2e-deployments.py` | 27 of 27 checks: nginx with limits, crashing busybox with captured diagnostics, restart on a paused channel, **Redis blueprint** (catalogue support flags, digest + render digest, rendered plan, only a `backup_policy_missing` warning, container answers PONG with the input-driven 128 MiB limit and a managed `e2e-redis-…-data` volume, generated 40-character password masked in the list, revealed on demand and present in the container, no failing default schedule). The first run of this scenario exposed D16. |

## 6. Not done in this pass (next phases)

Unchanged from the [second pass](gap-closure-plan.md#5-work-plan): native dumps for engines without a
saved connection, blueprints with config files/artifacts, game blueprints, R11 GitHub App, R12
promotion/CLI, R14/R15 multi-server and teams, D5 fleet p95 under parallel `-race`.
