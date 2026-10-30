# The jd command line

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

Drive deployments from a terminal, a CI job or an AI client, over the dashboard's own API.

`jd` drives your deployments from a terminal or a CI job, over the dashboard's own API. The installer
puts it at `/usr/local/bin/jd` on the server, and the dashboard keeps that copy current after every
update (`sudo ./scripts/manage.sh install-cli` installs it again by hand). For another Linux machine
of the same architecture, **Account → API keys** links the same file (`GET /api/v1/cli/jd`, for any
signed-in account); anywhere else, build it from a checkout with Go 1.26: `cd backend && go build ./cmd/jd`
— it uses only Go's standard library.

It signs in with a **deploy key**: under **Account → API keys → New key**, an administrator chooses
*Deploy key*, a name, an expiry and the deployments it reaches (every one, or only those you pick).
The key is shown once.

```bash
jd login https://dash.example.com --key - < key   # the key read from stdin; stored 0600 in ~/.config/jd
jd deployments                                    # every deployment with its health and last run
jd deploy shop --wait                             # exit status is the run's result
jd logs shop --follow                             # or --run 12 for an earlier run
jd runs shop / jd releases shop
jd rollback shop                                  # to the release before the live one, or --to <id>
jd rollback shop --instant                        # straight back to the release kept warm after the last deploy
jd keep shop                                      # keep the new release now; the warm one is retired
jd canary status shop                             # a canary's window and both releases' requests, 5xx and p95
jd canary promote shop / jd canary abort shop     # decide a canary now; abort leaves the live release untouched
jd promote shop --wait                            # staging's live release into production, as the same image
jd promote shop --from-preview 42 --wait          # PR #42's approved preview into staging, as the same image
jd maintenance shop on --bypass 10.0.0.0/8        # the switch acts at once; the bypass list with the next deploy
jd status shop                                    # environments, live release, rollback watch, maintenance, rehearsal
jd vars ls shop                                   # names, scopes and digests: never values
jd vars set shop STRIPE_KEY=- < stripe.key        # KEY=- reads the value from stdin, out of shell history
jd vars import shop .env --dry-run                # then without --dry-run
jd vars rm shop OLD_FLAG
jd config get shop --json > shop.json             # edit, then: jd config set shop shop.json
jd create --name blog --image nginx:1.27-alpine --port 80   # a new deployment, through a draft
```

Every read takes `--json`. In CI, set `JD_URL` and `JD_KEY` instead of logging in; nothing is written.
For a client in another language, the dashboard describes the whole deployment API as OpenAPI 3.1 at
`GET /api/v1/deploy/openapi.json` (or `.yaml`; `jd openapi` prints it), generated from its own router
and types; **Account → API keys** links it beside the `jd` download when a deploy key is made, and the
repository carries a copy at `docs/api/deploy-openapi.json`. Typed clients generated from it are in
the repository too: a Go package with no dependencies (`sdk/go`, module
`github.com/Wayy01/Just-Dashboard/sdk/go`) and a TypeScript module over `fetch` (`sdk/typescript`),
a call per route and a type per schema, regenerated with the description (see [`sdk/README.md`](../../sdk/README.md)).
`--env` picks an environment other than production, `--ca-file` trusts the internal Caddy certificate
authority, and plain `http://` is refused except to loopback (an SSH tunnel). Exit codes: 0 done (the
run succeeded), 1 refused or the run failed, 2 usage, 3 not logged in or the key is no longer valid.

**What a deploy key can do.** Everything an ordinary key could on the deployments — list, read, deploy,
cancel, retry, roll back, promote staging (and a pull request's approved preview into staging), switch
back to the warm release or keep the new one, promote or abort a canary, switch maintenance, runs and logs — plus what used to need
a browser session: create deployments from drafts
and duplicates (a key's drafts are its own, and a key narrowed to some deployments reaches the
deployments it commits), change configuration, source, Git policy and triggers, and set, import,
generate, rotate and delete variables and shared variables. Variables are write-only to it: it sees
names and digests, never a value. **What it cannot do, whatever it reaches:** reveal a value; list,
add, change or test saved registry and Git credentials; see or change secret-store connections; set the
rollback-watch or rehearsal terms or edit a route's maintenance and error pages; ask for, read or remove
a copy of a release's data; merge or comment on
pull requests as the dashboard's GitHub account; approve previews; archive or delete deployments; or
reach anything outside Deployments. It also cannot give a deployment access to the server itself — a
privileged container, the host's network, its devices or GPUs, a host folder, a release task in the
host's shell, a Compose file — nor point a saved credential somewhere new; a deployment that already has such settings, or
reads a saved credential through a variable, can be deployed and its variables set, but its
configuration and source are changed in the dashboard. Every
write is in the audit log under the key's name. See [the security notes](../internal/security/invariants.md)
for the full boundary; a key that can deploy code can still read that deployment's own variables from
inside it, as in any CI system.

**AI clients.** An MCP key given the deployments (**Databases → MCP server**, every deployment or the
ones you pick) reaches the same API as tools: six written by hand — list deployments, runs and
releases, read a run's log, deploy, roll back — and, when *Offer every deployment route as a tool* is
on for the key (it is for a new key, and off for every key made before it until you turn it on), 100
tools generated from the deployment API, one per route a deploy key may call, named for its operation
(`deployment_configuration_save`, `deployment_variable_put`, …): 113 tools in all with the seven
database tools. *Read* is offered the reads; *Deploy* also the changes, held to your role and to everything above that a deploy key cannot
do; a destructive route also needs a role that may take destructive actions. Each call goes through
the dashboard's own routes as the key and is in the audit log twice: as the MCP call and as the
route's own entry.
