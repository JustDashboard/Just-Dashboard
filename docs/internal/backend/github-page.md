# The GitHub page's backend: the repository App, sign-in, connections, repositories, cloning

The GitHub page (`/github`) connects GitHub accounts and manages the repositories they reach through
GitHub's REST API. This document is its core: the App every connection signs in through, the two
sign-in flows, the connections and their tokens, the connected account's own profile, pull requests,
issues and activity, the repository list, a repository's settings, archive, visibility and deletion,
and "Clone to server". What is inside one repository (branches, commits, pull requests, issues,
workflows, collaborators, its README and languages) is in
[github-page-repository.md](github-page-repository.md).

Code: `backend/internal/githubrepos/` (`client.go` and `types.go` are the shared client and policy;
`store.go`, `app.go`, `oauth.go`, `connections.go`, `repositories.go`, `clone.go`, `service.go` and, for
the account itself, `profile.go`, `inbox.go`, `events.go` are this document),
`backend/internal/api/handlers_githubrepos.go` (route skeleton and error mapper),
`handlers_githubrepos_core.go` (these routes) and `handlers_githubrepos_account.go` (the account's
own reads), `backend/internal/gitx/ops.go` (`PrepareClone`, `CloneStreaming`), schema in
`backend/internal/store/schema_github_page.go`. Tests run against a stand-in GitHub,
`backend/internal/githubrepos/githubfake`.

## Why a second App

The dashboard already has a GitHub App, the deploy App (`internal/githubapp`,
[deployments/github-app.md](../deployments/github-app.md)). That one *is the dashboard*: it acts with
installation tokens of its own, receives a webhook, posts commit statuses and decides what deploys
here. The GitHub page must never act as the dashboard. A person with a dashboard account must not gain
GitHub powers by having one; on GitHub they may do exactly what their own GitHub account may.

So the page has its own App, the **repository App**, which acts **as a person, never as itself**.
Every call carries a *user access token* (`ghu_…`) GitHub issued to the person who connected the
account. GitHub decides what each call may do from the intersection of three things: what the App was
granted, what its installation on the account covers, and what the person may do on the repository.
The page additionally works out that intersection first (`githubrepos.ComputeAbilities`) and refuses
with the reason before asking GitHub (403 `github_permission`), so a disabled control says *which* of
the three is missing. GitHub keeps the last word; its own refusal is shown as it is.

The one place the App's own key is used is a credential for git (below): a clone's, which can only
read one repository's contents, and a push of the assistant's workspace, which can only write them.

## The manifest

Created from the GitHub page by a `system.admin` through GitHub's manifest flow:
`POST /github/app/manifest` answers `{action, state, manifest}` and the page posts the manifest to
`action` (`https://github.com/settings/apps/new`, or `/organizations/<org>/settings/apps/new`) from the
admin's own browser, as GitHub requires. One App per dashboard; a second is refused (409
`github_conflict`) until the first is forgotten.

- **No active webhook.** `hook_attributes: {url: <origin>/api/v1/github/hook, active: false}` — the
  page reads GitHub when it is looked at, so GitHub never needs to reach this server. (The hook route
  does not exist; GitHub needs a syntactically valid URL in the manifest.)
- **Every address comes from the browser's origin.** `url`, `redirect_url` and
  `callback_urls: [<origin>/github/callback]`, `setup_url: <origin>/github?installed=1` with
  `setup_on_update: true`. The body carries `origin` and it must equal the request's `Origin` header
  (400 otherwise). These are browser navigations, never server-to-server calls, so a private address
  (`http://192.168.1.20:8080`), a name only the operator's network resolves, or a dashboard behind a
  reverse proxy all work: nothing assumes a public domain or HTTPS.
- `request_oauth_on_install: false` — GitHub's post-install authorization carries no `state`, so it
  could not be tied to the dashboard account that started it. The person signs in from the page.
- `public`: the admin's choice, default true (installable by organisations and other accounts).
  Installing it grants nothing on this dashboard; every use still needs a person's own sign-in here.
- `default_permissions`: `administration: write, contents: write, issues: write, pull_requests: write,
  actions: write, checks: read, statuses: read, metadata: read`; `default_events: []`. No `workflows`:
  editing workflow files is not offered. Each permission backs an operation the page offers.
- App name: `Just Dashboard Repos <4 hex>` by default, at most 34 characters; an organisation owner is
  checked with `ValidOwner`.

GitHub sends the browser back to `/github/callback?code&state`; the page posts both to
`POST /github/callback`, which exchanges the code at `POST /app-manifests/{code}/conversions` (once, an
hour) and stores the App. The redirect lands on the page and not on the API because it is a cross-site
navigation, on which the SameSite=Strict session cookie is not sent.

`POST /github/app/callback-urls {origin}` records `<origin>/github/callback` as an address the admin
added under **Callback URL** in the App's settings on GitHub (GitHub allows ten; so does this). It is
how one App serves a dashboard reached at several addresses (a LAN address and a tunnel name, say):
the admin adds the address on GitHub, then records it here from that address. The dashboard keeps its
own copy only to refuse a sign-in GitHub would refuse with a page the person cannot get back from.

`DELETE /github/app` (`system.admin`, `s.destructive`) revokes every GitHub account's grant best
effort, deletes every connection and forgets the App. A grant is one per account, so it is revoked
through the account's connections in turn, each token refreshed first as a disconnect does, until GitHub
accepts one: an older connection's token can be dead while a newer one works. The App stays on GitHub until its owner deletes it.

Settings links: a user-owned App's settings are at `https://github.com/settings/apps/<slug>`, an
organisation's at `https://github.com/organizations/<org>/settings/apps/<slug>`; it is installed from
`https://github.com/apps/<slug>/installations/new`.

## Signing in: two flows, both GitHub's own

1. **Web flow with PKCE (S256)**, the default. `POST /github/connections/authorize {origin}` answers
   `{url}`: `https://github.com/login/oauth/authorize?client_id&redirect_uri&state&code_challenge&
   code_challenge_method=S256&prompt=select_account`. `prompt=select_account` is how a second GitHub
   account is connected while the browser is signed in to GitHub as the first. `redirect_uri` is
   `<origin>/github/callback` and must be one of the App's registered callback URLs exactly; otherwise
   the start is refused with 409 `github_callback_unregistered`, whose message names the registered
   ones and the settings page. The callback exchanges the code at
   `POST https://github.com/login/oauth/access_token` with client id, client secret, code,
   redirect_uri and the PKCE verifier only this server holds.
2. **Device flow**, for a dashboard reached at an address GitHub cannot send the browser back to, or one
   whose address changes. `POST /github/connections/device` answers a user code and
   `https://github.com/login/device`; the page polls `GET /github/connections/device/{flow}`. The device
   code never leaves the server; the page holds an opaque flow id. The interval is enforced from the
   flow's own clock (a poll that comes early is answered `pending` without asking GitHub), and
   `slow_down` adds five seconds (or GitHub's new interval, if larger). The device flow needs
   **Enable Device Flow** ticked in the App's settings on GitHub; the manifest cannot set it. Without
   it GitHub answers `device_flow_disabled` and the page gets 409 `github_device_flow_disabled`, whose
   message says where to tick it.

GitHub answers OAuth refusals with 200 and an `error` field; they are read as refusals
(`OAuthError`, answered 422 `github_validation` with the reason in words, e.g. a code that expired or a
client secret that was reset on GitHub). No code, device code, verifier or token is ever put in an
error string, a log line, an audit entry or a URL the page sees.

### State

Random 32 bytes, held in memory, single use, ten minutes, bound to the dashboard account that started
it, to its kind (`app` or `connection`), to the `redirect_uri` and, for a connection, to the PKCE
verifier. A callback whose state is unknown, expired, used, or issued to another dashboard account is
400 `github_state`. A state tried by another account is *not* spent, so it cannot be burnt by
somebody else. The kind decides the callback's capability: finishing an App creation needs
`system.admin`, finishing a sign-in `service.control`. A restart forgets pending states; the person
starts again.

## Tokens: lifetime, rotation, reconnect

With "expire user authorization tokens" on (GitHub's default for new Apps) a user access token lasts
eight hours and comes with a refresh token good for six months. With it off there is no `expires_in`
and no refresh token, and the token lasts until revoked; both are handled.

**Refresh tokens rotate**: once used, the old refresh token and the old access token stop working. So
a refresh happens under the connection's own mutex, the row is read again inside it (another request
may have refreshed already), and the new pair is **persisted before it is used**. It happens five
minutes before expiry. Two requests at once refresh once.

- `bad_refresh_token` (or a refresh token past its own expiry) → status `expired`.
- Any **401** from the API with the connection's current token → status `revoked`. This is watched in
  the HTTP transport of every client a connection builds, so the repository resources' calls are
  covered too. A 401 for a token that is no longer the connection's (it was rotated while the request
  was in flight) is not a revocation and is ignored.

Both answer 409 `github_reconnect` until the person signs in again; a reconnect of the same GitHub
account by the same dashboard account updates the row in place (`UNIQUE(owner_user_id, github_id)`).

**Disconnect** (`DELETE /github/connections/{id}`) revokes on GitHub best effort and then deletes the
row whether or not GitHub answered. When no other connection on this dashboard is the same GitHub
account, it revokes the whole grant (`DELETE /applications/{client_id}/grant`, Basic
client_id:client_secret, the token in the JSON body), which ends every token GitHub issued to that
account through this App; otherwise only this connection's token
(`DELETE /applications/{client_id}/token`), so the other dashboard account's connection keeps working.
The grant is revoked *instead of* the token, not after it, because the grant call needs a token GitHub
still accepts. An expired access token is refreshed first so the revocation is accepted.

## Storage and ownership

Two tables (additive, `CREATE TABLE IF NOT EXISTS`):

- `github_repo_app`, one row (`id = 1`): app id, slug, name, owner and owner type, html URL, client id,
  public, the recorded callback URLs, the granted permissions, and `secret_enc`: the client secret and
  private key in one document sealed under the master key (`auth.Sealer`).
- `github_connections`: owner dashboard account (`REFERENCES users(id) ON DELETE CASCADE`), GitHub id,
  login, name, html URL, method (`web`/`device`), status and its message, `tokens_enc` (access and
  refresh token sealed together), their expiries, the installations as last read and when.

The secrets are unexported fields of `App` and of `Connection`; no encoding of either can carry them,
and the tests check every response for the stand-in's tokens, client secret and key.

**A connection belongs to the dashboard account that made it.** Every read of one is
`WHERE id = ? AND owner_user_id = ?`; another account's id answers exactly as a missing one (404
`github_connection`). Administrators get no exception: an admin cannot act on GitHub as another
person.

## Routes

All under `/api/v1/github`, all `httpx.RequireSession`. R = any session, SC = `service.control`,
D = `s.destructive` (ordinary confirmation), D+phrase = `s.destructive` plus the typed phrase
(`X-Confirm`), SA = `system.admin`, hub = refuses a hub session (`httpx.RefuseHubSession`).

| Route | Capability | Answer |
| --- | --- | --- |
| `GET /app` | R | `GitHubSetup` |
| `POST /app/manifest` | SA, hub | `GitHubManifestStart` |
| `POST /app/callback-urls` | SA | `GitHubSetup` |
| `DELETE /app` | SA + D | 204 |
| `POST /callback` | R, then SA (`app`) or SC (`connection`); hub | `GitHubCallbackResult` |
| `GET /connections` | R | own `GitHubConnection[]` |
| `POST /connections/authorize` | SC, hub | `{url}` |
| `POST /connections/device` | SC, hub | `GitHubDeviceStart` |
| `GET /connections/device/{flow}` | SC, hub | `GitHubDeviceState` |
| `POST /connections/{connection}/refresh` | R | `GitHubConnection` |
| `DELETE /connections/{connection}` | SC | 204 |
| `GET /connections/{connection}/profile?refresh=1` | R | `GitHubProfile` |
| `GET /connections/{connection}/pulls?filter=&state=&page=&refresh=1` | R | `GitHubInboxPage` |
| `GET /connections/{connection}/issues?filter=&state=&page=&refresh=1` | R | `GitHubInboxPage` |
| `GET /connections/{connection}/events?refresh=1` | R | `GitHubEventList` |
| `GET /connections/{connection}/repositories?refresh=1` | R | `GitHubRepositoryList` |
| `GET /connections/{connection}/create-options` | R | `GitHubCreateOptions` |
| `POST /connections/{connection}/repositories` | SC | 201 `GitHubCreateResult` |
| `GET /connections/{connection}/repos/{owner}/{repo}` | R | `GitHubRepositoryDetail` |
| `PATCH /connections/{connection}/repos/{owner}/{repo}` | SC (`administer`) | `GitHubRepositoryDetail` |
| `POST …/repos/{owner}/{repo}/archive` | SC (`administer`) | `GitHubRepositoryDetail` |
| `POST …/repos/{owner}/{repo}/visibility` | D (`administer`) | `GitHubRepositoryDetail` |
| `DELETE …/repos/{owner}/{repo}` | D+phrase `<owner>/<name>` (`delete`), hub | 204 |
| `POST …/repos/{owner}/{repo}/clone` | SC | 202 `GitHubCloneStart` |

The JSON shapes are `frontend/src/lib/github-types.ts`. Audit actions: `github.app.manifest`,
`github.app.create`, `github.app.callback-url`, `github.app.forget`, `github.connection.authorize`,
`github.connection.device`, `github.connection.connect` (the device poll that completes is recorded with
`httpx.AuditRead`, since it is a GET), `github.connection.refresh`, `github.connection.disconnect`,
`github.repository.create`, `.update` (the names of the fields changed, never their values),
`.archive`, `.visibility`, `.delete`, `.clone` (with the target path). Targets are logins or
`<owner>/<name>`, never a token or code. Reads are not audited, the account's own included.

Errors go through `githubPageErr` (`handlers_githubrepos.go`, with its code table): this lane adds
400 `github_state`, 409 `github_callback_unregistered` and 409 `github_device_flow_disabled`.

## The account itself: profile, pull requests, issues, activity

Four reads about the connected account rather than about one repository (`profile.go`, `inbox.go`,
`events.go`; `handlers_githubrepos_account.go`). Each needs only a session, starts from
`Service.active` like the repository list (another account's connection is 404 `github_connection`, a
connection GitHub no longer accepts 409 `github_reconnect`, **before** anything kept in memory is
looked at), acts with the person's own user access token, and maps its errors through
`githubPageErr`.

| Read | GitHub | Needs | When GitHub will not answer |
| --- | --- | --- | --- |
| Profile | `GET /user` | a user token; no App permission | an error |
| Contribution calendar | `POST /graphql`, `viewer.contributionsCollection.contributionCalendar` | a user token; GitHub decides what it counts | the profile still answers, without `contributions`, with `contributionsMessage` |
| Pull requests, issues | `GET /search/issues?q=…&sort=updated&order=desc&per_page=30&page=N` | metadata: read; pull_requests / issues: read for private repositories | an error; a spent search budget is 429 `github_rate_limited` |
| Activity | `GET /user` when no profile was read lately, then `GET /users/{login}/events?per_page=30` | a user token; **Events: read** (an account permission) for private events | an error |

- **Profile.** Login, name, bio, company, location, blog, page address, followers, following, public
  repositories and creation date from `GET /user`. What GitHub holds as null or empty is **absent**
  from the answer, never an empty string. `blog` is as GitHub holds it and may have no scheme.
- **The calendar is GraphQL**, the one GraphQL call the page makes (`Client.GraphQL`). Its address is
  derived from the REST root by `GraphQLURL`: `https://api.github.com` → `…/graphql`, a GitHub
  Enterprise root `https://host/api/v3` → `https://host/api/graphql`. It carries the same token and
  headers as a REST call. `contributionLevel` NONE and the four quartiles become `level` 0 to 4; a
  day whose date is not `YYYY-MM-DD` is left out. **A calendar GitHub will not give does not fail the
  profile**: an answer with `errors` (GitHub sends those with status 200: `GraphQLError`), a 403 or
  404, a 5xx, an unreadable answer or no calendar in `data` all answer 200 with the profile,
  `contributions` absent and `contributionsMessage` saying so. A 401, a rate limit, a connection that
  needs reconnecting and a request that ran out of time are still the whole answer, as for every
  read.
- **Pull requests and issues** are GitHub's issue search across every repository, never one walk per
  repository. The query is `is:pr` or `is:issue`, `is:open` or `is:closed`, `archived:false` and one
  qualifier naming **`@me`**, which GitHub reads as whoever the token belongs to: `filter=created` →
  `author:@me`, `assigned` → `assignee:@me`, `mentioned` → `mentions:@me`, `review` →
  `review-requested:@me` (pull requests only). No login is ever spelled into a query, neither a
  request's nor the one stored at connection time: an account renamed on GitHub since, whose old login
  somebody else then took, would otherwise list that person's work as its own. `filter` (default
  `created`), `state` (default `open`) and `page` (default 1, at most 34: search stops at 1,000
  results) are checked by `ParseInboxQuery` before the connection is looked up; anything else,
  `review` on `/issues` included, is 400 `bad_request` with no request to GitHub. The query string is
  built with `url.Values`.
  Each item's `repository` is read from `repository_url`; an item without one is left out. A pull
  request whose `pull_request.merged_at` is set is `merged`; `stateReason` is an issue's only;
  `draft` is false for an issue. `total` is GitHub's `total_count`, `incomplete` its
  `incomplete_results`, and `hasMore` its Link header naming a next page (never past page 34).
- **Activity** is read by the account's login **as GitHub has it now** (`currentLogin`): the profile's
  when one was read in the last five minutes, and otherwise `GET /user`'s, never the login stored at
  connection time. A login outside GitHub's alphabet (letters, digits, hyphen, the underscore of a
  managed account) never reaches the path.
- **A renamed account is followed.** When the profile or `currentLogin` reads a login that is not the
  stored one, the connection is stored under the new login, name and page (`followLogin`, best
  effort) and its repository list and activity are dropped, so everything that names the account by
  login (which repositories are its own, the identity line) follows it without a reconnect.
- **Activity** is the account's last thirty events folded into one flat shape per kind
  (`rawEvent.event`): `PushEvent` → `push`, `PullRequestEvent` → `pull`, `IssuesEvent` → `issue`,
  `IssueCommentEvent` → `comment`, `PullRequestReviewEvent` → `review` (its action is the review's
  state: `approved`, `changes_requested`, `commented`), `CreateEvent` → `create`, `DeleteEvent` →
  `delete`, `WatchEvent` → `star`, `ForkEvent` → `fork`, `ReleaseEvent` → `release`, `PublicEvent` →
  `public`, `MemberEvent` → `member`. Every other type is dropped, as is an event with no id or whose
  repository is not `owner/name`. A field its kind does not have, or GitHub did not send, is absent.
  An address is GitHub's own when it sent one a browser opens as a page (`webURL`), and otherwise
  built from the repository: `/compare/<before>...<head>` for a push to a branch that existed,
  `/tree/<ref>` for a first push or a tag, `/pull/<n>` and `/issues/<n>`.

What these cannot show, on a real App:

- **Private activity needs a permission the manifest does not ask for.** GitHub lists a person's
  private events only to a token with the account permission *Events: read*; the App's manifest
  grants repository permissions only, so the feed is the account's **public** events. Adding the
  permission is a change to the manifest and to every App already created (its owner accepts it on
  GitHub), which is a decision, not part of these reads.
- **GitHub's event payloads are thinner than they were.** Its documented push payload has `ref`,
  `head` and `before` and no commit count, and the pull request inside an event is a minimal one
  without a title or page address. `commits` and `title` are present when GitHub sends them and
  absent otherwise; the address is built from the number. The feed itself is at most 300 events of
  the last 30 days and may lag by minutes to hours.
- **Search answers for what the token reaches**: public repositories and the private ones the App's
  installations cover. A pull request in a private repository the App is not installed on is not
  listed, and `total` counts only what was searched.

## Abilities

`ComputeAbilities` (in `types.go`, the one place the policy is written) turns the person's role
(`RoleFromPermissions` over GitHub's permissions hash) and the installation's grant into
`administer, delete, push, merge, pulls, issues, createIssues, actions` with a reason for each false
one. It is computed for every listed repository and every resolved one. The handlers here refuse
before GitHub is asked: settings, archive and visibility need `administer`; deletion needs `delete`
and is refused for the role *before* the typed phrase is asked for. An archived repository is
read-only: every change but unarchiving is refused, except deletion, which GitHub allows and the
abilities policy does not hold to the archive rule. Visibility takes `public` or `private`.

A repository is reached through the installation on its owner's account (an App has at most one
installation per account). An installation with **selected repositories** covers only those its owner
picked: a repository GitHub answers for (a public one, say) that the installation does not cover is
404 `github_not_found` here, because the page manages only what the App was given.

## The repository list, caching and rate limits

`GET /user/installations` (paged), then `GET /user/installations/{id}/repositories?per_page=100` for
each, four at once, all sharing one budget of `MaxPages` (30) pages; more than that sets `truncated`.
An installation whose listing fails goes into `partial` and the rest still answer, except for the two
failures that belong to the account rather than the installation: a 401 (the connection is revoked)
and a spent rate limit, which are the whole answer. Repositories are deduplicated by id and sorted by
full name. The answer carries the rate limit GitHub last stated.

| Cache | Lifetime | Dropped by |
| --- | --- | --- |
| A connection's repository list | 60 s | `refresh=1`, every change made here, refresh, disconnect, a revocation |
| One repository's access (GitHub's answer, role, installation) | 30 s | every change to it (`forgetGitHubRepo`) |
| A connection's installations (stored on the row) | read again after 60 s by what depends on them | `refresh`, every listing |
| `.gitignore` templates and licences | 24 h | — |
| A connection's profile with its calendar | 5 min; 60 s when the calendar is missing | `refresh=1`, connect, refresh, disconnect, a revocation or expiry |
| One page of the account's pull requests or issues, by listing, filter, state and page | 60 s | the same, and every change made here to any repository (`Forget`) |
| A connection's activity | 60 s | the same, and a rename followed (`followLogin`) |

Each map is bounded (`maxCached`, 512 entries: what has expired goes first, then anything). The last
three are `answers` in `service.go`, dropped with everything else of the connection by
`forgetConnection`. A change made here to a repository (`Forget`, through `forgetGitHubRepo`) also
drops the connection's inbox pages, all of them, since a page that did not list the repository yet may
now (an issue just opened), and its activity; it keeps the profile. A rate limit is 429
`github_rate_limited` with `Retry-After` from GitHub's reset or retry-after; the page shows when it
lifts. **Search has its own budget of thirty requests a minute**, apart from the 5,000 an hour of
everything else, which is why an inbox page is kept a minute and why the page should not ask for
every filter at once.

## Creating a repository

`create-options` lists the owners a repository may be created under: the connected account and each
organisation **where the App is installed**. `canCreate` needs the installation's
`administration: write` and an installation that is not suspended. A selected-repositories
installation carries a note: the new repository must be added to the App's repositories in the
installation's settings on GitHub before it shows here. Templates and licences come from
`GET /gitignore/templates` and `GET /licenses`.

`POST …/repositories` validates the name (`ValidRepoName`), the description (350 characters, one line),
visibility (`public`/`private`) and the template names before GitHub is asked, and refuses an owner the
App is not installed on (403 `github_permission`). It calls `POST /user/repos` for the connected account
and `POST /orgs/{org}/repos` for an organisation (`private`, and `visibility` for an organisation;
`auto_init`, `gitignore_template`, `license_template`). Afterwards it checks whether the installation
reaches the new repository and answers `reachable` with a notice when it does not. Whether the person
may create repositories in an organisation is GitHub's to say; its 403 or 422 is shown.

## Settings

`PATCH …/repos/{owner}/{repo}` sends only the fields given, in GitHub's names (`name`, `description`,
`homepage`, `default_branch`, `is_template`, `has_issues`, `has_wiki`, `has_projects`, `has_discussions`,
`allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`, `allow_auto_merge`,
`delete_branch_on_merge`, `allow_update_branch`, `web_commit_signoff_required`); topics go through
`PUT /repos/{o}/{r}/topics` (at most 20, GitHub's topic rule). A rename answers the new full name.
GitHub states the merge settings only to people who may change them; they are absent otherwise.

## Clone to server

`POST …/repos/{owner}/{repo}/clone {parent, name?, branch?, depth?}`:

1. The repository is resolved with the person's own token, which proves they can read it.
2. The clone is checked whole by `gitx.PrepareClone`, the same code the Git page's `POST /git/clone`
   uses: the parent through `ResolveDir` (inside the Git roots, symlinks resolved), the name through
   `validateDirName` (one segment, not hidden, not an option; `../x` is refused), the branch and depth
   through `cloneArgs`, and the target must not exist. A refusal is answered before any job starts.
3. The address is `https://github.com/<owner>/<name>.git` from GitHub's own spelling, never from the
   request.
4. A job starts (`github.clone`, `service.control`, 15 minutes, target the new path) and the answer is
   202 `{jobId, target}`; the page streams it with the existing job console.
5. For a private or internal repository the job mints an **installation token** with the App's JWT,
   downscoped to that one repository and to reading its contents
   (`POST /app/installations/{id}/access_tokens` with `repository_ids: [id]` and
   `permissions: {"contents": "read"}`). A public repository is cloned with no credential. The person's
   own token is never given to git.
6. The token reaches git only through its environment: `GIT_CONFIG_COUNT/KEY/VALUE` set
   `credential.helper` to empty (no helper of the checkout owner's is asked, or told to store,
   anything) and `http.https://github.com/<owner>/<name>.git.extraHeader` to
   `Authorization: Basic base64("x-access-token:" + token)`, which git sends only to that repository's
   address. It is never in the argv (`ps`), the console, the job's record or the checkout's
   `.git/config`, whose remote is the plain HTTPS address.
7. `git clone --progress` runs through `gitx.CloneStreaming`: the same environment and owner switch
   (`hostexec.AsOwner`) as every other git the Git page runs. Git's carriage-return progress is kept to
   one line a second per phase plus the phase's last. A failure ends the job with git's own last line.
8. The token is revoked when the clone ends (`DELETE /installation/token`), whatever the outcome.

The assistant's push from a workspace ([working on code](../deployments/operator.md#working-on-code))
mints the same kind of token with `permissions: {"contents": "write"}` (`githubrepos.PushToken`), only
when the person's own connection may push to the repository (`Repository.Can.Push`) and the App was
granted Contents: write on the installation, carries it the same way for that one `git push`, and revokes
it when the push ends.

The checkout is then an ordinary checkout under the Git roots, so Local Git's discovery lists it;
later fetches, pulls and pushes use its owner's own git credentials, as every other checkout's do. The
GitHub page matches a repository to its checkouts on the frontend from `/git/` (normalised remote URLs,
never names); nothing is cloned automatically and no clone is assumed to exist.

## Limitations

- **What a GitHub App user token cannot do.** Everything here is limited to the App's permissions and
  the installations the person can see. Repositories of accounts where the App is not installed are
  not listed and cannot be managed; a repository outside a selected-repositories installation answers
  404 here even when it is public. Creating a repository under an account needs the App installed
  there with `administration: write`. Editing workflow files is not offered (no `workflows`
  permission). Organisation membership and the right to create repositories in an organisation are
  not read; GitHub's refusal is shown.
- **Selected-repositories installations and creation**: a repository created under such an
  installation may not be reachable until its owner adds it to the installation on GitHub; the answer
  says so (`reachable: false`, `notice`).
- **No webhook**: installation changes on GitHub are seen when the page refreshes (the setup URL brings
  the browser back after an install or permission change, and `POST …/refresh` reads again).
- **Pending states and device flows are in memory**; a restart forgets them.
- **The device flow** needs Enable Device Flow ticked on GitHub by the App's owner.
- **The account's own reads** have their limits listed with them above: public activity only,
  thinner event payloads, search only where the token reaches, and a calendar that may be withheld.
- **Nothing here was verified against the real github.com.** Every test runs against
  `githubrepos/githubfake` and, for cloning, `git http-backend` served locally. The shapes follow
  GitHub's REST documentation (API version 2022-11-28); a difference found on a real App is a bug to
  fix here.
