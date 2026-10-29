# Replicas and canary releases

Two ways a route shares its requests between more than one container: [replicas](#replicas), several
containers of one release, and [canary releases](#canary-releases), a candidate release taking a share
of the live release's requests beside it before it is promoted or aborted.

## Replicas

A release can run as several containers side by side on this host, behind one route that shares its
requests between them. Other self-hosted panels get this from Docker Swarm or Kubernetes; here it is
the runtime owner starting N containers of the same frozen snapshot and the proxy renderers writing a
pool, so a single-host install needs neither.

### The plan

`runtime.replicas` is 1 to 8 (`MaxRuntimeReplicas`); zero, the field left out, is one, so a plan
written before replicas keeps its digest. Each replica has the plan's limits of its own — a release of
three replicas with a 256 MiB limit holds up to 768 MiB, and twice that while a blue/green cutover runs
both releases, which preflight's `replicas` finding and the Runtime settings say in figures.

Only a stateless release has more than one (`replicaRefusal` in `deploy/runtime_replicas.go`, mirrored
by `replicaRefusal` in `frontend/src/components/deploy/replicas-model.ts`). Replicas share one host, so
anything only one container can hold refuses them, with the reason in words:

| The plan has | Why one container |
| --- | --- |
| a Compose build | a Compose stack runs one copy of each of its services |
| host networking | every replica would claim the same ports |
| a fixed host port | the port can be held by one container at a time |
| a published port | the same, for each published port |
| a writable mount | replicas would write to it at the same time |
| a tailnet-only publication (`runtime.tailnet`) | tailscale serve, or the environment's tailnet gate, maps one port of the tailnet address to one container's loopback port |

Scale to zero (`runtime.sleep`) refuses a plan of more than one replica in turn (`sleepRefusal`): a wake starts one container and holds the waiting request for it.

A read-only mount is shared without harm. `PlanConfiguration.Validate` refuses such a plan on
`runtime.replicas` (so a draft, a settings save, `jd config set` and a blueprint are all refused the same
way, and the Runtime settings put the sentence under the control), and preflight's
`replicas_ineligible` says it again beside the strategy finding for a plan that reaches it. A preview
copies production's plan with `replicas` cleared: it is for looking at a branch, not for carrying
production's traffic.

### The runtime owner

`DockerRuntimeOwner.startContainer` creates the replicas one after another from the same image,
command, environment, mounts and limits. The first keeps the name a release has always had,
`jd-e<env>-r<number>`; the others are `jd-e<env>-r<number>-<i>` (`replicaContainerName`), so
cancellation cleanup can still find every container a crash between `docker create` and the runtime
record left (`orphanReplicas`). Each carries the release's labels plus `io.just-dashboard.replica` (its
number) and `io.just-dashboard.replicas` (the count); a release of one container carries neither, as
before. A replica that fails to start removes the ones already started: a release is all of its replicas
or none.

The executor leases a loopback port for each replica (`replicaAddresses`, the same 30-minute candidate
lease the first one takes) and hands them over as `CandidateRuntimeRequest.ReplicaPorts`. The runtime
record stays one row per release: `RuntimeID`, `Host` and `Port` are the first replica's, and its
metadata lists every replica (`dockerReleaseRuntimeMetadata.Replicas`, `ContainerIDs`). Everything that
acts on a runtime reads that list through `runtimeReplicas` and `runtimeContainerIDs`: `Stop` stops every
replica at once (each with the whole grace period, so stopping eight takes as long as one), `StartExisting`
starts every one and joins each to the release's networks, a restart is both, diagnostics read up to five
of them, the run's metrics chart every replica's series, and the warm-release window pauses and resumes
all of them.

Because the containers share the release's labels, the inventory (`ObserveRuntimeServices`), the
lifecycle feed (which owns an event by its `environment-id` label) and Docker's own pages already read
them as one release. The project Overview says `2 of 3 replicas healthy` — running and not reported
unhealthy by its own healthcheck, counted against the release's own count, so a replica Docker no longer
lists still counts against it — and the Runtime page's cards say `replica 2 of 3` (`replicaReading`).

### Readiness and the cutover

`verifyChecks` runs the phase's checks against every replica in turn (`runReplicaChecks`, the targets
from `replicaTargets`), and stops at the first replica a required check fails on: once the route moves,
every replica takes traffic, so one that does not answer is a release that does not. Each result says
which replica it was for (`CheckEvidence.Replica`; the run page tags it), and a failure removes the whole
candidate. A public-route check runs once, through the route, whichever replica the proxy picks. The
instant rollback's readiness check of a warm release asks every replica the same way.

### The route

`proxysvc.DeploymentRoute.Upstreams` is the release's pool, one loopback address per replica
(`runtimeUpstreams`); `Upstream`, the one address a route had before, is kept as its compatibility
spelling, and `Servers()` is what both renderers read. A release of one container is still built with
`Upstream`, so its route, its bytes and every reader of the field are exactly what they were.

On nginx a pool of two or more is an `upstream` block (`deploymentPool`) the catch-all proxies to:
`least_conn`, because a route carries WebSockets and long streamed answers; each server
`max_fails=1 fail_timeout=10s`, nginx's passive health written out; `proxy_connect_timeout 2s` in the
location (`SitePool.ConnectTimeout`), because the kernel accepts a connection whatever the application is
doing, so a replica that takes longer is not there; and no `keepalive`, as before, so each request is
still a fresh upstream connection. nginx passes a request to the next server only when it is idempotent
or never reached the dead one, so a POST is never carried out twice.

On the Docker Caddy ingress the pool is one `reverse_proxy` with every replica's container address and a
block of `lb_policy least_conn`, `lb_try_duration 5s`, `fail_duration 10s`, `max_fails 1` and a 2-second
`dial_timeout` (`caddyReplicaBalancing`); the intercepted error pages share the block. The route records
one `# Just Dashboard target:` line per replica, and the lifecycle worker's pass repairs each
([Caddy ingress](caddy-ingress.md)).

### Verification

- `TestLiveRuntimeLossKeepsExactlyOneReleaseLive` (`internal/deploy`) now reads "exactly one release is
  live" as one release's containers and nothing else's: its `replicas` case starts two releases of three
  replicas, kills one replica of the candidate in the blue/green window and requires the whole candidate
  removed, kills one replica of the live release and requires it brought back whole, in place, then kills
  all three and starts them again.
- `TestLiveCutoverTrafficContinuity` (`internal/proxysvc`) runs its hundred activations under traffic for
  a release of one container and for one of two replicas, which also requires both replicas to answer and
  a replica killed under steady traffic to cost no request.
- `TestLiveDockerCaddyBalancesReplicas` (`internal/proxysvc`) puts three replicas behind a real Caddy:
  shared requests, the inventory's three upstreams, a replica killed under traffic with nothing lost, and
  a Caddy taken off the replicas' network repaired by one maintenance pass while a replica is dead.
- `TestLiveReplicatedReleaseServesAndRollsBackAsOne` (`internal/api`) deploys a release of two replicas
  through the routes and a Caddy ingress, reads the inventory, cuts over to a broken release of two only
  after both pass readiness, requires both of the first release's replicas paused through the window and
  only the new release running, and the automatic rollback to resume those same two containers and remove
  the broken release's two.

### Limits

- One host. Replicas buy a release that one process cannot carry, or that should not go down with one
  container; they do not survive the host.
- A replica of the warm release that is gone ends its window (`warm_lost`): the warm release is switched
  back to whole or not at all.
- The maintenance pass leaves a replica it cannot find at its old address rather than dropping it from
  the route; Caddy passes over it, and Docker's restart policy usually brings it back.
- A streamed answer or a WebSocket a replica was serving when it died ends with it, on either proxy.

## Canary releases

Vercel-class rolling releases on one host: a plan with the strategy `canary` gives each new release a
trial between its checks and the cutover. The candidate joins the live release's route for a share of
its requests, the two releases' answers are recorded apart, and the rollback watch's own comparison reads
the candidate against the live release side by side over the same minutes. A candidate that does worse is
removed with the live release untouched; one that does not is promoted, on its own or by an operator.

### The plan

`runtime.strategy: "canary"` with `runtime.canary {percent, durationSeconds, autoPromote}`: 1 to 50
percent of the requests, a window of 60 to 3600 seconds, and whether a trial that compared well is
promoted at the end of its window (`validateCanary` in `deploy/canary.go`). The terms belong to the
strategy and are refused beside another one, which is why the Runtime settings drop them when another
strategy is chosen. A canary is blue/green with a trial, so it needs everything blue/green does — a web or
static profile, no fixed or published host port, no host network, no writable mount (preflight's
`strategy_ineligible`, and `validateRuntimeActivationStrategy` at start) — and a domain that serves the
application, since the share is of the public route's requests. Preflight's `canary` finding states the
terms in words. A preview is replaced blue/green whatever production's plan says.

### The step

`canary` sits after `replay_traffic` and right before `activate` in a deploy's and a promotion's steps:
the replay sends recorded requests to a candidate nothing routes to yet, and the canary is the
candidate's first real traffic. A redeploy, a rollback and an instant rollback do not take the
step, as they do not take the replay: they go back to, or again to, a release already judged. Every plan
that is not a canary skips it, and so does a first release, which has nothing to share the route with. For a canary
plan the step (`runCanary`, `deploy/canary_executor.go`):

1. opens a trial under the run's lease (`deploy_canaries`, one open per environment by a unique partial
   index), copying the environment's rollback terms — the margins it is judged on — into it;
2. writes the live release's own route, with its frozen domains and rules, the candidate's replicas
   taking their share beside the live release's (`proxysvc.DeploymentCanary`);
3. gives the run's build slot back for the wait (its lease's class becomes `canary`, counted against
   neither budget, and its own class again once the trial is decided), so a ten-minute window does not
   hold every other deployment on the host behind it — the environment stays leased, so nothing else runs
   there meanwhile;
4. reads both releases every five seconds from the request record — `accesslog.Filter.Release`, each
   release by its id — and judges the candidate against the live release with `JudgeRollback`, the
   rollback watch's rule: at least `minRequests` requests and five 5xx answers before the 5xx rule
   decides, `errorRateMargin` points over the live release's share (a clean share when the live release
   served too little), or `latencyFactor`× its p95 and at least 250 ms slower, never on a record without
   durations (host nginx before the timed format); the readings are stored on the trial for the run page;
5. aborts at once on a tripped reading — it does not wait for the end of the window — and on an
   operator's abort; promotes at the end of a window it got through, when the plan says so, and on an
   operator's promote; otherwise holds (`holding`) with its share until an operator decides, for at most
   a day (`canaryHoldLimit`), after which it is aborted as `unanswered`.

To **abort**, the route is written back to the live release alone, then the candidate is stopped and
removed (`stopCandidateAndRestore`), and the step fails with `canary_failed` (the reading's sentence) or
`canary_aborted`; the run ends `failed` and the live release never changed. A route that cannot be put
back leaves the candidate running, since removing it would fail the visitors still sent to it, and says
so (`canary_route_failed`). To **promote**, the route is written back to the live release alone first and
the step passes; `activate` then makes the ordinary cutover from a route naming one release to a route
naming the other, so a failed cutover restores a route with no canary in it, and `retire_previous` keeps
the previous release warm exactly as after any blue/green cutover. A candidate whose containers all stop
during the trial is aborted (`canary_candidate_lost`).

Restart and cancellation: the trial is rows and a route. `Engine.Shutdown` does not wait out a canary's
window — the step is in `resumableWaits`, so Shutdown cancels it with `errEngineStopping`, the step is left
running and its lease is surrendered at once — and the next process reclaims the run, the reconciler
answers `resume` for `canary`, and the step finds the trial it opened for the run and carries on to the
same end, writing the same route again. A trial that ended before the step could record it is the step's
result as it was. An operator's cancellation takes the candidate off the route before it is removed
(`endCanaryForCancellation`) and closes the trial `cancelled`.

### The route

On nginx (`SiteSplit`, `proxysvc/sites_split.go`) each release is an upstream block of its replicas —
`<route>_pool` for the live release and `<route>_canary` for the candidate, each with the replicas'
least-connections balancing and passive health — and `split_clients "${request_id}"` gives each request
one of the two releases at random, by the release's id, which a `map` turns into the block
`proxy_pass` names. The access log's timed format gains `rel=$…_release`. On the Docker Caddy ingress
(`renderCaddyCanary`) the matcher `vars_regexp {http.request.uuid}` names the candidate's run of the 256
two-hex-digit prefixes of Caddy's per-request version-4 UUID (`canaryUUIDPattern`, the share to within
one 256th), and two `handle` blocks each carry `log_append release <id>` and their own `reverse_proxy`.

The share is per request on both proxies, not per visitor: a visitor may be answered by both releases
during a trial. The plan's spec called for nginx's `split_clients` on a stable key and Caddy's
`weighted_round_robin`; `caddy:2-alpine` 2.11.7 does have that policy, but over the two releases'
containers as one pool it loses least connections between a release's own replicas and records a
container address rather than a release, so both proxies split on a per-request random key instead and
record the release by name. Keeping a visitor on the release that first answered them is skew
protection's work, not the split's.

### Routes, CLI and UI

- `GET /deploy/{id}/environments/{env}/canary` — the open trial, or the latest ended one: terms, window,
  both releases' readings, the verdict, the decision and how it ended.
- `POST …/canary/promote` and `POST …/canary/abort` — the service-control capability, as a deploy is,
  `202` with the trial, `409 no_canary` without an open one and `409 canary_decided` when one decision
  has already been asked for; audited `deploy.canary.promote` and `deploy.canary.abort`. The run holding
  the trial carries the decision out within a second. A deploy key reaches all three (`deployKeyRoutes`),
  as it reaches promote and the instant rollback.
- `jd canary status|promote|abort APP [--env]`.
- The Runtime settings offer Canary beside Blue / green and Stop first, with its share, window and
  *Promote on its own*, and the reason when it cannot be chosen (`canary-model.ts`). The run page draws
  the trial while its step runs (`run-canary.tsx`): the window's countdown, the two releases side by side —
  requests, 5xx share, p95, coloured against the line the server aborts on — the reading's sentence, and
  *Promote now* and *Abort*; an ended trial stays as its outcome.

### Verification

- `TestLiveCanaryAbortsAFailingReleaseAndPromotesAHealthyOne` (`internal/api`, `JD_DEPLOY_LIVE=1`, as
  root) runs a real engine and its own Caddy ingress on a loopback port (`JD_INGRESS_CONTAINER`): a
  healthy release A; a canary B that passes readiness and answers 500 to everything else, on half the
  requests, aborted on the reading with A's same container still serving everything and B's removed; then
  a healthy canary C, with the dashboard restarted in the middle of its window, resumed with the same
  start and end and promoted on its own, after which C serves every request and A is kept warm, paused.
- `TestLiveCanarySplitsTheRouteOnNginx` (`internal/proxysvc`) splits a real nginx route 30% to a
  candidate, reads the access log's `rel=` for every answer, and moves between the plain and the canary
  route twenty times under fast, streamed and WebSocket traffic without losing a request.
- `TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun` kills the engine inside every step of a deploy
  and a promotion, `canary` among them; the unit matrix `TestRestartAtEveryStepUsesEvidenceWithoutDuplicatingSideEffects`
  resumes it once, `TestShutdownHandsACanaryWaitToTheNextProcess` proves Shutdown hands the wait over, and
  `canary_test.go` covers abort, promotion, holding for the operator, a restart mid-window, cancellation
  and the build slot.

### Limits

- The comparison needs the request record: on a host whose nginx records no durations the p95 rule does
  not judge, and without a record at all a trial is decided by its window and the operator.
- During the trial the route serves the live release's frozen rules; the candidate's own domains,
  headers and sign-in apply from its cutover.
- A trial is per environment and per run; a second deploy queues behind the run holding one.
