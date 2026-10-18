# Operation progress feedback

> **Superseded on 2026-10-16.** The working bar now appears only inside dialogs, drawn into `Modal`'s
> footer; the page panel, the job-console overlay, `OperationOverlay` and `use-operations` were
> removed. See the [dialog progress recordings](../2026-10-16-dialog-progress/). What follows is the
> record of the earlier placements.

Long waits now keep an action name, elapsed time and a visible working track above the content.
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

## Overlay revision

Progress now floats above its owner instead of inserting and removing rows. Page feedback uses
one compact panel at the upper right; simultaneous file operations stack inside it. Dialog feedback
stays inside the dialog above its existing footer, with long output scrolling inside the overlay
rather than covering its title or controls. Job feedback overlays a consistently sized transcript
area. Forms, listings and workbench columns retain their bounds while the reading appears
and disappears. Network tools and the proxy tester retain their empty results area during a request.
Cancellation, dismissal, byte/count semantics and request ownership are unchanged.

Only one new recording was taken for this revision: [files-overlay.gif](files-overlay.gif). It shows
copying a file with the listing stationary before, during and after the floating feedback. The 42
recordings below are retained from the original implementation and show its earlier placement.

Every `OperationProgress` consumer uses an overlay. Placement inventory:

| Placement | Applies to |
| --- | --- |
| Floating page panel | Files copy, move, duplicate and extraction; Git workspace operations, including fetch, pull, push, submodules and LFS; backup runs and recovery verification; SQL table exports; SQL query exports; MongoDB exports; blocklist refresh; Network Tools diagnostics; proxy request testing. |
| Inside the existing dialog | Every shared `ConfirmDialog` action, including Docker reclaim/cleanup/prunes and Compose actions, file deletion, backup restores and DNS actions; unused Docker networks; Git clone/initialisation; SQL imports; MongoDB imports. |
| Over the transcript | Every shared `JobConsole`: Packages; SSH apply; Suricata setup/rule updates; Remote Desktop engine setup/repair and Stream installation; saved network diagnostic runs; network tool installation; proxy engine jobs; proxy stream setup; certificate overview/fleet, site issuance and TLS repair; database backup/restore jobs. |

Owners for the transcript placement are `packages/page.tsx`, `security/ssh-panel.tsx`,
`security/suricata-setup.tsx`, `remote-desktop/engine.tsx`, `remote-desktop/stream.tsx`,
`network/run-inspector.tsx`, `network/install.tsx`, `proxy/engine.tsx`, `proxy/stream-setup.tsx`,
`proxy/certs-panel.tsx`, `proxy/tls-fleet.tsx`, `proxy/site-certificate.tsx`, `proxy/tls-fix-sheet.tsx`
and `database/ops/backups-job.tsx` (component paths relative to `frontend/src/components`, except
the Packages route).

To reproduce just the new GIF, with a production frontend already running:

```bash
cd frontend
JD_BROWSER_BASE_URL=http://127.0.0.1:43127 JD_PROGRESS_RECORD=1 \
  JD_PROGRESS_FRAMES=/tmp/jd-overlay-frames \
  bunx playwright test tests/browser/files-ui.spec.ts --grep 'overlay:.*1280' --workers=1
cd ..
python3 docs/audits/2026-10-14-operation-progress/make-gifs.py /tmp/jd-overlay-frames
```

## Recordings

These original GIFs show the command before starting, the earlier working UI, then settlement or refusal.

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

## Original implementation validation

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

## Overlay validation

- The final production build, changed-file Prettier/ESLint, TypeScript and all **3,564** fast tests
  passed. The revision uses the required selector against the preceding validated commit,
  `scripts/test-changed.sh 5b47039a`. See
  [PR #225](https://github.com/JustDashboard/Just-Dashboard/pull/225) for the selector's result and
  the GitHub checks.
- **17** focused cases passed on the final production build, covering confirmation geometry,
  console geometry at completion, measured exports and cancellation, import processing, reduced
  motion, phone layout and long Compose output staying clear of the dialog's title and buttons.
- Files copy bounds stayed identical before, during and after the operation at **1280px** and
  **390px**. The one new GIF is animated at **1280 × 900**, with **22** frames. All **42** original
  GIFs remain byte-for-byte unchanged.
- An initial geometry assertion sampled the end of the dialog's opening animation. It now waits
  for that animation before measuring; all five Docker confirmation cases passed on retry and
  again on the final build.

The frontend design, data-flow and feature guides document overlay placement and sizing. The root
operator/contributor guides, backend/deployment guides, configuration and `AGENTS.md` need no
updates: commands, ownership, capabilities, typed phrases and installation are unchanged.
