# Existing workload discovery and import

An existing application can be made visible in Deployments without transferring its runtime to a
different manager. This is the safe default: registering an observed project preserves the original
configuration, credentials, files, networking, volumes and manager in place. It never reconstructs
a Compose file from container inspection or silently converts a PM2/systemd application to Docker.
The application can remain active throughout the import.

## Workflow and API

`/deploy` offers **Import existing** to a session holding `system.admin`. `/deploy/import` is a flow
page: discover → review → imported. It uses the existing FlowPanel/FlowSteps/ChoiceRow design
system, search and source filters. Discovery reports silences separately from workloads so a missing
manager is never presented as proof there are no applications.

- `GET /deploy/import/discovery` returns `checkedAt`, `items` and `silences`.
- `POST /deploy/import/inspect` accepts `{key}` and refreshes the selected workload.
- `POST /deploy/import/register` accepts `{key,name,digest}`, re-observes the workload and commits
  only when its stable identity and visible topology match the review. `409 workload_changed`
  requires another inspection. Ordinary start/stop state changes do not invalidate the review.

These endpoints require an administrator's browser session. Mutating requests retain CSRF protection
and audit entries. Clients provide a discovered identity, never a command or a file to execute.
Source paths are resolved against `JD_DEPLOY_ROOTS` before checking availability; the importer does
not read source contents, environment values, arbitrary labels or process argv into its response.
There is no confirmation phrase because registration does not remove or interrupt any resource.

Registration is an atomic, idempotent database operation. Repeating the same resource returns its
existing project. A different resource cannot reuse a project name; a container already belonging to
an imported Compose stack cannot also become a separate imported project. Archives retain the
relationship until the archived record is permanently deleted.

## What is discovered

| Runtime | Identity and grouping | Preservation and limits |
| --- | --- | --- |
| Docker Compose | Compose project labels, with all existing containers including stopped ones; declared missing services use the existing static Compose inventory | One project preserves its original working directory, file order, profiles, environment, networks, mounts and volumes in place. Missing files do not prevent observation. Static declarations can be incomplete for includes/interpolation; discovery never executes Compose configuration. |
| Standalone Docker container | Full container ID | Environment, entrypoint, command, ports, restart policy, limits, mounts and networks remain in Docker. A replacement container is not silently substituted. Swarm tasks are identified as individual tasks with a warning; no Swarm-service ownership is claimed. |
| PM2 | Existing daemon/account, namespace and application name; cluster instances grouped | Original runtime, cluster mode, environment, ecosystem and boot configuration stay in PM2. Discovery speaks read-only RPC to existing daemon sockets; it does not invoke the CLI's daemon-start path. Missing/incompatible daemons yield a silence. |
| systemd | Unit name, including inactive loaded services | Unit files, drop-ins, dependencies, execution account, sandbox and environment files remain in systemd. App identity survives PID changes. PM2 daemon units and dashboard/container infrastructure are excluded where identifiable. |
| Listening host process | PID plus creation time, with its listening ports grouped | Handles a Node/Next/React/Vue development or production server without requiring a framework guess. It observes this process only: no reliable boot, restart or deployment recipe can be recovered from a listener. PID reuse is fenced; non-listening unmanaged workers are not discovered. |

The dashboard's own workload and dashboard-managed resources are excluded. If an inventory is
unavailable, discovery keeps successful results and reports the failed owner. Managers are queried
concurrently under a bounded deadline. Already imported resources remain identifiable in discovery.

## Project behavior and ownership

An import creates an ordinary deployment identity and a Production environment, an `import` source,
empty informational build/runtime plans and a `runtime` dependency with `ownership: observed`.
The dependency stores only the sanitized reviewed inventory. The project is disabled for hooks and
has no run, release, watcher, schedule, copied variable or runtime mutation.

Fleet and project reads refresh one inventory for the response and attach `importedWorkload` to the
deployment summary. Imported state is **Running**, **Partly running**, **Stopped** or **Not observed**,
independent of activation health and release pointers. A missing workload or unavailable manager
shows its retained inventory with a reason; it is not reported as stopped or healthy. An imported
project does not appear as having pending deployment changes simply because it has no release.

The imported Overview lists its services and points to its original manager. Navigation exposes the
Overview and archive controls rather than configuration that does not control the application.
Run enqueue and source/configuration conversion are refused at the backend. Archive and permanent
record deletion retain external resources, because observed dependencies never enter managed-only
removal plans. Importing is reversible by removing its project record after archiving.

## Why migration is separate

Inspection cannot recover the shell environment used when Compose was launched, unrecorded override
files, credentials stored elsewhere, data in a writable container layer, shell/cron boot behavior,
systemd socket activation or a PM2 ecosystem's JavaScript logic. A container may share a volume,
network, public proxy or database with another application. Recreating it from an approximation can
change ports, erase data or cause two managers to fight over the same service.

A future migration must explicitly identify the authoritative source, secret inputs, writable data
and shared resources, validate a reproducible plan, prepare and verify backups, stage an isolated
candidate and let the operator approve downtime and ownership transfer. Observation import never
claims that those steps have been completed.

## Upstream evidence

Docker documents Compose's project/service labels and its wider runtime configuration in
[the service specification](https://docs.docker.com/reference/compose-file/services/).
[Compose ls](https://docs.docker.com/reference/cli/docker/compose/ls/) distinguishes inclusion of
stopped projects. PM2 documents that [boot restoration requires saved process state and a startup
hook](https://pm2.keymetrics.io/docs/usage/startup/); a process list alone is not that configuration.
These contracts support preserving the original manager rather than synthesizing a new deployment.

Local acceptance and visual evidence are recorded in
[`2026-10-04-existing-workloads`](../../audits/2026-10-04-existing-workloads/README.md).
