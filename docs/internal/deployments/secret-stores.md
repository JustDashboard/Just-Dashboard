# Secret stores

A deployment variable can read its value from a secret manager the operator already runs instead of
holding it: HashiCorp Vault or OpenBao (KV version 2), Infisical (cloud or self-hosted), Doppler, AWS
Secrets Manager or Azure Key Vault. The connection is saved once under **Deployments → Secret stores**
(`/deploy/secret-stores`), and a variable references a key in it as

```
${{store.NAME/path#KEY}}              the newest version, read at every run
${{store.NAME/path#KEY@4}}            version 4, exactly
${{store.NAME#KEY}}                   Infisical's top-level folder, or Doppler
${{store.NAME/secret-name}}           a whole Secrets Manager or Key Vault secret
${{store.NAME/secret-name#KEY}}       one field of a JSON secret there
${{store.NAME/secret-name@AWSPREVIOUS}}   a Secrets Manager staging label or version id
${{store.NAME/secret-name@0f2a…}}      a Key Vault version (32 hexadecimal characters)
```

The code is `internal/secretstore` (the connections, the five clients — `client.go` for Vault, Infisical
and Doppler, `aws.go` with the SigV4 signer in `sigv4.go`, `azure.go` with the token cache — resolution
and Test), `secretstore/secretstoretest` (httptest fakes of Secrets Manager and Key Vault, which have no
server to run in a container) and `deploy/secret_store_references.go` (the reference kind, the run's read,
the per-run memory and the save-time checks). The table is `deploy_secret_stores`
(`store/schema_secret_stores.go`, additive).

## Connections

| Kind | Address | Signs in with | Settings |
| --- | --- | --- | --- |
| `vault`, `openbao` | required, the API's base (`https://vault:8200`) | a token, or AppRole (role ID and secret ID; `authMount`, default `approle`) | `mount` (the KV v2 engine, default `secret`), `namespace` (sent as `X-Vault-Namespace`) |
| `infisical` | empty for `https://app.infisical.com`, or the instance's (EU cloud, self-hosted) | a Universal Auth machine identity's client ID and client secret | `project` (the project ID) and `environment` (its slug) |
| `doppler` | empty for `https://api.doppler.com` | a service token, which is scoped to one project's config | none |
| `aws` | empty for `https://secretsmanager.{region}.amazonaws.com` (`.com.cn` in `cn-` regions); set for a VPC endpoint or LocalStack | an IAM access key ID and secret access key, and for temporary credentials their session token; every request is signed with Signature Version 4 (`sigv4.go`, standard-library HMAC-SHA256, no SDK) | `region` (held to `^[a-z]{2}(-[a-z]{2,16}){1,2}-[0-9]{1,2}$` before it becomes a hostname) |
| `azure` | empty for `https://{vaultName}.vault.azure.net` (`.vault.usgovcloudapi.net`, `.vault.azure.cn` by cloud) | an Entra application's client ID and client secret, through the OAuth 2.0 client-credentials grant at `{authority}/{tenantId}/oauth2/v2.0/token` for the cloud's vault scope (`https://vault.azure.net/.default`); the token is sent as a bearer and cached in memory until a minute before it expires | `vaultName` (3–24 letters, digits and dashes, starting with a letter, no `--`, held to that before it becomes a hostname), `tenantId`, `cloud` (`public` default, `usgov`, `china`), `authority` (an override of the cloud's sign-in address, for a private cloud or a test; the same https-or-loopback rule as the address) |

Vault and OpenBao are the same client: they speak the same API and are told apart only so the page names
the product. A self-hosted store's TLS certificate may be checked against a certificate authority given in
PEM (`caCert`), trusted beside the system's own; there is no option to skip verification. AWS and Azure
have no address of their own to type: `address` is empty and the connection's `endpoint` (in the read
model) is derived from the region or the vault name, or is the address when one is given as an override.

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

`secretstore.ParseReference` reads the target, `NAME[/PATH][#KEY][@VERSION]`: a path is up to 16
segments of letters, digits, dots, dashes and underscores, never `.`, `..` or empty; a key is the same
characters; a version is up to 64 of them (the version follows the key, or the path when there is no
key); a target with neither a path nor a key names nothing. Its slashes, `#` and `@` are admitted for the
`store` kind alone (`storeReferenceRE`), so no older kind's grammar widens. What the parts mean, and which
are required, depends on the store (`checkFits`, checked when a variable is saved and again when a run
reads):

| Kind | Path | Key | Version |
| --- | --- | --- | --- |
| `vault`, `openbao` | required: the secret's path under the KV mount | required | a whole number from 1 |
| `infisical` | the folder; empty is `/` | required | a whole number from 1 |
| `doppler` | refused: a service token reads one config | required | refused: Doppler serves no earlier version to a reader |
| `aws` | required: the secret's name (`prod/db`) | optional: a field of a JSON object value; without it, the whole `SecretString` (a `SecretBinary` is its decoded bytes) | a version id (32 characters or more, as Secrets Manager's are) or a staging label (`AWSCURRENT`, `AWSPREVIOUS`, the operator's own); empty is `AWSCURRENT` |
| `azure` | required: the secret's name, one segment of letters, digits and dashes | optional: a field of a JSON object value; without it, the whole value | the version's 32 hexadecimal characters, as the vault lists it; empty is the current version |

Secrets Manager and Key Vault keep whole secrets rather than key-value pairs, which is why a key is
optional there: `${{store.aws/prod/db#DATABASE_URL}}` is one field of a JSON secret and
`${{store.kv/database-url}}` is a string secret. A key named on a value that is not a JSON object fails
the run (`prod/db has no field DATABASE_URL: its value is not a JSON object with that key`).

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
  referenced — one `GetSecretValue` per Secrets Manager secret, one `GET /secrets/{name}` per Key Vault
  secret, whichever fields of it are referenced — and a pinned version is read on its own. No value is
  cached across runs. The one thing that is: a Key Vault connection's access token, which Entra issues
  for about an hour and offers no way to revoke, is kept in memory (`tokenCache`, keyed by the connection,
  its tenant, authority, scope, client ID and a digest of the client secret) and reused until a minute
  before it expires, so a rotated client secret or a changed tenant signs in again. Secrets Manager signs
  each request and has no token.
- **One consistent set per run.** What the run read is kept in memory (`storeReadCache`) for that run's
  later steps — the build's arguments, release tasks and the container read the same values — and dropped
  by an engine observer when the run finishes (or after six hours if that is never observed). A dashboard
  restarted mid-run reads the stores again at the next step that opens variables.
- **A failure fails the run with nothing touched**, as `secret_store_unreadable` with the store's own
  answer: `secret store bao, apps/web#DATABASE_URL: permission denied (403)`, `version 3 of secret/apps/web
  is deleted`, `Universal Auth sign-in refused: Invalid credentials (401)`, `could not reach
  http://127.0.0.1:8200: … connection refused`, `ResourceNotFoundException: Secrets Manager can't find the
  specified secret. (400)`, `InvalidSignatureException: The request signature we calculated does not match
  … (403)`, `Entra sign-in refused: invalid_client: AADSTS7000215: Invalid client secret provided. … (401)`
  (Entra's trace and correlation ids are dropped), `SecretNotFound: A secret with (name/id) x was not
  found in this key vault. … (404)` (Key Vault's help link is dropped). `answerMessage` reads each store's
  shape: Vault's `errors`, Infisical's `message`, Doppler's `messages`, AWS's `__type` and `Message`, Key
  Vault's `error.code` and `error.message`, Entra's `error` and `error_description`. A store's error
  answers carry no value, and any credential of the connection that turns up in one is masked — Entra
  names the application in its answer, so the client ID reads as `••••`.
- **What is recorded** is the version, never the value: the step's transcript says `Read DATABASE_URL from
  secret store bao: apps/web#DATABASE_URL, version 2`, and the run's metadata keeps the same as
  `secretStoreReads` (`reference`, `store`, `location`, `version`, `pinned`, `variables`). Doppler reports
  no version; Secrets Manager reports the version id read (a staging label resolves to one), Key Vault the
  version's 32 characters.
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
| `GET /deploy/secret-stores` | any signed-in role, or an ordinary key; a deploy key is refused `key_scope` | No credential, no value; `usedBy` names each active variable that reads the store — which, with the address and sign-in fields, is why a deploy key does not read the list (it cannot point a variable at a store either) |
| `POST /deploy/secret-stores`, `PUT …/{id}` | `system.admin`, session | `400` for an invalid connection, `409 name_taken` |
| `POST …/{id}/test` | `system.admin`, session | Signs in, says who the store took the connection to be, and with `{path, key}` reads that key and reports its version (a `path` alone is a whole Secrets Manager or Key Vault secret); a refusal is `200` with `ok: false` and the store's words, recorded on the connection |
| `DELETE …/{id}` | `s.destructive`, session | `409 secret_store_in_use` naming every variable that still reads it |

What Test does per kind: Vault and OpenBao look the token up (`auth/token/lookup-self`: display name,
policies, TTL); Infisical lists the top folder; Doppler downloads the config and names its project and
config; Secrets Manager calls `ListSecrets` with `MaxResults` 1 and names the first secret (an identity
allowed only `GetSecretValue` on named secrets is answered `AccessDeniedException` there, which Test
reports as signed in without the list permission and goes on to the probe); Key Vault lists one secret
(`GET /secrets?maxresults=1`, the same for an application with only the Get permission, answered
`Forbidden`). Then the probe, if given, is read and its version reported.

Audit: `deploy.secret_store.{create,update,test,delete}` with the kind, the endpoint (as `address`), the
sign-in method, `allowPreviews`, the names of the sign-in fields set (`signInFields`) and on update whether
they were replaced (`signInReplaced`); a test carries `ok`, the store's message and the probe's path and
key; a failed test and a refused removal are failures. No credential and no value is ever written there;
the audit log's own redaction is the second line.

## Limits

- Requests are bounded at 15 s each and a run's whole read at 60 s; an answer over 1 MiB is refused.
  There is no retry: a refused or rate-limited read fails the run with the store's answer, and the run can
  be retried. Infisical's and Doppler's rate limits are met by reading a path once per run.
- Vault KV version 1 is not read. An Infisical secret imported into a folder is read from the folder that
  defines it; the folder's own secrets are what a reference reaches. Secrets Manager is read with the
  connection's long-lived or temporary key pair, not through an instance role or SSO: the backend's own
  AWS identity is never used for a store. Key Vault's RBAC or access policy must grant the application
  Get on secrets (List is only for Test's listing).
- The variables page's reveal shows the reference, never the store's value; the dashboard holds a value
  only in memory, for the run that read it.

## Tests

- `secretstore/secretstore_test.go`: the reference grammar and each kind's shape (`checkFits`), sealing and
  names-only reads, the plain-http rule and other refusals (a region or vault name that is not one
  included), Vault through AppRole with a namespace (one read per path, the minted token revoked, values
  that are numbers or objects), the stores' own words for refusals with no value or credential in them, a
  redirect never followed, Infisical folders and pinned versions, a Doppler config read through an httptest
  server that follows Doppler's documented download API, Test with a probe, a custom CA trusted only where
  given, a store that never answers, and the error shape of every store. For the hosted kinds: the SigV4
  signer against AWS's documented example (the IAM `ListUsers` request of 2015-08-30 — canonical request
  hash, string to sign, signing key and signature — and the signing-key example of 2012-02-15) and against
  the signature the AWS SDK for Go computes for a Secrets Manager call with a session token; Secrets
  Manager through `secretstoretest.AWS`, which follows the JSON 1.1 protocol and verifies every request's
  signature with an implementation of its own (fields, whole values, staging labels, version ids, a binary
  secret, one read per secret, a missing secret, a wrong secret key as `InvalidSignatureException`, a wrong
  key id as `UnrecognizedClientException`, the session token, Test with and without the list permission);
  Key Vault through `secretstoretest.Azure` (Entra's token endpoint and the vault's REST API: whole
  secrets, pinned versions, fields, one token for a run and the next, a new one after a rotation or when
  less than a minute is left, `SecretNotFound` without the help link, `invalid_client` and a wrong tenant
  without the trace ids, Test with and without the list permission).
- `deploy/secret_store_references_test.go`: parsing in the store kind alone, the variable graph (aliases,
  store values as literals, cycles beside store references), save-time checks including the preview rule
  and removal refused while read, one read per run with versions in the transcript and metadata and none
  of the values, a down store failing the first step and never a stop, and a rollback's version note.
  `deploy/secret_store_cloud_test.go` is the live fixture's shape without Docker: the real repository and
  clients against the two fakes, read by a run's first step into the variables its runtime scope opens,
  with the versions in the transcript and none of the values, then Secrets Manager gone and the next run
  failing in its words.
- `api/handlers_deploy_secret_stores_test.go`: roles, session-only writes, Test, a variable reading a
  store, removal refused while read, and the audit trail without a credential or value; and the two hosted
  kinds through the routes, with the derived endpoint, the credential names and the audit's kind and
  sign-in fields.
- Live, `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveSecretStoreReferencesReachTheContainer`:
  a real OpenBao dev server (AppRole) and a real self-hosted Infisical (Universal Auth) in containers on
  loopback, each read into a real container's environment through the run's first step and the runtime
  variables activation opens, a pinned version included, then OpenBao stopped and the next run's first
  step failing in its words. It needs `openbao/openbao:2.7.2`, `infisical/infisical:v0.167.4`,
  `postgres:17-alpine` and `redis:7-alpine` and removes every container, network and image it made.
  `TestLiveSecretsManagerReferenceReachesTheContainer` does the same against a real LocalStack
  (`localstack/localstack:2026.10.3`, Secrets Manager only) through the dashboard's own SigV4 client: a
  JSON field, its `AWSPREVIOUS` label and a whole plain secret into a real container, then LocalStack
  stopped. LocalStack accepts any signature, so the signing is proved by the unit vectors and the
  verifying fake, not there. Azure Key Vault has no server to run; the fakes are its coverage, and the
  real service has not been exercised.
- Browser: `deploy-secret-stores.spec.ts` (the six kinds offered, the AWS and Azure forms and cards, a
  variable picking a whole Key Vault secret and a Secrets Manager field at a staging label) and
  `secret-stores.test.js`.
