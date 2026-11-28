# scripts

The repository's shell and Python tools: what an operator runs on the server, what `install.sh`
sources, what cuts a release, what CI runs, and the local check a contributor runs before a pull
request. The frontend's build helpers are in `frontend/scripts/`, and the changelog generator is
`backend/scripts/gen-changelog.go`.

## Every script

| Script | What it is for | Who runs it |
| --- | --- | --- |
| `manage.sh` | Dashboard accounts, linking to a site account or a Just Hub, stack status, logs and restart, `install-cli`, and `version`, `update` and `rollback` over SSH | Operator, with `sudo` |
| `create-user.sh`, `reset-password.sh` | Two account commands of `manage.sh` as scripts of their own; each hands straight over to it | Operator, with `sudo` |
| `preflight.sh` | Reads the machine and says whether it can build the stack; changes nothing | Installer (sourced) |
| `install-dependencies.sh` | Installs packages through whichever package manager the host has | Installer (sourced) |
| `dotenv.sh` | Writes a literal value into `.env` so Compose reads it unchanged | Installer (sourced) |
| `password.sh` | The backend's password rule in shell, so a weak password is refused while the question is on screen | Installer (sourced) |
| `build-stamp.sh` | The commit a build is stamped with when it is not the tagged release | Installer and `manage.sh` (sourced) |
| `release.sh` | Writes a version everywhere it is carried, generates `CHANGELOG.md` and regenerates the OpenAPI description; the release notes go into `changelog.json` first. Refuses while a version already on `main` has no tag on origin | Release |
| `release-tags.sh` | Which versions on origin's `main` have no `v<version>` tag there, and the list of those known to be left untagged, with why | `release.sh` (sourced) |
| `deps-check.sh` | Advisories that reach the code, and dependencies that are behind; needs the network | Release, and a contributor in between |
| `pin-images.py` | Moves the image digests the Dockerfiles and the compose file are held to; `--check` verifies them offline | Release |
| `test-changed.sh` | Runs the checks, Go tests and browser specs the diff can reach, and nothing else | Contributor |
| `e2e-deployments.py` | Deployment acceptance against a real backend and a real Docker daemon | Contributor, for deployment changes |
| `verify-distros/run.sh` | Installs the dashboard on a fresh machine of every release in `verify-distros/distros.txt`, uses it, restarts it and uses it again; `compare` lists what differs from the Ubuntu 24.04 baseline. The README's Supported distributions table is its result | Contributor and release, for installer changes |
| `ci-plan.py` | Decides which jobs one run of the Verify workflow needs, and deals the browser specs into them by the tests each runs | CI; `test-changed.sh` asks it which Go packages a change reaches |
| `go-test-race.sh` | Runs Go packages under the race detector, split across processes | CI |
| `check-live-test-results.py` | Fails a run whose required live fixture was skipped, absent or did not pass | CI |
| `test_*.py` | The tests of the scripts above, one file per script it names | `test-changed.sh`, and CI for `test_ci_plan.py` |

## Commands

```bash
scripts/test-changed.sh                      # from anywhere in the repository; or name a base
JD_TEST_CHANGED_PLAN=1 scripts/test-changed.sh   # print what it would run, and run nothing
python3 scripts/test_test_changed.py         # after changing the picker itself
sudo ./scripts/manage.sh status              # on a server; --help lists every command
```

`test-changed.sh` picks a script's tests by file name: a test must name the script it runs by its
last component (`manage.sh`, `docker-compose.yml`), or a change to that script runs nothing.

`ci-plan.py` weighs a browser spec by reading it, with no Bun and no Playwright: a test written
once in a loop counts for every item of the loop's list (a literal, a constant of the spec, or one
it imports from a file beside it), and a test that an unset environment variable switches off
counts for nothing. A spec says what the source does not in a comment on a line of its own:

```ts
// ci-plan: 190 tests                    the loop's list is computed, so the count is said
// ci-plan: each weighs 2.5              a test here costs two and a half ordinary ones
// ci-plan: 190 tests, each weighs 2.5
```

The specs are dealt heaviest first, each to the job that is lightest once its share of the spec's
own section (the first word of the file's name) is counted twice, so a slow section is spread. A
job is about a hundred tests in a pull request and two hundred in a merge's run, and a full run is
`MOST_SHARDS` jobs; the comments beside those numbers say what each was measured from.

## Where the detail is

- [`../AGENTS.md`](../AGENTS.md#required-checks): the required checks, and the release rules
  (`CHANGELOG.md` is generated; tags and releases are never made by hand).
- [`terminal-tools.md`](../docs/internal/operations/terminal-tools.md): `manage.sh` and the
  installer's helpers.
- [`platform.md`](../docs/internal/backend/platform.md): cutting a
  release, the dependency check and the image pins.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md): what a pull request runs, the race lane and the live
  test lanes.
- [`administration.md`](../docs/guides/administration.md): the account scripts as an operator
  reads about them.
