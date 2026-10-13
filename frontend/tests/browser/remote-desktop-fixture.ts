import type { Page, Route } from "@playwright/test"
import { host, json, snapshot, user } from "./host-fixture"

const nowSec = Math.floor(Date.now() / 1000)

export const ENGINE = {
  state: "running",
  image: "guacamole/guacd@sha256:8974eaa9ba32f713daf311e7cc8cd7e4cdfba1edea39eed75524e78ef4b08f4f",
  version: "1.6.0",
  pulled: true,
  address: "127.0.0.1:4822",
  container: "0d4c1b2a9f11",
  startedAt: nowSec - 86_400,
}

export const BRIDGE = {
  state: "running",
  image:
    "mrcreativ3001/moonlight-web-stream@sha256:4ee561ec4043526e93b0b3dba4926031c86ee3c9dab0333475a6b3c7117c701a",
  version: "2.10.0",
  pulled: true,
  address: "127.0.0.1:4823",
  container: "147b6c200e7f",
  startedAt: nowSec - 3600,
}

const peer = (id: string, name: string, os: string, address: string, extra = {}) => ({
  id,
  name: name.toUpperCase(),
  dnsName: `${name}.example.ts.net.`,
  os,
  address,
  online: true,
  direct: true,
  relay: "",
  lastSeen: nowSec,
  ...extra,
})

export const PEERS = [
  peer("n11", "office-pc", "windows", "100.101.12.7"),
  peer("n12", "gaming-pc", "windows", "100.101.12.8"),
  peer("n05", "kids-laptop", "windows", "100.101.12.9", {
    online: false,
    lastSeen: nowSec - 3 * 3600,
  }),
  peer("n06", "workshop-pc", "windows", "100.101.12.10", { direct: false, relay: "fra" }),
  peer("mac", "studio-mac", "macOS", "100.101.12.11"),
  peer("phone", "phone", "iOS", "100.101.12.12"),
]

const computer = (id: string, name: string, extra: Record<string, unknown>) => ({
  id,
  name,
  os: "windows",
  host: name,
  protocol: "rdp",
  port: 3389,
  username: "wayy",
  domain: "",
  hasPassword: true,
  security: "",
  layout: "",
  peerId: "",
  createdAt: nowSec - 86_400 * 3,
  updatedAt: nowSec - 86_400 * 3,
  lastSessionAt: 0,
  streamHostId: 0,
  live: 0,
  reach: { state: "open", latencyMs: 43.4, checkedAt: nowSec },
  ...extra,
})

export const OFFICE = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
export const GAMING = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
export const LAPTOP = "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c"
export const MAC = "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d"

export function computers() {
  return [
    computer(OFFICE, "office-pc", {
      peerId: "n11",
      peer: PEERS[0],
      live: 1,
      lastSessionAt: nowSec - 600,
      createdAt: nowSec - 86_400 * 9,
    }),
    computer(GAMING, "gaming-pc", {
      peerId: "n12",
      peer: PEERS[1],
      reach: { state: "open", latencyMs: 42.9, checkedAt: nowSec },
      lastSessionAt: nowSec - 86_400,
      createdAt: nowSec - 86_400 * 8,
    }),
    computer(LAPTOP, "kids-laptop", {
      peerId: "n05",
      peer: PEERS[2],
      protocol: "vnc",
      port: 5900,
      username: "",
      reach: { state: "offline", checkedAt: nowSec },
      createdAt: nowSec - 86_400 * 7,
    }),
    computer(MAC, "studio-mac", {
      os: "macos",
      protocol: "vnc",
      port: 5900,
      username: "",
      peerId: "mac",
      peer: PEERS[4],
      streamHostId: 41,
      reach: { state: "closed", error: "refused", checkedAt: nowSec },
      createdAt: nowSec - 86_400 * 6,
    }),
  ]
}

export function sessions() {
  return [
    {
      id: 9,
      computerId: OFFICE,
      actor: "operator",
      startedAt: nowSec - 600,
      endedAt: 0,
      outcome: "",
    },
    {
      id: 8,
      computerId: GAMING,
      actor: "operator",
      startedAt: nowSec - 86_400 - 2_700,
      endedAt: nowSec - 86_400,
      outcome: "closed",
    },
    {
      id: 7,
      computerId: OFFICE,
      actor: "operator",
      startedAt: nowSec - 2 * 86_400,
      endedAt: nowSec - 2 * 86_400 + 5_400,
      outcome: "closed",
    },
    {
      id: 6,
      computerId: MAC,
      actor: "operator",
      startedAt: nowSec - 4 * 86_400,
      endedAt: nowSec - 4 * 86_400 + 900,
      outcome: "closed",
    },
  ]
}

export const LAYOUTS = [
  "de-de-qwertz",
  "en-gb-qwerty",
  "en-us-qwerty",
  "fr-fr-azerty",
  "ro-ro-qwerty",
]

/** A computer's stream as the backend reads it: here Sunshine answers and nothing is paired yet. */
export function streamView(extra: Record<string, unknown> = {}) {
  return {
    bridge: BRIDGE,
    address: "100.101.12.40",
    sunshine: { state: "open", latencyMs: 38.2, checkedAt: nowSec },
    hostId: 0,
    paired: false,
    pairing: false,
    apps: [],
    ...extra,
  }
}

export const PAIRED = {
  hostId: 41,
  paired: true,
  apps: [
    { id: 7, title: "Steam Big Picture" },
    { id: 3, title: "Desktop" },
  ],
}

export function overview(extra: Record<string, unknown> = {}) {
  return {
    engine: ENGINE,
    stream: BRIDGE,
    tailnet: {
      installed: true,
      self: { ...peer("self", "web-01", "linux", "100.101.12.1"), id: "" },
      peers: PEERS,
    },
    computers: computers(),
    sessions: sessions(),
    layouts: LAYOUTS,
    ...extra,
  }
}

export type RemoteCalls = {
  installs: number
  added: Record<string, unknown>[]
  updated: { id: string; body: Record<string, unknown> }[]
  deleted: string[]
  probes: string[]
  streamInstalls: number
  pairs: string[]
  unpaired: string[]
}

/**
 * The Remote Desktop API as the Go handlers answer it: the overview, the
 * install job and its stream, the probe the add flow watches, and the
 * computers' create, edit and delete. `probe` decides whether the port in
 * the add flow answers; the session tunnel is each spec's own. Stream mode's
 * bridge, pairing and stream page are here too: pairing answers with a PIN,
 * and a spec decides when Sunshine takes it by changing `state.stream`.
 */
export async function mockRemoteDesktop(
  page: Page,
  options: {
    overview?: Record<string, unknown>
    probeOpen?: () => boolean
    stream?: Record<string, unknown>
  } = {},
) {
  const state = { overview: options.overview ?? overview(), stream: options.stream ?? streamView() }
  const calls: RemoteCalls = {
    installs: 0,
    added: [],
    updated: [],
    deleted: [],
    probes: [],
    streamInstalls: 0,
    pairs: [],
    unpaired: [],
  }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: host }))
    socket.send(JSON.stringify({ type: "metrics", data: snapshot }))
  })
  await page.routeWebSocket("**/api/v1/jobs/**", (socket) => {
    const started = new Date().toISOString()
    const job = {
      id: "job-1",
      kind: "remote-desktop.install",
      title: "Install Remote Desktop",
      status: "succeeded",
      exitCode: 0,
      startedAt: started,
      endedAt: started,
      lines: 2,
    }
    socket.send(
      JSON.stringify({
        type: "output",
        data: [
          { seq: 1, stream: "status", text: "Installing guacd 1.6.0", at: started },
          { seq: 2, stream: "stdout", text: "guacd 1.6.0 is ready", at: started },
        ],
      }),
    )
    socket.send(JSON.stringify({ type: "job", data: job }))
  })
  await page.route("**/api/v1/**", async (route: Route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/system/host") return json(route, host)
    if (path === "/updates/self") return json(route, { current: "0.7.4", latest: "0.7.4" })
    if (path === "/remote-desktop/" && method === "GET") return json(route, state.overview)
    if (path === "/remote-desktop/engine" && method === "POST") {
      calls.installs += 1
      state.overview = { ...state.overview, engine: ENGINE }
      return route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({
          id: "job-1",
          kind: "remote-desktop.install",
          title: "Install Remote Desktop",
          status: "running",
          exitCode: 0,
          startedAt: new Date().toISOString(),
          lines: 0,
        }),
      })
    }
    if (path.startsWith("/remote-desktop/bridge/"))
      return route.fulfill({
        status: 200,
        contentType: "text/html",
        body: "<!doctype html><title>Stream</title><p>The bridge's stream page</p>",
      })
    if (path === "/remote-desktop/stream" && method === "POST") {
      calls.streamInstalls += 1
      state.stream = { ...state.stream, bridge: BRIDGE }
      return route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({
          id: "job-1",
          kind: "remote-desktop.stream.install",
          title: "Install Stream mode",
          status: "running",
          exitCode: 0,
          startedAt: new Date().toISOString(),
          lines: 0,
        }),
      })
    }
    const stream = path.match(/^\/remote-desktop\/computers\/([0-9a-f]{32})\/stream(\/pair)?$/)
    if (stream && method === "GET") return json(route, state.stream)
    if (stream?.[2] && method === "POST") {
      calls.pairs.push(stream[1])
      state.stream = { ...state.stream, hostId: 41, pairing: true, pin: "4821" }
      return json(route, state.stream)
    }
    if (stream && method === "DELETE") {
      calls.unpaired.push(stream[1])
      state.stream = streamView()
      return route.fulfill({ status: 204 })
    }
    if (path === "/remote-desktop/probe") {
      calls.probes.push(`${url.searchParams.get("host")}:${url.searchParams.get("port")}`)
      const open = options.probeOpen?.() ?? true
      return json(
        route,
        open
          ? { state: "open", latencyMs: 41.2, checkedAt: nowSec }
          : { state: "closed", error: "refused", checkedAt: nowSec },
      )
    }
    if (path === "/remote-desktop/computers" && method === "POST") {
      const body = request.postDataJSON() as Record<string, unknown>
      calls.added.push(body)
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          ...computer("0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e", String(body.name), body),
          hasPassword: Boolean(body.password),
        }),
      })
    }
    const one = path.match(/^\/remote-desktop\/computers\/([0-9a-f]{32})$/)
    if (one && method === "PUT") {
      calls.updated.push({ id: one[1], body: request.postDataJSON() })
      return json(
        route,
        computers().find((c) => c.id === one[1]),
      )
    }
    if (one && method === "DELETE") {
      calls.deleted.push(one[1])
      return route.fulfill({ status: 204 })
    }
    return json(route, [])
  })
  return { calls, state }
}

/** Guacamole's wire format: length-prefixed elements, a comma between, a semicolon at the end. */
export function instruction(opcode: string, ...args: string[]) {
  return [opcode, ...args].map((part) => `${[...part].length}.${part}`).join(",") + ";"
}
