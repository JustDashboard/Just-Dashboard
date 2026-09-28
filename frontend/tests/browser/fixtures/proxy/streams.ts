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

/** A listed stream as the backend sends it: a file the dashboard wrote, and nginx holds its port. */
export function streamEntry(stream: StreamFixture) {
  const allowFrom = stream.allowFrom ?? []
  return {
    proxyProtocol: false,
    path: `/etc/nginx/streams/${stream.name}.conf`,
    managed: true,
    open: allowFrom.length === 0,
    unsupported: [],
    state: "live",
    ...stream,
    allowFrom,
  }
}

/** A stream nginx reads and cannot bind: another program holds its port. */
export function heldStream(stream: StreamFixture) {
  return streamEntry({
    state: "not-listening",
    stateReason: `Port ${stream.listen}/tcp is held by postgres (pid 900), so nginx cannot bind it, and every reload fails until it is free.`,
    blocker: { port: stream.listen, proto: "tcp", kind: "program", name: "postgres", pid: 900 },
    bindError: `2026/09/28 03:29:05 bind() to 0.0.0.0:${stream.listen} failed (98: Address already in use)`,
    ...stream,
  })
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

/** The drop-in Ubuntu's nginx.conf takes: a new file in modules-enabled. */
export const dropInPath = "/etc/nginx/modules-enabled/zz-just-dashboard-stream.conf"
export const dropIn =
  "# Managed by Just Dashboard.\n# Reads the streams on the Streams page, which live in /etc/nginx/streams.\nstream {\n    include /etc/nginx/streams/*.conf;\n}\n"

/** The connect's plan, a drop-in unless the options say otherwise. */
export function includePlan(overrides: Record<string, unknown> = {}) {
  return {
    mode: "dropin",
    path: dropInPath,
    exists: false,
    keepsCopy: false,
    before: "",
    after: dropIn,
    added: dropIn,
    line: 1,
    streams: [],
    conflicts: [],
    warnings: [],
    ...overrides,
  }
}

/** This host's nginx as `GET /proxy/modules` reports it: stream built dynamic and not installed. */
export const modules = {
  version: "nginx/1.26.3 (Ubuntu)",
  openssl: "OpenSSL 3.4.1 11 Feb 2025",
  modulesPath: "/usr/lib/nginx/modules",
  modules: [
    { name: "http_ssl_module", state: "static" },
    { name: "http_stub_status_module", state: "static" },
    { name: "http_realip_module", state: "static" },
    { name: "http_auth_request_module", state: "static" },
    { name: "http_v2_module", state: "static" },
    { name: "http_v3_module", state: "static" },
    {
      name: "stream",
      state: "not-installed",
      path: "/usr/lib/nginx/modules/ngx_stream_module.so",
      package: "libnginx-mod-stream",
    },
  ],
}

export const routes: ProxyRoutes = {
  "/proxy/streams/": (route, { included }) => json(route, streamStatus({ included })),
  "/proxy/streams/preview": (route) =>
    json(route, { content: "# Managed by Just Dashboard.\n", warnings: [] }),
  "/proxy/streams/include/plan": (route) => json(route, includePlan()),
  "/proxy/modules": (route) => json(route, modules),
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
          heldStream({
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
