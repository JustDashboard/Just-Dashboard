# Evidence and reproduction

These are fresh results for source commit `d1389e7d58196f147607193307e78b4b802c0b0a`, collected on
2026-09-16 UTC. Read the [report](../README.md) for interpretation. Tests that skipped are not proof;
successful retries do not erase the initial failures.

## Files

| Files | Contents |
| --- | --- |
| `backend-build.*`, `backend-vet.*` | Command, time, exit status, and output. Empty output is normal for successful Go build/vet. |
| `backend-tests.json` | Every test/subtest/package pass, failure, or skip; relevant skip output and command metadata. |
| `backend-tests.txt` | Condensed package/skip output. This is not the complete verbose execution transcript. |
| `deployment-race.*` | Same result indexing for the required five-package race suite. |
| `deployment-race-failure-detail.txt` | Exact JSON event lines containing the fleet budget failure. |
| `fleet-race-retry.txt` | Isolated rerun of the sole failed race-suite test. |
| `frontend-lint.*`, `frontend-build.*`, `frontend-unit.*` | Bun commands, exit records, and output. |
| `frontend-browser.*`, `browser-server.txt` | Complete Playwright console output/metadata and its isolated development server output. Screenshots/traces under ignored frontend test output are not copied into this bundle. |
| `live-artifacts-activation.*` | Real Docker build/activation test result index, including ten subtests. |
| `live-ingress.*` | Initial real nginx/Caddy result index, including the failure. |
| `live-ingress-failure-detail.txt`, `live-caddy-retry.txt` | Exact initial Caddy diagnostic and unchanged retry. |
| `live-database-connections.*` | Real PostgreSQL, MySQL, MariaDB, Redis, MongoDB app-container authentication cases. |
| `targeted-probes.txt` | SvelteKit misclassification, Go overrides ignored, and real build variable absent/present experiment. |
| `frontend-default-probes.txt` | Static/Vite/Containerfile defaults observed through the exported frontend configuration function. |
| `frontend-default-probe.ts` | Reproducible frontend input fixtures; emits observations, not a desired-behavior test. |
| `data-boundary-probes.txt` | Named-volume backup false positive; also contains the initial invalid preview probe fixture. |
| `preview-inheritance-probe.txt` | Corrected preview probe using an additive immutable plan revision. |
| `deployment_audit_probe_test.go.txt`, `api_audit_probe_test.go.txt` | Final Go probe sources injected through `go test -overlay`; no tracked backend source was edited. |
| `run_checks.py.txt` | Exact original audit harness. Paths/output targets describe this host and should be changed before reuse; rerunning it unchanged overwrites recorded evidence. |
| `summary.json` | Machine-readable counts, tool versions, and important qualifications. |
| `fixture-cleanup.json` | Explicit cleanup of the isolated nginx fixture left behind by the failed Caddy test; identified by creation time, image, loopback port and mount-free configuration. |

The full Go JSON event streams were generated under `/tmp/jd-deployment-audit-20260916/`; the retained
JSON files here index every completion result, rather than storing all verbose output. The original
runner searched all event output for data-race/failure markers; the race stream was separately checked
for `WARNING: DATA RACE`, with zero matches. That statement is narrower than a proof of no possible
data race.

## Test conditions

- Go 1.26.8, explicitly selected from `/tmp/jd-go1.26.8/bin/go`; `GOTOOLCHAIN=local`.
- Bun 1.4.0, Node 24.12.0; the declared Bun 1.3.11 was not separately exercised.
- Docker 29.8.0, Compose 5.5.1, Buildx 0.37.0; local nginx and Chromium available.
- Required build/vet/unit/race/live suites used uncached Go test runs (`-count=1`).
- The broad ordinary Go suite used unavailable loopback DSNs for network databases. This intentionally
  skipped those integrations instead of connecting to operator data. Deployment-specific live DB
  fixtures independently exercised all five quick-setup engines.
- Browser tests used one worker, Chromium, one retry (`CI=1`), and `JD_BROWSER_BASE_URL` pointing to an
  audit-owned dev server on a free loopback port. The repository's default Playwright server is also
  a dev server. APIs inside these tests are mocked.
- Some broad checks ran concurrently. The performance retry ran after the browser suite finished.
- No production workload was deliberately stopped/reconfigured. No daemon-wide prune was run.

## Normal suite commands

The required commands are maintained in [CONTRIBUTING.md](../../../../CONTRIBUTING.md). The harness
adds result capture and database isolation; inspect it before reproducing on a host with real data.
Live deployment checks create Docker fixtures and need a suitable release/test host.

```bash
# From backend/, with Go 1.26.8 selected:
go build ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store
JD_DEPLOY_LIVE=1 go test -count=1 ./internal/deploy -run 'TestLiveC[45]' -v
JD_DEPLOY_LIVE=1 go test -count=1 ./internal/proxysvc -run 'TestLive(DockerCaddy|DeploymentWebroot)' -v
JD_DEPLOY_LIVE=1 go test -count=1 ./internal/api -run TestLiveDeploymentDatabaseConnection -v

# From frontend/:
bun run lint
bun run build
bun run test
bun run test:browser
```

An isolated retry was used only to investigate each existing failure:

```bash
# From backend/:
go test -race -count=1 ./internal/deploy -run TestFleetReadStaysWithinItsBudgetAtReferenceScale -v
JD_DEPLOY_LIVE=1 go test -count=1 ./internal/proxysvc -run TestLiveDockerCaddyIngressPreservesExistingSiteAndRestoresRoute -v
```

## Reproduce the Go observations without editing product code

The probes deliberately assert the current defects. They are investigation fixtures, **not regression
tests to merge as passing behavior**. After a fix, write acceptance tests for the desired behavior.

From the repository root, create a temporary overlay pointing to the saved sources:

```bash
JD_AUDIT_TMP="$(mktemp -d /tmp/jd-deployment-audit.XXXXXX)"
export JD_AUDIT_TMP
python3 - <<'PY'
import json
import os
from pathlib import Path

root = Path.cwd()
evidence = root / 'docs/audits/2026-09-16-deployments/evidence'
overlay = {'Replace': {
    str(root / 'backend/internal/deploy/audit_20260916_test.go'):
        str(evidence / 'deployment_audit_probe_test.go.txt'),
    str(root / 'backend/internal/api/audit_20260916_test.go'):
        str(evidence / 'api_audit_probe_test.go.txt'),
}}
(Path(os.environ['JD_AUDIT_TMP']) / 'overlay.json').write_text(json.dumps(overlay))
PY
cd backend
go test -overlay "$JD_AUDIT_TMP/overlay.json" -count=1 ./internal/deploy ./internal/api \
  -run '^TestAudit(SvelteKit|GoBuildCommand|PreviewInheritance|NamedVolume)' -v
JD_DEPLOY_LIVE=1 go test -overlay "$JD_AUDIT_TMP/overlay.json" -count=1 ./internal/deploy \
  -run '^TestAuditBuildScopedVariableObservation$' -v
```

The build-variable probe creates/removes an audit-tagged image and temporary build files. The backup
probe archives only its temporary fixture directory. The preview probe writes only a temporary test
database; it never starts a preview against production resources.

The first preview experiment attempted to update an immutable plan row. Its failure was in the audit
fixture and is preserved in `data-boundary-probes.txt`. The corrected probe inserts revision 2 and
selects it as desired, preserving the product's additive revision contract. Only the corrected run is
used as evidence for preview inheritance.

## Reproduce frontend defaults

From `frontend/`:

```bash
bun ../docs/audits/2026-09-16-deployments/evidence/frontend-default-probe.ts
```

The final probe source was reconstructed into a standalone file from the same fixture inputs and
rerun to check that it reproduces the captured output. It prints the resulting port/check count,
Dockerfile name, and build secret mappings; it does not build or deploy an application.
