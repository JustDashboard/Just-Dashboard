# Smart stacks

A smart stack is several applications deployed as one Compose template and then set up and connected
to each other by the dashboard, so the person does not open six web interfaces to copy API keys
between them. The first is the media server stack (Jellyfin, Seerr, Radarr, Sonarr, Prowlarr,
qBittorrent, Bazarr, Recyclarr). It is offered on the **Stacks** tab of `/deploy/new` and reported
on a project's **Stack** page (`/deploy/[id]/stack`).

## What is new and what is not

Nothing about deploying is new. A stack's preset is a reviewed built-in Compose template
(`blueprint/builtin/media-complete.json`, `media-essential.json`) marked `"stack": "media"`, and it
deploys as `kind: compose, mode: blueprint` through the draft, commit and run path every Compose
template uses ([Multi-service templates](implementation.md)). Building, starting, networks, volumes,
the domain, logs, backups, alerts and rollback are the deployment's.

What is new is what happens after a release goes live: `internal/stacks` runs a reviewed graph of
configuration steps against the live release's containers and records each step's outcome.

| Piece | Where |
|---|---|
| Step, result, action and environment contracts | `stacks/stacks.go` |
| Manifest (presets, services, options, credentials, steps) and its validation | `stacks/manifest.go` |
| The registry of shipped stacks, held to their templates by a test | `stacks/catalog/` |
| The media stack: constants, presets and the whole step graph | `stacks/media/media.go` |
| The application adapters | `stacks/media/<application>.go` |
| The runner (order, concurrency, retries, actions, redaction) | `stacks/runner.go` |
| When a pass runs; the environment it runs in | `stacks/service.go`, `stacks/dockerenv.go` |
| Tables | `store/schema_deploy_stacks.go`, `stacks/store.go` |
| Routes and the state the page reads | `api/handlers_deploy_stacks.go` |
| The media-source picker: Prowlarr's catalogue, an add, a removal | `stacks/media/prowlarr_sources.go`, `api/handlers_deploy_stack_sources.go` |
| Test helpers (fake over httptest; DockerEnv over a test's own containers) | `stacks/stackstest/` |

## The templates

- **Complete** (eight services, the Compose template cap) and **Essential** (Jellyfin and Seerr).
  Images are pinned to exact upstream releases (linuxserver.io builds for the managers, the
  download client and Bazarr; Jellyfin's and Seerr's own images; Recyclarr's from Docker Hub).
- **Jellyfin is the public service**: the deployment's domain routes to it, as any template's
  public service. Every other web interface uses the `loopback` port exposure, which only a stack
  template may declare: published on `127.0.0.1` at a preference derived from the template, service
  and port (`blueprint.LoopbackHostPort`), moved by the Compose port recovery every published port
  already has (`dockerx/compose_ports.go`) when taken, so two stacks on one server do not collide.
  Nothing but Caddy binds a routable address (invariant 1). A service with a loopback port must
  declare `egress`: Docker publishes no port of a container that is only on an internal network.
  Each of them is reached from elsewhere at an address of its own beside the domain (below): a
  domain of the deployment that names the service (`domains[].service`, a
  [service domain](implementation.md#service-domains)), which the route sends to that loopback
  publication with every rule of the route.
- **One shared volume, `media`, at `/data`**: downloads and the library on one filesystem so a
  finished download is renamed into the library, not copied. Writers (qBittorrent, Radarr, Sonarr,
  Bazarr) mount it read-write; Jellyfin mounts it **read-only** in Complete. The validator allows a
  read-only mount of a data volume only when another service of the stack writes it.
- The linuxserver.io images run their application as `PUID`/`PGID` 1000; the folders the stack makes
  on the volume are given to that account and writes are checked as it.
- Every private service declares `egress`: the managers, Seerr and Recyclarr reach metadata services
  and the person's sources.
- **API keys** for Radarr, Sonarr and Prowlarr are template secrets (`RADARR_API_KEY`, …), generated
  at commit and handed to the applications through their `<APP>__AUTH__APIKEY` environment, and to
  Recyclarr. No key is ever copied by hand or by the person.
- **Options** are template inputs that become deployment variables: `MEDIA_QUALITY` (720p, 1080p,
  2160p), `MEDIA_SUBTITLE_LANGUAGES`, `MEDIA_REQUEST_APPROVAL` (`admin` or `auto`). Changing one is
  changing a variable and redeploying; the pass after the new release applies it.
- **The domain is required** (template version 1.1.0), and an optional secret input,
  `login-password` (`MEDIA_LOGIN_PASSWORD`, "Password for every app"), seeds the one login
  (below). Blank means the dashboard generates it.
- An added template may not claim `stack` (`validateUserRules`): the adapters trust the services to
  be the images they were written and tested against.

## Addresses

Every web interface of the stack has an HTTPS address of its own, made from the domain and the
service's address label (`Service.Address` in the template, a DNS label of at most 20 characters,
allowed only on a stack's service with a loopback port and unique in the template):

| Service | Label | `media.example.com` gives |
|---|---|---|
| Jellyfin | (the primary) | `media.example.com` |
| Seerr | `requests` | `media-requests.example.com` |
| Radarr | `radarr` | `media-radarr.example.com` |
| Sonarr | `sonarr` | `media-sonarr.example.com` |
| Prowlarr | `prowlarr` | `media-prowlarr.example.com` |
| qBittorrent | `downloads` | `media-downloads.example.com` |
| Bazarr | `subtitles` | `media-subtitles.example.com` |

- `blueprint.ServiceHostname(domain, label)`: a domain of three labels or more gets a sibling,
  `<first label>-<label>.<rest>`; an apex such as `example.com` gets `<label>.example.com`. A first
  label too long for the 63 a DNS label holds is cut from its end, never the service's label.
  Siblings, because a wildcard certificate for `*.example.com` covers them, the just-dashboard.com
  app-domain wildcard covers `<app>-requests.<handle>.<zone>` as another app name, and sslip.io
  resolves them; a sub-label (`requests.media.example.com`) is covered by none of those.
- Rendering a stack template with a domain yields, besides the primary, one plan domain per
  labelled service (`Plan.ServiceDomains` → `PlannedDomain{Hostname, HTTPS, Service}`). They are
  ordinary domains of the plan: the configure step and the Domains settings show them, and the
  person may change or remove them. Each routes to its service's loopback port: a domain naming
  another Compose service (`PlannedDomain.Service`, [deployments](implementation.md)).
- **Give every app its address.** A stack made from the 1.0.0 templates has only Jellyfin's domain,
  though those templates already publish the other interfaces on loopback. The state's
  `addresses` names the primary domain and, from the current configuration's domains and the
  shipped template's labels, each service that has no domain yet with the hostname it would get.
  `POST /deploy/{id}/stack/addresses` adds them through the configuration save the Domains settings
  make (`saveDeploymentConfiguration`: the same checks, a deploy key's guard, the
  `deploy.configuration.update` audit), each served over HTTPS and through the tunnel as the primary
  is, then starts the release at that revision as "Deploy changes" does (`deploy.run.request`), and
  answers `202 {runId, domains}`. With no primary domain the body's `domain` becomes the primary
  (`422 stack_no_domain` without one). With nothing missing but an address saved that the live
  release does not serve (`addresses.unpublished`: the release that was to publish it failed, has
  not run or is still running), it starts the release again without adding a domain
  (`202 {runId, domains: []}`); with neither it answers `409 stack_addresses_present`.

## When a pass runs

`stacks.Service` looks at every stack every ten seconds and at once after a commit or a Retry
(`Kick`). A pass runs when the environment's live release is a release of one of the stack's preset
templates and either the release is not the one last configured, the manifest version changed, or
a pass was requested. At most two stacks are configured at once, one pass per stack. A pass for a
release that stopped being live is cancelled, and so is one for a deployment that was removed; one a
restart cut short is queued again (`ResetInterrupted`). Changing the source to the other preset's template moves the stack to that
preset. A deployment whose source is no longer a preset is left alone, and Retry on it is refused
(`stack_not_preset`) rather than recorded for a pass that would never run.

The stack is registered in the commit request (`registerStack`, after the blueprint schedules), keyed
by project; its rows cascade with the project's.

## The runner

- A step runs when every step in its `After` is done, or skipped because its service is not in the
  preset (such steps are left out of the page). A step after one that failed, needs the person, or
  was itself held back is skipped with a sentence naming which.
- Three steps at once. Each attempt is bounded by the step's timeout; up to its attempts with
  5, 10, 20, 40, 60 s between them. `stacks.Permanent` stops retrying; `stacks.NeedsAction` records
  `action_required` with an action the page lists. A panic in an adapter fails that step only.
- Every secret the environment handed out (variables, credentials) is masked out of each recorded
  message and error before it is written, so an application echoing a request back cannot put a key
  in the step's record.
- What a step says while it runs (`Env.Logf`, such as Recyclarr's sync report) goes to that step's
  record (`log_json`, the step's `log` in the state), masked the same way and before it is cut to
  length: the newest 50 lines of the step's last pass, 500 bytes each. Each step gets its own view of
  the pass's environment for this (`stepEnv`), since three run at once.
- Steps converge: each reads what is there, changes only what differs from what the stack wants,
  reads it back, and finds its own entries by a fixed name. Running a pass twice makes no second
  change, so Retry and every redeploy simply run the whole graph again.

## Reaching the containers

The backend runs on the host's network and reaches each container by its address on
`<compose project>_internal`, read from Docker on every call (`DockerEnv.BaseURL`) — the way the
data-rollback copy is reached. Addresses written into one application for another are Compose DNS
names (`http://radarr:7878`). The HTTP client takes no proxy from the environment and follows no
redirect to another host. Commands inside a container (`ExecCapture`) are closed argv built by the
adapters, never a shell string from a request.

## One login

Every application signs in as `admin` with one password, the credential `login` (`stacks/login.go`,
`media/login.go`): Jellyfin's administrator (which is also how Seerr's owner signs in),
qBittorrent, Radarr, Sonarr, Prowlarr and Bazarr. It is sealed in `deploy_stack_credentials` with
the dashboard's sealer. Tokens an application hands back (Jellyfin's and Seerr's API keys,
Bazarr's) are kept there too and are never revealed.

- **Where it comes from**, the first time a step asks (`stackLogin`): a stack made before the one
  login adopts Jellyfin's administrator password (`jellyfin-admin`), so Jellyfin and Seerr sessions
  and saved passwords keep working; a new stack takes `MEDIA_LOGIN_PASSWORD` when the person chose
  one the dashboard's password rule accepts (one it refuses is not used, and the step says so);
  otherwise 24 letters and digits are generated. From then on only the change route sets it.
- **Migration.** The first pass of manifest 1.1.0 brings every other application to it: Radarr,
  Sonarr, Prowlarr and Bazarr with their API keys; qBittorrent by matching its settings file
  offline against the login, the previous login and its own old password, then signing in once
  with the one it holds, so no refused sign-in counts towards its lock-out. Each application's old
  credential (`qbittorrent-admin`, `radarr-admin`, …) is removed once that application answers to
  the login. The state lists only `login`.
- **Change.** `PUT /deploy/{id}/stack/login {"password"}` holds the password to the dashboard's own
  rule (`422 password_weak {message}`), at most 128 characters with no control character; refuses
  during a pass (`409 stack_busy`), since that pass has read the old one; keeps the old one as
  `login-previous`; asks for a pass and answers `202 {"status": "configuring"}` (`waiting` before a
  live release). Jellyfin's administrator password is set with the stack's API key, as an
  administrator changes anyone's, which signs its other devices out; qBittorrent is signed in to
  with the password its file holds. `login.verify`, after every step that signs in, removes
  `login-previous` once all of them took the new one; a pass cut short leaves it for the next.
  Changed twice before a pass reaches qBittorrent, qBittorrent can be left on the first; it is
  then the action `qbittorrent-password`.
- **Jellyfin is not signed in to on every pass.** A wrong password counts towards Jellyfin locking
  the account, so `jellyfin-login-applied` records (as a digest) which login its administrator was
  last confirmed with; a sign-in is tried only when that differs, and never for a Jellyfin someone
  else set up before the stack asked the person to give its account the password
  (`jellyfin-owner-asked`).
- **Reveal.** `GET /deploy/{id}/stack/credentials/login/reveal`, on the terms a variable reveal has
  (an administrator, in a session, not through Just Hub, audited without the value), as is the
  change (`deploy.stack.login.update`). Nothing in a response, a step record or a log carries a
  value otherwise.

## Routes

| Route | Who | What |
|---|---|---|
| `GET /deploy/stacks` | any signed-in account, any key | the catalogue: presets with their template, memory and disk estimate, limitations, services with their address label (`address`) and Jellyfin as `primary` |
| `GET /deploy/{id}/stack` | whoever reads the project | state, readiness, services with container state, each one's public `url` once the live release serves it (Jellyfin's is the primary domain) and a private one's loopback `address`, steps, actions, `login {username, stored}`, `credentials` (the one login), `addresses {primary, missing, unpublished}`; `404 stack_not_found` otherwise |
| `POST /deploy/{id}/stack/configure` | the deploy control capability | run a pass now; `409 stack_waiting` before a live release, `409 stack_busy` during a pass, `409 stack_not_preset` when the live release is not one of the stack's presets (no pass would ever run); audited |
| `GET /deploy/{id}/stack/credentials/{name}/reveal` | administrator, session, not Hub | the login's password (`name` is `login`); audited |
| `PUT /deploy/{id}/stack/login` | administrator, session, not Hub, not the assistant | change the login; a pass follows; audited without the value |
| `POST /deploy/{id}/stack/addresses` | a configuration save's terms (an administrator in a session, or a deploy key) | give every app without one its address and deploy, or deploy again for addresses saved but not live; audited |
| `GET /deploy/{id}/stack/sources` | whoever reads the project | the sources Prowlarr has, whoever added them |
| `GET /deploy/{id}/stack/sources/catalogue` | whoever reads the project | the torrent sources Prowlarr can add, each with `needsInput`, and `fetchedAt` |
| `GET /deploy/{id}/stack/sources/catalogue/{definition}` | whoever reads the project | one of them with its form |
| `POST /deploy/{id}/stack/sources` | the deploy control capability | test, save and share a source; `201`, or `422 source_test_failed`; audited as `deploy.stack.source.add` with the definition's name only |
| `DELETE /deploy/{id}/stack/sources/{sourceId}` | destructive, ordinary confirmation | remove one of Prowlarr's sources; `204`; audited as `deploy.stack.source.remove` |

Every source route answers `404 stack_not_found`, `409 stack_waiting` (no live release), `409
stack_not_preset`, `409 stack_no_sources` (a preset without Prowlarr: Essential) and `502
stack_app_unreachable` (Prowlarr did not answer, with what it said) before anything else. Deploy keys
and MCP keys reach all five: an add's values are write-only, as a variable's are, and a removal spends
the destructive budget as a rollback does. A session opened through Just Hub reaches them too: an add
sends a value in, as a variable's save does, and none of them shows one.

Blueprint summaries and details carry `stack`; the Template tab leaves those templates out.

## What the page may claim

`status` is `waiting` (no live release), `configuring`, `ready`, `action_required` (done but for what
only the person can do) or `failed`. `readiness` separates installed, healthy (every container
running and healthy), connected (every link step done), able to play (Jellyfin set up with its
libraries) and able to acquire (Complete, connected, the download link verified and at least one
source in Prowlarr). A Complete stack with no source is `action_required: Add a media source` — never
"set up" — and still plays what is in its library.

## Media sources from the dashboard

A Complete stack ends with one thing only the person may choose: a source they are allowed to use.
The source routes let them pick and add it on the stack's Sources page; everything after the choice
is the dashboard's.

- **The catalogue** is Prowlarr's own indexer schema (its `indexer/schema` endpoint) (6.3 MB and 662 definitions at
  Prowlarr 3.1.0), read with a 32 MiB bound (a longer answer is an error, never a list cut short) and
  kept ten minutes per project and live release (`media.SourceCache`, about 6 MB each in memory, for
  at most four projects at once; requests at once share one read). Only torrent definitions are listed, since the stack downloads with qBittorrent, sorted by
  name, and what is kept of each is trimmed of its capabilities and presets, which an add does not
  send. Prowlarr downloads the sites' definitions from its own server when it starts; a Prowlarr with
  no way out has only those built into it (57 torrent ones).
- **Names.** A definition is named by Prowlarr's definition name, except for the presets that share
  one, named `<definition>:<preset>` (`Torznab:Generic Torznab`, `Torrent RSS Feed:Torrent RSS Feed`).
- **`needsInput`.** The schema marks nothing as required, so it is read from what each shown,
  non-advanced setting is: an empty account, password, key, cookie, PIN or address, a captcha, or a
  select with no choices needs input; an empty number or checkbox means Prowlarr's own default, an
  address chosen from the definition's (`getUrls`) has its first, and a setting whose label or help
  says it is optional ("optional", "leave empty", "if your site…") does not.
- **The form** leaves out hidden settings, information lines and captchas (only Prowlarr's own page
  can show one). A setting is `text`, `password` (Prowlarr's password type, any setting Prowlarr
  keeps secret, such as Torznab's API key, and any whose name says it is one: most sites' keys,
  cookies and passkeys are plain textboxes in the schema), `number`, `checkbox` or `select` (its own
  choices, or the definition's addresses). Each value is Prowlarr's default.
- **An add** checks the values against the form (`422 source_invalid` with the setting in `field`),
  trims the spaces a paste brings from everything but a password, and refuses a name Prowlarr already
  has (`409 source_exists`, whatever its case). Then Prowlarr's own test (`POST indexer/test`); the
  save in Prowlarr's lowest app profile (the "Standard" it makes on first start), switched on, with
  `forceSave` because the test has just passed and a second sign-in to a site within a second can
  trip its limit; `ApplicationIndexerSync`, waited for up to a minute (`synced`); and a pass
  requested, so `prowlarr.sources` turns done. A failed test is `422 source_test_failed` with
  Prowlarr's reason, every text the person typed masked out of it, as typed and as a URL carries it.
- **A removal** is of any of Prowlarr's sources, by Prowlarr's id, the person's own included: it is
  what the Sources page lists. Prowlarr removes it from Radarr and Sonarr itself (full sync), and a
  pass is requested.
- **What it never does.** It picks, suggests or ships no source, and asks no site anything but
  through Prowlarr's test of the one the person chose. What the person types goes to Prowlarr in that
  request and nowhere else: not into the dashboard's store, the audit (the definition's name only; a
  removal's id and name), a step's record or a log. It changes no other Prowlarr setting.
- **The assistant** may add a source that needs no password, key or cookie. One with any of those
  is `403 source_needs_person`: what the assistant sends has passed through the model's provider,
  which is kept from every secret, so the person types it into the form, as a terminal's password is.
  For the same reason the assistant is not offered `PUT /deploy/{id}/stack/login`
  (`agentExcludedRoutes`): the person changes the one login on the Stack page.

## The Stacks tab and the Stack page

The frontend is `components/deploy/new-project/source-stack.tsx` (the tab) and
`components/deploy/stack/` (the project's pages), its words and reads in the unit-tested
`stack-model.ts` and `sources-model.ts`.

- **The Stacks tab.** The domain input is filled with the server's hostname suggestion and is
  required. Under it is the address each app will get, made in the browser by `serviceHostname`, a
  copy of `blueprint.ServiceHostname` with the catalogue's `address` labels and `primary`. The two
  must agree; `stack-model.test.js` holds the TS copy to the rule. The optional secret input
  `login-password` is a password field held in memory only (`useMemoryState`, never the tab's
  storage or `blueprintInputs`). A typed one is held to the dashboard's own rule (`passwordChecks`)
  and handed to Configure as an environment row named by the input's variable
  (`MEDIA_LOGIN_PASSWORD`), so it is sent with the draft's other typed variables and nowhere else.
- **The Stack page** (`/deploy/[id]/stack`) is a launcher. It opens with the lead and the five facts,
  then the one next step when something is left, then the apps and the login.
  - Next steps come in this order: a blocking action, then "Give every app its address" when
    `addresses.missing` or `addresses.unpublished` is not empty, then the optional actions, then
    each failed step with its error. The first one's command wears the brand.
  - The `media-source` action links to the source picker.
  - "Give every app its address" names the apps and their hostnames, confirms in a dialog and posts
    `POST …/stack/addresses`. Without a primary domain the dialog asks for one, prefilled with the
    hostname suggestion, and makes the names from the catalogue's labels. It then links to the run
    it started.
  - When every address is saved but the live release does not serve some of them
    (`addresses.unpublished`: the release that was to publish them failed), the same place says so
    and offers **Deploy again**, which posts the same route; with nothing missing, the server
    deploys the saved configuration again. While a run of the deployment is under way it links
    that run instead, since the run publishes them when it goes live.
  - Each app is one row: what it is for, its address (`services[].url`), its state and **Open** in
    a new tab. A worker says it runs in the background. An app with no address says so, with its
    loopback address only as the line's tooltip; there is no tunnel or SSH anywhere.
  - Beside the apps stands the one login (`login`, revealed through `…/credentials/login/reveal`),
    with Show, Copy and **Change password**. That dialog checks the new password against the same
    rule and sends `PUT …/stack/login`; a `password_weak` refusal is said in the dialog.
- **Media sources** (`/deploy/[id]/stack/sources`) is a page, not a sheet.
  - It lists the stack's sources, each with Remove behind its menu, confirmed.
  - Under them is Prowlarr's catalogue, searched and filtered Public or Private (semi-private
    counts as private). The list is cut to its first 60, with a line saying so.
  - A source opens on the page's own address (`?definition=`): its fields as a form, then **Add**,
    then what came of it in words. A password field is masked, the advanced fields sit behind More
    settings, and a `source_test_failed` refusal is shown with Prowlarr's reason.
  - From `xl` the catalogue stands beside the form as a rail; on a phone the page is one pane.
  - Values typed here stay in the form: never in the address or the tab's storage.
- **Domain rows.** A planned domain with a `service` says the app it routes to ("→ Seerr"), on the
  configure step's extra hostnames and on Domains settings, with the title from the stack catalogue.
  Every edit keeps `service` and `port` as they are.

## What the manager, subtitle and quality steps make

What these steps own in each application, by the name they find it again with. Anything under
another name is the person's and is not read past its name.

| Application | The stack's own | Left alone |
|---|---|---|
| Radarr, Sonarr, Prowlarr | the sign-in: a form on every address, user `admin`, the stack's login; the application URL (`applicationUrl`) is its public address (`config/host`; the whole resource goes back with those fields changed) | every other host setting |
| Radarr, Sonarr | the root folder `/data/media/movies` or `/data/media/tv` | other root folders |
| Radarr, Sonarr | the download client **qBittorrent (Just Dashboard)**: host `qbittorrent`, port, user, password, category `movies` or `tv`, enabled | other clients; every other setting of the stack's own client (priority, removal, tags) |
| Prowlarr | the applications **Radarr (Just Dashboard)** and **Sonarr (Just Dashboard)**: URLs, key, full sync | other applications; the sync categories, which come from Prowlarr's own schema |
| Bazarr | its sign-in; Radarr and Sonarr switched on with host, port and key; the languages profile **Just Dashboard**; the subtitle sources that need no account and are not for one language, turned on once: `embeddedsubtitles`, `gestdown`, `tvsubtitles`, `yifysubtitles` | other profiles and enabled languages (saving either replaces the list, so the person's go back with the stack's); a default profile the person chose; every other source, and the settings of any source; one of the four the person turned off stays off (the step's `turnedOn` output) |
| Recyclarr | `/config/recyclarr.yml`, written again on every pass | files under `/config/configs/` |

- A new download client or application starts from the application's own schema, so a setting the
  stack does not name keeps the application's default. The application's own test runs before every
  save. A secret is never read back (the applications answer `********`), so the only way to know a
  stored one is still right is that same test, run on the entry as stored.
- An account whose password was changed in the application is given the stack's password back: it is
  the one the dashboard reveals for that application.
- **Quality profiles.** Recyclarr creates the TRaSH Guides' profile for the chosen quality under a
  name that carries the stack's mark, because Recyclarr 8 takes over any profile or custom format
  that already has the name it is about to use and has no setting to stop it. A person's own profile
  never has the marked name; a person's own custom format that has exactly a guide format's name
  would be taken over.

  | `MEDIA_QUALITY` | Radarr | Sonarr |
  |---|---|---|
  | `1080p` | HD Bluray + WEB (Just Dashboard) | WEB-1080p (Just Dashboard) |
  | `2160p` | UHD Bluray + WEB (Just Dashboard) | WEB-2160p (Just Dashboard) |
  | `720p` | the built-in HD-720p | the built-in HD-720p |

  The guides have no 720p profile. For 720p Recyclarr keeps only the quality sizes and the
  applications' own HD-720p profile is used as it is; the step says so. Changing the quality leaves
  the earlier profile and its custom formats in place (`delete_old_custom_formats` is off, and a
  sync deletes no profile). `reset_unmatched_scores` is off, so a score the person gives a custom
  format of their own in the stack's profile stays. The API keys are not in the file: Recyclarr
  reads them from its environment (`!env_var`).
- `DockerEnv.Exec` gives a command what is left of its step's attempt. The sync is still run for
  Radarr and Sonarr separately, and an attempt first waits for any sync already running in the
  container (the image's daily one, or an earlier attempt cut short) rather than starting a second.
- `downloads.verify` has Radarr and Sonarr run their health checks now (the `CheckHealth` command)
  before reading them, since `GET health` otherwise answers from the last scheduled run. It fails on
  an error about the download client or its folders, names anything else, and leaves the checks
  about indexers to `prowlarr.sources`.
- `prowlarr.sources` counts the sources that are switched on. None is the blocking action
  `media-source`; the step never adds one (the person does, from the Sources page).

## What Jellyfin, qBittorrent and Seerr steps make

- **Set up for their public addresses** (`Env.PublicURL`, from the live release's domains: an
  HTTPS one when a service has one, the primary domain for Jellyfin):
  - `seerr.address`: Seerr's application URL is its address; "Enable proxy support"
    (`/settings/network` `trustProxy`) is on, which Seerr reads when it starts, so it applies from
    its next start; its Jellyfin "external URL" (`externalHostname`) is Jellyfin's address, so
    "Play on Jellyfin" opens the public one.
  - `qbittorrent.address`: reverse proxy support on, with the subnets of the networks its
    container is on as trusted proxies (`Env.NetworkSubnets`), so the ban counts the visitor's
    address and not the proxy's; its address added to the Host header list when that list is not
    `*` (the image's). Entries the person added stay.
  - `jellyfin.proxy`: the same subnets become Jellyfin's known proxies, so it no longer counts
    every visitor as local. Jellyfin 12.4 saves them at once but reads them when it starts, so they
    apply from its next start (the next deploy); the stack does not restart it, and a second pass
    changes nothing, so there is no restart loop. The published server URL is left as it is: it
    would send clients on the server's own network out through the internet.
- **Folders** (`storage.folders`): the four library and download folders and their two parents are
  made as root in qBittorrent's container (Jellyfin's in Essential) and given to 1000:1000 only when
  missing or root-owned, never recursively; every writer then proves it can create a file there as
  the account it runs as, and Jellyfin in Complete that it can read the library.
- **Jellyfin**: the first-run wizard is finished through its Startup API with `admin` and the stack's
  password, and an API key named "Just Dashboard" is made once and kept. Libraries are matched by
  path, since Jellyfin 12.4 makes "Movies2" rather than refusing a taken name. A Jellyfin whose
  wizard someone else finished, and which does not let the stack's account in, is the blocking action
  `jellyfin-owner`; nothing is reset.
- **qBittorrent**: the temporary password the linuxserver.io image prints on first start is read
  from its log once and replaced with the stack's; the stored one is then checked offline against
  the PBKDF2 value in its settings file before a single sign-in, because five refused sign-ins lock
  the address out for an hour. A password that is not the stack's is the action
  `qbittorrent-password`, tried no further. Categories `movies` and `tv` with their folders need
  automatic torrent management, which is turned on with the save path.
- **Seerr** is signed in to the stack's Jellyfin as `admin`, which makes that account Seerr's owner,
  and its Movies and Shows libraries are turned on by Jellyfin's ids. The stack's password is sent
  only to a Seerr the stack set up itself: it records that it began Seerr's setup before the request
  that does it, and a Seerr already set up by anyone else is the action `seerr-owner` with no
  sign-in, since which Jellyfin that Seerr would pass the password to cannot be read without it.
  Radarr and Sonarr are added as "Radarr (Just Dashboard)" and "Sonarr (Just Dashboard)" with the
  quality profile Recyclarr reported and the library folder; an update carries the rest of the entry
  as the person left it. The request policy changes only the auto-approve bits of the default
  permissions, for accounts made from then on.

## Verified end to end

On 26 Nov 2026 both presets were deployed through the API exactly as the Stacks tab does (draft,
source, detect, configuration, preflight, commit, run) on a dashboard running inside its own Docker
daemon (Docker-in-Docker, the backend in its network namespace), with nothing touching the host's
containers:

- Essential: the run went live and the pass finished `ready`, 6 of 6 steps on the first attempt;
  the revealed Jellyfin account signed in to Jellyfin 12.4.0 directly as an administrator.
- Complete: all eight containers healthy, every private interface published on loopback (each
  service is also on the `egress` network, which the validator requires of a loopback port: Docker
  publishes nothing from a network declared internal, checked on Docker 29.9), 21 of 22 steps done
  on the first attempt and `action_required: Add a media source`. Read directly from the
  applications afterwards: one "qBittorrent (Just Dashboard)" client on host `qbittorrent` with the
  `movies` category, the library root accessible, the synced profile, Prowlarr's two applications on
  full sync, and Radarr refusing an unauthenticated request.
- Retry on the configured Complete stack: a second pass changed nothing and made no second entry;
  a second press during it answered `stack_busy`.

Not exercised end to end: a request fetched through a real source, imported and played. That needs a
source the tester is allowed to use; none ships, by design.

On 27 Nov 2026 the one login and the address settings were run against the pinned images, each test
on containers, networks and volumes of its own:

- A stack set up as manifest 1.0.0 left it, every application on a password of its own
  (`TestLiveOneLoginMigratesAStackAndFollowsAChange`): the first pass made Jellyfin's password the
  login without changing Jellyfin, changed qBittorrent, Radarr, Sonarr, Prowlarr and Bazarr to it,
  removed the six old credentials, and qBittorrent logged no refused sign-in; the pass after changed
  nothing. A change of the login then reached all six (Jellyfin with its API key), Seerr's owner
  signed in with the new one, `login-previous` was gone after `login.verify`, and the pass after
  changed nothing.
- `TestLiveCompleteStepsConfigureOnceAndThenChangeNothing`: Seerr took its application URL, proxy
  support and Jellyfin's address; a wrong password sent as the proxy sends it, for a visitor's
  address, was counted by qBittorrent against that address (`IP: 203.0.113.9` in its log) and the
  stack still signed in; Jellyfin counted the forwarded visitor as outside its network once it had
  restarted, and not before.
- `TestLiveServarrBazarrRecyclarrSteps`: Radarr, Sonarr and Prowlarr report their addresses as the
  application URL; Bazarr reports the four sources it turned on as `Good` and holds none back.
  Whether each finds subtitles for a film was not tried: the test's library is empty.

On 27 Nov 2026 the whole of this version was run on the same Docker-in-Docker dashboard, its Caddy
ingress inside that daemon and every name plain HTTP (made-up names get no certificate there):

- **The upgrade a stack made before it takes.** A Complete stack was deployed from template 1.0.0
  by a dashboard of the base branch and left at `action_required`, then the dashboard was replaced
  by this one on the same store. The first pass made Jellyfin's password the one login, moved the
  other five applications to it, and `login.verify` finished; the revealed login signed in to
  Jellyfin through its public route. "Give every app its address" saved six service domains and
  started a release.
- **That release found its predecessor's Compose files gone**, taken by the workspace sweep of
  22 Nov (see [run workspaces](implementation.md)): it stopped the old release by its containers
  and went live. Each of the seven names then answered through the ingress (Jellyfin, Seerr's sign-in,
  the three managers' sign-in, qBittorrent's and Bazarr's pages), and every application took the
  login at its own address; a wrong password was refused.
- **The source picker:** a Generic Torznab source pointed at a Torznab server of the test's own on
  the stack's network. An unreachable address answered `source_test_failed` with Prowlarr's reason;
  the right one was tested, saved and synced in 15 s, appeared in Radarr and in Sonarr pointing at
  Prowlarr's new indexer, and the stack turned `ready` with all five readiness facts. Removing it
  and adding it again left one entry in each.
- **The Stacks tab, in the real UI:** an Essential stack with a password typed into "Password for
  every app" went through Configure and Review and was deployed; the login was that password, and
  Jellyfin and Seerr took it at their addresses. A change of the login on the Complete stack reached
  all seven applications.

## Not done yet

Said here so nobody builds on it by mistake:

- Adopting an existing Jellyfin or Seerr deployment into a stack. The Stacks tab offers fresh
  installs only.
- Seerr's proxy support and Jellyfin's known proxies apply from each one's next start, which is the
  next deploy; the stack does not restart either.
- Mounting an existing library folder from the host; media goes into the stack's own volume.
- Hardware transcoding and the Ultimate preset's optional modules. Media sources are added from the
  dashboard's source picker; a subtitle provider that needs an account is still set up in Bazarr.
- Jellyfin is reachable at the deployment's domain from the moment the release is live, and its
  wizard is finished by the stack within seconds of that, as the single Jellyfin template has always
  left it to the first visitor. A deployment without a domain has no such window.
- Stack-level backup and restore beyond the deployment's own volume coverage, and update actions of
  the stack's own: an update is a source change to a newer preset version and a redeploy, after
  which the pass checks every link again.

## Tests

- `go test ./internal/stacks/...` — runner semantics, manifest validation, the catalogue held to its
  templates, each adapter against recorded upstream answers (first run, a second run with no
  writes, upstream errors, the actions).
- `go test ./internal/blueprint/` — the templates render to their recorded digests; loopback ports
  only on loopback and only in a stack; address labels only on a stack's loopback service, unique;
  `ServiceHostname`; a service domain per label.
- `TestStackRoutesRegisterAWaitingStackAndRevealOnlyItsAccounts` (`api`) — commit registers, the
  page waits, Retry refuses in words, reveal is by name and audited, no secret in the state, one
  login listed, the catalogue's labels.
- `TestStackAddressesAreSavedAsDomainsAndDeployed`, `TestStackAddressesTakeADomainWhenThereIsNone`,
  `TestStackAddressesSavedButNotLiveAreDeployedAgain`,
  `TestStackLoginChangeKeepsThePreviousAndAsksForAPass` (`api`) — "Give every app its address" saves
  through the configuration save and starts the run, and deploys again for addresses saved but not
  live; the login's change, refusal, audit and reveal.
- `media/login_test.go`, `media/address_test.go` — a 1.0.0 stack migrating over fakes with no
  refused qBittorrent sign-in, a changed login reaching every app, the deploy-time seed, and each
  address step converging.
- Live (`JD_STACKS_LIVE=1`, as root): each adapter lane's test brings its pinned images up on a
  network of its own and runs its steps twice. See the header of each `*_live_test.go`.
  `TestLiveOneLoginMigratesAStackAndFollowsAChange` sets the seven applications up as manifest
  1.0.0 did, one password each, then runs this manifest's pass, a change of the login and a pass
  after each.
- `prowlarr_sources_test.go` (`stacks/media`) — the source picker against a stand-in for Prowlarr
  whose schema (`testdata/prowlarr-indexer-schema.json`) is eight entries of the pinned Prowlarr's,
  renamed so no real site is named: the trimming, `needsInput`, the form, the checks on what is
  typed, the add, the masking, the bounded read and the cache.
- `TestStackSourceRoutesGateAndAnswerInWords` (`api`) — every refusal in words, the capabilities, the
  catalogue read once, an add audited by its definition alone, a failed test without the typed key,
  the assistant kept from a key, a removal by id.
- `TestLiveSourcePickerAddsTestsSyncsAndRemoves` — the pinned Prowlarr, Radarr and Sonarr, connected
  by `prowlarr.apps`, and a Torznab server of the test's own (the test binary itself, in a container),
  on an internal network so no real site is reached. On 27 Nov 2026: the catalogue read in 11 s; a
  wrong key answered `source_test_failed` with the key and the typed address masked; the right one
  was tested, saved and synced in 11 s and appeared in Radarr and Sonarr as "Generic Torznab
  (Prowlarr)"; `prowlarr.sources` turned done; a removal took it out of both and the action came back.
