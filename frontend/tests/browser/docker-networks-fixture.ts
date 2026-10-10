import type { Page, Route } from "@playwright/test"

/**
 * A server's Docker networks, as the Networks page reads them: the network
 * list with each member's address (`GET /docker/networks/`), one network's
 * inspect, every container and its live counters over the containers socket,
 * the bridges' two-second traffic, the daemon's network events and the
 * engine's own description of its address pools.
 *
 * Eleven networks that each say something different: Docker's own three, a
 * shop whose database sits on an internal network behind its API, monitoring
 * with Alertmanager killed and so holding no address, a shared `proxy`
 * network made by hand with IPv6 on, a database network this dashboard made
 * for a deployment, a macvlan on the LAN, and two networks nothing is on — one
 * left behind by a stack that is down and one made by hand and forgotten.
 */

const NOW = Date.now()
const SEC = 1000
const MIN = 60 * SEC
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const KB = 1024
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString()

export const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(0),
    createdAt: iso(0),
  },
}

type NetSpec = {
  id: string
  name: string
  driver?: string
  subnets?: string[]
  gateway?: string
  internal?: boolean
  attachable?: boolean
  ipv6?: boolean
  labels?: Record<string, string>
  bridge?: string
  options?: Record<string, string>
  created: number
  /** Bytes a second, in and out, on its bridge. */
  rate?: [number, number]
}

const id = (seed: string) => seed.repeat(64).slice(0, 64)

const compose = (project: string, network: string) => ({
  "com.docker.compose.project": project,
  "com.docker.compose.network": network,
  "com.docker.compose.version": "2.29.7",
})

export const NETS: NetSpec[] = [
  {
    id: id("1a2b3c"),
    name: "bridge",
    subnets: ["172.17.0.0/16"],
    gateway: "172.17.0.1",
    bridge: "docker0",
    created: 41 * DAY,
    rate: [9 * KB, 3 * KB],
  },
  { id: id("2b3c4d"), name: "host", driver: "host", created: 41 * DAY },
  { id: id("3c4d5e"), name: "none", driver: "null", created: 41 * DAY },
  {
    id: id("4d5e6f"),
    name: "shop_default",
    subnets: ["172.18.0.0/16"],
    gateway: "172.18.0.1",
    labels: compose("shop", "default"),
    created: 12 * DAY,
    rate: [2.4 * 1024 * KB, 310 * KB],
  },
  {
    id: id("5e6f7a"),
    name: "shop_backend",
    subnets: ["172.19.0.0/16"],
    gateway: "172.19.0.1",
    internal: true,
    labels: compose("shop", "backend"),
    created: 12 * DAY,
    rate: [880 * KB, 1.3 * 1024 * KB],
  },
  {
    id: id("6f7a8b"),
    name: "monitoring_default",
    subnets: ["172.20.0.0/16"],
    gateway: "172.20.0.1",
    labels: compose("monitoring", "default"),
    created: 30 * DAY,
    rate: [182 * KB, 64 * KB],
  },
  {
    id: id("7a8b9c"),
    name: "proxy",
    subnets: ["172.21.0.0/16", "fd00:dead:beef::/48"],
    gateway: "172.21.0.1",
    attachable: true,
    ipv6: true,
    created: 2 * HOUR,
    rate: [1.1 * 1024 * KB, 2.9 * 1024 * KB],
  },
  {
    id: id("8b9c0d"),
    name: "jd-e6-db-gqfymsmso5i7",
    subnets: ["172.22.0.0/16"],
    gateway: "172.22.0.1",
    labels: {
      "io.just-dashboard.managed": "true",
      "io.just-dashboard.database-network": "GQFYMSMSO5I7OMYJG6HQRJMZIG",
      "io.just-dashboard.environment-id": "6",
    },
    created: 5 * DAY,
    rate: [41 * KB, 37 * KB],
  },
  {
    id: id("9c0d1e"),
    name: "old-staging",
    subnets: ["172.23.0.0/16"],
    gateway: "172.23.0.1",
    attachable: true,
    created: 64 * DAY,
  },
  {
    id: id("0d1e2f"),
    name: "wiki_default",
    subnets: ["172.24.0.0/16"],
    gateway: "172.24.0.1",
    labels: compose("wiki", "default"),
    created: 9 * DAY,
  },
  {
    id: id("e2f3a4"),
    name: "lan",
    driver: "macvlan",
    subnets: ["192.168.1.0/24"],
    gateway: "192.168.1.1",
    options: { parent: "enp3s0" },
    created: 20 * DAY,
  },
]

type CtSpec = {
  id: string
  name: string
  image: string
  stack?: string
  service?: string
  state?: string
  status?: string
  labels?: Record<string, string>
  ports?: { ip?: string; privatePort: number; publicPort?: number; type: string }[]
  /** Network name → its address on it; an empty string holds none. */
  on: Record<string, string>
  aliases?: string[]
  /** Bytes a second, in and out. */
  rate?: [number, number]
  started: number
}

export const CTS: CtSpec[] = [
  {
    id: id("a1"),
    name: "portainer",
    image: "portainer/portainer-ce:2.21.4",
    on: { bridge: "172.17.0.2/16" },
    ports: [{ ip: "0.0.0.0", privatePort: 9443, publicPort: 9443, type: "tcp" }],
    rate: [3 * KB, 1 * KB],
    started: 6 * DAY,
  },
  {
    id: id("b2"),
    name: "watchtower",
    image: "containrrr/watchtower:1.7.1",
    on: { bridge: "172.17.0.3/16" },
    rate: [2 * KB, 1 * KB],
    started: 6 * DAY,
  },
  {
    id: id("c3"),
    name: "node-exporter",
    image: "prom/node-exporter:v1.8.2",
    on: { host: "" },
    started: 30 * DAY,
  },
  {
    id: id("d4"),
    name: "shop-web-1",
    image: "nginx:1.27-alpine",
    stack: "shop",
    service: "web",
    on: { proxy: "172.21.0.3/16", shop_default: "172.18.0.2/16" },
    rate: [1.6 * 1024 * KB, 2.1 * 1024 * KB],
    started: 3 * MIN,
  },
  {
    id: id("e5"),
    name: "shop-api-1",
    image: "node:22-alpine",
    stack: "shop",
    service: "api",
    on: { shop_backend: "172.19.0.3/16", shop_default: "172.18.0.3/16" },
    rate: [920 * KB, 1.4 * 1024 * KB],
    started: 26 * MIN,
  },
  {
    id: id("f6"),
    name: "shop-worker-1",
    image: "node:22-alpine",
    stack: "shop",
    service: "worker",
    on: { shop_backend: "172.19.0.4/16", shop_default: "172.18.0.4/16" },
    rate: [140 * KB, 96 * KB],
    started: 26 * MIN,
  },
  {
    id: id("a7"),
    name: "shop-db-1",
    image: "postgres:16-alpine",
    stack: "shop",
    service: "db",
    on: { shop_backend: "172.19.0.2/16" },
    aliases: ["database"],
    rate: [610 * KB, 980 * KB],
    started: 12 * DAY,
  },
  {
    id: id("b8"),
    name: "shop-redis-1",
    image: "redis:7.4-alpine",
    stack: "shop",
    service: "redis",
    on: { shop_backend: "172.19.0.5/16" },
    rate: [210 * KB, 160 * KB],
    started: 12 * DAY,
  },
  {
    id: id("c9"),
    name: "monitoring-prometheus-1",
    image: "prom/prometheus:v2.54.1",
    stack: "monitoring",
    service: "prometheus",
    on: { monitoring_default: "172.20.0.2/16" },
    rate: [120 * KB, 18 * KB],
    started: 30 * DAY,
  },
  {
    id: id("d0"),
    name: "monitoring-grafana-1",
    image: "grafana/grafana:11.2.0",
    stack: "monitoring",
    service: "grafana",
    on: { monitoring_default: "172.20.0.3/16", proxy: "172.21.0.4/16" },
    rate: [36 * KB, 210 * KB],
    started: 9 * MIN,
  },
  {
    id: id("e1"),
    name: "monitoring-alertmanager-1",
    image: "prom/alertmanager:v0.27.0",
    stack: "monitoring",
    service: "alertmanager",
    state: "exited",
    status: "Exited (137) 12 minutes ago",
    on: { monitoring_default: "" },
    started: 12 * MIN,
  },
  {
    id: id("f2"),
    name: "monitoring-cadvisor-1",
    image: "gcr.io/cadvisor/cadvisor:v0.49.1",
    stack: "monitoring",
    service: "cadvisor",
    on: { monitoring_default: "172.20.0.4/16" },
    rate: [24 * KB, 41 * KB],
    started: 30 * DAY,
  },
  {
    id: id("a3"),
    name: "caddy",
    image: "caddy:2.8-alpine",
    on: { proxy: "172.21.0.2/16" },
    ports: [
      { ip: "0.0.0.0", privatePort: 80, publicPort: 80, type: "tcp" },
      { ip: "0.0.0.0", privatePort: 443, publicPort: 443, type: "tcp" },
    ],
    rate: [2.8 * 1024 * KB, 1.2 * 1024 * KB],
    started: 2 * HOUR,
  },
  {
    id: id("b4"),
    name: "lampino-db",
    image: "postgres:17-alpine",
    labels: { "io.just-dashboard.managed": "true", "io.just-dashboard.environment-id": "6" },
    on: { "jd-e6-db-gqfymsmso5i7": "172.22.0.2/16" },
    aliases: ["postgres"],
    rate: [22 * KB, 30 * KB],
    started: 5 * DAY,
  },
  {
    id: id("c5"),
    name: "lampino-r4",
    image: "ghcr.io/lampino/web:4",
    labels: {
      "io.just-dashboard.managed": "true",
      "io.just-dashboard.environment-id": "6",
      "org.opencontainers.image.source": "https://github.com/lampino/web",
    },
    on: { "jd-e6-db-gqfymsmso5i7": "172.22.0.3/16" },
    rate: [19 * KB, 11 * KB],
    started: 2 * DAY,
  },
  {
    id: id("d6"),
    name: "homeassistant",
    image: "ghcr.io/home-assistant/home-assistant:stable",
    on: { lan: "192.168.1.40/24" },
    rate: [48 * KB, 12 * KB],
    started: 20 * DAY,
  },
]

export function container(spec: CtSpec) {
  const running = (spec.state ?? "running") === "running"
  const labels = {
    ...spec.labels,
    ...(spec.stack
      ? {
          "com.docker.compose.project": spec.stack,
          "com.docker.compose.service": spec.service ?? spec.name,
        }
      : {}),
  }
  return {
    id: spec.id,
    names: [spec.name],
    name: spec.name,
    image: spec.image,
    imageId: `sha256:${spec.id}`,
    command: "",
    state: spec.state ?? "running",
    status: spec.status ?? "Up",
    createdAt: iso(spec.started + DAY),
    startedAt: running ? iso(spec.started) : undefined,
    uptimeSeconds: running ? spec.started / 1000 : 0,
    ports: (spec.ports ?? []).map((p) => ({
      ip: p.ip,
      privatePort: p.privatePort,
      publicPort: p.publicPort,
      type: p.type,
    })),
    labels,
    networks: Object.keys(spec.on).sort(),
    composeStack: spec.stack,
    composeService: spec.service,
    exposure: [],
    hasHealthcheck: false,
    inspected: running,
  }
}

export const CONTAINERS = CTS.map(container)

function membersOf(net: NetSpec) {
  return CTS.filter((c) => net.name in c.on).sort((a, b) => a.name.localeCompare(b.name))
}

export function network(net: NetSpec) {
  const members = membersOf(net)
  return {
    id: net.id,
    name: net.name,
    driver: net.driver ?? "bridge",
    scope: "local",
    internal: net.internal ?? false,
    attachable: net.attachable ?? false,
    ipv6: net.ipv6 ?? false,
    created: iso(net.created),
    labels: net.labels ?? {},
    subnets: net.subnets ?? [],
    containers: members.length,
    usedBy: members.map((c) => c.name),
    bridge:
      (net.driver ?? "bridge") === "bridge"
        ? (net.bridge ?? `br-${net.id.slice(0, 12)}`)
        : undefined,
    gateway: net.gateway,
    endpoints: members.map((c) => ({
      container: c.id,
      name: c.name,
      ipv4: c.on[net.name] || undefined,
      ipv6:
        net.ipv6 && c.on[net.name]
          ? `fd00:dead:beef::${c.on[net.name].split("/")[0].split(".").pop()}/48`
          : undefined,
      mac: c.on[net.name]
        ? `02:42:ac:${c.id.slice(0, 2)}:00:0${members.indexOf(c) + 2}`
        : undefined,
    })),
  }
}

export const NETWORKS = NETS.map(network)

export function networkDetail(net: NetSpec) {
  const base = network(net)
  return {
    ...base,
    options: net.options ?? {},
    system: ["bridge", "host", "none"].includes(net.name),
    endpoints: undefined,
    members: membersOf(net).map((c) => {
      const endpoint = base.endpoints.find((e) => e.container === c.id)
      const custom = net.name === "bridge" || net.name === "host" ? [] : [c.name, c.id.slice(0, 12)]
      return {
        id: c.id,
        name: c.name,
        ipv4: endpoint?.ipv4,
        ipv6: endpoint?.ipv6,
        mac: endpoint?.mac,
        aliases:
          net.name === "bridge" || net.name === "host"
            ? []
            : [...(c.service ? [c.service] : []), ...custom, ...(c.aliases ?? [])],
        state: c.state ?? "running",
        stack: c.stack,
      }
    }),
  }
}

/** Two seconds of a counter, on a wave so every line has some weather in it. */
function wave(base: number, t: number, phase: number) {
  if (base === 0) return 0
  const swing = Math.sin(t / 9 + phase) * 0.28 + Math.sin(t / 3.7 + phase * 2) * 0.12 + 1
  return Math.max(0, Math.round(base * swing))
}

/** Every bridge's readings up to now, the way `/network/traffic/live` answers `since`. */
export function liveTraffic(since: number) {
  const now = Math.floor(Date.now() / 2000) * 2
  const series: Record<string, { t: number; rx: number; tx: number }[]> = {}
  NETS.forEach((net, index) => {
    const device = network(net).bridge
    if (!device) return
    const points = []
    for (let t = now - 118; t <= now; t += 2) {
      if (t <= since) continue
      points.push({
        t,
        rx: wave(net.rate?.[0] ?? 0, t, index),
        tx: wave(net.rate?.[1] ?? 0, t, index + 1),
      })
    }
    series[device] = points
  })
  return { now, series }
}

/** A frame of `docker stats` for every running container, its counters `seconds` in. */
export function statsFrame(seconds: number) {
  return CTS.filter((c) => (c.state ?? "running") === "running").map((c, index) => {
    const [rx, tx] = c.rate ?? [0, 0]
    const total = (rate: number, phase: number) => {
      let sum = 1_000_000 * (index + 1)
      for (let s = 0; s <= seconds; s += 2) sum += wave(rate, s, phase) * 2
      return sum
    }
    const rxBytes = total(rx, index)
    const txBytes = total(tx, index + 1)
    return {
      id: c.id,
      name: c.name,
      ts: new Date(NOW + seconds * 1000).toISOString(),
      cpuPercent: 2 + index,
      cpuReady: true,
      memUsage: (40 + index * 23) * 1024 * KB,
      memLimit: 8 * 1024 * 1024 * KB,
      memLimited: false,
      memPercent: 1,
      memHostPercent: 1,
      hostCpus: 4,
      netRx: rxBytes,
      netTx: txBytes,
      // A container on the host network has no interface of its own to count.
      networks:
        "host" in c.on
          ? {}
          : { eth0: { rxBytes, txBytes, rxPackets: rxBytes / 900, txPackets: txBytes / 900 } },
      networkAvailable: !("host" in c.on),
      blockRead: 0,
      blockWrite: 0,
      pids: 8,
      onlineCpus: 4,
      cpuTotal: 1_000_000 * seconds,
      systemCpu: 4_000_000 * seconds,
    }
  })
}

const netId = (name: string) => NETS.find((n) => n.name === name)!.id
const ctId = (name: string) => CTS.find((c) => c.name === name)?.id

function event(
  msAgo: number,
  action: "connect" | "disconnect" | "create" | "destroy",
  name: string,
  member?: string,
) {
  const message =
    action === "create"
      ? `created network ${name}`
      : action === "destroy"
        ? `deleted network ${name}`
        : action === "connect"
          ? `a container joined network ${name}`
          : `a container left network ${name}`
  return {
    time: iso(msAgo),
    type: "network",
    action,
    name,
    id: NETS.find((n) => n.name === name)?.id ?? id("ff"),
    container: member ? (ctId(member) ?? id("99")) : undefined,
    message,
    level: action === "destroy" ? "notice" : "info",
    source: "docker",
  }
}

export const EVENTS = [
  event(3 * MIN, "connect", "proxy", "shop-web-1"),
  event(3 * MIN + 2 * SEC, "connect", "shop_default", "shop-web-1"),
  event(3 * MIN + 9 * SEC, "disconnect", "shop_default", "shop-web-1"),
  event(9 * MIN, "connect", "proxy", "monitoring-grafana-1"),
  event(12 * MIN, "disconnect", "monitoring_default", "monitoring-alertmanager-1"),
  event(26 * MIN, "connect", "shop_backend", "shop-api-1"),
  event(26 * MIN + SEC, "connect", "shop_backend", "shop-worker-1"),
  event(2 * HOUR, "create", "proxy"),
  event(2 * HOUR + 4 * SEC, "connect", "proxy", "caddy"),
  event(3 * HOUR, "disconnect", "staging_default", "staging-web-1"),
  event(3 * HOUR + 3 * SEC, "destroy", "staging_default"),
]

export const INFO = {
  ID: "atlas",
  Name: "atlas",
  ServerVersion: "29.8.0",
  Driver: "overlay2",
  OperatingSystem: "Ubuntu 24.04.1 LTS",
  KernelVersion: "6.8.0-45-generic",
  Containers: CTS.length,
  ContainersRunning: CTS.filter((c) => (c.state ?? "running") === "running").length,
  Images: 21,
  // Unset in daemon.json, so the engine reports none and allocates from its
  // built-in pools: fifteen /16s in 172.16.0.0/12 and sixteen /20s in
  // 192.168.0.0/16.
  DefaultAddressPools: null,
}

export const HOST = {
  hostname: "atlas",
  os: "linux",
  platform: "ubuntu",
  platformVersion: "24.04",
  kernelVersion: "6.8.0-45-generic",
  kernelArch: "x86_64",
  cpuModel: "AMD EPYC 7B13",
  cpuCores: 4,
  processes: 284,
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

export type NetworkMocks = {
  /** Every write posted, as `METHOD path` with its body. */
  writes: { method: string; path: string; body?: unknown }[]
}

/**
 * The server behind the Networks page. `networks` replaces the list, for a
 * spec that needs a host with nothing on it.
 */
export async function mockNetworks(
  page: Page,
  options: { networks?: object[]; events?: object[]; info?: object } = {},
): Promise<NetworkMocks> {
  const mocks: NetworkMocks = { writes: [] }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: HOST }))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/containers\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "containers", data: CONTAINERS }))
    let seconds = 0
    socket.send(JSON.stringify({ type: "stats", data: statsFrame(seconds) }))
    const timer = setInterval(() => {
      seconds += 2
      try {
        socket.send(JSON.stringify({ type: "stats", data: statsFrame(seconds) }))
      } catch {
        clearInterval(timer)
      }
    }, 2000)
    socket.onClose(() => clearInterval(timer))
  })
  await page.routeWebSocket(/\/api\/v1\/docker\/events\/stream/, (socket) => {
    socket.send(JSON.stringify({ type: "events", data: options.events ?? EVENTS }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = route.request().method()
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.7.1", latest: "0.7.1" })
    if (path === "/system/host") return json(route, HOST)
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "29.8.0" })
    if (path === "/docker/info") return json(route, options.info ?? INFO)
    if (path === "/docker/containers/") return json(route, CONTAINERS)
    if (path === "/docker/events") {
      return json(route, {
        listening: true,
        since: iso(41 * DAY),
        buffered: 11,
        events: options.events ?? EVENTS,
      })
    }
    if (path === "/network/traffic/live") {
      return json(route, liveTraffic(Number(url.searchParams.get("since") ?? 0)))
    }
    if (path === "/docker/networks/" && method === "GET") {
      return json(route, options.networks ?? NETWORKS)
    }
    // `prune` and `drivers` are routes of their own, as the server's router
    // matches them before a network's id.
    const one = path.match(/^\/docker\/networks\/([^/]+)$/)
    if (one && method === "GET" && !["prune", "drivers"].includes(one[1])) {
      const net = NETS.find((n) => n.id === decodeURIComponent(one[1]))
      return net
        ? json(route, networkDetail(net))
        : json(route, { error: { code: "not_found", message: "No such network" } }, 404)
    }
    // What attaching, detaching or removing would disturb, and what a prune
    // would take. Nothing here is in the way; a spec that wants a refusal
    // routes its own over this.
    const preview = path.match(/^\/docker\/networks\/([^/]+)\/(connect|disconnect|removal)$/)
    if (preview && method === "GET") {
      const net = NETS.find((n) => n.id === decodeURIComponent(preview[1]))
      return json(route, {
        network: net?.name ?? "",
        networkId: net?.id ?? "",
        container: url.searchParams.get("container") ?? undefined,
        owner: { kind: "manual" },
        conflicts: [],
        blocked: false,
        checkedAt: new Date().toISOString(),
      })
    }
    if (path === "/docker/networks/prune" && method === "GET") {
      return json(route, {
        checkedAt: new Date().toISOString(),
        candidates: NETS.filter(
          (n) => membersOf(n).length === 0 && !["bridge", "host", "none"].includes(n.name),
        ).map((n) => ({
          id: n.id,
          name: n.name,
          owner: n.labels?.["com.docker.compose.project"]
            ? { kind: "compose", project: n.labels["com.docker.compose.project"] }
            : { kind: "manual" },
          conflicts: [],
          removable: true,
        })),
      })
    }
    if (method !== "GET") {
      const body = route.request().postData()
      mocks.writes.push({ method, path, body: body ? JSON.parse(body) : undefined })
      if (path === "/docker/networks/prune") {
        return json(route, {
          kind: "networks",
          spaceReclaimed: 0,
          items: ["old-staging", "wiki_default"],
        })
      }
      if (path === "/docker/networks/" && method === "POST")
        return json(route, network(NETS[0]), 201)
      return route.fulfill({ status: 204 })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
  return mocks
}

export { netId, ctId }
