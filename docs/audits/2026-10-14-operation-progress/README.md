# Operation progress feedback

Long waits now keep an action name, elapsed time and a visible working track beside the command.
Docker prunes and cleanup, Compose confirmations, file operations, Git, backup runs/restores,
database transfers, network tools and shared job consoles use the same reading. A measured byte or
item count can fill the track; unknown completion sweeps. Upload completion switches to database
processing, and a quiet transcript retains its working reading. Compose's latest output remains
visible inside its active confirmation. Failed prune reports and partial batch deletions do not
also announce a generic completed success.

These recordings come from the production frontend on loopback port 43127, with mocked API
responses held until the working state is captured. Export fixtures send a bounded byte stream.
No recording prunes, copies, restores, installs, changes SSH/DNS or sends probes on a live host.
They verify the browser interaction, not native operation duration, host progress or backend
acceptance. Existing request ownership and cancellation rules remain; no new backend stages or
durable jobs were added. Scheduled backup output is still recorded at completion.

The shared console is recorded separately in Packages, certificate renewal, Suricata rule updates,
SSH apply, network tool installation and both Remote Desktop engine/Stream installation flows.

## Recordings

Each GIF shows the command before starting, the actual working UI, then settlement or refusal.

### Docker reclaim and cleanup

| Flow | Recording |
| --- | --- |
| docker reclaim | [docker-reclaim.gif](docker-reclaim.gif) |
| docker images | [docker-images.gif](docker-images.gif) |
| docker build cache | [docker-build-cache.gif](docker-build-cache.gif) |
| docker containers | [docker-containers.gif](docker-containers.gif) |
| docker untagged | [docker-untagged.gif](docker-untagged.gif) |
| docker volumes | [docker-volumes.gif](docker-volumes.gif) |
| docker networks | [docker-networks.gif](docker-networks.gif) |
| docker overview cleanup | [docker-overview-cleanup.gif](docker-overview-cleanup.gif) |
| docker compose | [docker-compose.gif](docker-compose.gif) |

### Files

| Flow | Recording |
| --- | --- |
| files copy | [files-copy.gif](files-copy.gif) |
| files move | [files-move.gif](files-move.gif) |
| files duplicate | [files-duplicate.gif](files-duplicate.gif) |
| files extract | [files-extract.gif](files-extract.gif) |
| files delete | [files-delete.gif](files-delete.gif) |

### Git

| Flow | Recording |
| --- | --- |
| git clone | [git-clone.gif](git-clone.gif) |
| git fetch | [git-fetch.gif](git-fetch.gif) |
| git pull | [git-pull.gif](git-pull.gif) |
| git push | [git-push.gif](git-push.gif) |
| git submodule | [git-submodule.gif](git-submodule.gif) |
| git lfs | [git-lfs.gif](git-lfs.gif) |

### Backups

| Flow | Recording |
| --- | --- |
| backup run | [backup-run.gif](backup-run.gif) |
| backup files | [backup-files.gif](backup-files.gif) |
| backup database | [backup-database.gif](backup-database.gif) |
| backup verify | [backup-verify.gif](backup-verify.gif) |

### Database transfers

| Flow | Recording |
| --- | --- |
| database table export | [database-table-export.gif](database-table-export.gif) |
| database query export | [database-query-export.gif](database-query-export.gif) |
| database mongo export | [database-mongo-export.gif](database-mongo-export.gif) |
| database sql import | [database-sql-import.gif](database-sql-import.gif) |
| database mongo import | [database-mongo-import.gif](database-mongo-import.gif) |

### Network and proxy tools

| Flow | Recording |
| --- | --- |
| network diagnostic | [network-diagnostic.gif](network-diagnostic.gif) |
| network blocklist | [network-blocklist.gif](network-blocklist.gif) |
| network dns apply | [network-dns-apply.gif](network-dns-apply.gif) |
| network dns create | [network-dns-create.gif](network-dns-create.gif) |
| network dns remove | [network-dns-remove.gif](network-dns-remove.gif) |
| proxy request test | [proxy-request-test.gif](proxy-request-test.gif) |

### Shared job consoles

| Flow | Recording |
| --- | --- |
| packages job | [packages-job.gif](packages-job.gif) |
| proxy certificate job | [proxy-certificate-job.gif](proxy-certificate-job.gif) |
| security suricata job | [security-suricata-job.gif](security-suricata-job.gif) |
| security ssh job | [security-ssh-job.gif](security-ssh-job.gif) |
| network install job | [network-install-job.gif](network-install-job.gif) |
| remote desktop install job | [remote-desktop-install-job.gif](remote-desktop-install-job.gif) |
| remote desktop stream job | [remote-desktop-stream-job.gif](remote-desktop-stream-job.gif) |

## Reproduce

Build and start the frontend from this task's worktree, as in `AGENTS.md`. Record only the progress
cases; `JD_PROGRESS_FRAMES` keeps their frames outside Playwright's disposable output directory:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43127 JD_PROGRESS_RECORD=1 JD_PROGRESS_CDP=1 \
  JD_PROGRESS_FRAMES=/tmp/jd-progress-frames bunx playwright test \
  tests/browser/operation-progress.spec.ts tests/browser/operation-jobs.spec.ts \
  tests/browser/git-ui.spec.ts tests/browser/git-features.spec.ts \
  tests/browser/files-ui.spec.ts tests/browser/backup-recovery.spec.ts \
  tests/browser/database-data.spec.ts tests/browser/database-query.spec.ts \
  tests/browser/database-mongo.spec.ts tests/browser/network-tools.spec.ts \
  tests/browser/network-dns-services.spec.ts tests/browser/network-gateway.spec.ts \
  tests/browser/stack-detail.spec.ts --grep 'progress:' --workers=1
cd ..
python3 docs/audits/2026-10-14-operation-progress/make-gifs.py /tmp/jd-progress-frames
```

The conversion helper requires Pillow. `JD_PROGRESS_CDP=1` uses Chromium's direct screenshot
capture for repeated frames. The network installer recording leaves unrelated throughput charts
without samples so they do not consume the recording's screenshot budget. Normal browser
verification does not take screenshots on each progress frame.

## Validation

- `bun run build`: passed on the final application source. The query export observes a native
  stream and lets the browser assemble its Blob, avoiding an additional retained chunk array.
- `scripts/test-changed.sh patch/0.7.5`: changed-file Prettier, ESLint, `tsc --noEmit` and all
  **3,564** fast tests passed. The selector ran **1,465** browser cases: **1,398 passed**, **55 existing
  skips**, and **12 initial failures**. Eleven loaded stale bundles while the frontend was rebuilt;
  the twelfth was a substring assertion matching both the job title and its new working label.
  The server was restarted, the assertion made exact, and the saved **12-case failure set passed**
  on the final production build with `--last-failed --last-failed-file=/tmp/jd-progress-last-run.json`.
- A further **25 targeted cases passed** on that final server, covering the working flows,
  upload/processing separation, byte-stream exports and cancellation, partial cleanup refusal,
  repeated-read recovery, reduced motion and phone layout. Focused loading retries also passed.
- All **42** GIFs were opened as animated media and verified at **1280 × 900**; their gallery links
  resolve. Recordings use the actual frontend with mocked operations, not native host acceptance.

No whole browser suite, whole Go test suite, CI or release changes are part of this work. The
frontend design, data-flow and feature guides were updated. `AGENTS.md`, the root `README.md`,
`CONTRIBUTING.md`, backend/deployment guides and configuration need no changes: installation,
capabilities, typed phrases, backend operation ownership and the required commands are unchanged.
