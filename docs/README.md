# docs

Everything written about Just Dashboard that is not code. Two audiences share the directory: the
people who run it (`features/`, `guides/`) and the people who change it (`internal/`). The public
entry points are [`../README.md`](../README.md) and [`../FEATURES.md`](../FEATURES.md) at the
repository root; contributors start from [`../AGENTS.md`](../AGENTS.md).

## Layout

| Path | What it is | Written for |
| --- | --- | --- |
| [`features/`](features/) | One page per area of the product: what it does, in the operator's words | Operators |
| [`guides/`](guides/) | Installing, configuring, administering, the `jd` command line, linking | Operators |
| [`readme/`](readme/) | The screenshots `README.md` and `FEATURES.md` show | Readers of the repository |
| [`internal/`](internal/README.md) | The engineering reference: architecture, security invariants, one guide per backend feature, the deployment subsystem, the frontend and its design system | Contributors |
| [`api/`](api/) | `deploy-openapi.json`, the deployment API's description. Generated from the router and held to it by a test; never edited by hand | Users of the API and the SDKs |
| [`audits/`](audits/) | Dated records of audits and redesigns. History: a record describes the code as it was on its date | Contributors |
| [`screenshots/`](screenshots/) | Pictures older pull requests left. New binary evidence goes on a `pr-assets/*` branch instead | Nobody new |
| `*-report.html` | Three standalone reports of earlier passes, kept as they were written | Contributors |

## Where to start in `internal/`

- [`internal/README.md`](internal/README.md) is the index of every internal document.
- [`internal/reference/finding-things.md`](internal/reference/finding-things.md) answers "where is
  X": the route, components, handler, package, spec and document behind each page.
- [`internal/reference/repository-map.md`](internal/reference/repository-map.md) has one row per
  source area.
- [`internal/overview.md`](internal/overview.md) has the commands and the test strategy.
- [`internal/security/invariants.md`](internal/security/invariants.md) is read before a change
  near authentication, the allowlist, capabilities, paths, host commands or the store.

## Commands

Nothing here is built. Three things are checked or regenerated:

```bash
scripts/test-changed.sh              # for a document: the Go packages whose tests read it, if any
cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent
cd backend && go test ./internal/version -count=1    # holds README.md and docs/api to the version
```

The second line rewrites `api/deploy-openapi.json` after a change to a deployment route.

## Rules that apply here

They are stated once, in [`../AGENTS.md`](../AGENTS.md): documentation is reviewed against the
complete diff before every push, and `CHANGELOG.md` is generated, never edited.
