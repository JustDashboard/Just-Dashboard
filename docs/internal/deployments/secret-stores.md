# Secret stores

A deployment variable can read its value from a secret manager the operator already runs instead of
holding it: HashiCorp Vault or OpenBao (KV version 2), Infisical (cloud or self-hosted) or Doppler. The
connection is saved once under **Deployments → Secret stores** (`/deploy/secret-stores`), and a variable
references a key in it as

```
${{store.NAME/path#KEY}}        the newest version, read at every run
${{store.NAME/path#KEY@4}}      version 4, exactly
${{store.NAME#KEY}}             Infisical's top-level folder, or Doppler
```

The code is `internal/secretstore` (the connections, the three clients, resolution and Test) and
`deploy/secret_store_references.go` (the reference kind, the run's read, the per-run memory and the
save-time checks). The table is `deploy_secret_stores` (`store/schema_secret_stores.go`, additive).

## Connections

| Kind | Address | Signs in with | Settings |
| --- | --- | --- | --- |
| `vault`, `openbao` | required, the API's base (`https://vault:8200`) | a token, or AppRole (role ID and secret ID; `authMount`, default `approle`) | `mount` (the KV v2 engine, default `secret`), `namespace` (sent as `X-Vault-Namespace`) |
| `infisical` | empty for `https://app.infisical.com`, or the instance's (EU cloud, self-hosted) | a Universal Auth machine identity's client ID and client secret | `project` (the project ID) and `environment` (its slug) |
| `doppler` | empty for `https://api.doppler.com` | a service token, which is scoped to one project's config | none |

Vault and OpenBao are the same client: they speak the same API and are told apart only so the page names
the product. A self-hosted store's TLS certificate may be checked against a certificate authority given in
PEM (`caCert`), trusted beside the system's own; there is no option to skip verification.

- **Credentials** are sealed in `credentials_enc` with the dashboard's master key, as a notification
  channel's and a log drain's headers are. Only their names come back (`credentialNames`), and an edit
  that leaves them out keeps them; changing how a Vault store signs in needs that way's credentials.
- **Plain http**: a store is reached over https, or over plain http only on loopback (`localhost`,
  `127.0.0.0/8`, `::1`) — the notification channels' rule. Every connection carries a credential, so
  there is no local-network exception like a header-less log drain's.
- **Name and kind are fixed** once saved: the name is what references say, and a token of one kind means
  nothing to another. A name is lowercase letters, digits and dashes, up to 40.
- **Redirects are never followed**: one would carry the token to an address nobody typed (a Vault
  standby's redirect is answered with "set the address to where it answers itself").
- **Previews**: `allowPreviews` (off by default) lets a pull request preview's variables read the store.

## The reference

`secretstore.ParseReference` reads the target, `NAME[/PATH]#KEY[@VERSION]`: a path is up to 16 segments
of letters, digits, dots, dashes and underscores, never `.`, `..` or empty; a key is the same characters;
a version is a whole number from 1. Its slashes, `#` and `@` are admitted for the `store` kind alone
(`storeReferenceRE`), so no older kind's grammar widens. What a path means depends on the store: under
the KV mount for Vault and OpenBao (required), the folder for Infisical (empty is `/`), and nothing for
Doppler, which refuses a path and a pinned version since a service token reads one config and Doppler
serves no earlier version of a secret to a reader.

A store reference is read like every other reference kind, at the variable boundary a run opens
(`OpenRunScopedVariables`), through the variable graph: a variable alias to a store reference resolves
to the store's value and is a secret leaf, and a value read from a store is a literal — reference-shaped
text in one is never followed, so a store cannot form a chain or a cycle. Only values saved in
`reference` mode are store references; a legacy value of that shape stays the literal text it always was
(`parseStoredReference`). A dotenv import writes literals, and copying production's variables into a
preview skips every reference, store references included.

Saving a variable that references a store (`PutVariable`, the new-project wizard's commit) checks that
the variable is secret, the store exists and can answer the reference's shape, and, in a preview
environment, that the store allows previews (`storeReferenceProblem`). The run checks the same again.

## When it is read, and what a rollback reads

A run reads every store its variable snapshot references **at its first step**, before anything is
acquired or started: `resolve_source` for a deploy, `render_runtime` for a rollback or redeploy,
`start_candidate` for a restart or start (`readsSecretStoresAt`). A stop, a preview's removal and a managed
removal read nothing, so a store that is down never keeps anybody from stopping a deployment.

- **Fresh for each run.** Each store is signed in to once per run (an AppRole token or an Infisical access
  token minted for it is revoked afterwards), each path is read once however many of its keys are
  referenced, and a pinned version is read on its own. Nothing is cached across runs.
- **One consistent set per run.** What the run read is kept in memory (`storeReadCache`) for that run's
  later steps — the build's arguments, release tasks and the container read the same values — and dropped
  by an engine observer when the run finishes (or after six hours if that is never observed). A dashboard
  restarted mid-run reads the stores again at the next step that opens variables.
- **A failure fails the run with nothing touched**, as `secret_store_unreadable` with the store's own
  answer: `secret store bao, apps/web#DATABASE_URL: permission denied (403)`, `version 3 of secret/apps/web
  is deleted`, `Universal Auth sign-in refused: Invalid credentials (401)`, `could not reach
  http://127.0.0.1:8200: … connection refused`. A store's error answers carry no value, and any credential
  of the connection that turns up in one is masked.
- **What is recorded** is the version, never the value: the step's transcript says `Read DATABASE_URL from
  secret store bao: apps/web#DATABASE_URL, version 2`, and the run's metadata keeps the same as
  `secretStoreReads` (`reference`, `store`, `location`, `version`, `pinned`, `variables`). Doppler reports
  no version.
- **Rollback** re-reads the store as it is at that moment, the rule shared variables and credentials
  already follow: a release freezes the reference, not the value. That is what a rotated credential needs —
  the old one has usually been revoked by the time anybody rolls back. A rollback's transcript says when the
  store now holds another version than the release it restores read (`version 5 (release #41 read version
  3; pin a version in the reference to keep one)`). A reference that pins a version is frozen in the
  release with its version, so rolling back to that release reads that version exactly.

Every run that read a store records the outcome on the connection (`read_*`), shown on the page as the
store's last read, in amber with the store's words when it failed.

## Routes

| Route | Who | Notes |
| --- | --- | --- |
| `GET /deploy/secret-stores` | any signed-in role | No credential, no value; `usedBy` names each active variable that reads the store |
| `POST /deploy/secret-stores`, `PUT …/{id}` | `system.admin`, session | `400` for an invalid connection, `409 name_taken` |
| `POST …/{id}/test` | `system.admin`, session | Signs in, says who the store took the connection to be, and with `{path, key}` reads that key and reports its version; a refusal is `200` with `ok: false` and the store's words, recorded on the connection |
| `DELETE …/{id}` | `s.destructive`, session | `409 secret_store_in_use` naming every variable that still reads it |

Audit: `deploy.secret_store.{create,update,test,delete}` with the kind, address, sign-in method,
`allowPreviews`, the names of the sign-in fields set (`signInFields`) and on update whether they were
replaced (`signInReplaced`); a test carries `ok`, the store's message and the probe's path and key; a
failed test and a refused removal are failures. No credential and no value is ever written there; the
audit log's own redaction is the second line.

## Limits

- Requests are bounded at 15 s each and a run's whole read at 60 s; an answer over 1 MiB is refused.
  There is no retry: a refused or rate-limited read fails the run with the store's answer, and the run can
  be retried. Infisical's and Doppler's rate limits are met by reading a path once per run.
- Vault KV version 1 is not read. An Infisical secret imported into a folder is read from the folder that
  defines it; the folder's own secrets are what a reference reaches.
- The variables page's reveal shows the reference, never the store's value; the dashboard holds a value
  only in memory, for the run that read it.

## Tests

- `secretstore/secretstore_test.go`: the reference grammar, sealing and names-only reads, the plain-http
  rule and other refusals, Vault through AppRole with a namespace (one read per path, the minted token
  revoked, values that are numbers or objects), the stores' own words for refusals with no value or
  credential in them, a redirect never followed, Infisical folders and pinned versions, a Doppler config
  read through an httptest server that follows Doppler's documented download API, Test with a probe, a
  custom CA trusted only where given, and a store that never answers.
- `deploy/secret_store_references_test.go`: parsing in the store kind alone, the variable graph (aliases,
  store values as literals, cycles beside store references), save-time checks including the preview rule
  and removal refused while read, one read per run with versions in the transcript and metadata and none
  of the values, a down store failing the first step and never a stop, and a rollback's version note.
- `api/handlers_deploy_secret_stores_test.go`: roles, session-only writes, Test, a variable reading a
  store, removal refused while read, and the audit trail without a credential or value.
- Live, `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveSecretStoreReferencesReachTheContainer`:
  a real OpenBao dev server (AppRole) and a real self-hosted Infisical (Universal Auth) in containers on
  loopback, each read into a real container's environment through the run's first step and the runtime
  variables activation opens, a pinned version included, then OpenBao stopped and the next run's first
  step failing in its words. It needs `openbao/openbao:2.7.2`, `infisical/infisical:v0.167.4`,
  `postgres:17-alpine` and `redis:7-alpine` and removes every container, network and image it made.
- Browser: `deploy-secret-stores.spec.ts` and `secret-stores.test.js`.
