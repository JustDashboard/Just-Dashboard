# Replicas

A release can run as several containers side by side on this host, behind one route that shares its
requests between them. Other self-hosted panels get this from Docker Swarm or Kubernetes; here it is
the runtime owner starting N containers of the same frozen snapshot and the proxy renderers writing a
pool, so a single-host install needs neither.

## The plan

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

## The runtime owner

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

## Readiness and the cutover

`verifyChecks` runs the phase's checks against every replica in turn (`runReplicaChecks`, the targets
from `replicaTargets`), and stops at the first replica a required check fails on: once the route moves,
every replica takes traffic, so one that does not answer is a release that does not. Each result says
which replica it was for (`CheckEvidence.Replica`; the run page tags it), and a failure removes the whole
candidate. A public-route check runs once, through the route, whichever replica the proxy picks. The
instant rollback's readiness check of a warm release asks every replica the same way.

## The route

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

## Verification

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

## Limits

- One host. Replicas buy a release that one process cannot carry, or that should not go down with one
  container; they do not survive the host.
- A replica of the warm release that is gone ends its window (`warm_lost`): the warm release is switched
  back to whole or not at all.
- The maintenance pass leaves a replica it cannot find at its old address rather than dropping it from
  the route; Caddy passes over it, and Docker's restart policy usually brings it back.
- A streamed answer or a WebSocket a replica was serving when it died ends with it, on either proxy.
