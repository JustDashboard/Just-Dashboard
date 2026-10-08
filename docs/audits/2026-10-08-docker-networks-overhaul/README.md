# Docker Networks overhaul — 2026-10-08

`/docker/networks` was a column of eleven grey cards, each a name, a subnet and a container count,
with the members one press away in a sheet of label/value pairs. Nothing on it moved, nothing said
which network was busy, and nothing said that the address pool every `compose up` takes a subnet
from runs out. This records what the page became and the screenshots of before and after.

All screenshots are a production build against the mocked API in
`frontend/tests/browser/docker-networks-fixture.ts`: a host with Docker's three networks, a shop
whose database sits on an internal network behind its API, monitoring with Alertmanager killed and
holding no address, a shared `proxy` network with IPv6, a deployment's database network, a macvlan
on the LAN, and two networks nothing is attached to.

## What changed

| Before | After |
| --- | --- |
| A grey card per network: name, subnet, driver, "N containers" | A table: the network's colour down its edge, who made it, subnet and gateway, its members as their products with how many run, its traffic now with two minutes of shape, Attach and Remove (disabled with Docker's reason where they would fail) |
| Chips: All, User-created, Docker system | Owner chips that count and narrow — Compose, Standalone, Just Dashboard, Docker's own — and Unused in amber |
| Nothing above the list | Docker's identity line with a verdict (pool spent or nearly, else unused networks, else all in use) and a band: Traffic by network, the address pool as blocks, Recent network events |
| Members only inside the sheet, as a tree and a list | A containers table: each network a container is on, in that network's colour, its address there, the names it answers to, its traffic |
| Sheet: basics, access, a tree, labels, a member list | Sheet: four live readings, the bridge wired to each member with a pulse while it moves bytes, the members as a table with Detach, the settings |

## API additions (all additive)

- `GET /docker/networks/` carries `gateway` and `endpoints` (container id and name, IPv4/IPv6 with
  prefix, MAC) per network, joined from the container summary it already read for `usedBy`.
- Docker network events carry `container`: the id a `connect` or `disconnect` moved.

## Screenshots

| File | What |
| --- | --- |
| `before-page-1440.png`, `after-page-1440.png` | First screen at 1440 |
| `before-page-1280.png`, `after-page-1280.png` | First screen at 1280 |
| `after-networks-1440.png` | The networks table |
| `after-containers-1440.png` | The containers table |
| `after-on-proxy-1440.png` | A network's chip pressed: the containers on `proxy` |
| `before-sheet-1440.png`, `after-sheet-1440.png` | `shop_backend`'s sheet |
| `before-page-1024.png`, `after-page-1024.png` | 1024: the band two across, Recent under it |
| `before-page-phone.png`, `after-page-phone.png`, `after-phone-networks.png`, `after-phone-containers.png` | Phone (390) |
