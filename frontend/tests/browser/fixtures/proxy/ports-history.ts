import { json, type ProxyRoutes } from "./shared"

const DAY = 24 * 60

/** An instant some minutes before `now`, as the API writes one: to the second, in UTC. */
export function minutesBefore(now: number, minutes: number) {
  return new Date(now - minutes * 60_000).toISOString().replace(/\.\d{3}Z$/, "Z")
}

type History = Record<string, unknown> & { events: unknown[] }

/** What every history answer carries besides its events, for a window of `hours` before `now`. */
export function historyOf(
  now: number,
  events: unknown[],
  overrides: Record<string, unknown> = {},
  hours = 24,
): History {
  return {
    recordingSince: minutesBefore(now, 3 * DAY),
    lastSample: minutesBefore(now, 0.5),
    stalled: false,
    intervalSeconds: 60,
    retentionDays: 30,
    since: minutesBefore(now, hours * 60),
    events,
    truncated: false,
    ...overrides,
  }
}

/** A socket as an event carries it, placed and graded as GET /ports places one. */
function socket(overrides: Record<string, unknown>) {
  return {
    protocol: "tcp",
    family: "ipv4",
    address: "0.0.0.0",
    port: 0,
    pid: 0,
    process: "",
    scope: "all",
    reach: "all",
    network: "all",
    exposed: true,
    ...overrides,
  }
}

/**
 * A day on a host, as the recorder writes it, newest first: Redis opening on
 * every interface in both families, a dev server on loopback passing from
 * node to python3, caddy's tailnet listener closing after an hour, a port
 * Docker published while the dashboard was stopped, and — a day earlier — a
 * Postgres that had listened since before recording began going away.
 */
export function hostHistory(now = Date.now()): History {
  const ago = (minutes: number) => minutesBefore(now, minutes)
  return historyOf(
    now,
    [
      ...["0.0.0.0", "::"].map((address) =>
        socket({
          kind: "opened",
          at: ago(14),
          after: ago(15),
          since: ago(14),
          family: address === "::" ? "ipv6" : "ipv4",
          address,
          port: 6379,
          pid: 2000,
          process: "redis-server",
          cmdline: "/usr/bin/redis-server *:6379",
          user: "redis",
          level: "critical",
        }),
      ),
      ...[
        { kind: "closed", process: "node", pid: 1400, cmdline: "node server.js", since: ago(300) },
        { kind: "opened", process: "python3", pid: 1500, cmdline: "python3 -m http.server 3000" },
      ].map((change) =>
        socket({
          at: ago(40),
          after: ago(41),
          since: ago(40),
          address: "127.0.0.1",
          port: 3000,
          user: "app",
          scope: "loopback",
          reach: "loopback",
          network: "loopback",
          exposed: false,
          ...change,
        }),
      ),
      socket({
        kind: "closed",
        at: ago(120),
        after: ago(121),
        since: ago(180),
        address: "100.110.34.31",
        port: 8443,
        pid: 3100,
        process: "caddy",
        cmdline: "caddy run --config /etc/caddy/Caddyfile",
        user: "caddy",
        scope: "interface",
        reach: "network",
        network: "tailnet",
        interface: "tailscale0",
      }),
      ...[
        { family: "ipv4", address: "0.0.0.0", pid: 501 },
        { family: "ipv6", address: "::", pid: 502 },
      ].map((pair) =>
        socket({
          kind: "opened",
          at: ago(300),
          after: ago(540),
          since: ago(300),
          port: 8080,
          process: "docker-proxy",
          cmdline: `/usr/bin/docker-proxy -proto tcp -host-ip ${pair.address} -host-port 8080 -container-ip 172.17.0.2 -container-port 80`,
          user: "root",
          ...pair,
        }),
      ),
      socket({
        kind: "closed",
        at: ago(DAY + 60),
        after: ago(DAY + 61),
        since: ago(3 * DAY),
        baseline: true,
        address: "127.0.0.1",
        port: 5433,
        pid: 900,
        process: "postgres",
        cmdline: "postgres -D /var/lib/postgresql/16/main",
        user: "postgres",
        scope: "loopback",
        reach: "loopback",
        network: "loopback",
        exposed: false,
      }),
    ],
    {},
    7 * 24,
  )
}

/** Seventy dev servers opening on loopback in one afternoon: more than the list draws at once. */
export function busyHistory(now = Date.now()): History {
  const ago = (minutes: number) => minutesBefore(now, minutes)
  return historyOf(
    now,
    Array.from({ length: 70 }, (_, i) =>
      socket({
        kind: "opened",
        at: ago(10 + i),
        after: ago(11 + i),
        since: ago(10 + i),
        address: "127.0.0.1",
        port: 4000 + i,
        pid: 5000 + i,
        process: "node",
        cmdline: `node dev-server.js --port ${4000 + i}`,
        user: "app",
        scope: "loopback",
        reach: "loopback",
        network: "loopback",
        exposed: false,
      }),
    ),
    { truncated: true },
  )
}

/** A recorder that has not sampled yet. */
export function unstartedHistory(now = Date.now()): History {
  return historyOf(now, [], { recordingSince: null, lastSample: null })
}

/** A recorder whose samples stopped an hour ago. */
export function stalledHistory(now = Date.now()): History {
  return historyOf(now, [], { lastSample: minutesBefore(now, 60), stalled: true })
}

/** A quiet host: recording for three days, nothing changed in the window asked for. */
export function quietHistory(now = Date.now(), hours = 24): History {
  return historyOf(now, [], {}, hours)
}

export const routes: ProxyRoutes = {
  "/ports/history": (route) => json(route, quietHistory()),
}

export const showcase: ProxyRoutes = {
  "/ports/history": (route) => json(route, hostHistory()),
}
