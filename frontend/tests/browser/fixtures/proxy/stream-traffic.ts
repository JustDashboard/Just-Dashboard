import type { Page } from "@playwright/test"
import { json } from "./shared"
import { heldStream, streamEntry, streamStatus } from "./streams"

/*
 * Streams that carry something: a listing with a forward open to anyone, a
 * restricted one, one nginx cannot bind, a UDP resolver and a paused one,
 * each stream's last hour for the list, and a window of sessions, clients and
 * live connections for a stream's own page. The Streams spec and the
 * overhaul's captures read these; nothing in the shared proxy table does.
 */

export const carrying = [
  streamEntry({
    name: "postgres-replica",
    listen: 5432,
    protocol: "tcp",
    upstream: "10.0.0.5:5432",
    logConnections: true,
    timeout: 600,
  }),
  streamEntry({
    name: "private-redis",
    listen: 6379,
    protocol: "tcp",
    upstream: "10.0.0.9:6379",
    allowFrom: ["10.0.0.0/8"],
    logConnections: true,
  }),
  heldStream({
    name: "minecraft",
    listen: 25565,
    protocol: "tcp",
    upstream: "10.0.0.6:25565",
    proxyProtocol: true,
  }),
  streamEntry({
    name: "resolver",
    listen: 53,
    protocol: "udp",
    udpMode: "request",
    upstream: "10.0.0.2:53",
    allowFrom: ["10.0.0.0/8", "192.168.0.0/16"],
    logConnections: true,
  }),
]

export const pausedStream = streamEntry({
  name: "old-bastion",
  listen: 2222,
  protocol: "tcp",
  upstream: "10.0.0.9:22",
  allowFrom: ["10.0.0.0/8"],
  state: "paused",
  paused: true,
  path: "/etc/nginx/streams/paused/old-bastion.conf",
})

export const carryingStatus = streamStatus({ streams: carrying, paused: [pausedStream] })

/** Each stream's last hour, as the list reads it. */
export const summaries = {
  "postgres-replica": { sessions: 142, denied: 0, bytes: 1_240_000_000, complete: true },
  "private-redis": { sessions: 9, denied: 3, bytes: 18_400_000, complete: true },
  resolver: { sessions: 2_310, denied: 41, bytes: 512_000, complete: true },
}

const until = Math.floor(Date.UTC(2026, 10, 13, 12, 0, 0) / 1000)

/** A window of postgres-replica's sessions: twelve five-minute buckets. */
export function trafficOf(name: string, window = "1h") {
  const shape = [9, 11, 14, 10, 12, 16, 13, 9, 11, 14, 12, 11]
  return {
    name,
    logging: true,
    path: `/var/log/nginx/stream-${name}.log`,
    window,
    since: until - 3600,
    until,
    complete: true,
    sessions: 142,
    denied: 3,
    failed: 2,
    bytesIn: 240_000_000,
    bytesOut: 1_000_000_000,
    statuses: { "200": 137, "403": 3, "502": 2 },
    buckets: shape.map((sessions, i) => ({
      start: until - 3600 + i * 300,
      sessions,
      denied: i === 4 ? 3 : 0,
      failed: i === 9 ? 2 : 0,
      bytes: sessions * 8_000_000,
    })),
    duration: { p50: 2.4, p90: 31, p99: 410, max: 7300 },
    clients: [
      {
        address: "10.0.0.21",
        sessions: 96,
        denied: 0,
        failed: 2,
        bytes: 900_000_000,
        last: until - 40,
      },
      {
        address: "10.0.0.34",
        sessions: 43,
        denied: 0,
        failed: 0,
        bytes: 340_000_000,
        last: until - 600,
      },
      { address: "203.0.113.50", sessions: 3, denied: 3, failed: 0, bytes: 0, last: until - 1500 },
    ],
    skipped: 0,
  }
}

export const sessionsOf = (name: string, listen: number) => ({
  name,
  listen,
  sessions: [
    { client: "10.0.0.21:51234", local: `10.0.0.1:${listen}` },
    { client: "10.0.0.34:40112", local: `10.0.0.1:${listen}` },
  ],
  total: 2,
})

/**
 * The listing, the hour of every stream, and each stream's window and
 * sockets. Registered after `mockProxy`, so these answer first.
 */
export async function mockCarrying(page: Page, status: Record<string, unknown> = carryingStatus) {
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET" ? json(route, status) : route.fallback(),
  )
  await page.route("**/api/v1/proxy/streams/traffic", (route) => json(route, summaries))
  await page.route(/\/api\/v1\/proxy\/streams\/[^/]+\/traffic/, (route) => {
    const url = new URL(route.request().url())
    const name = decodeURIComponent(url.pathname.split("/").at(-2) ?? "")
    return json(route, trafficOf(name, url.searchParams.get("window") ?? "1h"))
  })
  await page.route(/\/api\/v1\/proxy\/streams\/[^/]+\/sessions/, (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2) ?? "")
    const stream = [...carrying, pausedStream].find((s) => s.name === name)
    if (stream?.protocol === "udp") {
      return json(route, { name, listen: stream.listen, udpOnly: true, sessions: [], total: 0 })
    }
    return json(route, sessionsOf(name, stream?.listen ?? 0))
  })
}
