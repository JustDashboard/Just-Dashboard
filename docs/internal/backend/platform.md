# Platform services

What the features share: streaming, jobs, secrets and agent mode; then the dashboard's own
configuration, version, releases, self-update, settings and uninstalling.

## Streaming, jobs, secrets, agent mode

**`internal/wsx`** wraps gorilla/websocket: origin check on upgrade (a WS handshake is not subject to
CORS, so this is what stops a malicious page using the operator's cookie), serialised writes, ping/pong,
1 MB read limit, `Envelope{type, data, error, ts}` frames. **Server-side filtering is the rule** — log
grep and level filters apply before lines are sent. A container log line is capped at 256 KB
(`dockerx.maxLogLine`), so a container emitting a gigabyte without a newline cannot exhaust memory.

**`internal/jobs`** runs what takes minutes — certbot, package upgrades, sshd applies — which as ordinary
handlers held a request open for the length of a certbot run and left a dropped VPN meaning nobody knew
whether their SSH config had applied. It is deliberately **not** the compose runner's shape:
`RunComposeStream` owns its command and refuses to reconnect, because re-issuing the GET runs the command
again. A job inverts that — `context.Background()`, a ring buffer with a sequence per line, and
`Subscribe(id, after)` resuming from what the client already has — so closing the tab leaves the work
running and the transcript complete.

- `Manager.Start(spec, run)` returns immediately; the API answers `202`. `StartExclusive(prefix, …)`
  starts nothing while a job of that kind family runs and returns that job instead, checked and started
  under one lock. The `certbot.` family also takes certbot's turn (`proxysvc.HoldCertbot`, wrapped
  around every runner by `api.startCertbotJob`): a job that finds a deployment run ordering a
  certificate starts as before and waits in its own console, naming the run, as the run's certificate
  step waits for a job — one certbot across the dashboard, with certbot's own lock left to arbitrate
  only against what the dashboard does not run (see the certificate step in
  [`deployments/implementation.md`](../deployments/implementation.md)). `Emitter` gives runners `Status`, `Line`, `Run`/`RunEnv` through
  `hostexec.CommandOnHost`, and `RunCmd` for a command the caller built on the side it chose. A slow subscriber is skipped rather than allowed to stall
  the command — the buffer is the record, the channel only the tail.
- **`RunDetached` is for work that must finish even if the dashboard does not** — today every package
  install, upgrade and removal (`api.runPackageCommand`). A child of the backend lives in its container's
  cgroup and dies when the container stops, and an apt upgrade that configures docker-ce stops it:
  dpkg was killed mid-configure and every later apt command refused with "dpkg was interrupted". So the
  command runs as a transient host unit, `just-dashboard-job-<id>.service`, via `systemd-run` with stdout
  and stderr written to `<data dir>/jobs/<id>.out|.err` rather than to a pipe (a dead reader would kill
  the writer with SIGPIPE). Every argument's `$` is doubled, because systemd expands `$VAR` and `${VAR}` in
  a unit's command line and `$$` is its literal dollar. The job tails the files every 200 ms and asks `systemctl show` once a second;
  cancelling or the timeout runs `systemctl stop` and still waits for the unit to end. A `<id>.json`
  record (job, shown argv, deadline, boot id) written after the unit starts lets `Manager.Adopt`, called
  at startup, re-register the job under the same id — the watching console reconnects to it — and follow
  the unit to its end, or replay a unit that finished while nothing watched. Failed units stay loaded
  until their code is read and `reset-failed`; a successful one is collected by systemd and reads as
  success. A different boot id means the server rebooted under the command, which is reported as a
  failure. Without root, a host systemd or the data directory at the same path on the host, the
  command runs as a child exactly as `RunEnv` would, and a failed `systemd-run` says so in the console.
  `JD_DETACHED_JOBS_LIVE=1` (as root) runs `detached_live_test.go` against the real host systemd.
- Bounded: 5000 lines a job, 64 KB a line, 50 jobs. `prune` runs on finish as well as on start, or a burst
  ending after the last `Start` sits over the cap until something else happens, which on an idle dashboard
  is never. A running job is never pruned.
- **Validation stays synchronous** — a bad email, a wildcard over HTTP, an sshd change that would lock the
  operator out — so a refusal answers the click rather than arriving a minute later as a failed job. That
  is why `certbot.go` exposes `IssueArgs`/`RenewArgs`/`RevokeArgs`, `netsec.PlanSSHSettings` is separate
  from `ApplySSHPlan`, and `updates.UpgradeCommand` returns an argv.
- `GET /jobs/{id}/stream` sends job, backlog, then batches every 120 ms. Subscribing after a job
  finishes still returns its retained backlog and closes the live stream immediately. Output delivery,
  disconnects and completion share the job lock so a closing subscriber cannot interrupt the runner.
  Cancelling is `service.control`.

**Secrets, four places, one goal.** `main.scrubSecretEnv` unsets boot secrets (`JD_` and `VPSD_`) once
consumed. `deploy.mergeEnv` strips every `JD_*`/`VPSD_*` from a deploy child's environment — the command
is content the repository owner controls — and is the half that keeps working when a new secret-bearing
variable is added later. `dbx` never puts a password in argv (`/proc/*/cmdline` is world-readable):
`PGPASSWORD` in the environment, a temporary defaults file for MySQL. `dockerx.RedactEnv` masks
credential-shaped container env, because that is where deployments keep secrets and container detail is
not a `system.admin` route.

**Agent mode** (`JD_AGENT_MODE` / `-agent`) swaps the human login surface for mutual TLS: no password
route, no session, no 2FA. `httpx.HubOnly` admits only the enrolled hub's certificate; `/agent/enrol` is
the one route reachable before enrolment, which is why TLS *asks* every caller for a certificate without
requiring one at the handshake and `HubOnly` enforces per route. The enrolment token is printed once per
boot while unclaimed. Feature routes are the same program either way. Not useful standalone yet.

## Configuration, version, release, self-update

**`internal/config`** resolves `JD_*` at boot and **fails closed**: no `JD_ALLOWED_CIDRS` with a
non-loopback bind is a startup error, a missing or malformed `JD_MASTER_KEY` is fatal. A `loader`
collects *every* malformed setting so `Load` refuses with the whole list rather than one error at a time.
`config.Env` falls back to the legacy `VPSD_*` prefix and `adoptLegacyData` picks up pre-rename
`/var/lib/vps-dashboard`. A handful of variables are **not** in `config.go`: `JD_LOG_LEVEL` and
`JD_BOOTSTRAP_USER`/`JD_BOOTSTRAP_PASSWORD` are read in `cmd/server/main.go`, and `JD_SITE` belongs to
`deploy/Caddyfile`. Wherever a variable is read, its documentation lives in four places that must stay in
step: **the reading site, `.env.example`, `docker-compose.yml`, and the configuration table in `docs/guides/configuration.md`.**
`JD_ACCOUNT_URL` and `JD_ACCOUNT_LINK` follow it: `config.go` reads both (the URL through
`accountlink.ParseSiteURL`, so a malformed one refuses to start) and [account link](account-link.md) says what
they decide. `JD_USAGE_PING` follows it too, and [the usage count](usage-ping.md) says what it decides. An install
that is never linked, and whose administrators never said yes to the count, sends the site nothing.
`JD_HUB_LINK` and `JD_HUB_SIGNIN` follow it as well ([hub link](hub-link.md)). `JD_HUB_LINK_TEST_ORIGIN` is the
one deliberate exception: a loopback origin for test harnesses, read by `config.go` (through
`hublink.ParseTestOrigin`, so anything else refuses to start) and the terminal commands, and on purpose in no
`.env.example`, Compose map or operator guide, so a Compose install cannot pick it up from `.env`. Beside it
`JD_HUB_SIGNIN=true` refuses to start unless the binary was built with `-tags hublinktest`, as only the local
end-to-end harness's is (`hubsignin_test_build.go`).

### Cutting a release

When the user names a version ("make this 0.6", "release 0.6.1"), that is the trigger. Cutting a release is a
commit: the changelog on the tracked branch is what every install compares itself against. *Releasing* it
is the tag `v<version>`, which nobody makes by hand (below) and without which no install is offered the
version. Six files carry it. `scripts/release.sh <version>` writes five of them,
`backend/internal/version/version.go`, `frontend/src/lib/version.ts`, `frontend/package.json`, `README.md`
and the `info.version` of `docs/api/deploy-openapi.json`, and the sixth,
`backend/internal/selfupdate/changelog.json`, is written by hand first. Tests fail the run if they drift
(`version_test.go`, which skips a file that is absent and also holds the API description to the version,
and `TestChangelogHeadIsTheProductVersion`).

```bash
# 1. Write the release notes FIRST in backend/internal/selfupdate/changelog.json
#    (anywhere in the array; it is sorted on read).
# 2. Then:
scripts/release.sh 0.6
```

`release.sh` bumps the version files, regenerates the root `CHANGELOG.md` from the same JSON, regenerates
the API description (`JD_UPDATE_OPENAPI=1`, so its `info.version` follows the release instead of staying
where the last route change left it), and
runs the tests; it refuses before touching anything if the changelog does not already describe the
version, and prints the skeleton. **`CHANGELOG.md` is generated — never edit it by hand.** The ordering
is the point: a version bumped with no changelog entry is a release nobody is told about, and a changelog
entry with no version bump is every install permanently offering itself an update it already has.

Before the release is cut, run `scripts/deps-check.sh` (it needs the network): `govulncheck` over the
backend, `bun audit` over the frontend, and the direct modules and packages that are behind. It fails on
an advisory that reaches the code unless the script lists it as accepted with the reason, which is only
for an advisory with no fixed version (today the two daemon-side ones in `github.com/docker/docker`,
which end when `internal/dockerx` moves to `github.com/moby/moby/client`). `release.sh` names it in the
steps it prints and does not run it, because a release must be cuttable offline;
`scripts/test_deps_check.py` checks what it makes of a report.

The same moment moves the images. The five images the stack is built from (`golang`, `debian` twice,
`oven/bun`, `node` twice) and the one it runs as it is (`caddy`) are named `name:tag@sha256:...` in
`backend/Dockerfile`, `frontend/Dockerfile` and `docker-compose.yml`: Docker uses the digest, and the tag
stays to say which line of releases the digest follows. `scripts/pin-images.py` asks Docker Hub what
each tag names today and rewrites the digests; `--check` (and `scripts/test_pin_images.py`, which
`test-changed.sh` runs for a change to any of the three files) fails when a reference has none. Named
by tag alone, what an update built was whatever the registry served that day, and Caddy was never
fetched again after the first install, because Compose pulls a tag only when it is missing; a server
installed with 2.11.7 was still on it under 0.8.5 with 2.11.9 out. With a digest neither the installer
nor the updater needs `--pull`: a digest that is not on the machine is fetched, by the build for a base
image and by `compose up` for Caddy, and one that is there is not asked about, so an update no longer
needs Docker Hub at all unless the release moved an image. Run it, then `docker compose build`, before
the release is merged: no check builds the images (OPS-10's job is a proposal), so this is the one
place a moved base is tried. A release cut without it keeps the previous release's images, which is
safe and is how base images age: nothing forces the step. Not covered: the Caddy containers
`internal/proxysvc` starts by name for the deployments' ingress and the tailnet gate
(`docker run ... caddy:2-alpine`), which keep the image they were created from until they are removed.

A note added to a release whose version is already bumped — every change merged into an open patch
branch after its release was cut — is in `changelog.json`, and so in every install's notice, but not
in `CHANGELOG.md` until the markdown is generated again. Nothing fails meanwhile:
`TestRootChangelogMentionsThisRelease` checks only that the release has a section there, not what the
section says. Run `scripts/release.sh` with the version the branch already carries (`0.7.5` on
`patch/0.7.5`): the version files are rewritten with the values they hold, so the only file that
changes is `CHANGELOG.md`. Its first step alone does the same without the tests:
`cd backend && go run ./scripts/gen-changelog.go -expect 0.7.5 -out ../CHANGELOG.md`, which refuses
unless the changelog's newest release is the version named. Do it last before the branch is merged into
`main`, because the GitHub release takes its notes from that section.

A release is cut on its patch branch (`patch/0.7.2`), merged into `main`, and reaches installs when that
merge is tagged. `.github/workflows/release.yml` runs after Verify passes on a push to `main`, reads
`latest` from the changelog, and when no release `v<latest>` exists, tags that commit and publishes the
release with the version's `CHANGELOG.md` section as its notes. Until 0.8.6 the merge itself was the
announcement and the tag only decorated the Releases page: 0.8.5 was offered to every install for days
with a failed Verify run and no tag. Now a failed run means nothing is tagged and nothing is offered. The version
comes from the changelog, never from the branch name, so a merge whose version is already released
publishes nothing.
Nothing is tagged by hand; a manual run of the workflow on `main` retries a failed publish.

### Which build this is

The version constant is bumped only when a release is cut, so everything built from a patch branch, a
fork or an operator's own commits carries the last release's number. Such a build is told apart by the
commit it was made from: `version.Commit`, set by the linker from the `JD_COMMIT` build argument of
`backend/Dockerfile`, and `version.Build()` reads `0.8.5+98c9349`. `version.Version` stays the only
thing compared; the commit is only ever shown, in the backend's start line, in `jd version` (the same
argument is appended to `main.version`) and on the Version page (`commit` in the report of
`GET /dashboard/update`, absent for a release).

The argument is passed by the three things that build the stack and only when the checkout is **not**
on the release's own commit, so a release's compile layer stays cached and a release never shows a
commit: `selfupdate.BuildCommit` for an update and a rebuild from Settings (through
`BuildEachService`), and `jd_build_commit` in `scripts/build-stamp.sh` for `install.sh`. They are the
same rule in two languages and are changed together. HEAD is a release when it is the commit that
`refs/tags/v<version>` names, or that `refs/just-dashboard/release/v<version>` names. The second ref is
written by the updater when it installs a tag: the tag it verified was asked of origin and is not
stored, and the checkout's own tag of that name may be missing (a checkout that fetches without tags)
or the operator's. Limits, on purpose: a plain `docker compose build` passes nothing and calls itself a
release; a checkout git cannot read is built unstamped; uncommitted edits are not marked; the frontend
bundle is not stamped, so the wordmark keeps the release number and the Version page is where the
commit shows.

### Signed releases

A tag says a version was released, not by whom: whoever can push to the repository can push a tag, and an
update builds and runs what it names as root. `internal/selfupdate/signing.go` can require the tag to be
signed with a key the *running* release was built with, so a stolen token or a compromised workflow
secret cannot ship code to installs by itself. **It ships switched off**: `allowed_signers` (embedded
beside the changelog) names no key, `SignaturesRequired()` is false and nothing is verified. Making the
key, guarding it and signing with it are the project owner's steps, and this is the order to do them in.
`scripts/manage.sh update`, the update over SSH, makes the same checks against the `allowed_signers`
committed in the checkout ([terminal tools](../operations/terminal-tools.md#version-update-and-rollback)),
so the two are changed together:

1. Make a key that lives nowhere in the repository: `ssh-keygen -t ed25519 -C release@just-dashboard.com
   -f jd-release`. Keep `jd-release` offline or in the one secret the release job reads; a second copy
   in a safe is what makes losing it survivable.
2. Have the release sign. `.github/workflows/release.yml` makes a lightweight tag today; it has to make a
   signed one instead, with the key from a repository secret:
   `git -c gpg.format=ssh -c user.signingkey=<path to the private key> tag -s v<version> -m "Just Dashboard <version>"`.
   That is a change to CI, which is the operator's to ask for by name (`AGENTS.md`).
3. Add the public half to `backend/internal/selfupdate/allowed_signers` as one line,
   `release@just-dashboard.com namespaces="git" ssh-ed25519 AAAA…`, and release.
   `TestTheShippedSignersFileIsWellFormed` holds the line to that form, because `ssh-keygen` stops at the
   first line it cannot parse and a file it cannot parse verifies nothing.
4. **Sign that release's own tag and every one after it.** Installs older than it check nothing and take
   it either way. Installs *on* it refuse any later tag that is not signed, so one unsigned release after
   the key ships leaves every install unable to update from the dashboard until a signed one follows.

Do steps 2 and 3 in the same release, in that order of readiness: a key in the file before the workflow
can sign is the outage in step 4.

What is checked, once a key is there (`verifyReleaseTag`, from `updateTarget`, for the project's own
repository only): the fetched ref is a tag object, not a lightweight tag; the tag object's own name is
`v<ToVersion>`, since a signature covers the name and a real signed tag for an older release pushed again
under a newer name would otherwise verify; and `git verify-tag` accepts it with every deciding setting
given on the command line (`gpg.ssh.allowedSignersFile` set to the embedded file written to a temporary
file, `gpg.ssh.program=ssh-keygen`, `gpg.minTrustLevel=fully`, and the OpenPGP and X.509 programs set to
`false`), so nothing in the checkout's own `.git/config` can vouch for a tag. The verdict line goes into
the transcript. A refusal changes nothing and says so. The image already carries what this needs: `git`
and `openssh-client`'s `ssh-keygen`.

A fork named with `JD_UPDATE_REPO` is not held to the project's key, which did not sign its tags; a fork
that wants the same check replaces the file with its own key. The check that *offers* a version cannot
see a signature (it reads one file over HTTPS), so an unsigned tag is still offered and then refused at
install with the reason.

To replace a key, add the new line in a release signed with the old key and remove the old line one
release later; installs must pass through the release that carries both. If the key is lost with no
successor shipped, installs cannot verify anything new and are updated by hand from the shell (a
fast-forward of the checkout and `sudo ./install.sh`) to a release that ships the new key. The tests make their
own key with `ssh-keygen` and sign real tags in a real repository (`signing_test.go`); there is no
release key to test with until step 1 is done.

A changelog entry is written for the person deciding whether to upgrade a root-equivalent panel on their
own server. Each change has a `kind` (`added`, `changed`, `fixed`, `removed`, `security`, `deprecated` —
closed set, enforced on the committed file) and reads as what they can now do; `detail` is for a non-obvious
consequence, and most changes need none. `breaking: true` requires a `breakingNote` naming what must be
done by hand, and the UI refuses to fold it away.

**`internal/selfupdate`** manages the product rather than the server.

- **The changelog is data, not prose**: embedded with `go:embed` *and* fetched from
  `raw.githubusercontent.com`. The compiled-in copy is read by `ParseManifest`, which refuses anything it
  cannot render, so a malformed file fails the test run before it can be a malformed file every install
  downloads. It is what an install with `JD_UPDATE_CHECK=false` still shows.
- **The published changelog is read leniently** (`ParsePublishedManifest`): the file the check fetches,
  and the one in the commit an update is about to install, which the *old* image reads. The reader is
  always older than the file, so a field it does not know is ignored, a kind outside its six is shown as
  `changed`, a day that grew a time of day is cut back to the day, and an entry it cannot make sense of
  is left out while the rest are kept. Every release it keeps still has a comparable version, a title, a
  day and a change with text. `Latest` is the newest release it could read; the versions named but not
  read are in `Manifest.Unread`, and a check with nothing readable to offer and a newer one unread
  reports that as its error instead of "up to date". Until 0.8.6 the strict parser read both, so one new
  field would have made every install report that the published changelog could not be read, the release
  carrying the fix included.
- **The published file has a size every old install fixed.** Releases up to 0.8.5 read the first 1 MiB
  (`FieldManifestCap`) and fail to parse what is cut off; from 0.8.6 the ceiling is 8 MiB and a longer
  file is reported as too long. The file was 300 KB at 0.8.5 and grew about 4 KB a day, so
  `TestTheChangelogStaysWhereInstallsInTheFieldCanReadIt` fails at 700 KB. When it does: keep the recent
  releases in `changelog.json`, move the older ones to a file beside it that the build embeds and the
  check never fetches (`Local()` joins the two; `CHANGELOG.md` stays complete), and raise the test's
  number only once no install older than 0.8.6 is worth reaching. For the same reason a new field or kind
  is only ever *added* to this file: what an old reader needs (`version`, `date`, `title`, `changes[].kind`
  and `.text`) keeps its meaning, and a change of shape goes to a new path.
- **Only a released version is offered** (`Checker.released`, `TagsOnly`). The changelog on the tracked
  branch says which versions exist; whether one was released is whether the same file answers at
  `refs/tags/v<version>/` on the raw host (a `HEAD`, spelled with the full ref so a branch named `v0.9`
  cannot answer for the tag). For the project's own repository, whatever branch is tracked, the check
  walks the newer versions from the newest and offers the first tagged one and everything older; the
  newest untagged one is reported as `pending` and not offered. A tag found is remembered for the life
  of the process, a missing one is asked about again, at most `maxTagProbes` versions are asked about per
  check, and a probe that cannot be answered fails the check (the previous answer is kept) rather than
  guessing either way. A fork named with `JD_UPDATE_REPO` is offered every newer version as before, and
  `tagged` says whether the newest has a tag. The report carries `tagged`, `pending` and
  `check.tagsOnly`; the Version page's hint reads them through `frontend/src/lib/update-offer.ts`
  ("tagged v0.8.6", "no release tag, installs main as it stands", "0.8.6 is on main, not released yet"),
  and the update dialog says when what it installs is a branch head.
- **Update discovery is independently controlled**: one unauthenticated
  GET and the tag's `HEAD`, a user agent with product and version only, and a switch to turn it off. Automatic deployment
  branch monitoring uses separate outbound Git connections regardless of that switch. A failure keeps the
  previous good answer rather than blanking the banner — a dropped tunnel is a normal Tuesday here.
- Cadence is two floors, not a timer, because the moment somebody wants a current answer is the moment
  they open the page. `Freshness`: `Cached` nudges past `checkInterval` (2 h), `OnLoad` past
  `nudgeInterval` (5 min), `Forced` waits. Both nudges answer from cache **immediately** and check behind
  the request — blocking would turn one unreachable repository into page loads that hang for fifteen
  seconds.
- **Where the install lives is discovered, not configured**: which container bind-mounts our own data
  directory (decisive where a service name is not), then its compose `working_dir` label. `JD_UPDATE_DIR`
  is the escape hatch, and an unidentifiable install says so rather than showing a button that fails.
- **The upgrade runs in a sibling container**, and that is the load-bearing decision: `compose up -d
  --build` recreates the container running the command, so a child process is killed mid-way with the
  frontend and proxy never recreated and nothing able to report it. The backend creates a separate
  container through the socket, running its own image (which already carries git, docker and compose), and
  writes `self-update.json` + `self-update.log` into `JD_DATA_DIR` — mounted by both halves, so the *new*
  backend can read what the old one was doing. `Installer.Reconcile` at boot: alive → leave it; gone with
  the version moved → it worked (this process running is the proof); gone with the version unchanged → it
  stopped, and what it had changed is put back (below).
- It **builds one service at a time** (`selfupdate.BuildEachService`, also used by Settings' rebuild,
  and the same loop in `install.sh`). Compose builds every service at once, so the frontend's Turbopack
  peak (2.5–3 GB) and the backend's Go compile (over 1 GB) used to land together; in turn the server
  needs only the larger. The services are read with `compose config --services` from the file being
  built, and if that fails the single all-at-once build runs as before.
- It **fast-forwards, never resets** — unlike a managed deployment checkout, this is the operator's own
  checkout and an
  edited compose file is normal, so a local change survives unless it genuinely collides. Git runs there
  as root, so afterwards the updater hands back to the checkout's owner everything root-owned in `.git`
  and each file the update changed with the folders above it (`handBack`, through `hostexec.GiveBack`).
  Until 0.7.1 an update's new folders stayed root's, and the operator's own next pull stopped halfway in
  them.
- **It fetches the branch the install follows and the release tag, by name** (`fetchBranch`,
  `fetchReleaseTag`): `+refs/heads/<ref>:refs/remotes/origin/<ref>`, never the checkout's own refspec.
  A plain `git fetch origin` in an ordinary clone brings every branch, and the project's repository
  keeps pull requests' screenshots on `pr-assets/*` branches, so every update downloaded all of them
  made since the last one. It also makes the update independent of how the checkout was cloned: the
  install guide's clone is `--depth 1 --single-branch --branch main`, and the fast-forward to a tag,
  the fork's branch and `restoreCheckout`'s ancestor test are all tested from one
  (`TestAShallowSingleBranchCheckout...`). For the project's repository the branch only keeps
  `git status` honest, so one that cannot be fetched (a patch branch deleted once merged) is said in
  the transcript and passed over; for a fork it is what is installed, and failing to fetch it ends
  the update. Remote branches an older version already fetched are left alone; the administration
  guide has the commands that drop them.
- **It installs the release that was offered** (`updateTarget`). After the fetch it takes the tag
  `v<ToVersion>`, asked of `origin` with `ls-remote` and fetched without touching local tags, so a tag
  the operator made cannot stand in for it and one that clashes cannot fail the fetch. With no tag there
  are two answers, and the backend writes which into the record (`Run.RequireTag`, from `TagsOnly`)
  because the updater has no configuration of its own. For the project's own repository nothing is
  installed, and that includes an origin that could not be asked: an update that falls back to the
  branch whenever origin is slow has no gate. The check and the updater ask different places: the
  check reads the tag from the repository `JD_UPDATE_REPO` names, the updater from the checkout's
  `origin`. They disagree when the checkout was cloned from a fork or a mirror and `JD_UPDATE_REPO` was
  left alone: every check offers the version and every install refuses it, so that refusal names the
  two ways out (fetch the tag into the fork, or name the fork in `JD_UPDATE_REPO`) and does not say to
  wait. For a fork it uses `origin/<ref>`. Either way the
  changelog at the target must name `ToVersion`, or nothing changes. A branch that has moved past
  `ToVersion` with no tag to pin it is refused rather than installed, because nobody was shown that
  release's notes and checking again offers it properly. The version it reports
  (`Run.InstalledVersion`) is read from the changelog at `HEAD` after the merge and recorded only on
  success, and `Run.Tagged` says the commit it landed on is the one the tag names.
- **It goes back if the new version does not come up** (`rollback.go`). Before the build it tags each image
  the compose file builds `<image>:previous`; one that cannot be tagged is a warning, not a stop. On a
  failed build, a failed `up --wait` or a failed health check it retags them, runs `up -d --no-build`,
  moves the checkout with `git reset --keep <FromCommit>`, runs `up -d --no-build --remove-orphans`
  (the release may have changed the compose file itself) and probes for three minutes. A failed build
  has recreated no container, so it skips the first `up`: the release's compose file is never run against
  a stack still serving. The reset is `--keep`, so an operator's edit survives and one in the way makes
  git refuse. `update-index --refresh` comes first because `handBack`'s chown leaves the index stale,
  which `--keep` otherwise reads as an edit. The checkout stays put if somebody else moved it during the
  update. If an old image is missing after containers were replaced, stack and checkout both stay on the
  failed update, since moving only one would make them disagree. Every failure is reported in the run's
  error with the commands to finish by hand, and no step stops the ones after it. `.env` is not restored:
  a port a Reconcile moved was moved because something holds the old one, and the old version runs on the
  new one just as well. The rollback relies on database changes being additive (invariant 5), since the
  new backend may already have migrated; it does not put the database back. The `:previous` images stay
  until the next update replaces them, so a server holds one older generation on disk, and an uninstall
  removes them.
- **It prunes the build cache it has outgrown** (`PruneBuildCache`). Both images are built without cache
  mounts, on purpose (`backend/Dockerfile` records why: the updater that builds a release is always one
  release older than the file it builds, and a classic builder refuses `--mount` outright), so every
  update leaves its layers in BuildKit's cache. Docker's own collector lets an entry sit for sixty days
  and the cache grow until a fifth of the disk is left, which on a 20 GB server is updates failing for
  room first. So once the new version answers (`updateSteps.tidy`, never after a failure), and at the end
  of an uninstall, `docker builder prune --force --filter until=168h` runs with a two-minute bound and
  its total goes into the transcript. It is not `--all`, and a week keeps the build that has just run.
  The cache is the daemon's rather than this project's, so other builds' entries unused for a week go
  too, which costs their next build time and nothing else. Cache mounts for `GOCACHE` and `.next/cache`
  would cut a repeat update to tens of seconds; they stay out for the reason above, and would need the
  updater to refuse cleanly when it is older than the first release that uses them.
- **A backend that finds the updater gone puts back what it changed** (`putback.go`). The rollback above
  runs inside the updater, so an updater that is killed (the Docker daemon restarting under it, a reboot,
  the kernel taking it when the frontend build runs out of memory) rolled nothing back: the checkout
  stayed fast-forwarded and the images already built were `:latest`, while the old containers kept
  running. The run was marked failed, which was true until the next thing recreated the stack (a restart
  from Configuration, `manage.sh restart`) and brought up half of each version. Now `replaceStack` writes
  the images it tagged into the record before the build (`Run.SavedImages`), and `Reconcile`, once it has
  established the updater is gone and read the record again (an updater that rolled back by itself has
  closed the run, and its account is the one to keep), retags each `:previous` over its `:latest` on the
  configured daemon and runs the same `restoreCheckout` the rollback does: `reset --keep`, only when
  `HEAD` is exactly `Run.ToCommit`. A checkout the update never recorded moving is left where it is.
  **No container is recreated**: from inside one of them that is the manoeuvre the sibling exists to
  avoid, and with the tags and the checkout back, whatever recreates the stack next gets the old version
  whole. The run's error says what was put back and gives the one `compose up -d --no-build` to run if a
  container is still on a new image; whatever could not be done (a local edit in the way, Docker not
  answering) is listed with the commands, as a rollback's is. It all goes into the transcript after the
  dead updater's last line. Not covered: an updater killed *after* the backend was recreated on the new
  version is still settled as a success, though the frontend or proxy may not have been recreated yet.
- **It copies the database first** (`dbcopy.go`). Additive columns keep the old binary able to open the
  store, but opening it is not only additive (`store.Open` rewrites archived project names in place and
  maps legacy deployments into new tables), and a release whose migration is wrong is not protected by
  the rule at all. So `Installer.Start`, before it writes the record or starts the updater, has the store
  copy itself with `VACUUM INTO` (`Options.Database`, wired as `Server.updateDatabase`: the store's own
  connection, one read transaction, a whole database as of one moment) into
  `JD_DATA_DIR/pre-update/vpsd-<from>-<run id>.db`. The file is created empty and `0600` first, because
  it holds every password hash and sealed secret and SQLite would create it with the umask. The newest
  two are kept and only files of that name are ever removed. The copy is skipped when it would leave
  less than 512 MB free, and given up after three minutes; either way the update still runs, as it does
  when an image cannot be tagged, and the transcript's first lines say there is no copy and what that
  costs. Its path is in the record (`Run.DatabaseCopy`), the transcript, and the message of every
  rollback (`copyNote`). **It is never restored automatically**: going back to it discards everything
  written since, which after a rollback that worked is an hour of audit log lost for nothing, so that is
  left to a person, with the steps in the administration guide. The suggested "Dashboard data" backup job
  excludes the directory.
- A checkout cloned with sudo has no owner but root for `handBack` to return to, so at every start
  the backend gives the checkout's root-owned entries to the owner of the directory holding it
  (`Service.adoptCheckout`, through `hostexec.Adopt`), when that is not root: whoever owns `/home/ubuntu`
  can already replace `/home/ubuntu/Just-Dashboard` with a tree of their own, so this grants nothing, and
  a checkout in `/opt` or `/srv` stays root's. Without it that account's git refused the repository as
  of dubious ownership, `git worktree add` with it, and agents cloned beside it instead — a clone the
  Git page rightly lists as a second repository. A walk that changed anything then calls
  `Options.Adopted`, which the API uses to carry root's GitHub login to the new owner
  (`ghx.CarryLogin`; see [GitHub sign-in](processes-terminal-github.md#github-sign-in)). The walk never follows a link (directories opened
  `O_NOFOLLOW` relative to their parent, everything else `O_PATH` and re-checked on the descriptor),
  hands a directory over only after its contents, and leaves alone another account's files, a file with
  more than one link, anything only its owner can read (the installer's `.env`) and other filesystems.
  It runs under the lifecycle lock and skips a start with an upgrade, reconfiguration or uninstall in
  flight; the restart after it settles the tree. And it **waits
  for the health URL to answer** before calling itself finished, since `compose up -d` returns as soon as
  containers start and a backend that starts then dies looks identical from there.
- **The transcript is read two ways.** The report carries `Store.Tail()` — the last 64 KB, with a
  `… earlier output trimmed …` marker when there is more — because it is polled every two seconds during a
  run and a rebuild prints a few hundred kilobytes. `GET /api/v1/dashboard/update/log` answers
  `Store.Transcript()`, the whole file up to 8 MiB from the end, as `text/plain` with `no-store`; it is
  readable by the same every-role audience as the report that already carries its end. The console on
  `/dashboard` reads it once when the tail says it was trimmed and extends it with each polled tail
  (`frontend/src/lib/transcript.ts`).
- **The changelog is not in the report the page polls.** `GET /api/v1/dashboard/update?history=false`
  answers what is polled (every five minutes, every two seconds during a run): the version, the latest,
  the releases newer than the installed one, the check, the install and the run. `useSelfUpdate` says
  `history=false` on every read. **A request that does not say it still gets `history`**, as every
  report did before: a page that was open through an update goes on polling the new server with the
  script it loaded from the old one, and that script reads `report.history` as a list without asking
  whether it is there (the sidebar notice spreads it the moment the run succeeds, the Version page
  filters it on every poll). Leaving the field out of the default answer would throw in every tab left
  open through the update that brought the change in, so the default is not to be flipped while an
  install can still come from a version whose page reads it.
  What this build knows about its own past
  (`selfupdate.Local()`, about 280 KB) is `GET /api/v1/dashboard/update/history`, `{"history": […]}`,
  for the same every-role audience. It is encoded once per process and carries an `ETag` with
  `Cache-Control: private, no-cache`, so a browser keeps it and a reload costs a `304`; `etagHolds`
  also reads the tag the way a compressing proxy rewrites it (nginx's `W/"…"`, Caddy's `"…-gzip"`).
  `useUpdateHistory(version)` (`frontend/src/hooks/use-update-history.ts`) reads it once per version
  per tab: for `/dashboard`, and for the sidebar notice only when it has an outcome to head.

`GET /api/v1/dashboard/health` is the dashboard's reading of itself for whoever is signed in, kept apart
from `/healthz`, which the container's health check and the updater's rollback read:
[the dashboard's own health](../architecture/runtime-boundaries.md#the-dashboards-own-health).

### The dashboard's own settings

**`internal/selfcfg`** is the settings half of the same idea, and it borrows the same manoeuvre for the
same reason: applying a port change recreates the container serving the request that asked for it, so the
work runs in a sibling container (`just-dashboard-reconfigure`, this image, `-self-restart`) writing
`self-config.json` and `self-config.log` into `JD_DATA_DIR`. It shares `selfupdate`'s answer to "where is
this install" rather than asking Docker the same question twice — `Options.Locate` is
`selfupdate.Service.Location`.

Update, apply, restart, rebuild and uninstall admission share `JD_DATA_DIR/lifecycle.lock`, held before
any run record, transcript or `.env` backup can be replaced. All three durable run records
(`self-update.json`, `self-config.json`, `self-uninstall.json`) are checked under that lock: a
pending/running operation blocks the other paths even after the launching process exits.
Unreadable or corrupt records fail closed and must be dismissed explicitly. This prevents concurrent
requests from overwriting the configuration needed for rollback.

The three siblings are started, replaced (`docker rm -f` of the fixed name) and removed with the docker CLI,
always through `selfupdate.DockerCommand`, which appends `DOCKER_HOST=<JD_DOCKER_HOST>` to the inherited
environment as `dockerx`'s compose calls do. A CLI left to the process environment talks to
`/var/run/docker.sock` whatever `JD_DOCKER_HOST` says, so on a host where the two differ a restart or an
update would replace a container of the same fixed name on another daemon — another dashboard's. Under the
lock and before anything is written, update, apply, restart, rebuild and uninstall ask that daemon for its
version (`selfupdate.DockerReachable`, 15 s). One that does not answer refuses the operation with
`ErrDockerUnreachable` and a message naming the host and the CLI's own reason (`the restart was not started:
the Docker daemon cannot be reached at unix:///… (JD_DOCKER_HOST): …`): `503 docker_unavailable` from the
routes, a `failed` command for a linked account. The previous run's record and transcript, the `.env` and
its backup stay as they were, no `docker rm` or `docker run` is attempted, and an uninstall pauses nothing.
The tests stand a recording `docker` on `PATH` (`selfupdate/selfupdatetest`), so none of them reaches the
daemon of the machine running them.

- **One source of truth**: the `.env` beside the compose file, edited the way a person would. `EnvFile`
  keeps the file as lines, so the paragraph of explanation above each setting survives a change made from
  a browser, and a setting already present is replaced in place rather than appended. Every write takes a
  backup (`.env.jd-previous`) first — that copy is what the rollback restores.
- **Validation happens before anything is written**, because the process doing the checking is the process
  about to be restarted. Ports are range-checked and occupied or duplicate preferences are moved to available numbers.
  The lifecycle runner verifies actual ownership before startup and synchronizes `.env`, Caddy, the
  health probe and endpoint through `internal/stackports`; durations must parse; the allowlist must still contain
  loopback **and** the caller's own address. That last rule is the most valuable one in the package — the
  allowlist runs before authentication, so an operator who drops their own network out of it does not get
  an error page, they get a dashboard that has silently stopped existing for them.
- **Rollback is what makes the feature offerable at all.** If the new configuration does not answer its
  health probe, the sibling restores the previous `.env`, brings the stack back up on it and records
  `StatusRolledBack` — a failure of the change and a success of the net, which is why it is a fourth
  status rather than "failed".
- **No typed phrase on the apply**, deliberately. What has to be read is *where the dashboard will be*,
  since the browser that asked cannot follow it there — and the dialog says exactly that, listing every
  before-and-after and stating the URL to open next. Transcribing a forty-character MagicDNS name on top
  of that tested typing rather than attention, and rollback already covers the change that does not come
  back.
- **`Report.Drift`** compares the file with the running process on the fields this backend can observe
  about itself, which is what an operator who edited `.env` over ssh and never restarted is looking at.
- **`DetectTailscale`** is what stops the settings form rejecting its own suggestion. Choosing a
  Tailscale certificate needs three facts — the MagicDNS name (which is on the certificate), the
  tailnet IP (which is what the proxy binds, since it resolves names through Docker's resolver) and
  the tailnet range in the allowlist — and all three are one `tailscale status --json` away. The
  report carries them as `Identity`, cached for 30 s, and the form fills them in when the mode is
  picked. `CertDomains` is Tailscale's own answer to "may this node ask for a certificate", so a
  tailnet with HTTPS switched off is said *before* an apply runs into it. It is a caveat and not a
  refusal, which is the point: **the useful half of the Tailscale mode is the address, not the
  issuer**. `Apply` tries to issue, and where it cannot it drops any certificate left from the
  previous address (`DropStaleCertificate` — a stale one would be served under the new name, which
  browsers reject harder than a self-signed one), records the reason on `Run.Note` and carries on.
  The proxy entrypoint already falls back to `tls internal` when there is no certificate to serve,
  so the dashboard answers at the MagicDNS name from every tailnet device with the warning it
  already had, and `CertKeeper` — retrying every 10 minutes rather than every 12 hours after a
  failure — swaps the real certificate in and restarts the proxy on its own once HTTPS is turned on.
  `Report.Certificate` is what is actually on disk for the configured address, so the settings page
  says "trusted" only when the padlock will agree. `Issue` also rewrites Tailscale's own "your
  account does not support getting TLS certs" into the admin-console sentence, since the refusal is
  a switch rather than anything about the account. `parseTailscaleStatus` is split out from the
  subprocess so the part that reads somebody else's JSON is tested against a fixture.
- **`CertKeeper`** is why a Tailscale install shows an ordinary padlock rather than a warning:
  `tailscale cert` runs on the host through `hostexec` (tailscaled's socket is the host's, and this image
  deliberately carries no Tailscale client) and writes into `JD_DATA_DIR/certs`, which the proxy mounts
  read-only. Checked at boot and every 12 h, renewed with 30 days to spare, and the proxy is restarted
  afterwards because Caddy reads a file-based certificate once. `deploy/proxy-entrypoint.sh` turns
  `JD_TLS` into the scheme and the `tls` directive: a Caddyfile cannot branch, and an installer that
  edited a tracked one would make every later `git pull` a merge conflict.
- Routes: `GET/PUT /api/v1/dashboard/config`, `POST /api/v1/dashboard/restart`,
  `DELETE /api/v1/dashboard/config/run` and `GET /api/v1/dashboard/config/log` (the whole restart
  transcript as `text/plain`, the same two reads the update transcript has), all `system.admin`, the two
  mutations inside `s.destructive`. Applying settings also takes a session, and not one a Just Hub sign-in
  ticket opened (`RequireSession`, `RefuseHubSession`): the allowlist, the address, `JD_REQUIRE_2FA`, the
  session lifetimes and the terminal outlive whoever changed them (hub-link-protocol.md section 9.9).
  Restarting is left to the role: it comes back on the configuration already on disk.
  `POST /api/v1/dashboard/config/certificate` (`system.admin`, outside the destructive budget, audited
  as `dashboard.config.certificate`) runs the keeper's `Ensure` now rather than at its next check, and
  is refused with a 400 unless `JD_TLS` is `tailscale`.

Database provisioning uses the shared `internal/portalloc` range selection instead of a 64-port window.
It returns and audits Docker's actual host binding if a competing process claims the initial choice.

`install.sh` sources `scripts/install-dependencies.sh` before any operation requiring curl or OpenSSL.
It groups setup into four labeled stages and prints account-recovery and stack-command examples after
successful health verification. The terminal scripts use the built backend's isolated `--admin` mode
for account operations and Compose for status/logs/recreation; see
[terminal tools](../operations/terminal-tools.md) for behavior, privilege boundaries and verification.
It installs only missing curl/OpenSSL/Certbot packages using apt, dnf, yum, apk, zypper or pacman, validates
Certbot's HTTP authenticators and enables an existing packaged renewal timer. Package failures stop setup.
The same `jd_pkg_install` dispatch (one `apt-get update` per run) backs `jd_install_terminal_extras`,
which `install.sh` runs when the terminal is enabled: it installs `zsh`, `zsh-autosuggestions` and
`zsh-syntax-highlighting` where missing and, on success, appends `JD_TERMINAL_SHELL=<zsh path>` to a fresh
`.env`. A re-run that kept its `.env` asks first, and never touches a file that already names a shell.
That install is best effort: a failure is a warning, and the terminal opens the account's own shell.
`jd_install_host_tools` runs on every install and re-run, before any question: the web terminal is a host
shell, so its git and the Git page's GitHub sign-in need host packages, and Network → Tools runs `whois`
and `traceroute` on the host. It installs whichever of `git`, `git-lfs`, `whois` and `traceroute` are
missing one package at a time, so an unavailable one costs only itself, and `gh` through `jd_install_gh`:
GitHub's signed apt repository on Debian and Ubuntu (their packaged gh is years behind on an LTS release;
the image uses the same repository), the distribution's package on dnf, yum and zypper with GitHub's RPM
repository as the fallback, and `github-cli` on apk and pacman. Failures are named and warned about, never
fatal. Firewalls, fail2ban and cron are deliberately absent: the dashboard manages them when present, and
installing one changes the host's security or scheduling rather than supplying a tool.
The backend image still includes Certbot for container execution; host installation supplies host tooling
and the distribution renewal schedule. `python3 scripts/test_install_dependencies.py` verifies the
installer with fake package commands, without changing host packages. Hostname readiness returns
`certificateIssue` alongside `certificateMethod`, preserving listener/plugin errors for Quick Deploy.
Quick Deploy keeps HTTPS selected when readiness fails, presenting the actual blocker instead of
silently switching the initial configuration to plain HTTP.

### Uninstalling

**`internal/uninstall`** removes the dashboard from the server, from the Danger zone at the end of
`/dashboard/configuration`. It is the third lifecycle operation and the only one nothing comes back from,
so it is built around the moment the page watching it loses its API.

- **One question, two modes.** `keep` removes the stack — its three containers, the two images it builds
  with the `:previous` tags an update keeps, the build cache unused for a week (`PruneBuildCache`, as an
  update does),
  the lifecycle siblings, `/tmp/just-dashboard` and the held terminal sessions (`systemctl stop
  jd-terminal-*` on the host) — and leaves everything the dashboard made: deployments keep running, and
  `JD_DATA_DIR`, the backups and the checkout's `.env` stay, with a copy of the `.env` written to
  `JD_DATA_DIR/install.env`. `wipe` also removes the deployments and deletes `JD_DATA_DIR`, the proxy's
  two Compose volumes (its CA) and every copy of the `.env` in the checkout (`.env`, `.env.jd-previous`,
  `.env.jd-port-previous-*`, `.env.jd-tmp`, `.env.backup.*`). `deleteBackups` deletes `JD_BACKUP_DIR` too
  and is honoured only with a wipe. Neither mode touches databases the dashboard provisioned, proxy sites and their `jd-*` files,
  certificates, the network module's `/etc/just-dashboard/network` and its boot units (the docs promise a
  reinstall finds them), SSH and fail2ban drop-ins, Linux accounts, or the checkout itself.
- **The work is split across two processes, in the order the stack can lose them.** `Service.Begin`
  admits under the lifecycle lock, records the run (`self-uninstall.json`, transcript `self-uninstall.log`)
  and answers 202; the backend's own half then runs in a goroutine while it still serves the page.
  `Server.pauseForUninstall` stops what would put a deployment back — the Git watcher, the schedules, the
  preview reconciler and quarantine, the database-network keeper, the backup scheduler and the ingress
  keeper — and `deploy.Engine.Shutdown` waits up to 15 minutes for running deployments to reach a point
  they can resume from. For a wipe, `Teardown` removes the deployments (below). Then it starts the sibling
  `just-dashboard-uninstall` from its own image (`-self-uninstall -state-dir`), which takes the stack down
  and removes the rest. The paused modules cannot be restarted in place, so a run that fails in the
  backend's half records the failure and recreates the stack through `selfcfg.Restart` (`Options.Resume`),
  which is what brings them back.
- **What the deployments own is read two ways** (`deployments.go`). Docker marks containers and networks
  — `io.just-dashboard.managed=true`, the `com.docker.compose.project` of a managed container, the
  `com.just-dashboard.ingress=true` ingress — and the database knows the rest: every project's
  `RemovalPlan` (archived or not) supplies managed volumes, bind paths, routes, database networks and image
  digests. The dashboard's own compose project is excluded by name. `Remove` goes in dependency order:
  preview tailnet addresses (`NormalizedStepExecutor.WithdrawPreviewAddresses`), containers, routes
  through `proxysvc.RemoveDeploymentRoute`, the ingress container and `proxysvc.IngressDir`, networks
  (disconnecting any other container still attached — a database a deployment was linked to — first),
  volumes, bind paths through the deployment roots' `files.Delete`, then images, never forced. A
  `just-dashboard/deployment-*` image is removed by tag, so an image the dashboard only retagged keeps its
  other names. Anything that will not go is a warning on the run, not a failure: an uninstall that stopped
  at an in-use volume would leave the dashboard installed.
- **The sibling** (`runner.go`) runs `--privileged --pid host --rm` beside the checkout and data mounts:
  host commands go through `hostexec` and host paths through PID 1's root. Only `docker compose down`
  can fail the run — the stack is then started again so the operator is not left with half of one —
  and everything after it is best effort. Deletions are done in Go and stay on the filesystem they start
  on (`removeContained`), since BusyBox `rm` has no `--one-file-system`; `deletable` refuses the system
  directories whatever the record says. A wipe leaves `JD_DATA_DIR/nginx-cache` when a file under
  `JD_NGINX_DIR` names it, because nginx creates a cache's last directory but not its parents. The image
  the sibling runs from cannot be removed while it runs, so it asks the host to: a `systemd-run` timer 30
  seconds later runs `docker image rm`; without systemd the transcript says to do it by hand.
- **The transcript outlives the stack.** Both halves also write `/var/log/just-dashboard-uninstall.log`,
  outside the data directory a wipe deletes; it is the only record of the sibling's half. The page reads
  a failed poll during a live run as the stack having gone (`frontend/src/lib/uninstall.ts`), whatever
  phase it last saw, because a keep finishes between two polls; a backend that merely crashed restarts
  and answers with the run closed as failed.
- **Reinstalling.** At boot `Service.Start` closes a live run whose sibling is gone as failed, and clears
  a successful one — this process running after a kept uninstall is the reinstall — removing
  `install.env` once the key in it is the key the process runs with. `install.sh` on a fresh clone with
  no `.env` offers to restore `install.env` and continues as a re-run that keeps its settings. The
  suggested self-backup job excludes `install.env`.
- Routes: `GET /api/v1/dashboard/uninstall` (the run and its transcript's tail, polled),
  `GET /api/v1/dashboard/uninstall/plan` (what each mode removes and leaves, read once by the dialog),
  `GET /api/v1/dashboard/uninstall/log`, `DELETE /api/v1/dashboard/uninstall/run` and
  `POST /api/v1/dashboard/uninstall` (`{mode, deleteBackups}`, inside `s.destructive`), all
  `system.admin`. A wipe requires the host name as the typed phrase (invariant 3); a keep does not.
