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
- `POST /deploy/import/recover` accepts `{key,name,digest,scope?}` and returns a server-created configured
  draft. Recovery reads private configuration separately from the public inventory. It returns
  `422 recovery_blocked` with actionable reasons if a complete supported recipe cannot be recovered.
  Compose stacks additionally accept `scope: "all_services" | "existing_services"`, defaulting to
  the full declared recipe. **Existing containers only** retains every running and stopped container
  and excludes only declared services with no container. The server reports the exact excluded names
  in the recovered draft and requires acknowledgement in final Review; missing dependencies remain
  blockers. Scope and exclusions are sealed server-owned provenance, not adoption request authority.
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

If Docker deleted an existing container's original image, a verified platform descriptor permits
[bounded read-only filesystem export](docker-image-recovery.md) into a private recovery image. This
creates a local image artifact while leaving the original runtime unchanged; mounted data is excluded
and recovered separately. Captured secrets remain sealed variables rather than image configuration.
In the default complete-recipe scope, an absent service with no image or container still requires
its original image/source before adoption.

Compose recovery defaults to `all_services`, including every declared service. A separately reviewed
`existing_services` scope includes every existing container, running or stopped, and explicitly lists
only declarations with no container under `excludedServices`. Recovery records one
`compose_services_excluded` issue for each omitted declaration. Normal preflight turns each adoption
warning into an `adoption_warning_N` finding, whose code must be acknowledged at commit. Deploy changes
does not create the excluded services, and their original Compose definitions stay untouched. Retained dependencies, links,
service namespaces, volumes-from or shared build contexts referring to an exclusion block recovery;
the importer never removes those relationships to force a usable recipe. Unused resources belonging
only to excluded declarations are omitted from the managed recipe without deleting existing resources.
The existing-services summary counts captured containers, including each stopped replica, so its total
and running count use the same unit even when one service has multiple replicas.

The saved scope and exclusion list are server-owned. Adoption recaptures with the saved scope and
fences the full resolved original configuration, including excluded declarations, so changes there
require fresh recovery. An omitted scope on older drafts means `all_services`. Existing-services scope
is available only to Compose stacks; containers and native managers keep their complete recovered plan.

## Supported recovery and explicit boundaries

Docker capture also checks the original raw `Config` and `HostConfig` against the negotiated SDK
representation, including nested mounts, health checks and structured options. Unknown effective
fields block adoption before any missing-image export/import; field names are reported without their
values or dynamic map keys. An unknown null value has no populated setting; every unknown non-null
field blocks, including zero, false, empty strings and empty collections. Those values can explicitly
disable a newer Engine default, so the importer does not infer that they are inert.
This prevents a newer Engine option from silently disappearing through typed JSON decoding.

| Runtime | Recovered plan and baseline | Cases that require resolution before adoption |
| --- | --- | --- |
| Docker Compose | Canonical effective Compose configuration, exact running local image IDs, captured settings for existing replicas, current file order and project identity. Existing named volumes and networks become explicit external resources. Baseline records existing service/replica IDs and which were running. | Missing authoritative configuration, unresolved paths/resources, absent local images for missing services, one-off/Swarm ownership, divergent replica settings, replica-number gaps, unrepresentable non-default Engine fields, or meaningful writable-layer data. |
| Standalone Docker | A Compose recipe capturing environment, command/entrypoint, user/cwd, ports, mounts, networks/aliases, restart policy, health check, logging, resource limits and supported security/host options. Live baseline initially points to the unchanged original container. | Unmapped Engine options, unsupported namespace/resource relationships, Swarm tasks, missing image identity, or meaningful writable-layer data. The first **Deploy changes** action preserves the original container name, aliases and reviewed resources. |
| PM2 | Exact account/daemon, namespace, application and instance identities; private environment and argv; source evidence; a supported Dockerfile or bounded Node recipe; original PM2 restart and log authority for the live baseline. | Unknown toolchain, missing/unsafe source, incompatible interpreter or process topology, unsupported manager-only behavior, secret-bearing opaque files, or settings which cannot be translated faithfully. Cluster discovery does not imply every cluster topology is convertible. Matching entries in `dump.pm2` or `dump.pm2.bak`, or an unverifiable saved list, require a reviewed startup handoff before migration. |
| systemd | Exact persistent unit and drop-in/configuration identity, private environment/argv, supported restart policy, stop signal/grace and source/build recipe. Native baseline uses the original unit and journal until Deploy changes creates the Docker release. | Transient units which can disappear on stop, socket activation, credentials, complex execution chains, unsupported sandbox/dependency/shutdown semantics, unavailable restart authority or an unreproducible source/build. A loaded unit alone is not proof it is an application. Original startup authority must be verifiably disabled; known loaded or installed activation relationships and unsupported resource/scheduling directives block migration. |
| Bare listening process | PID plus creation time and safe inventory; capture can explain the source and missing requirements. | No verified manager can restart the original process for compensation. Automatic managed adoption is refused until a reproducible source and restart authority exist. A port or framework name alone cannot supply these. |

The source of an image-only workload is its immutable local image. Recovery cannot manufacture the
original Git history or source checkout. An existing Compose `build` definition is reported for
review; the captured image is the initial reproducible baseline/redeploy input. The source settings
can later attach supported Compose source for builds. A native plain/dirty directory uses
`local_directory`: a bounded content identity and private copy preserve uncommitted files without
pretending they are a Git commit. Excluded persistent data remains linked rather than copied into
an image. Source-copy containment, symlink checks and size/count limits fail closed.
The dashboard-visible source directory must be the same filesystem object as the host directory.
Custom deployment roots require matching host mounts; a similarly named directory inside the
dashboard image cannot substitute for the original application's source.
Review and advisory inspection read a bounded private copy with the same reviewed content digest;
a changed directory requires fresh detection before inspection or building. PM2's process-specific
Node IPC descriptor variables are omitted from the Docker environment and identified in the review.
Node recipe recovery selects the supported catalogue image for the captured interpreter major;
its patch version and container operating system can differ from the original host. Applications
which rely on host packages, native dependencies or PM2 IPC need an appropriate Dockerfile and
compatibility review before migration.
Automatic Node recipes install dependencies from reviewed manifests and lockfiles and exclude the
host's `node_modules` from the build context. They do not reproduce patched dependency files or
undeclared global modules. Verify the manifests/locks represent the running dependency tree, or use
a reviewed Dockerfile and source plan which preserves those dependencies before cutover.
Native recovery does not universally reconstruct inherited host process/file-descriptor limits,
umask, scheduling, capabilities or security defaults. The review includes a mandatory compatibility
warning: preserve application-relevant policies in a reviewed Dockerfile/runtime plan before cutover.
Explicit systemd directives outside the supported unit subset fail closed, including resource limits,
rlimits, scheduling, watchdog/backoff, additional lifecycle commands, dependencies and sandbox policy.
The supported file directives are `Description`/`Documentation`, `Type`, `User`/`Group`,
`WorkingDirectory`, `ExecStart`, `Environment`, `Restart`, `KillMode`/`KillSignal` and `TimeoutStopSec`;
install metadata is retained only alongside verifiably disabled startup authority. Unknown directives
or sections require explicit review instead of silently losing their behavior.

Whole filesystem environment values within the captured source and local `file`/`sqlite` URIs map
to the same `/app` layout, while the original values remain privately sealed for the native baseline.
Unknown application filesystem values and interpreter path variables (including `NODE_PATH`, CA
files and OpenSSL configuration) which point outside that tree require verified mounts before
recovery. Standard manager/tool metadata such as `HOME`, `PATH` and PM2 log paths retain their private
values with an explicit filesystem warning unless source detection uses them, in which case an
external path blocks recovery. `NODE_OPTIONS` carries a bounded set of memory and boolean options;
loaders, filesystem operands and unknown options require explicit interpreter review. Captured argv
keeps argument boundaries, translates source-contained absolute flag operands, and rejects external,
escaping or ambiguous interpreter operands. Network URLs are not rewritten as filesystem paths.
Recognizable credential files (`.env`, registry/account credentials, SSH/cloud configuration and
key/certificate/keystore filenames) are conservatively excluded from build copies and block automatic migration when
they appear in application source. Arbitrary configuration files still require operator review;
filename checks cannot identify every embedded secret. Static-serving recipes cannot reproduce a running Node development server's command;
those migrations require a reviewed Dockerfile instead of an automatic runtime override.

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

Before stop-first cutover, Compose up or baseline restoration, the runtime checks the full current
regular-container population of every included service. It accepts exact captured container IDs or
dashboard-managed containers with the exact environment and expected current, predecessor or candidate
release authority for that operation. An unowned extra replica, including one belonging to a newly
added desired service in the original project, blocks the operation before any previous runtime is
stopped. Project-name reservation alone grants no authority over those replicas. Unrelated services
and true Compose one-off containers remain outside the operation; adopted Compose up keeps orphans.
Runtime metadata records the included service names. Older metadata derives that scope only from
captured baseline identities or a complete current match of its recorded container IDs; unavailable
evidence blocks control rather than guessing a service scope.
Baseline rollback removes a stopped service added by a managed release only when its labels match
that candidate or its trusted predecessor release. It validates every such removal before deleting
the first container, preserves one-off containers and never treats an arbitrary positive release
label as cleanup authority.

Initial runtime observation joins exact captured Docker IDs without changing their labels. Recreated
baseline containers are accepted only with the exact environment/release ownership labels and
captured service/replica identity. Native services expose their manager, current verified identity,
PID and native log source rather than fabricated Docker IDs. Container console requires an actual
Docker runtime; PM2 files and systemd journal supply native output before migration. Git-only features
require a real Git source, and Docker-specific metrics/tools require a Docker runtime.

Recovery captures the application runtime. Existing external reverse-proxy routes, certificates,
schedulers and integrations retain their original ownership. Review their addresses, credentials and
startup handoff before cutover. Native recovery preserves the host listening port; Docker recovery
preserves supported names, aliases and network identity. Configure managed Domains separately when
moving ingress into the dashboard.

Recovered Docker recipes retain per-service configuration in captured inline Compose documents.
Review lists service mounts and makes the saved files inspectable. General settings edits those
documents through the normal source inspection and revision-guarded save endpoint; invalid YAML
does not create a revision, and a valid save remains pending until Deploy changes. The live baseline
is unchanged by source editing. Runtime and Storage link to the Compose source and describe empty
aggregate fields as absent overrides, preserving each service's existing settings rather than
claiming zero limits or no persistent data. Private inputs remain variable references, and in-progress
YAML edits are never placed in browser session storage.
The Configure/Review plan summary likewise points to per-service Compose limits when aggregate
overrides are unset, rather than calling the application unlimited.

General settings also exposes the build directory and optional subdirectory for native
`local_directory` sources. Select a separate allowed directory for new code: a source save inspects
it and creates a pending revision, while the frozen original source remains the baseline rollback
input. Captured data exclusions are shown read-only and retained on save along with the source's
other metadata. Rejected paths or revision conflicts keep the unsaved fields visible; readers can
inspect them without editing, and source paths are not remembered in browser storage.

Original startup authority also needs a reversible handoff before container migration. PM2 capture
checks both `dump.pm2` and `dump.pm2.bak` by application namespace/name, independent of numeric IDs.
Matching saved entries block; malformed, unreadable, nonregular or oversized lists cannot prove absence
and block too. Other applications' valid saved entries remain untouched. The controller rechecks this
private evidence using bounded no-follow descriptor reads immediately before its exact lifecycle RPC.
The importer never runs `pm2 save` or rewrites a shared startup list.

Systemd recovery permits only verifiably disabled original units and rejects current reverse activation
or control relationships (`WantedBy`, `RequiredBy`, `TriggeredBy`, `BoundBy`, `UpheldBy`, `ConsistsOf`,
`OnFailureOf`, `OnSuccessOf`). A bounded installed inventory reads service/timer/path/socket/target metadata,
service aliases and their authoritative files using read-only `systemctl` arguments; this can load
metadata but never starts, enables or reloads a unit. Explicit activation/dependency references and
same-basename default triggers are checked against canonical and alias service names even when those
startup units were previously unloaded. Matching references block, and their private evidence joins
the baseline configuration digest. Unreadable or ambiguous inventory also blocks. The limits are
10,000 listed entries, 512 inspected units, 4 MiB per command/file and 8 MiB of inspected file contents.
Concrete template instances are included, and uninstantiated templates are inspected through bounded
`systemctl cat` and authoritative-file reads. Explicit template activation references block; enabled
templates with ambiguous service/target specifiers fail closed. Unrelated templates such as getty do
not substitute for the application's startup authority. The real adapter recaptures startup/configuration and PID identity before manager
control. No global enablement or startup settings are changed by import or cutover.

These checks cannot prove absence of cron, custom scripts, future administrator actions or every
external launcher. The mandatory review warning requires checking all external startup authority,
including additional targets and template launchers, so the original app cannot restart beside Docker
after reboot. Clearing a startup blocker requires an operator-reviewed reversible handoff, followed
by fresh recovery; it is not permission to disable shared production startup services globally.

Native lifecycle operations recheck manager configuration and captured source evidence before
controlling the original app. The reviewed original source directory remains frozen while its native
baseline is retained: a full bounded original-tree digest covers modules, private files and directory
permissions/ownership as well as the executable/entrypoint evidence. Private file values remain
excluded from build copies; native metadata retains only bounded content hashes. Already reviewed
linked data remains outside this native digest. Changed source blocks stop-first cutover and baseline restart/rollback
before manager control; restore the captured source or use a separate managed checkout for new code.
This avoids claiming a restart of modified original files is an immutable release restoration.
Before adoption, known runtime-source edits later than the active process birth time block recovery,
including module or recognizable private startup-file edits while the entrypoint remains unchanged.
The comparison allows two seconds for procfs/filesystem clock precision and excludes reviewed data
paths. A filesystem snapshot
cannot attest every in-memory module, backdated file or dynamic setting: verify the actual running
source and startup authority, restoring or reviewing a restart under the original manager when needed.
Port reuse requires a freshly verified listener PID and creation time,
not just a matching port number. The stop-first deployment path uses the normal readiness, activation
and compensation engine. A failed replacement restores the retained original baseline; native
baseline restoration leaves the original manager record/unit available. Persistent volume or database
writes are not reversed by image/configuration rollback: configure and verify application-appropriate
backups before deploying changes which can alter data or schema.

Native baseline replay depends on the retained original manager record, unit files and frozen source
remaining available. A PM2 daemon reset can lose an app removed from saved startup lists. Systemd unit files or drop-ins
under `/run` or `/var/run` block recovery even when the unit is not marked transient, because reboot
can remove that authority. Move to a persistent reviewed unit/configuration before fresh recovery.
Native captures include a mandatory authority-availability warning. Review how to retain a recoverable
original authority before cutover: the current adapter refuses missing manager records/units and does
not silently reconstruct them. An immutable Docker release remains independent of native authority.

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
Node's [CLI and environment contract](https://nodejs.org/docs/latest-v24.x/api/cli.html) defines
`NODE_OPTIONS`, module and CA-file inputs; systemd's
[resource pressure protocol](https://systemd.io/PRESSURE/) identifies the manager's pressure-watch
paths which require explicit review when moving an application into a container. The upstream
[systemctl contract](https://github.com/systemd/systemd/blob/main/man/systemctl.xml) explains why
loaded reverse dependencies alone cannot inventory every installed launcher, while
[resource control](https://github.com/systemd/systemd/blob/main/man/systemd.resource-control.xml)
describes inherited cgroup policy beyond a service's explicit directives.

Acceptance uses temporary databases and uniquely named fixture resources. Live proof and its exact
limits are recorded in [`2026-10-04-existing-workloads`](../../audits/2026-10-04-existing-workloads/README.md).
Read-only observation of an operator's real stack is distinct from destructive migration tests on
owned fixtures; neither an imported project screen nor mock browser coverage alone proves rollback.
