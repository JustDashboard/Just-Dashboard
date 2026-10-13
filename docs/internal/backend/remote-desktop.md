# Remote Desktop

Remote Desktop opens the operator's own computers — a Windows PC, a Mac, a Linux desktop — in a
browser tab, from `/remote-desktop`. The backend is `internal/remotedesk`; the handlers are
`internal/api/handlers_remote_desktop.go`; the page is `frontend/src/components/remote-desktop/`.

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

A session is a keyboard and a mouse on another machine, the same trust as a terminal, so it takes
the terminal's capability; an API token has no screen to draw it on and is refused. The session is
logged when it opens, before the upgrade, so a crash mid-session still leaves the record.

## Tests

- `protocol_test.go`, `service_test.go`: the reader against every chunking, code-point lengths and
  malformed input; computers' validation and sealing; peer matching and skipped probes; the
  handshake against a fake guacd, whose `connect` values must line up with its `args`; the relay's
  UUID first, whole-instruction frames, echoed pings that never reach guacd; refusals; the history.
- `handlers_remote_desktop_test.go`: the routes round-trip without the password, are refused to a
  limited account, validate a probe's address and refuse an install without Docker.
- `live_test.go`, opt-in: `JD_REMOTE_DESKTOP_LIVE=1 go test ./internal/remotedesk -run Live -v`
  installs the real engine, starts a throwaway XFCE desktop over VNC and one over RDP on its network,
  opens a session to each and waits for the first frame (`img` and `sync`), then removes the engine
  unless `JD_REMOTE_DESKTOP_KEEP=1`.
- `frontend/tests/browser/remote-desktop.spec.ts` mocks the API and the tunnel: the overview, the
  install, the add flow's instructions, port watch and saved input, a session drawing what the
  tunnel sends, a download, a refusal in the reader's words, and removal.

## Not here

- **Stream mode.** Video-quality streaming (Sunshine on the computer and a WebRTC bridge here) is not
  part of this module; Guacamole sends the screen as images, which is right for using a computer and
  wrong for watching video on it.
- **Wake-on-LAN.** A sleeping computer cannot be reached; the add flow says to turn sleep off.
- **Windows Home** has no RDP server; the add flow sends it to TightVNC as a service, over VNC.
