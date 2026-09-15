# Contributing

Thanks for looking. Issues and pull requests are welcome.

## Licence of the project

This project is licensed under the **GNU Affero General Public License v3.0**
(see [`LICENSE`](LICENSE)).

The Affero clause is deliberate. This dashboard is root-equivalent software, and
the licence means anyone who runs a modified version as a network service has to
publish their modifications. You are free to run it, change it and distribute
it; you are not free to take it closed.

## Licence of your contribution

By opening a pull request you agree to the following. There is nothing to sign —
submitting the PR is the agreement.

1. **You wrote it, or you have the right to submit it.** Your contribution is
   your own work, or you have permission from whoever owns it. If your employer
   has rights to work you do, you have their clearance to contribute it.

2. **Your contribution is licensed under AGPL-3.0**, the same terms as the rest
   of the project.

3. **You also grant the project owner (Wayy01) a separate, additional licence**
   to your contribution: perpetual, worldwide, non-exclusive, royalty-free and
   irrevocable, to use, reproduce, modify, distribute and sublicense it under
   *any* terms, including proprietary ones.

You keep the copyright to your work. Point 3 is an additional grant, not a
transfer — you can still do anything you like with your own code.

### Why point 3 exists

It is the only way the project can offer a commercial licence later without
having to track down every past contributor for permission. Practically, it
means paid multi-server features can fund the maintenance of the open source
part, rather than the project stalling the way most single-maintainer
infrastructure tools eventually do.

If that trade is not one you want to make, please open an issue describing the
change instead of a pull request — a good bug report is worth as much, and it
carries no licensing question at all.

## Before you open a pull request

- Run the checks: `cd backend && go build ./... && go vet ./... && go test ./...`, then
  `cd ../frontend && bun run lint && bun run build && bun run test:browser`. Install the required
  Chromium build once with `bun run test:browser:install`.
- Changes to deployment builders or artifact handling also run the opt-in Docker boundary on a release
  host: `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC4ArtifactAdapters -count=1 -v`.
- Changes to runtime activation, checks, graceful shutdown or Compose release ownership also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC5ActivationAdapters -count=1 -v` on a Docker
  and Buildx release host.
- Changes to deployment variables, feature links, backup gates or managed-resource lifecycle also run
  `go test -race ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store -count=1`;
  the browser gate covers the project overview, build transcript and focused settings, including
  variables, domains, storage, dependencies, automation and lifecycle.
- Changes to database provisioning or deployment connection URLs also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
  on a Docker host. It exercises all five quick-setup engines from separate application containers
  and cleans up its own containers and volumes.
- Keep the security posture intact. The network allowlist runs before
  authentication, enrolled accounts always require their second factor, every destructive route sits behind
  the destructive capability with an audit entry, and the rare irreversible ones
  require a typed confirmation phrase enforced server-side. A change that
  weakens any of those needs to say so explicitly in the PR description.
- Before putting a typed confirmation on a new route, read invariant 3 in
  `docs/internal/security/invariants.md`. The test is frequency, not severity: everything behind
  `s.destructive` is dangerous, and adding a phrase to something done several
  times a sitting is what teaches operators to type phrases without reading
  them.
- Never edit `CHANGELOG.md` by hand — it is generated from
  `backend/internal/selfupdate/changelog.json`, which is also the file every
  install in the world reads to find out whether it is behind. If your change
  is worth a release, add the entry there and run `scripts/release.sh <version>`;
  it bumps the version everywhere it appears and regenerates the markdown. The
  Go test run fails if the two ever disagree, in either direction.
- Match the surrounding code. Comments explain *why*, not *what*.

## Security issues

Please do not open a public issue for a vulnerability. Report it privately
through GitHub's **Security → Report a vulnerability** on this repository.

## Running the database tests against real engines

`go test ./...` passes with nothing installed: the database integration tests
skip when they cannot reach a server, because a suite that fails for want of a
daemon is a suite people learn to ignore.

They are worth running for real before touching `internal/dbx`, though. The
unit tests prove the generated SQL is the SQL intended; only a live server
proves it is SQL that server accepts, and the catalogue queries are exactly
where that gap bites — every engine spells its metadata differently.

Each engine reads a DSN from an environment variable, defaulting to a local
instance on the standard port:

| Variable | Default |
| --- | --- |
| `JD_TEST_POSTGRES_DSN` | `postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable` |
| `JD_TEST_MYSQL_DSN` | `jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest` |
| `JD_TEST_MSSQL_DSN` | `sqlserver://sa:…@127.0.0.1:1433?database=master` |
| `JD_TEST_CLICKHOUSE_DSN` | `clickhouse://default@127.0.0.1:9000/default` |
| `JD_TEST_MONGO_DSN` | `mongodb://127.0.0.1:27017/jdtest` |
| `JD_TEST_REDIS_DSN` | `redis://127.0.0.1:6379/0` |

The quickest way to get all of them is containers:

```bash
docker run -d -p 5432:5432 -e POSTGRES_USER=jdtest -e POSTGRES_PASSWORD=jdtest -e POSTGRES_DB=jdtest postgres:16
docker run -d -p 3306:3306 -e MARIADB_ROOT_PASSWORD=jdtest -e MARIADB_DATABASE=jdtest -e MARIADB_USER=jdtest -e MARIADB_PASSWORD=jdtest mariadb:11
docker run -d -p 1433:1433 -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD='JdTest#2024pw' mcr.microsoft.com/mssql/server:2022-latest
docker run -d -p 9000:9000 clickhouse/clickhouse-server:latest
docker run -d -p 27017:27017 mongo:8
docker run -d -p 6379:6379 redis:7
```

Then `go test ./internal/dbx/ ./internal/api/ -run Live -v` and watch which
engines report rather than skip. SQLite needs nothing — it is embedded.

Oracle has a dialect but no test coverage. Its server is a 1.4 GB download
behind a click-through licence whose installer prompts interactively for a
password and cannot be driven headlessly from a script, so there is nothing a
CI job can point at. Treat changes to `dialect_oracle.go` as unverified and say
so in the pull request; if you have an instance, set `JD_TEST_ORACLE_DSN` and
add a fixture alongside the others.
