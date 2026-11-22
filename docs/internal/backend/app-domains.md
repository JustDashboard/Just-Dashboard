# App domains: `<app>.<handle>.just-dashboard.com`

A server linked to a just-dashboard.com account can hold one **handle** in the site's zone. The site
publishes `<handle>.<zone>` and `*.<handle>.<zone>` (A records, IPv4, not proxied) at the server's public
address, and the server keeps **one** wildcard certificate for both, proved over DNS-01 through the site. After
that a deployment needs nothing from the site: the wildcard record resolves every app name, and the wildcard
certificate covers it.

Everything lives in `backend/internal/appdomain`; the calls to the site are `accountlink/domain.go`, the
certificate is `proxysvc/app_domain.go` and `app_domain_routes.go`, the routes are
`api/handlers_appdomain.go` and `api/appdomain_wiring.go`, the hook is `cmd/jd/appdomain.go`, and the deploy
side is `deploy/app_domains.go` and `api/handlers_deploy_hostname.go`.

**The wire contract is not in this repository.** It is
[`docs/app-domains-protocol.md`](https://github.com/JustDashboard/frontend/blob/main/docs/app-domains-protocol.md)
in the site's repository, an extension of the [link protocol](account-link.md). `accountlink/domain_test.go`
carries its signed-request vectors (`GET /domain`, `PUT /domain`, `POST /domain/challenges`).

It is optional. Nothing is claimed until a person chooses a handle: on just-dashboard.com, on the link's
approval page or the server's page there (an **offer**, which the server then claims by itself), or on the
Settings card here. `JD_APP_DOMAINS=false` switches it off
entirely (and `JD_ACCOUNT_LINK=false` with it): nothing about domains is sent, no wildcard is issued, the hook
is refused, and every name under the zone is treated as not this server's.

## Trust model

- **Identity is the account link.** Every call is signed with the link's identity and takes its number from
  the link's sequence counter; the site knows which account answers for each name. The calls are only sent on
  an active account link (`Service.domainLink`: linked, admitted, the account profile).
- **Claiming publishes this server's IP address in public DNS under the project's domain**, so claiming and
  releasing are a person's own act, as linking is (security invariant 12): a browser session (an API key is
  refused with `session_required`), never the assistant (`agentExcludedRoutes`), releasing in the destructive
  budget. Every change is audited.
- **The DNS token never reaches a dashboard.** The site changes exactly three names for this server's handle.
  The server proves the address is its own by calling from it: the site refuses an address that is not the
  one the request came from (`address_mismatch`).
- **No per-app certificate under the zone, ever.** Let's Encrypt's limits are per registered domain and shared
  by every server. `proxysvc.SetAppDomainZone` makes `EnsureDeploymentCertificate` refuse any name under the
  zone (`ErrAppDomainName`) before anything is ordered, on nginx and on the Docker Caddy ingress, and the
  deployment issuer sends names under the held handle to the wildcard instead (`appDomainCertificateIssuer`).
  Caddy never runs ACME for them: their site block serves a copy of the wildcard.

## State (`appdomain/state.go`)

`settings['app.domain.state']` holds the handle, zone, hostname, address, status (`active`, `suspended`,
`detached`), reason, `autoAssign`, `lastCheckedAt`, `lastError`, the site's last word on whether it offers app
domains, and the last issuance error. Every write is a read-modify-write in one transaction with a raised
`revision` (`update`). A state with no handle holds nothing and keeps `autoAssign` (default on) for the next
claim. `settings['app.domain.hook']` holds the SHA-256 of the hook's token.

## Background work (`appdomain/service.go`)

One loop, through `safego`, started with the account link and only when the feature is on:

- **At start-up and every 15 minutes**, the address follow-up: `FirstPublicIPv4(proxysvc.PublicAddresses())`
  against the stored address; a difference is a `PUT /domain` with the new one (audited `app.domain.address`).
- **At start-up and hourly, and when the Settings card is opened** (at most once a minute: the site allows 60 an
  hour), `GET /domain`. The site's record is mirrored: a suspension (`suspended`, with the admin's reason), an
  unsuspension, a handle held on the site that this store did not have. A handle the site no longer lists is
  `detached`. A site that cannot be reached changes nothing but `lastError`.
- **When the account link is cleared**, whatever the reason, the account link's `OnClear` hook runs
  `appdomain.DetachInTx` in the transaction that ends it (the running dashboard's link and `--admin unlink`
  both carry the hook). A link found missing by the loop is detached too.

Nothing else changes by itself, except the claim of an offer below.

## Claiming at link time (offers)

The person approving the link on just-dashboard.com can give the server an address there, prefilled with a
free handle made from the account's name; the server's page on the site can do the same later. That choice
is an **offer**: `GET /domain` answers `offer: "<handle>"` (or `null`) beside `domain`, never both set
(`accountlink.DomainInfo.Offer`, decoded strictly: a string that is not a handle, or one beside a domain,
is a protocol error). An offer reserves nothing. The site clears it once the server's domain is active, on
`DELETE /domain` (so a released address is not claimed straight back), when the server is removed, and when
the owner clears it.

`autoClaim` claims it with the ordinary signed `PUT /domain` from the server's own address, exactly as the
Claim button does, and so issues the wildcard, when the feature is on, the stored link is active, the site
says `available`, the server holds no domain (none, or a detached one) and the site named an offer. It is
checked wherever `GET /domain` is asked: at start-up, hourly, when the Settings card asks (at most once a
minute), and right after a link is approved. That last one is the account link's `OnClear` hook, which runs in
the transaction that starts a link, followed once it commits by `Service.LinkChanged`; a link made by
`--admin link` in another process is found by the loop's look at the stored link every 30 seconds. One claim
runs at a time. A failure keeps its sentence (`offerError`, shown on the card) and the same handle is not tried
again for an hour (the site allows ten `PUT`s an hour per server and five new handles a day per account); the
Claim button is never held back. Each automatic claim is audited as `app.domain.claim` by `system` with
`automatic: true`. `JD_APP_DOMAINS=false` ignores offers.

## REST API (`api/handlers_appdomain.go`)

Under the account link's routes, inside the admin-only `/dashboard` group.

| Route | Guards | Does |
| --- | --- | --- |
| `GET /dashboard/link/domain` | | The view below. Asks the site first, at most once a minute |
| `PUT /dashboard/link/domain` `{handle}` | session; audited `app.domain.claim` | Claim, repair (same handle), or change the handle. `409 domain_in_use` (the message lists the deployments) when the handle changes while any deployment uses the address. Then issues the wildcard in a job; the answer shows `certificate.state = "issuing"` |
| `DELETE /dashboard/link/domain` | session, `s.destructive`; `app.domain.release` | Release (`204`), and remove the lineage. `409 domain_in_use` while `usedBy` is not empty. A detached handle is cleared here without asking the site |
| `POST /dashboard/link/domain/certificate` `{dryRun?, force?}` | `app.domain.certificate.start` | Issue or renew the wildcard now, as job `app-domain.certificate` (`202`). `dryRun` runs against the staging authority and saves nothing |
| `PUT /dashboard/link/domain/settings` `{autoAssign}` | `app.domain.settings` | Whether new deployments get the address by default |
| `GET /deploy/app-domain` | any account that reads deployments; a deploy key | `{enabled, zone, base, status, autoAssign}`, for a deployment page to place a name |

`GET` answers `enabled`, `linked`, `available` (`null` when unknown, with `reason` saying why: not linked, or the
site not asked or not reachable), `zone`, `reason`, `publicAddress`, `autoAssign`, `domain` (`handle`, `hostname`,
`wildcard`, `address`, `status`, `reason`, `updatedAt`), `certificate` (`name`, `state` one of `missing`,
`issuing`, `valid`, `expiring`, `failed`, `expiresAt`, `error`, `jobId`), `usedBy` (every configured deployment
domain under the handle, across projects and environments), and `detached` (deployment domains under the zone
that the handle does not cover), and `offer` (`handle`, `hostname`, `error`: the address chosen on the site that
the server claims by itself; `null` with no offer or while the server holds a domain; `error` is the last
automatic claim's failure, `null` while it has not been tried or is under way). A field with nothing to say is
`null`; the lists are never `null`.

Site refusals keep the protocol's code (`handle_taken`, `handle_reserved`, `handle_quarantined`,
`address_mismatch`, `limit_reached`, `rate_limited`, `dns_failed`, `domains_unavailable`, …) and a sentence for
the operator (`accountlink.DomainError`); the site refusing this server's signature is a `502`, never a `401`.
Ours: `app_domains_disabled` (403), `not_linked`, `link_idle`, `no_public_address`, `no_domain`,
`domain_in_use` (409), `invalid_handle` (400).

## The wildcard certificate

certbot issues `jd-app-domain-<handle>` for `*.<handle>.<zone>` and `<handle>.<zone>` with
`--manual --preferred-challenges dns`, `--manual-auth-hook "/usr/local/bin/jd app-domain challenge add
--credentials <data dir>/app-domain-hook.json"` and the matching `--manual-cleanup-hook`, through the certbot
gate and the Certificates page's runtime (`proxysvc.IssueAppDomainCertificate`). A dry run is `--dry-run`
against staging, as the page's test runs are; a forced renewal is `--force-renewal`; otherwise
`--keep-until-expiring`. It refuses before certbot runs when `jd` is missing on certbot's side. After an
issuance the Docker Caddy ingress's copies are refreshed and nginx reloaded (`ServeAppDomainRenewal`).
`certificate.state` comes from the lineage on disk (`AppDomainCertificate`). A test certificate from a
staging authority is `valid` while `JD_ACME_DIRECTORY` names a staging directory (it is what that directory
issues) and `failed` under any other, where browsers refuse it; a deployment page's `covered` for an app name
reads the same state (`WildcardCovers`), so the card and `GET /deploy/hostname` never disagree, and the release
issues a failed wildcard again. A release or a handle change
removes the old lineage (`certbot delete`), so the timer stops renewing a name the site will not prove.

**Renewal is certbot's own timer**, which keeps the hooks in the lineage's renewal configuration. It works for
as long as the server is linked and holds the handle. `renewalProblems` reports an app domain lineage whose
authenticator is not `manual`, whose hooks are not `jd`'s, or whose `jd` is missing. When the dashboard is
downgraded below 0.9.1 the timer's renewals fail: `jd` no longer has the subcommand. That is not supported.

**The hook** (`jd app-domain challenge add|remove`) reads `CERTBOT_VALIDATION` and `CERTBOT_DOMAIN` and posts
them to `POST /api/v1/app-domain/hook/challenge` on the backend's loopback listener, with the token from
`<data dir>/app-domain-hook.json`. There was no local channel between `jd` and the backend that works with
nobody signed in (`jd` speaks the API with a person's key), so the backend writes this one: `{url, token}`,
0600, written in one step; only the token's SHA-256 is stored, and the file is rewritten when it is missing,
unreadable or names another listener. Whoever can read it is root on the server already. The route sits
behind the network allowlist and the per-address limiter, answers only a caller on loopback that no proxy
forwarded (as the tailnet gate does), checks the token in constant time, accepts only a 43-character
base64url value, and publishes or withdraws a value only for the held handle's own name. `JD_ALLOWED_CIDRS`
must keep `127.0.0.1/32` (the default does) for the hook to be let in. Each call is audited as
`app.domain.challenge.add|remove` by `certbot`.

After the add the backend asks the zone's **authoritative** nameservers (`zoneNameservers` then `Query` to
each) until every one answers the value, for up to 120 seconds, so a slow update fails as "the nameservers ...
did not answer" rather than as Let's Encrypt's vague error.

## Deployments

- **Hostname suggestion** (`GET /deploy/hostname?name=`): the operator's own wildcard certificate first (the
  app domain's lineage is not one); then `method: "app-domain"`, `<slug>.<handle>.<zone>`, when the domain is
  active and `autoAssign` is on, `covered` when the wildcard is valid and `certificateMethod: "app-domain"`
  otherwise; then sslip.io. `&method=app-domain` always answers the app name (empty `hostname` and a reason in
  `detail` when there is none to give), and `&tunnel=1` is never offered one. The slug is one DNS label
  (`hostnameSlug`); a name another project already uses gets the deterministic suffix. A typed
  `?hostname=` under the zone answers `appDomain: "held"` or `"detached"`.
- **Certificate step**: names under the held handle are the wildcard's (`appdomain.EnsureCertificate`: reused
  when valid, issued through the site when missing or expiring, one at a time); the rest go to the
  Certificates feature as before. Activation then serves each set with its own certificate
  (`DeploymentRoute.Certificates`: a server of their own on nginx, a site block with a copy on Caddy).
- **Names under a handle this server no longer holds** (released, suspended, detached, renamed, never ours):
  every step reads the release without them (`decodeReleaseSnapshot`), and so does the build step's
  certificate prewarm. When other public names remain the certificate step passes as a **warning** that names
  them (`evidence.detached`, `evidence.warning`); when it was the only one it fails with
  `app_domain_detached`, saying to claim the address again or pick another. A redirect to such a name goes with
  it. The Domains page reads `GET /deploy/app-domain` (or the Settings view's `detached`) to mark them.

## Lifecycle

| Situation | What happens | Proved by |
| --- | --- | --- |
| Deployments that exist before a handle is claimed | Nothing changes; their names resolve and certify as before | `TestTheCertificateStepGivesAppNamesTheWildcardAndTheRestTheirOwn` (an sslip name goes to the Certificates feature) |
| Moving an existing app over | Adding `<slug>.<handle>.<zone>` beside its names: the wildcard serves the app name, the route's own certificate the rest | `TestTheCertificateStepGivesAppNamesTheWildcardAndTheRestTheirOwn`, `TestAnNginxRouteServesItsAppNameWithTheWildcardInAServerOfItsOwn`, `TestACaddyRouteServesItsAppNameWithACopyOfTheWildcard` |
| A link approved with an address chosen on the site | The server claims it by itself and issues the wildcard; a failure shows on the card and waits an hour | `TestAnApprovedLinkWithAnOfferClaimsItAndStartsTheWildcard`, `TestAFailedAutomaticClaimIsKeptAndNotRetriedWithinTheHour`, `TestNoOfferMeansNoClaim`, `TestOffersAreIgnoredWhenAppDomainsAreOff`, `TestAServerHoldingADomainIgnoresAnOffer` |
| New project with `autoAssign` on | The app name is the default suggestion; sslip.io or a typed domain stay possible | `TestTheHostnameSuggestionOffersTheAppDomain` |
| Redeploy, release or rollback of an app using it | The valid wildcard is reused; nothing is asked of the site | `TestADeploymentReusesAValidWildcardAndIssuesAMissingOne` |
| Server not linked | The card says why; suggestions stay sslip.io | `TestTheViewSaysWhyNothingIsOffered`, `TestAClaimIsRefusedWithoutWhatItNeeds` |
| Unlinking a server whose address is in use | `GET /dashboard/link/domain`'s `usedBy` is what the unlink confirmation lists; the link's end detaches the handle | `TestUnlinkingDetachesTheHandle` |
| Released, suspended or detached while in use | The card shows it; the next release leaves the name out (warning) or fails (`app_domain_detached`) | `TestTheStateFollowsTheSite`, `TestEveryStepReadsAReleaseWithoutItsDetachedNames`, `TestTheCertificateStepNamesTheDetachedNamesItLeftOut` |
| Changing the handle | Refused while any deployment uses it; the error lists them | `TestAHandleInUseCannotBeChangedOrReleased` |
| Server IP changes | Updated within 15 minutes | `TestTheAddressFollowsTheServer` |
| Site unreachable | Existing apps keep working; claiming, releasing and issuing fail with a message; renewal retries on certbot's schedule | `TestTheStateFollowsTheSite` (state unchanged); the rest is a live run |
| Reinstall or restore onto a new server | A new link is a new server; the same account can claim the handle again | the site's quarantine rule; a live run |
| Downgraded below 0.9.1 | Not supported: the renewal hook needs `jd app-domain` | — |

## Verification

```bash
scripts/test-changed.sh patch/0.9.1
cd backend && go test ./internal/appdomain ./internal/accountlink -run 'Domain|Handle' ./cmd/jd
cd backend && go test ./internal/proxysvc -run 'AppDomain|AppName|TXTWait'
```

No test calls the real site, Cloudflare or Let's Encrypt: the account link runs against its fake site, the
service against a fake link and certificate store, issuance against a fake certbot that runs the real hook
argv, and the TXT wait against stubbed lookups. An end-to-end run against the real zone is done by hand.
