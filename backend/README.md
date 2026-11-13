# backend

The Go half of Just Dashboard: one module (`go.mod`, Go 1.26.11) that builds the API server and four
small programs beside it. In an install it is the `backend` service of the Compose stack, bound to
loopback behind Caddy.

## Layout

| Path | What it is |
| --- | --- |
| `cmd/server/` | The dashboard's process: configuration, store, API and background services. Also the `--admin` mode `scripts/manage.sh` runs and the `-self-update`, `-self-restart` and `-self-uninstall` siblings |
| `cmd/jd/` | The `jd` command line, which drives deployments with an API key |
| `cmd/terminal-holder/` | Keeps one terminal session's PTY open across dashboard restarts |
| `cmd/healthcheck/` | The container's health check |
| `cmd/vantage/` | The optional external-check agent, built and run on another machine |
| `internal/api/` | The one router (`routes.go`) and every HTTP handler (`handlers_<area>*.go`) |
| `internal/store/` | The SQLite schema and the columns added since a table shipped |
| `internal/<feature>/` | One package per feature. Each opens with a package comment saying what it owns and which document describes it: `go doc ./internal/<feature>` |
| `scripts/` | `gen-changelog.go`, which writes `CHANGELOG.md` from `internal/selfupdate/changelog.json`; the root's `scripts/release.sh` runs it |
| `Dockerfile` | The backend image: the four shipped binaries (`cmd/vantage` is not one of them) |

## Commands

Run from this directory. The checks a change needs are picked by `scripts/test-changed.sh` at the
repository root; these are what it runs, for use while iterating.

```bash
go build ./...
go vet ./internal/gitx
go test ./internal/gitx -run TestBranchesReportMergedGoneAndRemoteHalves -v
go run ./cmd/server                  # needs JD_MASTER_KEY and a writable JD_DATA_DIR
```

Name the package and the test. `go test ./...` is never run here: it takes minutes to report on
packages the change did not touch.

## Where the detail is

- [`../AGENTS.md`](../AGENTS.md): the workflow, the required checks and the security requirements.
  Read it before changing anything near authentication, the allowlist, capabilities, paths, host
  commands or the store.
- [`../docs/internal/reference/finding-things.md`](../docs/internal/reference/finding-things.md):
  which package, handler file and document belong to a given page.
- [`../docs/internal/reference/repository-map.md`](../docs/internal/reference/repository-map.md):
  one row per package.
- [`request-lifecycle.md`](../docs/internal/architecture/request-lifecycle.md) and
  [`runtime-boundaries.md`](../docs/internal/architecture/runtime-boundaries.md): how a request is
  handled and how the process is put together.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md): the live and race test lanes, and the licence rules
  for dependencies.
