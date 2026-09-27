import { json, type ProxyRoutes } from "./shared"

export const snippet = "stream {\n    include /etc/nginx/streams/*.conf;\n}"

/** A module nginx has: the state of every host where streams can work. */
export const moduleLoaded = { state: "loaded", usable: true }

/** This Ubuntu host: built dynamic, libnginx-mod-stream not installed. */
export const moduleNotInstalled = {
  state: "not-installed",
  usable: false,
  path: "/usr/lib/nginx/modules/ngx_stream_module.so",
  package: "libnginx-mod-stream",
}

type StreamFixture = {
  name: string
  listen: number
  protocol: "tcp" | "udp" | "both"
  upstream: string
  proxyProtocol?: boolean
  allowFrom?: string[]
  [key: string]: unknown
}

/** A listed stream as the backend sends it: a file the dashboard wrote. */
export function streamEntry(stream: StreamFixture) {
  const allowFrom = stream.allowFrom ?? []
  return {
    proxyProtocol: false,
    path: `/etc/nginx/streams/${stream.name}.conf`,
    managed: true,
    open: allowFrom.length === 0,
    unsupported: [],
    ...stream,
    allowFrom,
  }
}

/** The listing, readable by nginx unless the options say otherwise. */
export function streamStatus(overrides: Record<string, unknown> = {}) {
  return {
    included: true,
    module: moduleLoaded,
    snippet,
    dir: "/etc/nginx/streams",
    streams: [],
    ...overrides,
  }
}

export const routes: ProxyRoutes = {
  "/proxy/streams/": (route, { included }) => json(route, streamStatus({ included })),
  "/proxy/streams/preview": (route) =>
    json(route, { content: "# Managed by Just Dashboard.\n", warnings: [] }),
}

export const showcase: ProxyRoutes = {
  "/proxy/streams/": (route) =>
    json(
      route,
      streamStatus({
        streams: [
          streamEntry({
            name: "postgres-replica",
            listen: 5432,
            protocol: "tcp",
            upstream: "10.0.0.5:5432",
          }),
          streamEntry({
            name: "private-redis",
            listen: 6379,
            protocol: "tcp",
            upstream: "10.0.0.9:6379",
            allowFrom: ["10.0.0.0/8"],
            timeout: 600,
          }),
          streamEntry({
            name: "minecraft",
            listen: 25565,
            protocol: "tcp",
            upstream: "10.0.0.6:25565",
            proxyProtocol: true,
          }),
        ],
      }),
    ),
}
