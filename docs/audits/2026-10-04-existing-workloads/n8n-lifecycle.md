# Real n8n adoption lifecycle

On 2026-10-04, `TestLiveManagedN8NAdoptionAndRollback` passed in 163.99 seconds using the
catalogue's actual `n8nio/n8n:2.39.10` image, not an HTTP server shaped like n8n. Its immutable local
image ID is recorded in [the sanitized evidence](managed-n8n-lifecycle.json).

The fixture used a unique container, Docker network, named data volume and loopback port, plus a
temporary dashboard database. The dashboard environment ID was seeded from a unique large value
to prevent its managed Docker namespace from overlapping an installed dashboard. It changed no
operator application. Cleanup verified that its containers, volume and network were removed.

The test created an owner, a real workflow containing a persistent marker, and an encrypted HTTP
header credential through n8n's own API. At each stage it read the saved workflow, checked the
SQLite database and asked n8n's credential export command to decrypt the credential with the
original encryption key. Private fixture values and response bodies do not enter the evidence.

- Adoption preserved the original container ID, PID, start time, Config and HostConfig.
- Both the original and desired environment captures were independently sealed; draft/recovery
  JSON contained no encryption-key value.
- The normal deployment engine applied the recovered Compose recipe and passed n8n's real
  `/healthz/readiness` endpoint. Workflow data and the decrypted credential survived.
- Baseline rollback succeeded, again preserving the saved workflow, decrypted credential and
  original named volume. The immutable image, image user, memory and CPU limits were retained.

An initial strict run correctly refused 1,595 writable-layer paths. Investigation traced them to
regenerated editor assets/type definitions and an empty upload directory. The final run used the
backend's positive proof of the unchanged pinned generators, corresponding shipped asset names,
exact generated metadata files and an empty upload directory. Unknown additions, changed/deleted
files, altered source, symlinks and actual uploads remain blockers. No broad cache or `/tmp`
exception was added to make the fixture pass.

This proves one standard single-container n8n installation using SQLite in its existing data
volume. It does not claim queue-mode or external-database n8n coverage, version upgrades, schema
reversal, or migration of an operator's production app. Application data and schema writes still
require an appropriate verified backup before deploying changes.

Reproduce from the task worktree:

```bash
docker pull n8nio/n8n:2.39.10
cd backend
JD_N8N_ADOPTION_LIVE=1 JD_ADOPTION_EVIDENCE_DIR=/tmp/jd-n8n-proof \
  go test ./internal/deploy -run '^TestLiveManagedN8NAdoptionAndRollback$' -count=1 -v -timeout=20m
```

n8n documents [persisting its SQLite database and encryption data in `/home/node/.n8n`](https://docs.n8n.io/deploy/host-n8n/install-options/install-with-docker.md)
and distinguishes [database readiness from mere reachability](https://docs.n8n.io/deploy/host-n8n/keep-n8n-running/monitor-n8n.md).
