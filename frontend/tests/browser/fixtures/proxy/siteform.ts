import { json, type ProxyRoutes } from "./shared"

/** A site as the form sends it, with the switches the form starts from. */
export function siteSpec(overrides: Record<string, unknown> = {}) {
  return {
    name: "app.example.com",
    domains: ["app.example.com"],
    kind: "proxy",
    upstream: "http://127.0.0.1:3000",
    tls: true,
    certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
    keyPath: "/etc/letsencrypt/live/app.example.com/privkey.pem",
    forceHttps: true,
    hsts: true,
    http2: true,
    webSockets: true,
    gzip: true,
    blockExploits: true,
    securityHeaders: true,
    clientMaxBody: "50m",
    proxyTimeout: 60,
    allowFrom: [],
    denyFrom: [],
    accessLog: true,
    locations: [],
    ...overrides,
  }
}

/** legacy.example.com, a plain-HTTP site whose /static path was written with root. */
export const legacySpec = siteSpec({
  name: "legacy.example.com",
  domains: ["legacy.example.com"],
  upstream: "http://127.0.0.1:8080",
  tls: false,
  certPath: undefined,
  keyPath: undefined,
  hsts: false,
  locations: [{ path: "/static", root: "/srv/legacy", rootMode: "root", webSockets: false }],
})

/** What POST /proxy/sites/ answers for a saved site, before a test changes it. */
export function siteResult(overrides: Record<string, unknown> = {}) {
  return {
    name: "app.example.com",
    path: "/etc/nginx/sites-available/app.example.com",
    content: "",
    warnings: [],
    enabled: true,
    reloaded: true,
    ...overrides,
  }
}

/** The site form's own endpoints: reading a site back, its preview, and saving it. */
export const routes: ProxyRoutes = {
  "/proxy/sites/app.example.com": (route) =>
    json(route, { spec: siteSpec(), managed: true, content: "", warnings: [] }),
  "/proxy/sites/legacy.example.com": (route) =>
    json(route, { spec: legacySpec, managed: true, content: "", warnings: [] }),
  "/proxy/sites/preview": (route) => {
    const { spec } = route.request().postDataJSON()
    return json(route, {
      content: `# Site: ${spec.name}\nserver {\n    server_name ${spec.domains.join(" ")};\n}\n`,
      warnings: [],
    })
  },
  "/proxy/sites/": (route) => {
    const body = route.request().postDataJSON()
    return json(
      route,
      siteResult({
        name: body.spec.name,
        path: `/etc/nginx/sites-available/${body.spec.name}`,
        reloaded: body.reload,
      }),
    )
  },
}

export const showcase: ProxyRoutes = {}
