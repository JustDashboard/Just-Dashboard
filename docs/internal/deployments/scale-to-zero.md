# Scale to zero: sleep when idle

An environment whose plan says so (`runtime.sleep`) has its live release's container stopped once its
public route has served no request for the plan's idle minutes, and started again by the next request.
The route keeps answering throughout: a request that finds the application asleep is held by the proxy
while the dashboard starts the container and waits for its readiness checks, and is then sent on as it
came — method, path, query and body — so the visitor gets the page after a pause rather than an error.
One that outlasts what a visitor is held for gets a waking page that tries again by itself.

Stopped, not removed: the image, the volumes, the networks and the published port stay, and the
release's runtime record stays `live`. Sleep is its own record (`deploy_sleep_spans`), so everything
that reads a stopped container — the overview, the host alerts, the availability figure — can tell a
container asleep by design from one that is down.

Code: `deploy/sleep.go` (terms, the idle decision, sleeping and waking), `deploy/sleep_wake.go` (the
wake socket and its handler), `proxysvc/deployment_sleep.go` (both proxies' wake, the switch and the
socket's place), `api/handlers_deploy_sleep.go` (the routes, the audit and the lifecycle feed's
attribution), `store/schema_sleep.go`.

## The terms

`runtime.sleep {enabled, idleMinutes, wakeTimeoutSeconds}`, in the plan like every runtime field, so a
change goes live with the next release and the live release's snapshot holds the terms in force.

| Field | Bounds | Zero |
| --- | --- | --- |
| `enabled` | — | off; a plan starts off, because the first visitor after a quiet spell waits |
| `idleMinutes` | 5–1440 | 15 |
| `wakeTimeoutSeconds` | 5–600: how long a wake waits for the readiness checks | 60 |

A visitor is held for `min(wakeTimeoutSeconds, 30)` seconds (`holdSeconds`): past half a minute a page
reads as broken and a CDN or load balancer in front gives up too. `canonicalSleepPlan` stores off with no
terms as nothing, so a switch turned on and off is no change. The release comparison lists it as
`sleep when idle`.

`validateSleepPlan` refuses `enabled` (field `runtime.sleep.enabled`) for a plan where stopping the
container would break something a request cannot bring back:

- **A Compose stack** — several containers that start in an order of their own; sleep stops one container.
- **Host networking** — the application is reached at the host's own port, not through the route, so no
  request could wake it.
- **A tailnet-only environment** (`runtime.tailnet`) — it is reached through `tailscale serve`, not
  through the proxy's route, so no request could wake it.
- **No served domain** — only a request through the proxy's route wakes it.
- **A required readiness check that would fail a stopped application.** `docker_health` reads Docker's
  health verdict, which a started container is given only after its image's `HEALTHCHECK` interval (30 s
  unless the image says otherwise) and a stopped one has none at all, so every wake would outlast what a
  visitor is held for; `public_route` asks the public route, which while the application sleeps answers by
  waking it, so the wake would wait on itself. HTTP, TCP and command checks run against the started
  container as at a deploy.

Two refusals need more than the plan and are the API's (`handlers_deploy_sleep.go`): saving a
configuration with sleep on while an enabled schedule of the environment has a `container_command` or
`game_command` step — a command run inside a stopped container fails, and a schedule firing is no request
through the route — answers `422 invalid_plan` on `runtime.sleep.enabled`; creating or updating such a
schedule while the configuration has sleep on answers `409 sleep_conflict`. **Replicas** (another pull
request) are to be refused the same way: several containers behind one route are not one that sleeps.

Preflight says what it will do (`sleep_after_idle`, a pass: the idle minutes, the hold, that the
container is stopped and not removed) or that it cannot measure (`sleep_without_record`, blocked: no
proxy records the domain's requests, so it would sleep and never wake).

## The idle decision

The `Sleeper` (`NewSleeper`, started after the engine) passes every minute over each unarchived
environment with a live release whose snapshot sleeps. It counts from the latest of the release going
live, its last wake and its last request, reading the same held request record the Logs page, the
rollback watch and the traffic replay read (`RequestRecord.Window` on the environment's route). Every
request counts — a crawler's too; a scanner's probe wakes the application like any other request, so not
counting it would put it to sleep under one. Nothing is put to sleep while:

- the environment is a preview (it is reached at its own tailnet address, not through a route a request
  wakes);
- a run of the environment is queued or in progress;
- the previous release is kept warm for an instant rollback (`deploy_rollback_watches` open);
- the live runtime is not a single container, was stopped by hand (`state` `stopped`), or its container
  is not running;
- the wake is not listening for this host's proxy, the proxy cannot be asked, or the live route does not
  carry the wake (a release from before the plan slept);
- the request record cannot be read — never put to sleep blind.

The pass keeps its word on each environment (`SleepDecision`: why awake, when it sleeps if nothing
arrives, the last request), which `GET …/sleep` reads.

## Sleeping

Under the environment's lock, in this order: the idle decision is audited (`deploy.environment.idle`,
actor `system`, the idle minutes, the last request); the stretch is recorded (`deploy_sleep_spans`:
release, container, idle minutes, reason, last request, `slept_at`; a unique partial index allows one open
stretch per environment); the route's switch is written; the container is stopped (`RuntimeOwner.Stop`,
`remove=false`, the plan's signal and grace); `deploy.environment.sleep` is audited. In that order a
dashboard that stops half-way leaves a stretch the next pass or the next request repairs, never a switch
with no stretch behind it, which would refuse every visitor. A stop that fails clears the switch and closes
the stretch with the error.

## The route

The route of a release that sleeps carries the wake (`DeploymentRoute.Sleep{HoldSeconds}`), rendered by
activation like the rest of it; a release that does not sleep has none of it, so its bytes are what they
were. Two things send a request to the wake on either proxy: the **switch**, `sleeping.on` in the route's
pages folder, read on every request as the maintenance switch is, so nothing is reloaded; and the
application **not answering at all** (the proxy's own 502 or 504), which covers a switch that went
missing. While the switch is there the route never dials the stopped container's old address: Docker
hands a stopped container's address on its network — and its loopback port — to whatever starts next, and
a visitor must not be answered by that. Every check the route makes runs before the wake: the fence,
maintenance, the password and the sign-in. Only a request the route would have handed to the
application can wake it.

**nginx.** At server level, after the maintenance switch, `set $<ident>_up <host:port>` and, while the
switch is there, `set $<ident>_up unix:<pages>/asleep.sock` — a socket that is never created, so the
connection fails at once. `location /` forwards through the variable (`proxy_pass http://$<ident>_up`) —
a variable set at server level rather than an `if` in the location, where `proxy_pass` would be another
location's — with `recursive_error_pages on` and `error_page 502 504 = @jd_wake`. A location that names
any `error_page` replaces the server's, so the server's own are said again there: the sign-in's
`401 =302` and the route's pages. `@jd_wake` asks `auth_request /__jd/wake` (its own `auth_request_set`,
so the sign-in's are not read again from the wake's answer and the visitor's identity is sent on as it
was) and then forwards to the application's literal address; its 401 (still starting) and a 502 or 504 on
the retry are `=503 /__jd/waking.html`, its 403 or a 500 (nothing to wake, or the dashboard not
answering) go to `@jd_down`, which `return 502`s through the server's own 502 page. `/__jd/wake` is
internal and proxies to the wake socket with no request headers, no body and `proxy_method GET`, a read
timeout of the hold plus ten seconds. The waking page is served like the route's other pages, with
`Retry-After: 5` and `Cache-Control: no-store`. The Proxy page's parser reads the route back as the site
it serves: its own address, its pages, and none of the wake's locations as the operator's.

**Caddy.** In the route block, after the fence, maintenance and the password or sign-in, `@jd_asleep file
{ try_files /sleeping.on }` and `route @jd_asleep { … }`, then `vars jd_app 1` just before the
application's `reverse_proxy`, whose `dial_timeout` is 1 s (a stopped container's address answers nothing
at all, and Caddy waits three seconds by default). `handle_errors` wakes first — `@jd_wake` is a 502 or
504 **with** `jd_app` set, since an auth server that does not answer is a 502 too and must never wake
anything past the sign-in — and the route's pages after. The wake is `reverse_proxy
unix//config/just-dashboard/wake/wake.sock` with `method GET`, `rewrite "/wake/<route>"`, `Host jd-wake`
and the visitor's `Cookie`, `Authorization` and `Proxy-Authorization` removed; a 2xx sets `vars
jd_wake_upstream {rp.header.X-Jd-Upstream}` and the request goes on to `reverse_proxy
{vars.jd_wake_upstream}` (`lb_try_duration 5s`, `lb_try_interval 250ms`): the address the wake answers
with, because a container started again may have been given another, and with the cookies the route
drops (`DropCookies`: the dashboard's session, on a route serving the dashboard's own name) taken out as
the route's own `reverse_proxy` takes them out — nginx's `@jd_wake` is a location of the route, so it
does the same. A 401 is the waking page with a 503; anything else is the route's 502 page, or Caddy's
`502`.

## The wake socket

The proxy asks over a Unix socket rather than the dashboard's API, whose routes sit behind the network
allowlist and a session. No address is bound at all, routable or not (security invariant 7): host nginx
reaches `<JD_NGINX_DIR>/jd-wake/wake.sock`; the Docker Caddy ingress reaches
`/config/just-dashboard/wake/wake.sock`, which the dashboard creates at the host path of the ingress's
own `/config` mount (its Docker volume, which the dashboard's container has mounted at its real name, or
under `/host`), so only that ingress reaches it. A volume's path is longer than a socket address carries,
so it is bound through the directory's file descriptor (`/proc/self/fd/<n>/wake.sock`). The socket is
`0666` in a root-owned `0755` directory, because nginx's workers are not root; what it does is what any
visitor's request to the domain does. Every connection is checked again: `SO_PEERCRED` gives the peer's
process, and only one whose `/proc/<pid>/comm` is nginx or caddy is answered — the dashboard runs in the
host's process namespace, so the ingress container's Caddy is a process it sees. The sleeper re-opens a
socket whose file went (a recreated ingress has an empty volume) on every pass, and listens only where
the current proxy expects it (`proxysvc.WakeSockets`).

It answers one thing, `GET /wake/just-dashboard-env-<id>.conf`: **200** once the application answers,
with `X-Jd-Upstream: <ip:port>` for Caddy; **401** while it is still starting after the hold; **403**
when there is nothing asleep to wake (its container down for another reason) or the wake failed. Those
are the three answers nginx's `auth_request` understands.

## Waking

One wake runs per environment: every request arriving while one is under way waits on it, up to the
hold, and one whose hold ends first is answered with the waking page while the wake carries on, on a
context of its own, to the plan's bound. Under the environment's lock it reads the open stretch — none,
and the container running, is a request that met the switch an instant before a wake removed it, let on
to the application — starts the container (`StartExisting`), and waits for it to be ready: the release's
own required readiness checks, each asked once (`singleAttempt`) every 150 ms, since a check's own
interval is a second or more and its first attempt at a process still starting would cost the visitor
that second; for a release without one, the application answering HTTP at all (a connection alone says
nothing: Docker's proxy holds the published port whether or not anything listens behind it). On Caddy it
then brings the route to the container's address (`RefreshDeploymentRoute`: the two containers are
inspected, Caddy joined to the network if it left, the route rewritten and reloaded only if the address
changed). The outcome is answered to every waiting request at once; the switch is cleared, the stretch
closed (`woke_at`, the trigger, the host or person, the seconds the start took, any error) and
`deploy.environment.wake` audited after. A start that fails closes the stretch with its error and
answers 403: failing to wake is down, and the host alerts say so. Readiness not reached within the bound
still lets the request on — the application is running, and what it answers is the visitor's best
answer — and records the failure.

Measured by `TestLiveSleepingDeploymentWakesOnTheFirstRequest`: the first request after sleep through a
real Caddy is answered by the application in about a second.

**Runs.** Every run of an environment, at its first step (`endSleep`, beside `endWarmWindow`), wakes it
if it is asleep — trigger `run`, detail `run #N` — so a deploy, a restart or a rollback finds the live
release as it expects it: running, routed and awake. A stop needs no container started to stop it, so its
stretch is only closed. A scheduled marker and a preview's removal do neither.

**Repair.** The pass closes a stretch the environment is no longer in: its release is no longer live, or
its container is running again (started by hand, or by Docker after a daemon restart) — trigger
`repair`, the switch cleared.

**Wake now.** `POST …/sleep/wake` (service control) wakes it and waits for the bookkeeping too, so the
reading it answers says awake; `409 not_asleep` when there is nothing asleep.

## Asleep by design is not down

- The deployment summary carries `asleepSince` while a stretch is open, and the project reads **Asleep**
  rather than Stopped: the fleet, the header, the overview. Its primary verb is Wake now rather than Start.
- The overview draws the sleeping panel (`sleep-panel.tsx`): since when and why, the last request, how
  long a visitor waits, the share of the week asleep, the last wake, and Wake now. Its site preview is not
  loaded while asleep (`SitePreview asleep`): loading it would be a request through the route, and wake it
  for nobody.
- The host's `container_down` alert reads a stopped container with an open stretch as fine: "It is asleep
  by design, and the next request to its domain starts it."
- The availability figure cuts the stretches out of the down intervals, and an environment asleep now is
  not down now: a visitor then got the page after a pause, not an error.
- The rollback watch never meets a sleeping environment: nothing sleeps while a window is open, and the
  run that opens one wakes it first.
- The lifecycle feed attributes the container's stop to the idle decision (written just before it) and its
  start to the wake (written just after it), each matched to the nearest entry on the side it falls within
  eleven minutes (`correlateSleepEvents`, run before the release pass, whose directional window would
  otherwise file a wake's start under the sleep before it).

The Prometheus exposition is unchanged: `containers_running` reads 0 while asleep, as it is.

## Routes

| Route | Who | What |
| --- | --- | --- |
| `GET /deploy/{id}/environments/{env}/sleep` | read, a deploy key too | `SleepStatus`: the live release's terms, `state` (off, awake, asleep, waking), since, why, the last request, when it sleeps, the switch, the last seven days' stretches and the seconds asleep in them |
| `POST /deploy/{id}/environments/{env}/sleep/wake` | `service.control`, a deploy key too | Wake now, audited `deploy.environment.wake` |

Turning it on or off is `PUT …/configuration` (`runtime.sleep`). Both routes are in `deployKeyRoutes`
(`keyOpen`: waking is what any visitor's request does, and Start does) and `deployOperations`.

## UI

Runtime settings carries **Sleep when idle** (`settings/sleep.tsx`, `sleep-model.ts`) after Traffic replay,
drawn for a web or static profile or a plan that already sleeps: the switch, the idle minutes and the wake
limit, the head saying what the next release does, the bounds refused under their field before a save,
and — before a save the server would refuse — why this plan could not sleep (`sleepBlocker`).

## Tests

- `proxysvc`: `TestSleepingRouteOnNginx`, `TestSleepingRouteKeepsTheSignIn`, `TestSleepingRouteOnCaddy`
  (the order of the checks, the wake and the `jd_app` mark), `TestSleepingRouteValidation`,
  `TestSleepingSwitchOnNginx`.
- `deploy`: `TestSleepPlanValidation`, `TestIdleEnvironmentSleepsAndARequestWakesIt`,
  `TestSleepWaitsForRunsAndRunsWakeIt`, `TestSleepIsRepairedWhenTheContainerRunsAgain`,
  `TestWakeSocketAnswersOnlyTheProxy` (a peer that is not the proxy is refused),
  `TestAvailabilityLeavesSleepOut`; `api`: `TestASleepsStopAndStartAreItsIdleDecisionAndItsWake`;
  `store`: `TestSleepSpansAreOneOpenPerEnvironment`.
- Live, as root on a Docker host:
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run 'TestLiveDeploymentRouteWakesOnNginx|TestLiveDockerCaddyRouteWakes' -count=1 -v`
  puts a real `traefik/whoami` container behind the host's real nginx (a private prefix) and behind a
  real `caddy:2-alpine`, with a wake socket of the test's that starts it with `docker start`: stopped with
  no switch, the first request is answered by the application; with the switch on, a POST reaches it with
  its body (on Caddy after another container was given the old address); a visitor without the route's
  password gets 401 and wakes nothing; a wake still starting is the waking page; nothing to wake is the
  route's 502.
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveSleepingDeploymentWakesOnTheFirstRequest -count=1 -v`
  is the whole feature against a real Caddy ingress on a loopback port (`JD_INGRESS_CONTAINER`) and the
  real engine, sleeper and socket: a release that sleeps serves a request; the pass, on a clock moved six
  minutes, stops its container (not removed); the next request through the ingress is answered by the
  application; the stretch, the three audit entries and the lifecycle feed's attribution are checked; then
  Wake now.
- Browser: `deploy-sleep.spec.ts` (the overview asleep and Wake now; the terms saved with the runtime plan
  and refused out of bounds; the server's refusal under the switch).

Not measured: a host whose Docker runs without the userland proxy (connections to a stopped container's
loopback port are then refused by iptables rather than accepted and closed, which the wake handles the
same way); an operator's own Caddy whose `/config` is a bind mount outside the paths the dashboard's
container mounts (the wake then cannot place its socket, says so, and nothing sleeps).
