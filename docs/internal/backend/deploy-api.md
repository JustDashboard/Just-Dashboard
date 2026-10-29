# The deployment API's OpenAPI description

The dashboard describes its deployment API — every route under `/api/v1/deploy` — as an OpenAPI 3.1
document, served at `GET /api/v1/deploy/openapi.json` (and `.yaml`) and committed as
[`docs/api/deploy-openapi.json`](../../api/deploy-openapi.json) for client generators. This page says
how the description is produced, what keeps it honest, and what it deliberately does not claim.

## What it is for

A deploy key ([`invariants.md`, invariant 4](../security/invariants.md)) lets a CI pipeline or a
script drive deployments without the browser; `jd` is one client of that API, written by hand. The
description is for every other client: the repository's own typed clients are generated from it
(`sdk/go` and `sdk/typescript`, [below](#the-clients-generated-from-it)), and so are the MCP tools an
AI client calls the API with ([below](#the-mcp-tools-generated-from-it)); generate one in another
language (`openapi-generator`, or whatever the language has), read it to learn what a route takes and
answers, or check a script against it. Account → API keys links it beside the `jd` download when a deploy key
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
  every closed string vocabulary (`deploy.RunState`, `blueprint.InputKind`, …), each written inline
  with the vocabulary's name as its `title` so a generated client names the type its constants
  belong to, and `openapiWriteOnly` names every field that carries a secret in — a variable's value, a `.env` text, a
  credential's secret, a channel's token, a drain's headers, a store's credentials — which the
  description marks `writeOnly` and no route reads back.
- **Security** is derived from the two places the gate itself decides from, so the description and
  the gate cannot disagree: a route lists `apiKey` unless `httpx.RequireSession` is in its middleware
  chain (found by function identity in the walked chain), `deployKey` when its line in
  `deployKeyRoutes` admits a deploy key, and `session` always. `x-deploy-key` on each operation says
  what a deploy key gets there and why, in the table's own words. The one route called by no person,
  `POST /deploy/{id}/hooks/{triggerID}`, lists the `webhookSignature` scheme (`X-JD-Signature-256`).
  `x-destructive: true` marks a route behind `s.destructive` (the destructive capability and its
  budget), found in the walked chain by the `markDestructive` marker that `s.destructive` puts first
  in its group, as `RequireSession` is found; a route whose handler decides from the body (a run's
  stop or restart) is not marked, and its note says so.
- **Errors** are the one `Error` response (`{"error":{"code","message",…}}`) under `default`. Its
  properties are read off `httpx.APIError` as `encoding/json` writes it (`code`, `message`, `phrase`,
  `field`, `resource`, `operation`, `reason`, `raw`, `retryable`), each described by a line of
  `openapiErrorFields`; a field added to the type without one is a problem `TestEveryDeployRouteIsDescribed`
  reports.

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
- **Capabilities are not described,** but for `destructive`. Which role a route needs
  (`service.control`, `system.admin`) is a closure in the middleware chain that reflection cannot
  name; `x-destructive` marks the routes behind `s.destructive`, from its marker, and the notes say
  "Destructive" there too.
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

## The clients generated from it

`backend/internal/openapigen` reads the document and writes two clients, committed under `sdk/`
(usage in [`sdk/README.md`](../../../sdk/README.md)):

- **Go**, `sdk/go`: its own module, `github.com/Wayy01/Just-Dashboard/sdk/go`, package `jd`, with no
  dependency outside the standard library. It is a nested module rather than a package of the backend
  module because a consumer would otherwise download the backend's every dependency (Docker, the
  AWS SDK, the database drivers) and its Go 1.26.8 floor to call an HTTP API, and could import
  nothing under `internal/` anyway; its floor is Go 1.24 (`omitzero`). `types.go` has a struct per
  component and per object a route spells out inline (named for the operation and the property where
  it was met: `DeployGetResponse`), a string type and its constants per vocabulary (`RunState`,
  `RunStateQueued`), a pointer where the description admits `null`, and JSON tags that leave a zero
  scalar out of a request (the API reads an absent field as its zero value) while a list or a map is
  always sent. `operations.go` has a method per operationId on `Client`: its path parameters typed
  and in the path's order, then the body, then a `…Params` struct of the query parameters, then
  `RequestOption`s (`WithHeader("Idempotency-Key", …)`). An answer that is one of several documents is
  `json.RawMessage`, one that is not JSON is `[]byte`, and a WebSocket route is `…URL`, its `ws://` or
  `wss://` address. `client.go` is the runtime: the base URL (`/api/v1` added unless given), the key as
  a bearer token, an `*http.Client`, the context, and `*Error` carrying the status and every field of
  the error envelope (`IsCode(err, "key_scope")`).
- **TypeScript**, `sdk/typescript`: `types.ts` has an interface per object (every property optional,
  `| null` where the description admits it) and a constant object with its union type per vocabulary;
  `client.ts` has the runtime — a `Client` with the base URL, the key, an optional `fetch` and headers,
  and `ApiError` carrying the status and the envelope — then one function per operationId,
  `fn(client, ...pathParameters, body, query?, options?)`, over `fetch` with no runtime dependency, so
  it runs in a browser, Node, Deno or Bun. It is outside `frontend/`, so nothing of it reaches the
  dashboard's bundle; its own `tsconfig.json` type-checks it.

Names come from the component's own name less its package (`deploy.EngineRun` is `EngineRun`), with
the package kept where two packages share a name (`LogdrainWrite`, `SecretstoreWrite`) or the name is
one the runtime declares; operation names are the operationIds. The output is the same bytes every
time — declarations, fields and operations in the order of their names, nothing from the version, the
time or the machine — and each file opens with a line saying it is generated and how to regenerate it.

| Test | Fails when |
| --- | --- |
| `TestTheCommittedSDKsAreCurrent` (`api/sdk_test.go`) | `sdk/go/{client,operations,types}.go` or `sdk/typescript/{client,types}.ts` is not what this build's description generates |
| `TestTheCommittedGoSDKBuildsAndPassesItsTests` (`api/sdk_test.go`) | `go test ./...` fails in `sdk/go`: the generated Go does not compile or vet, or the hand-written request-builder tests fail |
| `TestTheGeneratedClientsAreTheGoldenOnes` (`openapigen`) | the clients generated from `openapigen/testdata/fixture.json` differ from `testdata/golden/`, so a change to the generator shows in review as a diff of small files |
| `TestTheGeneratedClientsSayWhatTheDocumentSays`, `TestTheGeneratedGoCompiles`, `TestGenerationIsDeterministic` (`openapigen`) | a feature of the fixture (a shared name, a nullable reference, a stream, a CSV answer, the envelope) is not in the output; the golden Go does not vet; two generations differ |

The TypeScript client's `tsc -p sdk/typescript` and `bun test` (its request builder against a
recording `fetch`) are run by `scripts/test-changed.sh` for any change under `sdk/`, since the Go
tests have no Bun. Regenerate after regenerating the description:

```bash
cd backend && JD_UPDATE_SDK=1 go test ./internal/api -run TestTheCommittedSDKsAreCurrent
```

and commit `sdk/` with the change; a change to the generator itself also rewrites its golden files
with `JD_UPDATE_SDK=1 go test ./internal/openapigen`.

## The MCP tools generated from it

Every operation whose route the deploy key table opens (`keyOpen`, `keyEvery`) is also an MCP tool,
named for its operationId in snake case, its input schema read from this document (the path and
query parameters, and the body with the components it refers to copied into the schema's `$defs`).
The tools are read off the same router and the same description, so a route described here and given
a deploy key line is a tool with nothing else written. How they are offered, called and audited is in
[the MCP server](databases-proxy-platform.md#the-mcp-server).

## Who reads it

`GET /deploy/openapi.json` and `.yaml` are in the feature group behind the network allowlist and
authentication, like every other route: any signed-in account and any ordinary API key read them,
and their lines in `deployKeyRoutes` (`keyOpen`) admit a deploy key, narrowed or not, since the
key's holder is who generates a client. An MCP key is refused as it is everywhere but its endpoint.
