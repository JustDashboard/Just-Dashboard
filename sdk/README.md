# Just Dashboard deployment API clients

Typed clients for every route under `/api/v1/deploy`, generated from the dashboard's OpenAPI 3.1
description ([`docs/api/deploy-openapi.json`](../docs/api/deploy-openapi.json)), which the dashboard
generates from its own router. A route is a method in Go and a function in TypeScript named for its
operationId; a schema is a type. They call the API with a key: a **deploy key** (Account → API keys →
New key → Deploy key), which reaches the routes a pipeline needs, or an ordinary **API key**, which
reaches every route that does not need a signed-in session. Each method's comment says which.

Do not edit `go/client.go`, `go/operations.go`, `go/types.go`, `typescript/client.ts` or
`typescript/types.ts`: they are regenerated from the description, and a test fails when they differ
from what the dashboard's build describes. After a change to a deployment route or a type it takes or
answers:

```bash
cd backend && JD_UPDATE_OPENAPI=1 go test ./internal/api -run TestTheCommittedDeployOpenAPIDescriptionIsCurrent
cd backend && JD_UPDATE_SDK=1 go test ./internal/api -run TestTheCommittedSDKsAreCurrent
```

The tests beside them (`go/client_test.go`, `typescript/client.test.ts`), `go/go.mod` and
`typescript/tsconfig.json` are written by hand. How the clients are generated, and why, is in
[`docs/internal/backend/deploy-api.md`](../docs/internal/backend/deploy-api.md#the-clients-generated-from-it).

## Go

`sdk/go` is its own module with no dependencies outside the standard library (Go 1.24 or newer):

```bash
go get github.com/Wayy01/Just-Dashboard/sdk/go@main
```

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	jd "github.com/Wayy01/Just-Dashboard/sdk/go"
)

func main() {
	ctx := context.Background()
	c := jd.NewClient("https://dash.example.com", os.Getenv("JD_KEY"))

	// Every environment of deployment 12, then a deploy of production.
	environments, err := c.DeploymentEnvironments(ctx, 12)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%+v\n", environments)

	run, err := c.DeploymentRunCreate(ctx, 12, 34,
		jd.DeploymentRunCreateRequest{Operation: jd.DeployOperationDeploy, Ref: "main"},
		jd.WithHeader("Idempotency-Key", os.Getenv("CI_PIPELINE_ID")))
	if jd.IsCode(err, "already_running") {
		log.Fatal("a run is already in progress")
	}
	if err != nil {
		log.Fatal(err) // *jd.Error: the status, code and message the API answered with
	}
	fmt.Println("queued run", run.RunNumber, run.State)
}
```

`BaseURL` is the dashboard's address; `/api/v1` is added unless it is already there. Set
`HTTPClient` for timeouts or a private certificate authority. A route whose answer is one of several
documents returns `json.RawMessage` (its comment names them), one that is not JSON returns `[]byte`,
and a WebSocket route is `…URL`, its `wss://` address for a WebSocket client of your own.

## TypeScript

`sdk/typescript` has no runtime dependencies: copy `client.ts` and `types.ts` into a project, or
import them from a checkout. They run anywhere with `fetch` — a browser, Node 18 or newer, Deno, Bun.

```ts
import { ApiError, Client, deploymentEnvironments, deploymentRunCreate } from "./sdk/typescript/client"
import { DeployOperation } from "./sdk/typescript/types"

const client = new Client({ baseUrl: "https://dash.example.com", key: process.env.JD_KEY })

const environments = await deploymentEnvironments(client, 12)
console.log(environments)

try {
  const run = await deploymentRunCreate(
    client,
    12,
    34,
    { operation: DeployOperation.Deploy, ref: "main" },
    { headers: { "Idempotency-Key": process.env.CI_PIPELINE_ID ?? "" } },
  )
  console.log("queued run", run.runNumber, run.state)
} catch (error) {
  if (error instanceof ApiError) console.error(error.status, error.code, error.message)
  else throw error
}
```

A function takes the client, the path parameters in the path's order, the body when the route takes
one, the query parameters as an object, and options (`headers`, `signal`). A browser cannot send a key
to the dashboard from another origin, so call it from a server, a script or a CI job.

## Checks

```bash
cd sdk/go && go vet ./... && go test ./...
cd sdk/typescript && ../../frontend/node_modules/.bin/tsc -p . && bun test
```

`scripts/test-changed.sh` runs both for any change under `sdk/`; the dashboard's API tests also build
and test the Go client.
