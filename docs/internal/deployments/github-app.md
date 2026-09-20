# GitHub App

One GitHub App per dashboard, created through GitHub's manifest flow and installed on the accounts
whose repositories deploy here. It replaces three manual steps: a webhook and secret per repository, a
personal token for private clones and commit statuses, and reading a run log to find a preview's
address.

## Connecting

`POST /api/v1/deploy/github-app/manifest` (`system.admin`) answers with the manifest, the GitHub page to
post it to (`/settings/apps/new`, or `/organizations/<org>/settings/apps/new` when an organisation was
named) and a `state` good for fifteen minutes. The Credentials page posts that manifest from the
operator's own browser, as GitHub requires; GitHub creates the App and redirects the browser to
`/deploy/credentials?code=…&state=…`. The page strips both from the address and posts them to
`POST /api/v1/deploy/github-app/callback` (`system.admin`), which exchanges the one-time code for the
App's id, slug, client id and secret, webhook secret and private key and stores them sealed under the
master key in the `github_app` row. The redirect lands on the page rather than on the API because it
is a cross-site navigation from github.com: the browser withholds the SameSite=Strict session cookie
on it, so an API callback could only ever answer 401. The manifest asks for `contents:read`, `metadata:read`, `pull_requests:write` and
`statuses:write`, subscribes to `push` and `pull_request`, and points the App's webhook at
`<dashboard endpoint>/api/v1/hooks/github-app`; the dashboard therefore refuses to start the flow until
it knows its own public HTTPS address. `DELETE /api/v1/deploy/github-app` forgets the App (the App
itself stays on GitHub until its owner deletes it) and removes the installation credentials no
current source uses.

`GET /api/v1/deploy/github-app` (any session) reports whether an App is connected, its installations
(read from GitHub at most once a minute, or immediately after an `installation` delivery), the link to
install it on another account and the webhook address; a GitHub-side failure sits in `error` beside a
still-configured App. `GET …/github-app/repositories` lists every repository every installation grants,
each with the `credentialId` that clones it and with `pushedAt`, `fork` and `archived` — the import
picker sorts by the last push and marks the other two, and until they travelled with this listing a
repository the App granted could not be told apart from one abandoned two years ago.

## What it does

- **Clones.** Every installation has one deploy credential of kind `github_app` named
  `GitHub-App-<account>` — a pointer to the installation, with no secret of its own. Opening it mints (or
  reuses, until two minutes before expiry) the installation's hour-long token and hands it to the same
  HTTPS path a token credential takes (HTTP Basic, `x-access-token` and the token, scoped to the exact
  remote). The import picker lists the App's repositories under a heading naming the App, ahead of
  anything only the signed-in CLI account can reach, and an import through it sets that credential. These credentials are created and removed by the App service; the credential
  routes list and delete them and refuse to create or edit one.
- **Deliveries.** A GitHub trigger created with `config.delivery: "app"` (the form's default while an App
  is connected) has nothing configured on GitHub: `POST /api/v1/hooks/github-app` verifies every
  delivery with the App's webhook secret, and a `push` or `pull_request` reaches every enabled trigger on
  that repository that asked for App delivery, each through the same
  `dispatchAutomationEvent` decision the per-trigger hooks take (filters, the replay fence per trigger and
  delivery id, policy, preview approval, queue admission). The answer lists what each trigger decided;
  a repository no trigger watches, and the App's own lifecycle events, are accepted and ignored.
- **Commit statuses and pull request comments.** See [notifications](notifications.md): statuses go
  through the App wherever it is installed, and each preview keeps one comment on its pull request.

## Package layout

`internal/githubapp` speaks GitHub's REST API with the standard library: `Client` (RS256 JSON web tokens
over the App's key, installation tokens with a cache, paged listings, statuses, comment upsert),
`ExchangeManifestCode`/`NewManifest`, `Store` (sealed row) and `Service` (manifest states, the
per-minute installation cache, the credential sync and the two observer adapters). The API layer
(`handlers_github_app.go`) mounts the routes under `/deploy/github-app` and the public webhook under
`/hooks/github-app`; `modules.go` wires the service into the planning store (token minting) and the
run observers.

## Verification

- `githubapp_test.go`: JWT signature and claims, PKCS#1 and PKCS#8 keys, token minting once per hour
  with refresh near expiry, paged installations and repositories, statuses through the granting
  installation, comment create-then-edit, the manifest flow against a fake GitHub with sealed storage,
  the installation cache, and a configured App whose GitHub side fails.
- `github_app_credentials_test.go`: an App credential mints rather than stores, refuses paste and edit,
  and survives a disconnect only while a project uses it. `github_comments_test.go` and
  `TestAppDeliveryTriggersAreFoundByRepository` cover the observer and the delivery lookup.
- `handlers_github_app_test.go`: the routes end to end — manifest, forged and real callbacks, status
  without secrets, repositories with credential ids, a trigger on App delivery, a bad signature, an
  installation event, a foreign repository, an accepted push that enqueues one run, its replay, a
  status posted through the App, and disconnection.
- Not verified here: GitHub itself. The manifest exchange, installation tokens and webhook signatures
  follow GitHub's documented shapes and are exercised against a local fake; a real App is the first
  thing to connect on a public dashboard.
