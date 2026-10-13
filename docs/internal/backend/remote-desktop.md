# Remote Desktop

Remote Desktop opens the operator's own computers — a Windows PC, a Mac, a Linux desktop — in a
browser tab, from `/remote-desktop`. The backend is `internal/remotedesk`; the handlers are
`internal/api/handlers_remote_desktop.go` and `handlers_remote_desktop_stream.go`; the page is
`frontend/src/components/remote-desktop/`.

A session draws the computer one of two ways, switched in its header and kept in the address
(`?mode=stream`):

- **Control** (the default) is guacd speaking RDP or VNC to the computer. It needs nothing installed
  on the computer beyond its own Remote Desktop or Screen Sharing, but VNC is a pull protocol: each
  frame is asked for after the last arrived, so a 50 ms path caps it near twenty frames a second, and
  a Mac's Retina framebuffer makes each frame large.
- **Stream** is Sunshine on the computer encoding its screen as H.264 at its own frame rate, received
  by a bridge on this server and played in the page ([Stream mode](#stream-mode)).

## How a session travels

```
browser ──WebSocket "guacamole"──▶ backend ──TCP 127.0.0.1──▶ guacd ──RDP / VNC──▶ computer
         (Guacamole protocol)       (handshake,              (container the
                                     relay, audit)             dashboard owns)
```

- **guacd** is Apache Guacamole's protocol daemon. It speaks RDP and VNC to the computer and the
  Guacamole protocol to its client. The dashboard runs it as a container it owns (below).
- **The backend does guacd's handshake itself** (`tunnel.go`, `handshake`): `select`, the `args`
  guacd answers, then `size`, `audio`, `video`, `image`, `timezone`, `name` and `connect` with the
  values in the order guacd listed its arguments. The computer's address, account and password are
  among those values, so **the password never reaches the browser**; the page only ever sees the
  screen.
- **After `ready`, the backend relays** (`Session.Relay`). guacd's output is read as whole
  instructions and sent in text frames that always end on a `;`, at most about 8 KiB each and
  flushed as soon as nothing more is buffered, because the browser client parses every frame as
  whole instructions. From the browser, the tunnel's own channel (the empty opcode) is answered
  here: a `ping` is echoed, as Guacamole's own servlet does, and never forwarded; everything else is
  passed to guacd untouched.
- **Lengths count code points, not bytes** (`protocol.go`). The reader scans whole buffered chunks
  for rune starts rather than decoding rune by rune, since image blobs are megabytes of ASCII a
  second.
- **The browser end** is `guacamole-common-js` 1.5.0 (Apache-2.0), loaded on the session page only.
  Its `WebSocketTunnel` asks for the `guacamole` subprotocol, so the route upgrades through
  `wsx.Upgrader.UpgradeProtocol`, which answers with the protocol only when the client offered it.

**A Mac signs in with one of its accounts.** macOS Screen Sharing offers Apple's own security
type (30) before plain VNC passwords, and libvncclient takes the first it supports, so guacd signs
in with the account's short name and the password it logs in with, never a separate VNC password.
When the dashboard holds neither, guacd sends `required` and waits; the session page asks for the
names it lists and answers on the same connection with `argv` streams, saving them unless told not
to.

**Names are resolved here, not in guacd.** guacd's container resolves with musl through Docker's
DNS, which never applies the tailnet's search domain, so a MagicDNS short name such as `office-pc`
answered the probe (which runs on this server) and then failed inside guacd. `Connect` resolves
the computer's name with this server's resolver, preferring IPv4, and hands guacd the address; a
name that does not resolve is refused as such.

**The screen's size follows the frame only once it is up.** libvncclient crashes on a size update
that arrives while it is still signing in, and Apple's sign-in is slow enough for the page's first
resize to land in it: the guacd child dies with nothing sent to the page, which waited on
*Connecting* for ever. The page sends its size after the first frame, never before.

guacd's status codes travel to the page unchanged; `lib/remote-desktop.ts` (`sessionProblem`) says
them as what to check on the computer. A session that cannot start is refused in the protocol's own
terms (`RefuseSocket`: the tunnel's UUID, then an `error` instruction), so the page shows the reason
in place of the screen.

## The engine

`engine.go` (`dockerEngine`) owns one container, one bridge network and one volume, each named
`just-dashboard-remote-desktop` (the volume `…-files`) and labelled
`io.justdashboard.remote-desktop=engine`. Nothing about the engine is stored: Docker is the record, so
a container removed by hand reads as missing.

- **Image.** `guacamole/guacd` 1.6.0, pinned by digest (`GuacdImage`). An install pulls that exact
  reference when it is not cached, and refuses a cached image whose `RepoDigests` does not hold it.
- **Listening.** guacd's 4822 is published on `127.0.0.1` only, on the first free loopback port from
  4822 (`portalloc`). The status reads the port back from the binding and only from a `127.0.0.1`
  binding, so a container someone edited to publish on every address is not one the backend dials.
  Nothing binds a routable address; [invariant 7](../security/invariants.md#invariants-that-must-not-regress) is unchanged.
- **Hardening.** All capabilities dropped, `no-new-privileges`, 2 GiB memory without swap, 512 PIDs,
  half the default CPU shares (encoding a busy screen must not starve the dashboard), json-file logs
  capped at 2 × 5 MB, restart `unless-stopped`.
- **Reaching computers.** The container is on its own bridge; Docker's NAT carries its connections
  out through the host's routes, the tailnet's included, so guacd reaches a `100.x` computer the way
  the host does.
- **Files.** The volume is guacd's home. Each Windows session shares `/home/guacd/<computer id>` as a
  drive named *Just Dashboard*: a file sent from the page lands there, and a file the computer puts
  in the drive's `Download` folder arrives in the browser as a download. guacd creates only the last
  folder of a drive path, which is why each computer's folder sits directly in its home.
- **Install** is a job (`remote-desktop.install`, started exclusive of another): pull, verify the
  digest, network, volume, container, start, then wait for guacd to answer a `select rdp` with its
  `args`. Starting a stopped engine is the same command. **Remove** stops and removes the container,
  then the network (refusing if other containers joined it) and the volume, never a resource without
  the label. The image stays, so installing again does not download it. Removal is refused while a
  session is open.

## Stream mode

```
browser ──HTTPS / WebSocket──▶ backend ──HTTP 127.0.0.1──▶ Moonlight Web ──Moonlight protocol──▶ Sunshine
 (bridge page in an iframe,     (proxy under                 (container beside       (on the computer,
  WebCodecs)                     /remote-desktop/bridge)      guacd)                  TCP 47984-48010, UDP 47998-48000)
    ▲                                                            │
    └──────────── WebRTC, UDP on the tailnet address ────────────┘  (the picture, when the browser is on the tailnet)
```

- **Sunshine** (LizardByte, GPL-3.0) runs on the computer and is the only thing installed there.
  It encodes the screen in hardware and pushes frames as they change, so the picture runs at the
  computer's own rate.
- **The bridge** is Moonlight Web (`mrcreativ3001/moonlight-web-stream` 2.10.0, pinned by digest
  as `BridgeImage`), an unofficial Moonlight client that speaks Moonlight's protocol to Sunshine and
  hands the video to a browser page. `stream.go` runs it through the same `engineSpec` machinery as
  guacd (`bridgeSpec`): container `just-dashboard-remote-stream`, labelled
  `io.justdashboard.remote-desktop=stream`, on the engine's network, its 8080 published on
  `127.0.0.1` from 4823, volume `just-dashboard-remote-stream-data` for its pairings, the same
  hardening with 1 GiB. Its `config.json` is copied into the container before its first start
  (`bridgeSeed`): the URL prefix the proxy serves it at, the account header it trusts, pairing as
  *Just Dashboard*.
- **The picture goes over WebRTC on the tailnet, else over the dashboard's own connection.** On a
  server that is on a tailnet, the bridge publishes a block of 20 UDP ports (`bridgeMediaPorts`,
  the first free block from 40000) on this server's tailnet IPv4 address only
  (`remoteDesktopMedia`), and its WebRTC is held to them: `port_range` in its config, the same
  range in `WEBRTC_PORT_RANGE` (the image sets its own, which outranks the config), and `nat_1to1`
  naming the tailnet address as the host candidate, never the container's own; no STUN servers,
  no loopback candidates. `EngineStatus.Media` reads the block back from the container. The stream
  view's `transport` is `webrtc` only when the bridge has a block **and** the request came from a
  tailnet address (100.64.0.0/10, fd7a:115c:a1e0::/48): only such a browser can reach the block,
  and any other would wait out a failed connection first. The page then asks the bridge for
  `auto`, WebRTC with the WebSocket as its fallback. WebRTC carries the picture over UDP with no
  jitter buffer (`playoutDelayHint` 0), so a lost packet costs that packet rather than stalling
  the frames behind it the way TCP does; the signalling still rides the authenticated WebSocket,
  so a tailnet device without a dashboard session gets nothing from the ports. Off a tailnet the
  picture rides the dashboard's own HTTPS connection (`websocket`). The page decodes H.264 with
  WebCodecs, which needs the secure context the dashboard already is.
- **A bridge made by an older dashboard is made again.** The container carries
  `io.justdashboard.remote-desktop.revision` (`bridgeRevision` and the media address); an install
  that finds another one stops and removes the container, keeps its volume (so its pairings), and
  makes it again. `Server.Start` runs that install in the background for a running bridge
  (`UpgradeBridge`), so an upgrade of the dashboard upgrades the bridge, and a change of tailnet
  address moves its block.
- **The bridge's own page draws the stream.** The session page frames
  `/api/v1/remote-desktop/bridge/stream.html?hostId=…&appId=…` (Sunshine's *Desktop* app). The page
  reads its settings from this origin's `localStorage` (`mlSettings`), so the session page writes
  them just before it loads the frame: the transport, H.264, a frame queue of one (a frame that
  arrives late is dropped for the next rather than shown behind it), and the reader's quality
  (Light 720p60 at 8 Mb/s, Smooth 1080p60 at 20, Sharp 1440p60 at 40, Max 1440p120 at 80; a
  computer whose screen runs at 60 Hz sends 60), over whatever the reader changed in the stream's
  own menu.

**A session outlives its page.** Sessions are drawn by the shell, not the session page
(`components/remote-desktop/held-sessions.tsx`): the dashboard layout holds every session opened in
the tab and draws it after the page, the one the address names in the page's place and the rest
hidden but mounted. A hidden frame keeps its document and a hidden Guacamole display its tunnel,
so a look at another page and back finds the stream still playing and the Mac still signed in;
only **Disconnect**, a change of mode or leaving the dashboard ends one. While another page is up,
a dock at the window's corner lists them, one press back to each and one to end it, and the rail's
*Remote Desktop* leads back into the last one shown rather than to the overview. A held session
stops reading the computers until it is shown again, and Control does not send the computer a new
size while its frame has none.

**One pointer, and where the keys go.** The computer draws its own pointer into the stream, so the
browser's is hidden over the picture (a style the session page puts into the bridge's page, which
is this origin); *Show this computer's pointer too* in the tools brings it back. With the screen
focused, every key a page can receive goes to the computer; the system's own (Alt+Tab, the Windows
key, Cmd+Tab) and the browser's (Ctrl+W, Ctrl+T) never reach a page, except in fullscreen with the
keyboard locked (Chrome and Edge), which is what the session's fullscreen does, in both modes; held
Esc leaves it.

**The proxy** (`mountRemoteDesktopBridge`) is the only way to the bridge. It sits in its own group
in `routes.go`: authenticated, `system.admin` with a browser session, audited, on a rate limit of
its own (`streamLim`, 3000 a minute with a burst of 600, since the stream page loads about a hundred
files at once and would spend the whole API budget). The bridge's page posts without the dashboard's
CSRF header, which it cannot know, so the group has no `RequireCSRF`; instead every non-GET request
and every WebSocket upgrade must carry an `Origin` of this dashboard (`wsx.Upgrader.SameSite`).
Every request is rewritten: the browser's `Cookie`, `Authorization`, CSRF header and any copy of the
account header are dropped, and the account header is set to `just-dashboard`. The bridge creates
that one account on first use; who may stream is the dashboard's decision, so every administrator
sees the same pairings. Responses lose the bridge's own `Set-Cookie`, CSP and `X-Frame-Options` and
carry the dashboard's instead: a CSP of `'self'` plus the decoder's `wasm-unsafe-eval` and the one
inline script the pinned page ships (by hash), with `frame-ancestors 'self'`, and
`X-Frame-Options: SAMEORIGIN`. The stream's socket (`…/api/host/stream`) is audited as
`remote_desktop.stream.open` and `.close` with its length; the computer is named inside the socket,
so the record says a stream rather than which computer.

**Pairing.** Sunshine streams only to clients it was paired with, by a PIN typed into Sunshine's
own page on the computer (`https://localhost:47990`). `Service.Pair` resolves the computer's name
here (as `Connect` does), probes Sunshine, adds the address to the bridge
(`POST /api/host`), saves the bridge's id for it in `remote_desktop_computers.stream_host_id`, and
asks the bridge to pair. The bridge answers one JSON line with the PIN and holds the request open
until Sunshine takes it; the PIN goes back to the page at once and a goroutine keeps the request,
for up to ten minutes. `Service.Stream` reads where it got to. The session page shows the PIN, and
keeps it over the Control screen when the reader goes there to type it into Sunshine (the computer is
usually far away, and Control reaches its Sunshine page). Recent Sunshine versions list the waiting
request by the name Moonlight always pairs under, `roth`, which the page says. A computer whose
address changes is paired again (the `UPDATE` clears `stream_host_id` when `host` changes), as is
one the bridge no longer knows (reinstalled, or the host removed there).

**Sunshine is probed by asking it, not by connecting** (`probeSunshine`): a `GET /serverinfo` on
47989 with three seconds to answer, which is the bridge's own first request for a new host. A
Sunshine that is running but stuck (a macOS prompt nobody answered, or a hung copy holding the port)
accepts the connection and then answers nothing; a TCP probe called that "answering", and the
bridge's add then waited its fifteen seconds and failed as `context deadline exceeded`. It reads as
`closed` with the error `stuck`, the page names it and lists how to free it (the prompt, a restart,
Sunshine's log), and pairing refuses before the bridge is asked. A bridge that still times out is
said the same way.

**`deploy/Caddyfile`** frames nothing by default: `X-Frame-Options: DENY` is set only where the
response has none (`?X-Frame-Options`), so the backend's `SAMEORIGIN` on the bridge (and on a
deployment's preview frame) stands; and `Permissions-Policy` allows `autoplay=(self)`, since the
stream starts its muted video without a press, inside a frame of this origin.

## Computers and sessions

`computers.go`. A computer is a name, a system (`windows`, `macos`, `linux`), a host (an IP or a host
name), a protocol (`rdp`, `vnc`; a Mac defaults to VNC, which is Screen Sharing), a port (3389 or
5900 unless given), an account, a domain, a sealed password, an RDP security mode (`any`, `nla`,
`tls`, `rdp`), an RDP keyboard layout from the 23 guacd 1.6 compiles in (`Layouts`) and the tailnet
node it was added from. The password is sealed with the master key like every stored credential;
readers get `hasPassword`. On an edit a missing password keeps the sealed one and an empty one
clears it.

RDP connections always set `ignore-cert`: the path is the tailnet, which already authenticates and
encrypts both ends, and a PC's self-signed RDP certificate has nothing to be checked against. They
also set `resize-method=display-update`, so the PC's screen follows the browser window.

Every session is a row in `remote_desktop_sessions` (computer, who, start, end, outcome), opened
after a successful handshake and closed when the relay ends; rows go with their computer
(`ON DELETE CASCADE`). `Server.Start` closes rows a restart cut off as `interrupted`. The overview
reads the last 14 days.

The overview (`Service.Overview`) joins each computer with the tailnet device it matches — by the
node id it was added from, else by address, MagicDNS name or host name — and probes its port: a
device the tailnet reports offline is not dialled at all; every other one gets one TCP connect with
a 1.5 s timeout, whose time is shown as the computer's round trip.

## Routes

All under `/api/v1/remote-desktop`, inside the authenticated group.

| Method | Path | Capability | Does | Audited as |
| --- | --- | --- | --- | --- |
| `GET` | `/` | `system.admin` | The engine, the tailnet, every computer with its probe, the sessions and the layouts | — |
| `GET` | `/engine` | `system.admin` | The engine's status | — |
| `POST` | `/engine` | `system.admin` | Starts the install job, `202` with the job | `remote_desktop.engine.install` |
| `DELETE` | `/engine` | `destructive` through `s.destructive` | Removes the engine; `409` while a session is open | `remote_desktop.engine.remove` |
| `GET` | `/probe?host=&port=` | `system.admin` | One TCP probe of an address, for the add flow | — |
| `POST` | `/computers` | `system.admin` | Adds a computer | `remote_desktop.computer.add` (address, account, never the password) |
| `GET` | `/computers/{id}` | `system.admin` | One computer with its probe | — |
| `PUT` | `/computers/{id}` | `system.admin` | Edits it | `remote_desktop.computer.update` |
| `DELETE` | `/computers/{id}` | `destructive` through `s.destructive` | Forgets it and its history; `409` while it is in a session | `remote_desktop.computer.delete` |
| `GET` | `/computers/{id}/session` | `terminal`, browser session only (`RequireSession`) | The WebSocket the screen travels on | `remote_desktop.session.open`, `.refused`, `.close` |
| `GET` | `/stream` | `system.admin` | The stream bridge's status | — |
| `POST` | `/stream` | `system.admin` | Starts the bridge's install job (`remote-desktop.stream.install`), `202` with the job | `remote_desktop.stream.install` |
| `DELETE` | `/stream` | `destructive` through `s.destructive` | Removes the bridge and its pairings; `409` while a stream is open | `remote_desktop.stream.remove` |
| `GET` | `/computers/{id}/stream` | `system.admin` | The bridge, Sunshine's port, the pairing (and its PIN while it waits) and Sunshine's apps | — |
| `POST` | `/computers/{id}/stream/pair` | `system.admin` | Adds the computer to the bridge and starts pairing; answers with the PIN | `remote_desktop.stream.pair` |
| `DELETE` | `/computers/{id}/stream` | `system.admin` | Forgets the computer on the bridge, to pair it again | `remote_desktop.stream.unpair` |
| any | `/bridge/*` | `system.admin`, browser session, same-site `Origin` for writes and sockets | The bridge itself, proxied | mutations; `remote_desktop.stream.open`, `.close` |

A session is a keyboard and a mouse on another machine, the same trust as a terminal, so it takes
the terminal's capability; an API token has no screen to draw it on and is refused. The session is
logged when it opens, before the upgrade, so a crash mid-session still leaves the record.

## Tests

- `protocol_test.go`, `service_test.go`: the reader against every chunking, code-point lengths and
  malformed input; computers' validation and sealing; peer matching and skipped probes; the
  handshake against a fake guacd, whose `connect` values must line up with its `args`; the relay's
  UUID first, whole-instruction frames, echoed pings that never reach guacd; refusals; the history.
- `stream_test.go`: the bridge's seeded configuration, its WebRTC held to the published block and
  the tailnet address as its host candidate (and left alone without one); pairing against a fake bridge (the PIN at
  once, the bridge handed the address resolved here, a second press joining the waiting pairing,
  paired when Sunshine takes it, every request as the dashboard's account); a refused PIN that can
  start again without adding the host twice; a host the bridge lost being forgotten; a closed
  Sunshine port refused before anything is added; a Sunshine that accepts and never answers
  reported as stuck, and never handed to the bridge; a moved computer unpaired.
- `handlers_remote_desktop_test.go`: the routes round-trip without the password, are refused to a
  limited account, validate a probe's address and refuse an install without Docker. The bridge's
  proxy sends the dashboard's account whatever the browser claims and never its cookie, replaces the
  bridge's CSP, `X-Frame-Options` and cookie, refuses a write with no `Origin` or another site's,
  takes one from this dashboard without the CSRF header, and refuses a limited account and no
  session; a stopped bridge answers `503`, and pairing without it `409`. WebRTC is offered only
  to a browser on the tailnet and only when the bridge publishes a block.
- `live_test.go`, opt-in: `JD_REMOTE_DESKTOP_LIVE=1 go test ./internal/remotedesk -run Live -v`
  installs the real engine, starts a throwaway XFCE desktop over VNC and one over RDP on its network,
  opens a session to each and waits for the first frame (`img` and `sync`), then removes the engine
  unless `JD_REMOTE_DESKTOP_KEEP=1`. `TestLiveBridgeServesTheStreamPage` (`-run LiveBridge` alone)
  installs the real bridge under its own name with its seeded configuration, makes it again once it
  has an address to publish WebRTC on (its block read back, the range in its env), and reads its
  stream page and account under the dashboard's prefix and header.
- `frontend/tests/browser/remote-desktop.spec.ts` mocks the API and the tunnel: the overview, the
  install, the add flow's instructions, port watch and saved input, a session drawing what the
  tunnel sends, a download, a refusal in the reader's words, removal, a Mac asking for its account
  answered from the page and saved, and no size update before the first frame. Stream: pairing
  shows the PIN, keeps it over Control (which opens only then) until Sunshine takes it, then frames
  the bridge's page for the Desktop app with the WebSocket and quality settings written; the
  bridge's install as a job; a closed Sunshine port answered with the Mac's own steps; and a
  stuck Sunshine named as stuck, with how to free it and no Pair. A Control session survives a
  visit to another page (one socket, never closed, the dock leading back) until Disconnect closes
  it; a stream's frame is loaded once across the same round trip, and hides this computer's pointer
  until asked.

## Not here

- **Which computer a stream was.** The stream's socket names its host inside the socket, so a
  stream is audited as one but not counted in a computer's session history or time.
- **A shorter path.** The picture always passes through this server: the bridge runs here (it has
  no macOS build to run beside Sunshine), so a browser and a computer near each other but far from
  the server pay the server's distance twice.
- **Wake-on-LAN.** A sleeping computer cannot be reached; the add flow says to turn sleep off.
- **Windows Home** has no RDP server; the add flow sends it to TightVNC as a service, over VNC.
