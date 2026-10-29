# Game servers

The two Minecraft templates (`minecraft-java`, `minecraft-bedrock`) deploy as ordinary image releases
with three things the release path gained for them: **install-time files** and **pinned artifacts**
written into a managed volume before the container starts, **UDP** published on the plan's bind
address, and **protocol readiness checks** (`game_handshake`, `udp_probe`). A game release then
has the Console, Players and Server settings pages of `internal/gameserver`. Nothing here is
Minecraft-specific except the definitions and the handshake; another game is a definition.

`blueprint.DeploymentSupport` no longer refuses a game profile, a definition's `files`, a
`fetch_artifact` or a UDP port. The one refusal left is an artifact found through a version source
(`fetch_artifact` with `value` and no `checksum`): the helper has nothing to verify those bytes
against, so such a definition stays a preview with that reason.

## Install-time files

A definition's install step may carry `write_file` operations (`path`, `content`). Validation
(`blueprint/validate.go`, `validateVolumeWrites`) holds each to the definition's own volumes: the
path is a clean absolute path strictly inside a declared, writable volume (never the volume's root),
written once per definition, at most 64 KiB of text, and its only placeholders are `{{input.name}}`
and `{{secret.name}}` naming a declared input or generated secret. `write_file` is refused in any
phase but install, and `fetch_artifact` in any phase but startup, so no step is silently dropped.

`Render` turns each into a `Plan.InstallFiles` entry: inputs are substituted (escaped as a Java
properties file reads them when the path ends in `.properties` — a backslash doubled, anything
outside ASCII as `\uXXXX`), and a secret becomes the reference `{{variable.NAME}}` to the variable
that holds it, so the plan and its digest still hold no value. An input value that itself spells a
placeholder (`{{` or `}}`) is refused, because the helper fills every reference it finds and a
server name typed as `{{variable.RCON_PASSWORD}}` would otherwise publish the console password in
the server list.

`deploy/blueprint_source.go` maps install files and pinned artifacts onto `runtime.seeds[]`
(`deploy/runtime_seeds.go`): `{mount, path, content, format}` for a file, `{mount, path, url,
checksum, maxBytes, timeoutSeconds}` for an artifact, where `mount` is the target of a plan mount.
`PlanConfiguration.Validate` refuses a seed in anything but a managed named volume the plan mounts,
a path that is not clean and relative, more than 32 seeds, seeds on a Compose release, a file with
a placeholder other than a reference to one of the plan's variables, and a file line that assigns
a credential-shaped key a literal value (`rcon.password=hunter2` is refused,
`rcon.password={{variable.RCON_PASSWORD}}` is not).

**Who writes them.** `DockerRuntimeOwner.startContainer` calls `materializeSeeds` before it creates
the release's container. For each volume a helper container is **created, never started**, from
the release's own immutable image, with that one volume mounted where the release mounts it,
`network: none`, no environment and labels `io.just-dashboard.seed=true` plus the environment and
release number — not `io.just-dashboard.managed`, so the seconds a helper exists it is never read as
one of the release's containers. Creating it is what initializes a new volume from the image (the
copy-up Docker does for the release itself), so the volume's root keeps the owner the image gave
it. Each file is then written through the Engine's archive API (`dockerx.WriteContainerTree`) owned
by the nearest existing directory's uid/gid — the Minecraft image runs as 1000, PostgreSQL's data
directory is 70 — with any missing directories between created the same way and no existing one
rewritten. A file is written **only where nothing is yet**: the first release writes it, and every
later release, rollback, restart and the operator's own edits through Server settings leave it
alone (the run transcript says `Kept …` or `Wrote … before the first start`). A reference to a
variable the release does not have, or one that is empty, is refused rather than written blank. No
host shell or host path is involved; the helper is removed however the step ends, and a leftover
from a run that died part way is removed by name at the next one.

## Pinned artifacts

A `fetch_artifact` startup operation with `path`, an https `url` (never templated when a checksum
pins it), `checksum` (`sha256:<hex>`), `maxBytes` (≤ 8 GiB) and an optional `timeoutSeconds`
(≤ 3600; ten minutes when absent) becomes an artifact seed. If the destination exists it is kept.
Otherwise a second helper runs `deploy.SeedFetchImage` — `curlimages/curl:8.17.0` pinned by digest —
as the volume's owner with only that volume mounted at `/jd-seed`, as `curl --proto =https
--proto-redir =https --fail --location --max-redirs 5 --max-filesize <maxBytes> --max-time
<timeout> --output <path>.jd-partial <url>`. The bytes land under a partial name the release never
reads; the dashboard reads them back through the archive API (`dockerx.DigestContainerFile`,
bounded by `maxBytes`), and only a matching digest is moved into place (`mv`, again in the helper).
A mismatch removes the partial file and fails the step with both digests and the size in the
message (`… has digest sha256:… (1479 bytes), but the definition pins sha256:…; it was removed and
nothing was started`). No shipped definition fetches an artifact yet; the path is exercised by the
unit tests and the live test below.

## The Minecraft templates

Both definitions seed `/data/server.properties` from their inputs: the server name, players,
difficulty, game mode, allow list, online mode and distances; for Java `enable-rcon=true`,
`rcon.port=25575` and `rcon.password={{secret.rcon-password}}`, and for Bedrock
`server-udp-ports=19132`, the one UDP port its gameplay uses (without it the server allocates
ephemeral ports nothing publishes). The inputs that became the file no
longer reach the image as variables. The itzg images apply every property whose variable is set on
**every** start, so the Server settings page used to be overwritten by the deployment's own
environment at the next restart; now the image applies only what the dashboard still sets (Java's
`ENABLE_RCON`, `RCON_PORT` and `RCON_PASSWORD`, so rotating the console password still reaches the
file; Bedrock's `SERVER_PORT`), and an edit made on Server settings survives restarts and
redeploys. Changing one of those inputs in the source after the first release changes nothing for
that reason; the input descriptions say so.

Two things the live sweep found against today's upstream builds, which `VERSION=LATEST` resolves to:

- **Java needs Java 25.** Minecraft 26.x is compiled for Java 25 (class file 69) and crash-loops on
  the image's Java 21 build, so the definition pins `itzg/minecraft-server:2026.9.1-java25`, the same
  image release's Java 25 variant.
- **Bedrock accepts players over NetherNet only.** Bedrock Dedicated Server 1.26 defaults to
  `transport=nethernet` — an HTTP signaling handshake over **TCP** on `server-port` and gameplay
  over the **UDP** port it negotiates — and in RakNet mode logs that "NetherNet is the only supported
  transport type. Players will not be able to connect". The definition therefore publishes 19132 on
  both protocols, sets `TRANSPORT=nethernet` (which the image's own healthcheck keys on: without it,
  it sends a RakNet ping that a NetherNet server never answers), and checks readiness with an `http`
  GET of `/v1/join`, the server's own status answer (`{"name", "version", "players",
  "maxPlayers", …}`). A pinned version from before NetherNet answers neither check, so its release
  fails readiness rather than going live unjoinable.

The console password is a generated secret like any other (`RCON_PASSWORD`, 32 characters, sealed
at commit, revealed through the audited reveal route); the image writes it into `rcon-cli`'s own
configuration, which is what `gameserver.Console` runs.

## Ports and binds

A direct primary port becomes the plan's fixed host port on its own number — `25565/tcp` for Java,
`19132/tcp+udp` for Bedrock — on **loopback** (`127.0.0.1`), like every other publication.
`runtime.protocol` may now be `udp`, or `tcp+udp` for one number on both, but only for a fixed
publication: validation refuses either without `hostPort` and `internalPort`, with host networking,
or with a domain (no proxy in front of the dashboard carries a datagram). A definition's second
direct port on the primary's own number in the other protocol is folded into that one publication
(`blueprintConfiguration`), so the single Bind address control decides where players reach both;
any other direct port stays a `runtime.ports[]` entry as before. The runtime owner publishes the
primary once per protocol on the plan's bind address (`runtimePortMappings`), checks and the
runtime's own address read its TCP side, and a published port of the same number conflicts only
within the same protocol. RCON (`25575`) is internal and never published.

Players on another machine need a routable bind, which is the operator's choice under Settings →
Runtime → Bind address (`0.0.0.0`/`::`), never a default. Preflight says what loopback means for a
game (`game_loopback_only`, a warning measured as `25565/tcp on 127.0.0.1`, field
`runtime.bindAddress`); once the bind is public, the existing firewall review runs with the right
protocol (`firewall_matches`, or `firewall_mismatch`, which blocks until the port is admitted), and
the existing `public_bind` warning applies.

`GET /deploy/{id}/game` carries `reach` — the live release's port, protocol and bind, and what the
host firewall does with it (`admits`, `drops`, `off`, `open` or `unknown` with a reason), read from
the release's own runtime snapshot (`deploy.DescribeGameReach`) — and the identity line on the game
pages says what stands between a player and the server: loopback, with the way to Runtime, or a
firewall that drops the port, with **Allow 25565/tcp** for an administrator where the firewall is
editable. That button is the Firewall page's own `POST /firewall/rules` (audited
`firewall.rule.add`, recorded in the rule history, applied through the pending network confirmation
and refused if it would cut off the caller); there is no game-specific firewall route.

## Readiness

`game_handshake` and `udp_probe` are executable check kinds now (`deploy/checks.go`); neither is in
`unrunnableCheck` any more, so a configuration may save them.

- `game_handshake` connects over TCP and runs the Minecraft Java Server List Ping
  (`gameserver.JavaStatusHandshake`: a handshake for the status state with protocol `-1`, a status
  request, one bounded answer of at most 256 KiB decoded as JSON naming a version). A socket that is
  open is not the answer: the server binds before its world is ready. Failure code
  `handshake_failed`.
- `udp_probe` sends `payload` (lowercase hex, ≤ 512 bytes) to the port and passes when a datagram
  comes back beginning with `expect` (hex, ≤ 64 bytes; empty accepts any answer). Silence until the
  attempt's deadline is `no_answer`, a different first byte `unexpected_answer`, and an ICMP refusal
  `connection_failed`. It is for a game whose UDP port answers an unsolicited datagram — a RakNet
  server answers an unconnected ping (`01…`) with an unconnected pong (`1c…`). No shipped definition
  uses it: Bedrock's NetherNet gameplay port binds only once a client has negotiated it, so Bedrock is
  checked over its TCP join endpoint instead. The live test below proves the probe through a
  published UDP port against Bedrock run in RakNet mode.

A blueprint check names its port by declared name, and validation holds the kind to the port's
protocol (`game_handshake` and `tcp` to a TCP port, `udp_probe` to a UDP one); `payload`/`expect`
are refused on any other kind. Both definitions gate readiness on the container's own health first
(600 seconds: each server downloads its build and generates a world on the first start) and then on
the protocol check (120 seconds): Java's `game_handshake`, Bedrock's `http` `/v1/join`.

## The game pages

Console, Players and Server settings (`api/handlers_gameserver.go`) act on the live release's
running container, found through the runtime owner. The console is RCON, which a definition
declares as its `rcon` port; Java has it. Bedrock has no RCON — its server reads commands only from
its own input, which this dashboard does not attach to — so its overview reports `console: false`
with `consoleReason`, its player list is `unsupported` with that reason, and the console and player
action routes answer `409 console_unsupported` after judging the command, so a shell string is still
refused as one. Server settings works for both: it reads and writes the declared
`/data/server.properties` in the running container, writes only declared keys, and the change now
survives the restart it asks for.

## Tests

- `go test ./internal/blueprint` — the counts (`CatalogueDeployable` 117), the refusals in
  `TestValidationRejectsEachUnsafeShape`, `TestDeploymentSupportRefusesOnlyAnUnpinnedArtifact`,
  `TestInstallFilesRenderInputsAndReferenceSecrets`.
- `go test ./internal/deploy -run 'Game|Seed|Artifact|UDP|Handshake'` — the draft journey for both
  templates through commit and queue admission, seed validation, materialization against a fake
  daemon (ownership, keep-if-present, a missing variable refused, a mismatched artifact removed), the
  two runners against loopback servers, and the reach description.
- `go test ./internal/gameserver -run Handshake` and `go test ./internal/api -run OnlyAGame`.
- Live, as root on a Docker host: `JD_DEPLOY_LIVE=1 JD_BLUEPRINT_ONLY=minecraft-java,minecraft-bedrock`
  with `-run TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks` starts both on real Docker with their
  seeded file and asks their own checks, and `-run TestLiveReleaseSeedsAreWrittenIntoTheVolumeBeforeStart`
  writes seeds into a PostgreSQL image's data volume (owned 70:70), downloads a pinned file over
  https, refuses a wrong pin and leaves no helper behind; `-run TestLiveUDPProbeIsAnsweredThroughAPublishedUDPPort`
  starts Bedrock in RakNet mode through the runtime owner with a UDP-only primary, checks Docker
  publishes it on loopback alone, and has `udp_probe` answered through it.

## Not covered

A game whose players need more than one published port beside its primary uses `runtime.ports[]`
as any other plan does, with its own bind address; the reach readout and the loopback finding speak
for the primary port only. A Bedrock console would need the container's input attached, which the
runtime owner does not do. No definition fetches an artifact yet. Nothing here joins a game as a
client: whether a player on the internet completes NetherNet's negotiation through the host's NAT
(`server-udp-ports` also accepts a `public:local` mapping and a public address for that) is not
measured, only that each server is up, answers its own protocol and is published where the plan
says.
