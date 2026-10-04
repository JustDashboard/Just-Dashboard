# Existing workload discovery and managed adoption

The importer recovers a regular deployment plan from an existing application, records its current
runtime as a pinned live release, and leaves the application running. This is a managed deployment
with settings, variables, release history, runtime services, logs and the normal deployment controls.
Importing itself neither enqueues a deployment nor restarts, recreates, relabels or rewrites the
original runtime. Recovery refuses incomplete translations instead of claiming they are safe.

## Workflow and API

An administrator opens **Import existing** on `/deploy`, discovers the workload, reviews the
inventory, and chooses **Review migration**. `/deploy/import` then opens the normal Configure,
Variables and Review sequence with the recovered settings. The final **Adopt deployment** action
records the existing app. **Deploy changes** later applies the desired recipe. **Redeploy** restores
the frozen live release; it does not apply pending settings. Docker adoption retains existing Docker
resources; an explicitly deployed PM2/systemd recipe runs under the dashboard's Docker deployment
engine, with the original manager retained as the baseline recovery authority.

- `GET /deploy/import/discovery` returns `checkedAt`, `items` and per-manager `silences`.
- `POST /deploy/import/inspect` accepts `{key}` and refreshes the selected inventory.
- `POST /deploy/import/recover` accepts `{key,name,digest}` and returns a server-created configured
  draft. Recovery reads private configuration separately from the public inventory. It returns
  `422 recovery_blocked` with actionable reasons if a complete supported recipe cannot be recovered.
- Draft configuration and preflight use the normal draft routes. Recovery provenance and the original
  baseline are server-owned; saving a source or runtime dependency cannot replace that authority.
- `POST /deploy/import/adopt` accepts `{draftId,revision,acknowledgedWarnings,gitPolicy?}`. It
  recaptures the original workload, compares its identity, topology, configuration, images,
  environment and supported source identity, and commits only after the normal preflight review.
  `409 workload_changed` requires another inspection. A committed retry returns the same project.
- The generic draft commit route rejects recovered adoption drafts, so it cannot bypass the fresh
  original-runtime check. Git automation defaults to manual for recovery; a plain local directory
  has no Git branch or automatic-commit controls.

These routes require an administrator's browser session, CSRF for writes, and audited mutations.
Discovery accepts only a discovered resource identity, not a host command. Host commands use explicit
`hostexec` arguments. Original paths are contained by `files.Resolve` and `JD_DEPLOY_ROOTS`, including
Compose files, includes, environment files, bind mounts and native source/configuration paths. The
private capture never enters discovery, audit data or browser remembered setup. Secret values are
sealed in draft and variable storage; recovered source documents use variable references instead
of embedding captured environment, credential arguments or label values.

Recovery writes only private, bounded staging files under the dashboard data directory. It never
writes the application's original directory. Docker effective configuration is read with Compose
`config`, not `up`, `build` or `pull`; source inspection does not execute an ecosystem JavaScript
file, lifecycle script or application command. PM2 capture speaks to an existing daemon rather than
using a CLI path which could start one.

## Supported recovery and explicit boundaries

| Runtime | Recovered plan and baseline | Cases that require resolution before adoption |
| --- | --- | --- |
| Docker Compose | Canonical effective Compose configuration, exact running local image IDs, captured settings for existing replicas, current file order and project identity. Existing named volumes and networks become explicit external resources. Baseline records existing service/replica IDs and which were running. | Missing authoritative configuration, unresolved paths/resources, absent local images for missing services, one-off/Swarm ownership, divergent replica settings, replica-number gaps, unrepresentable non-default Engine fields, or meaningful writable-layer data. |
| Standalone Docker | A Compose recipe capturing environment, command/entrypoint, user/cwd, ports, mounts, networks/aliases, restart policy, health check, logging, resource limits and supported security/host options. Live baseline initially points to the unchanged original container. | Unmapped Engine options, unsupported namespace/resource relationships, Swarm tasks, missing image identity, or meaningful writable-layer data. The first Deploy changes creates a managed container name; original aliases and resources are reviewed explicitly. |
| PM2 | Exact account/daemon, namespace, application and instance identities; private environment and argv; source evidence; a supported Dockerfile or bounded Node recipe; original PM2 restart and log authority for the live baseline. | Unknown toolchain, missing/unsafe source, incompatible interpreter or process topology, unsupported manager-only behavior, secret-bearing opaque files, or settings which cannot be translated faithfully. Cluster discovery does not imply every cluster topology is convertible. |
| systemd | Exact unit and drop-in/configuration identity, private environment/argv and supported source/build recipe. Native baseline uses the original unit and journal until Deploy changes creates the Docker release. | Socket activation, credentials, complex execution chains, unsupported sandbox/dependency semantics, unavailable restart authority or an unreproducible source/build. A loaded unit alone is not proof it is an application. |
| Bare listening process | PID plus creation time and safe inventory; capture can explain the source and missing requirements. | No verified manager can restart the original process for compensation. Automatic managed adoption is refused until a reproducible source and restart authority exist. A port or framework name alone cannot supply these. |

The source of an image-only workload is its immutable local image. Recovery cannot manufacture the
original Git history or source checkout. An existing Compose `build` definition is reported for
review; the captured image is the initial reproducible baseline/redeploy input. The source settings
can later attach supported Compose source for builds. A native plain/dirty directory uses
`local_directory`: a bounded content identity and private copy preserve uncommitted files without
pretending they are a Git commit. Excluded persistent data remains linked rather than copied into
an image. Source-copy containment, symlink checks and size/count limits fail closed.

Docker writable-layer checks block application data, altered source and unknown file changes.
Verified Docker-generated files and narrowly identified regenerable Python bytecode caches are
reported separately; this is not a blanket exemption for `/tmp`, logs or cache directories. Stopped
and missing services remain inactive during adoption. A later Deploy changes may create/start the
services in the reviewed recipe; baseline redeploy/rollback restores only the captured existing
replicas and running set.

Discovery reads the configured Docker daemon, existing PM2 homes and the host systemd manager.
It does not enumerate every rootless daemon, Podman engine, custom `PM2_HOME`, user systemd manager,
non-listening unmanaged worker or remote server. Unavailable facilities remain visible as silences.
The dashboard itself and dashboard-managed resources are excluded where identifiable. Resources
already reserved by managed or legacy observed imports link to their existing project.

## Transaction, ownership and runtime behavior

Adoption commits project, Production environment, desired plan revision 2, original baseline revision
1, sealed original and desired variable revisions, immutable baseline artifacts, completed migration
history, live runtime and its live-release pointer in one SQLite transaction. It queues no execution.
A review edit remains pending against the original baseline. Original variables are snapshotted
separately from desired variables, and original checks/dependencies remain the baseline even if the
review changes them. A failed transaction leaves no partial project, release or ownership reservation.

A server-owned managed `runtime` dependency reserves the original resource and overlapping Compose
containers. Archives retain reservations. Configuration saves preserve both this reservation and the
original Compose project name; they cannot claim another application through these fields. Duplicate
and preview plans must shed original runtime authority and isolate their names and writable storage.
External volumes and shared resources retain their external/linked ownership, including during
cleanup; adopting the app is not permission to delete a shared volume.

Initial runtime observation joins exact captured Docker IDs without changing their labels. Recreated
baseline containers are accepted only with the exact environment/release ownership labels and
captured service/replica identity. Native services expose their manager, current verified identity,
PID and native log source rather than fabricated Docker IDs. Container console requires an actual
Docker runtime; PM2 files and systemd journal supply native output before migration. Git-only features
require a real Git source, and Docker-specific metrics/tools require a Docker runtime.

Recovered Docker recipes retain per-service configuration in captured inline Compose documents.
Review lists service mounts and makes the saved files inspectable. General settings edits those
documents through the normal source inspection and revision-guarded save endpoint; invalid YAML
does not create a revision, and a valid save remains pending until Deploy changes. The live baseline
is unchanged by source editing. Runtime and Storage link to the Compose source and describe empty
aggregate fields as absent overrides, preserving each service's existing settings rather than
claiming zero limits or no persistent data. Private inputs remain variable references, and in-progress
YAML edits are never placed in browser session storage.

Native lifecycle operations recheck manager configuration and captured source evidence before
controlling the original app. Port reuse requires a freshly verified listener PID and creation time,
not just a matching port number. The stop-first deployment path uses the normal readiness, activation
and compensation engine. A failed replacement restores the retained original baseline; native
baseline restoration leaves the original manager record/unit available. Persistent volume or database
writes are not reversed by image/configuration rollback: configure and verify application-appropriate
backups before deploying changes which can alter data or schema.

## Compatibility

`POST /deploy/import/register` remains available for legacy observation-only records. Those retain an
`import` source, observed ownership, no live release and guarded lifecycle/automation controls. They
are not the new managed migration flow. Legacy existing-checkout adoption retains its source-based
behavior. Old `/deploy/new?source=import` and imported-profile links lead to discovery; remembered
external imports offer an inspection link without silently converting the saved record.

## Evidence and upstream contracts

Docker's [Compose config command](https://docs.docker.com/reference/cli/docker/compose/config/)
explains effective configuration and interpolation; the
[service specification](https://docs.docker.com/reference/compose-file/services/) defines the runtime
mapping, and [external volumes](https://docs.docker.com/reference/compose-file/volumes/) preserve
existing storage identity. PM2's [application declaration](https://pm2.keymetrics.io/docs/usage/application-declaration/)
and [startup restoration](https://pm2.keymetrics.io/docs/usage/startup/) explain why a process list
alone is insufficient. The capture uses the existing daemon's
[monitor RPC](https://github.com/Unitech/pm2/blob/master/lib/Client.js).

Acceptance uses temporary databases and uniquely named fixture resources. Live proof and its exact
limits are recorded in [`2026-10-04-existing-workloads`](../../audits/2026-10-04-existing-workloads/README.md).
Read-only observation of an operator's real stack is distinct from destructive migration tests on
owned fixtures; neither an imported project screen nor mock browser coverage alone proves rollback.
