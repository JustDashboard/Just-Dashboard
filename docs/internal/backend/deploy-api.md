# The deployment API's OpenAPI description

The dashboard describes its deployment API — every route under `/api/v1/deploy` — as an OpenAPI 3.1
document, served at `GET /api/v1/deploy/openapi.json` (and `.yaml`) and committed as
[`docs/api/deploy-openapi.json`](../../api/deploy-openapi.json) for client generators. This page says
how the description is produced, what keeps it honest, and what it deliberately does not claim.

## What it is for

A deploy key ([`invariants.md`, invariant 4](../security/invariants.md)) lets a CI pipeline or a
script drive deployments without the browser; `jd` is one client of that API, written by hand. The
description is for every other client: generate a typed client from it (`bunx openapi-typescript`,
`openapi-generator`, or whatever the language has), read it to learn what a route takes and answers,
or check a script against it. Account → API keys links it beside the `jd` download when a deploy key
is minted, and `jd openapi [--yaml]` prints it as the server serves it.

## How it is produced

Nothing in the document is written by hand except one sentence per route. `Server.Routes` keeps the
finished router in `Server.deployOpenAPI`, and `buildDeployOpenAPI`
(`backend/internal/api/deploy_openapi.go`) reads the document off it the first time either route is
asked for, once (`sync.Once`); the two handlers serve those bytes and read nothing from the request
or the store. It is not built in `Routes` itself because it costs about ten times what the rest of
`Routes` does (some 30 ms here), and the API tests build routes by the thousand.

- **The routes** come from `chi.Walk` over the router, exactly as `routes_test.go` walks it: every
  method and pattern under `/api/v1/deploy/`, with the middleware chain in front of each. A route
  becomes an operation on its path item; its `{params}` become path parameters
  (`openapiPathParam`: `{id}` is the deployment's except under `/deploy/blueprints/` and
  `/deploy/credentials/`, `{action}` is the closed set of player actions). The `operationId` is the
  handler's own name read off the router (`handleDeploymentRunCreate` → `deploymentRunCreate`), and
  the method and path when two routes share a handler.
- **The operation table**, `deployOperations`, is keyed like `deployKeyRoutes`: `METHOD /deploy/…`.
  Each line has a one-sentence summary, a tag, the Go value the handler decodes (`Request`), the Go
  value a successful answer encodes (`Response`), the status, query parameters, and a note. The
  values are zero values of the handlers' own types — `deploy.ConfigurationWriteRequest{}`,
  `(*deploy.EngineRun)(nil)`, `[]deploy.Release(nil)` — so a renamed type fails to compile here and a
  changed field appears in the next generation. An answer a handler assembles in a `map[string]any` is
  given as a `shape{…}` of its keys and their value types, read off the handler, and a route that
  answers one of several documents as an `anyOf{…}`. That is `anyOf` in the description, not `oneOf`:
without `required` lists an object or an empty list matches more than one alternative, which a strict
`oneOf` would refuse.
- **The schemas** (`deploy_openapi_schema.go`) are read by reflection, following the rules
  `encoding/json` applies: the `json` tag names a property and `-` hides it, an embedded struct's
  fields are promoted, `time.Time` is a `date-time` string, `time.Duration` an integer of
  nanoseconds, `json.RawMessage` any value, `[]byte` base64. A pointer, slice or map may be `null`,
  because Go writes `null` for a nil one. A named struct becomes a component named `package.Type`
  (`deploy.EngineRun`, `api.secretStoreView`) and is referenced; anonymous structs are inline. Two
  tables beside the types say what reflection cannot see: `openapiEnums` lists the constants of
  every closed string vocabulary (`deploy.RunState`, `blueprint.InputKind`, …), and
  `openapiWriteOnly` names every field that carries a secret in — a variable's value, a `.env` text, a
  credential's secret, a channel's token, a drain's headers, a store's credentials — which the
  description marks `writeOnly` and no route reads back.
- **Security** is derived from the two places the gate itself decides from, so the description and
  the gate cannot disagree: a route lists `apiKey` unless `httpx.RequireSession` is in its middleware
  chain (found by function identity in the walked chain), `deployKey` when its line in
  `deployKeyRoutes` admits a deploy key, and `session` always. `x-deploy-key` on each operation says
  what a deploy key gets there and why, in the table's own words. The one route called by no person,
  `POST /deploy/{id}/hooks/{triggerID}`, lists the `webhookSignature` scheme (`X-JD-Signature-256`).
- **Errors** are the one `Error` response (`{"error":{"code","message"}}`) under `default`.

The YAML is written from the JSON tree by a small emitter that double-quotes every string, so no
value is mistaken for a number, a boolean or null; the two are the same document.

## What keeps it honest

Six tests in `deploy_openapi_test.go`, run with the API package's tests and by
`scripts/test-changed.sh` for any change beside them:

| Test | Fails when |
| --- | --- |
| `TestEveryDeployRouteIsDescribed` | a deployment route has no line in `deployOperations`, a line names a route the router no longer has, a summary is not a sentence, or a tag is not declared — so adding a route without describing it is a test failure, like a session-only route without a deploy key decision |
| `TestTheDeployOpenAPISecurityMatchesTheGate` | an ordinary key is refused `session_required` on a route that lists `apiKey` (or admitted on one that does not), or a deploy key is refused on a route that lists `deployKey` (or admitted on one that does not), walking every deployment route with both keys |
| `TestTheDeployOpenAPIDocumentIsStructurallyValid` | the document breaks OpenAPI 3.1's structural rules: the required top-level fields, a `$ref` that resolves to nothing, a `{param}` without a path parameter or a path parameter without its `{param}`, a duplicate `operationId`, an undeclared tag or security scheme, a malformed response key, an unknown schema type |
| `TestTheDeployOpenAPIDescriptionCarriesShapesNotValues` | the document carries an `example` or a `default` value, a key, or a line of `openapiWriteOnly` matched no field of a described type (a renamed secret field would otherwise be unmarked with its line still there) |
| `TestOpenAPIEnumsMatchTheConstants` | a vocabulary in `openapiEnums` differs from the type's constants in the source (read with `go/parser`), or a named string type the description reaches has constants and no entry |
| `TestTheCommittedDeployOpenAPIDescriptionIsCurrent` | `docs/api/deploy-openapi.json` is not what this build serves, `info.version` aside; the message says how to regenerate it |

`TestTheDeployOpenAPIRouteAnswersEveryCredential` covers the route: a session, an ordinary key and
a deploy key all read both forms (200), nobody without a credential does (401), and an MCP key is
refused `key_scope` as everywhere outside its endpoint. `TestOpenAPIPrintsTheDashboardsDescriptionAsItCame`
(`backend/cmd/jd`) pins that `jd openapi` prints the server's bytes unchanged.

### Regenerating the committed copy

After a change to a deployment route, to a type a deployment handler decodes or encodes, or to the
tables:

```bash
cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent
```

and commit `docs/api/deploy-openapi.json` with the change. The committed file's `info.version` is the
version of the build that wrote it; the comparison ignores that field alone, so a release bump does
not require a regeneration by itself.

## What it does not claim

- **No `required` lists.** Go's decoder accepts a body with any field left out (it is then its zero
  value), and a handler's own validation decides what it refuses; the description would have to guess.
  The trade is stated in `info.description`: a property without `omitempty` is always present in an
  answer, and may be left out of a request. A generated client therefore has every property optional.
- **Lists and maps may be null** wherever Go can write a nil one, even where a handler always
  initialises its slice. That is honest about the type, not about every handler.
- **Capabilities are not described.** Which role a route needs (`service.control`, `system.admin`,
  `destructive`) is a closure in the middleware chain that reflection cannot name; the notes say
  "Destructive" where the route is behind `s.destructive`.
- **Query parameters are listed by hand** in the table, read off each handler's `r.URL.Query()`.
- **Hand-shaped answers.** 33 operations answer a `map[string]any` the handler assembles; their
  `shape` is read off the handler and marked "Assembled by the handler" in the schema. The anonymous
  request structs the handlers used to decode were given named types in their own files
  (`gitPolicyWrite`, `previewApproveRequest`, `previewRejectRequest`, `enabledRequest`,
  `variableRevisionRequest`, `variableGenerateRequest`, `githubAppCallbackRequest`,
  `releasePinRequest`, `maintenanceSwitchRequest`, `sitePageWrite`, `diagnoseRequest`) so the
  description references the type the handler decodes; two routes that decode an empty object
  (`POST /deploy/drafts`, `POST …/detect`) are described as one.
- **WebSocket routes** (`…/stream`) are described by their upgrade (`101`), not their messages.
- **No values.** The description carries shapes: no examples, no defaults, no secret. The route is
  static and reads nothing, so it is not audited as a sensitive read; it tells a caller what the API
  is, not what exists.

## Who reads it

`GET /deploy/openapi.json` and `.yaml` are in the feature group behind the network allowlist and
authentication, like every other route: any signed-in account and any ordinary API key read them,
and their lines in `deployKeyRoutes` (`keyOpen`) admit a deploy key, narrowed or not, since the
key's holder is who generates a client. An MCP key is refused as it is everywhere but its endpoint.
