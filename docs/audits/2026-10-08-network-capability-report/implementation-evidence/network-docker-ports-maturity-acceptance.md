# Docker networking, ports and host exposure maturity acceptance (C115–C119, C127–C129)

This records the Docker networking, ports and host-exposure package of the feature-by-feature
maturity work, branch `implement/network-docker-ports-maturity` from local checkpoint `465480ba`
(PR head `4338e6cc` plus the integrated VPN package). The behaviour and its boundaries are documented
in [Docker network creation and guarded changes](../../../internal/backend/docker-files-logs.md#network-ownership-dependencies-and-guarded-changes),
[the inbound path to a published port](../../../internal/backend/network-investigator.md#inbound-path-to-a-published-port),
[the ports routes](../../../internal/backend/databases-proxy-platform.md) (free ports, external
evidence), [posture unknowns](../../../internal/backend/observability-security.md#netsec-exposure-posture-login-history)
and [external checks from the ports page](../../../internal/backend/network-external-checks.md#use-from-the-ports-page).
This evidence changes no ledger status; proposed statuses are listed at the end.

Raw logs are kept beside this document in [`network-docker-ports-maturity/`](network-docker-ports-maturity/)
and named under each check. The production host was only read, apart from the owned Docker
fixtures described below; no published port, firewall rule, route, foreign network or container
was changed.

## Per row

### C115 Docker network inventory, detail and topology

Shipped: every listed and inspected network carries its owner from its labels — Docker system,
the dashboard's own Compose project (told by the data directory its backend mounts), a deployment
or its database link (joined to the project and environment by name while that environment
exists), a Compose project, or created by hand. A failed container listing is no longer an
unused network: the list says `membersKnown: false` with the error, the page says "members
unread" instead of "0 containers", warns once, and offers neither removal nor prune until a
refresh reads them; the detail reports a failed listing and each member whose own inspect failed
(aliases unread rather than none). Members carry both families' addresses and their other
networks — the containers joining this network to the rest — and the shared ingress and the
dashboard's own containers are marked.

Tests: `TestOwnerOfNetworkAndIngress`, `TestNetworkDependenciesReadsMembersAndFailsClosed`
(Engine wire), `TestNetworkInventoryNamesOwnersAndUnreadMembership` (API, including a failing
container listing), the Bun tests in `lib/docker-networks.test.js`, and browser cases "networks name
their owners, and unread membership never reads as unused", "a network's detail names its owner,
both families and the networks its members join" and "readers see owners and previews' results but
are offered no change".

Limits: ownership is label evidence of who created a network, not of who uses it now; a network
labelled by hand outside the dashboard as Compose is read as Compose.

### C116 Docker network creation

Shipped on top of the integrated safe advanced editor: `GET /docker/networks/drivers` reads the
Engine's catalogue (built-in drivers with the option keys Docker documents, installed network
plugins enabled or disabled, overlay refused without a swarm manager, host/null as network modes).
Creation checks it before the Engine is asked — an absent or refused driver, or a macvlan/ipvlan
parent that is not a host device, is `400 driver_unavailable`, and any driver but bridge is refused
when the catalogue cannot be read. The form suggests creatable drivers, says why one is refused,
labels a plugin's options as passed through unchecked, and names option keys a built-in driver
would silently ignore.

Tests: `TestDescribeNetworkDriversSeparatesBuiltinsPluginsAndRefusals`,
`TestUnknownDriverOptionsAreTheBuiltinTyposOnly`, `TestNetworkDriversReadsInfoAndPlugins` (Engine
wire), the extended `TestNetworkCreationGatesHostDriversOptionsAndReservedOwnerLabels` (absent
driver, absent parent, overlay without swarm never reach creation), the Bun driver-reading test,
browser case "the creation form reads the Engine's drivers and names options Docker would ignore"
and the unchanged `docker-network-create.spec.ts`. Native: `TestLiveNetworkDriverCatalogueAndIgnoredOption`
read Engine 29.8.1's catalogue (six drivers, no plugins, swarm inactive) and created one owned
internal bridge with the undocumented `com.docker.network.bridge.mtu=1400`: the Engine accepted it
and the bridge kept MTU 1500, which is exactly what the warning says.

Limits: no third-party network plugin is installed on this host, and installing one would change
the host, so plugin drivers are exercised only against recorded Engine shapes; a plugin's options
are its own.

### C117 Docker attachment and disconnection

Shipped: attach and detach are previewed from one fresh dependency reading (network, every
container including stopped ones, every network, an inspect per member and candidate; a failed
listing refuses rather than reporting no dependents). Attaching refuses an existing attachment,
host/none/`container:` modes, a swarm network without `--attachable`, unresolvable aliases, aliases
on the default bridge, a full IPv4 pool and the dashboard's own network, and warns of a name another
member already answers to, overlapping ranges and a deployment's or database-link network.
Detaching refuses the dashboard's own containers, the shared ingress and database-link members, and
warns of the last network, published ports carried on it and peers losing the member's names.
The mutations refuse what the preview blocks (`409 network_conflict`) and record the acknowledged
warnings in the audit. The attach dialog previews as the draft changes and keeps the container and
alias when the preview cannot be read; attach is offered on any local network (the old
non-attachable hint applied a swarm-only rule to bridges) but not on the dashboard's own, and no
detach control is drawn for the dashboard's containers or the ingress.

Tests: `TestPreviewConnectNamesSharedNamesOverlapsAndRefusals`,
`TestPreviewConnectRefusesTheDashboardsNetworkAndAFullPool`,
`TestPreviewDisconnectGuardsOwnersAndNamesWhatIsLost`, `TestNetworkMutationsRefuseWhatTheirPreviewBlocks`
(API: a refused change never reaches the Engine; a permitted detach goes to the inspected network
by ID), browser cases "attaching previews shared names and refusals, and keeps the draft when the
preview fails", "detaching names who loses the member, and a refused detach cannot be confirmed",
both width cases "network previews fit without page overflow", and the updated retention case in
`network-report.spec.ts`. Native: `TestLiveNetworkDependencyPreviewsAgainstTheEngine` ran both
previews over owned containers on two owned internal /28 bridges.

Limits: a preview is a reading at one moment; the Engine still decides at the change.

### C118 Docker network deletion and pruning

Shipped: `GET /docker/networks/{id}/removal` lists running members (refused), the dashboard's own
stack (refused), a deployment's network whose environment still exists (refused; removal goes
through its plan; an unreadable record counts as existing), stopped containers that still name the
network, shared IPAM reservations still recording it as owner, and Compose recreating it. The
reviewed prune (`GET` then `POST /docker/networks/prune {ids}`) lists every network the Engine's
own prune would take and removes only those nothing blocks or names, rechecked at removal; the
cleanup category and the global sweep remove the same removable set instead of running the
Engine's prune. The card's delete and the Prune button open these previews.

Tests: `TestPreviewRemoveListsStoppedDependentsAndManagedOwners`,
`TestPruneCandidatesKeepWhatStoppedContainersAndDeploymentsName`,
`TestNetworkRemovalAndPruneKeepWhatIsStillNamed` (API: a live deployment's network is refused, the
Engine's prune endpoint is never called, only the still-removable reviewed ID is deleted),
`TestHeldForNamesTheReservationsAResourceStillHolds`, browser cases "a removal says what still
names the network, and a live deployment's network is refused" and "the reviewed prune removes only
what nothing names and says why the rest is kept". Native: the owned fixture showed the hazard the
preview warns of — Engine 29.8.1 removed an owned network a stopped owned container still named,
and that container then failed to start ("network … not found") — and the reviewed prune kept that
network while Docker's own prune would have taken it.

Limits: Caddy route records that name a network are not read separately; the ingress is a running
member of any network a live route uses, which the Engine and the preview already refuse.

### C119 Container port publishing

Shipped: one inbound investigator for a published port (`GET /docker/containers/{id}/published/{port}`,
administrator-only like the connection investigator, read-only): Docker's publication; the host
socket or NAT-only publication; Docker's DNAT rule compared with the container's current address;
DOCKER-USER with operator rules listed, unevaluated; the forwarded leg in FORWARD's own order with
Docker's filter accept; the dashboard's gateway (a competing owned forward, any foreign forward-hook
chain that can drop); the proxy; provider policy, always unknown, with whether the host has a public
interface address; and retained external measurements. The container's reachability rows and the
ports sheet of a Docker-published socket open it, and every "reachable from outside" verdict now
ends with the unseen provider.

Tests: `TestInvestigatePublishedJoinsDockerNATFiltersProviderAndMeasurement`,
`TestInvestigatePublishedKeepsUnreadLayersUnknown`, `TestParseDockerNATReadsBindingsAndSkipsOtherShapes`,
`TestReadDockerChainsKeepsEachPartsError`, `TestForwardOrderFindsDockersAcceptAheadOfUfw`,
`TestPublishedPathJoinsTheInboundLayersForAdmins`, `TestPublishedPathIsTheInvestigatorsCapability`,
browser cases "an administrator traces a published port's path from outside, layer by layer"
(container page, with the limited operator offered no trace) and "a port Docker publishes is traced
from outside through its NAT, unknown layers named" (ports sheet). Native, read-only, as root on
this host: `TestLivePublishedPathOnThisHost` assembled the shared ingress's `0.0.0.0:80` — DNAT
observed to `10.0.0.3:80`, DOCKER-USER empty, FORWARD `ts-forward → DOCKER-USER → DOCKER-FORWARD →
ufw-…`, Docker's accept observed ahead of ufw and qualified because Tailscale's `ts-forward` comes
first and is not evaluated, provider unknown with public address 57.131.21.87, no external
measurement. The first native run (kept, below) exposed that the adapter alone predicted ufw's
routed deny for that port; FORWARD's order is now read. External measurements are matched to the
binding's protocol, family and address, so a loopback or other-address binding never borrows
another socket's proof.

Limits: no provider adapter exists, so provider policy is always unknown; the path is a sequence of
snapshots, not a packet trace; an Engine on its nftables firewall backend reads as unknown NAT;
off-host traversal is proven only by an enrolled external source, none of which exists here.

### C127 Port inventory, ownership, exposure and history

Shipped: `GET /ports/external` (administrator-only) joins the external-check owner's retained
checks to their sources by host port: measured TCP state, measured address and whether it is on
this host, placement (an external host, or a controlled fixture that is not off-host), and checks
that ended unmeasured as unknown. An exposed socket's sheet shows those measurements, offers "Check
from <source>" for every enrolled scope naming the port in the socket's family (through
`POST /network/external/checks`, reading again until the source answers) and says when no source is
enrolled that reachability from outside is unproven.

Tests: `TestExternalPortEvidenceJoinsSourcesAndKeepsUnmeasuredChecksUnknown`,
`TestPortsExternalIsAnAdminsReading`, the Bun tests in `ports-reachability.test.js`, browser cases "a
published port's sheet says provider policy is unseen, shows the external proof and checks again"
and "an unenrolled port says reachability from outside is unproven, and a reader is offered no
check".

Limits: no enrolled off-host source exists on this host, so the proof is exercised against recorded
evidence; actual off-host acceptance stays with P6.

### C128 Free-port selection and service identification

Shipped: the search passes over, and names, claims a bind cannot see — unexpired deployment port
leases, the tailnet preview range (for wildcard or tailnet addresses), the kernel's ephemeral range,
a port an enabled firewall's inbound rule already decides (read as the posture reads rules, so an
interface-limited or profile rule is not taken for one covering every address) and a port a gateway
forward translates away; a loopback search skips the inbound sources. Every source is listed as
checked, unavailable (with why) or not supplied — provider reservations, for which no adapter
exists, always say so. The finder shows what it passed over and what it could not read.

Tests: `TestReservationChecksNameFuturePolicyConflicts`, `TestInboundRuleForReadsOnlyRulesWhoseTargetIsSaid`,
`TestFreePortsPassOverLeasesAndSayWhatWasNotSupplied` (a real lease row passed over and named;
loopback and wildcard source states), browser case "the free-port search names what it passed over
and the sources it could not read". Native, read-only, as root: `TestLiveFreePortPolicyOnThisHost`
read this host's sources for a wildcard TCP search without binding — ufw's 25 rules pass over 11
ports (80, 443, 3000, 5432, 5435, 5439, 8000, 8080, …), the preview range 1000 ports, the ephemeral
range 28,232, no leases or gateway forwards, provider not supplied.

Limits: provider reservations need a provider adapter (P19); a check is an observation, not a
reservation.

### C129 Host exposure and security posture

Shipped: `Posture.Unknowns`, neither finding nor pass: provider policy always (with whether the host
has a public interface address), and nftables base chains at the input/forward/prerouting hooks that
no adapter models — not an iptables-nft table, not firewalld's, not the dashboard's gateway — whose
decision is more than an unconditional accept, from the gateway's own reading of the ruleset; an
unread or unreadable ruleset is itself unknown. The Security page lists them as "Not seen by these
checks", a clean list beside them reads "No findings in the layers these checks can see", the ports
sheet says an exposed socket's provider is not visible, and container reachability verdicts end
with it.

Tests: `TestAssessAlwaysNamesProviderPolicyAsUnknown`, `TestAssessNamesComplexForeignNftablesChainsOnly`,
`TestPolicyCoverageKeepsAnUnreadRulesetApartFromAnEmptyOne`, the extended
`TestPostureAnswersOnABareHost`, browser case "the posture names the layers its checks could not
see". Native, read-only, as root: `TestLivePostureUnknownsOnThisHost` graded this host — provider
unknown with its public interface address; the ruleset was read and held no foreign chain whose
decision is unmodelled, so no nftables unknown was raised.

Limits: foreign chains are reported, never evaluated; provider policy stays unknown until a provider
adapter exists.

## Checks

The final `scripts/test-changed.sh 465480ba` ran on committed source `69744b03` against a
production build served on port 43215 with one browser worker, holding the shared lock: **exit 0
in 1,435 s** ([log](network-docker-ports-maturity/test-changed-final.log)). It covered 75 changed
files: Prettier, ESLint and `tsc` clean; 3,287 Bun tests passed; `go build ./...`, `go vet` and the
tests of `api`, `dockerx`, `netpath`, `netipam`, `proxysvc` and `netsec` passed (served from Go's
test cache, the source being identical to the earlier fresh run); 19 selected browser specs ran
**581 passed, 24 optional screenshot cases skipped, 0 failed**, including every case of
`docker-network-maturity.spec.ts`. A targeted recheck of the cases the first attempt failed passed
25 of 25 beforehand ([log](network-docker-ports-maturity/browser-recheck-final.log)); the first
attempts are kept beside them ([gate](network-docker-ports-maturity/test-changed-attempt1.log),
[browser](network-docker-ports-maturity/browser-targeted-attempt1.log)). The native Docker and
host-reading runs on owned objects are linked from each row above.

## Review before the gate

An independent review of the committed diff found no capability, audit, `hostexec` or
`s.destructive` regression and one guard bypass already closed by `d10c5c59` (a preview resolving a
container reference differently from the Engine). Its other findings were fixed in `e6a804d7` and
`f5b69fec`, each with a test: external measurements matched by port alone (now protocol, family
and the binding's address; a loopback binding takes none); a network whose IPAM reservations could
not be read counted as prunable (now a warning that keeps it); Docker's FORWARD accept reported
without the unevaluated DOCKER-USER rules or earlier chains ahead of it (now qualified, as the
native run shows for `ts-forward`); a full first IPv4 pool refusing an attach although a second
pool had room; a forced disconnect of a stale endpoint refused; swarm networks among prune
candidates; a confirmation left enabled on the previous preview while a refused one was reread;
and the audit recording the typed reference rather than the container acted on.

## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C115 | verified | Owners, unread membership and member topology ship with Engine-wire, API, Bun and browser acceptance, and a failed read never makes a network look unused. |
| C116 | implemented / acceptance pending | The driver catalogue, pre-Engine refusals and ignored-option warnings pass, natively for built-in drivers; third-party plugin drivers need an installed plugin and their own acceptance. |
| C117 | verified | Attach/detach conflict previews, server-side guards and retained drafts pass unit, API, owned native and browser acceptance. |
| C118 | verified | Removal and reviewed-prune dependency previews pass unit, API and browser acceptance, with the stopped-dependent hazard shown natively on owned objects. |
| C119 | implemented / acceptance pending | One read-only inbound investigator joins Docker NAT, the forwarded leg, gateway and provider policy and passes natively on this host; provider policy is explicitly unknown and off-host traversal unmeasured. |
| C127 | implemented / acceptance pending | Retained external proof and checks from enrolled scopes pass against recorded evidence; no enrolled off-host source exists to measure a real port. |
| C128 | verified | Leases, preview/ephemeral ranges and firewall/gateway conflicts are passed over and named, read natively on this host; provider reservations are explicitly not supplied because no adapter exists. |
| C129 | verified | Provider policy and unmodelled foreign nftables chains are named unknown in the posture, ports sheet and reachability verdicts, with unit, API, native and browser acceptance. |
