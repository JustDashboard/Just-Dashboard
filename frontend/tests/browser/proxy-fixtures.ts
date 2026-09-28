import type { Page } from "@playwright/test"
import * as certs from "./fixtures/proxy/certs"
import * as engine from "./fixtures/proxy/engine"
import * as ports from "./fixtures/proxy/ports"
import * as portsHistory from "./fixtures/proxy/ports-history"
import { json, user, type ProxyMockOptions, type ProxyRoutes } from "./fixtures/proxy/shared"
import * as siteform from "./fixtures/proxy/siteform"
import * as sites from "./fixtures/proxy/sites"
import * as streams from "./fixtures/proxy/streams"
import * as tls from "./fixtures/proxy/tls"

/**
 * One mocked host for the proxy pages, assembled from a route table per area
 * under fixtures/proxy/ so each area adds its endpoints in its own file.
 * Anything no table answers is a 503, which the pages render as an error —
 * a new endpoint left unmocked shows up as a broken page, not a quiet pass.
 */

export { json, user } from "./fixtures/proxy/shared"
export { now, inThirtyDays, yesterday } from "./fixtures/proxy/shared"
export { availability } from "./fixtures/proxy/engine"
export { vhosts } from "./fixtures/proxy/sites"
export { snippet } from "./fixtures/proxy/streams"
export { ports } from "./fixtures/proxy/ports"
export { certs } from "./fixtures/proxy/certs"
export { scan } from "./fixtures/proxy/tls"

/** Every proxy page, for the checks each of them has to pass at every width. */
export const PROXY_PAGES = [
  "/proxy",
  "/proxy/sites",
  "/proxy/certificates",
  "/proxy/tls?domain=app.example.com",
  "/proxy/streams",
  "/proxy/ports",
]

const areas = [engine, sites, siteform, streams, ports, portsHistory, certs, tls]

/** One table from several, refusing a path two areas both claim. */
function merge(tables: ProxyRoutes[]): ProxyRoutes {
  const out: ProxyRoutes = {}
  for (const table of tables) {
    for (const [path, handler] of Object.entries(table)) {
      if (out[path]) throw new Error(`${path} is mocked by two proxy fixture tables`)
      out[path] = handler
    }
  }
  return out
}

const ROUTES = merge(areas.map((area) => area.routes))
const SHOWCASE = merge(areas.map((area) => area.showcase))

async function serve(page: Page, options: ProxyMockOptions, tables: ProxyRoutes[]) {
  await page.routeWebSocket("**/api/v1/system/stream**", () => {})
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/dashboard/update") return json(route, { current: "0.6.7", latest: "0.6.7" })
    for (const table of tables) {
      const handler = table[path]
      if (handler) return handler(route, options)
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
    })
  })
}

/** A host with three sites, two certificates, three sockets and no streams. */
export async function mockProxy(page: Page, options: ProxyMockOptions) {
  await serve(page, options, [ROUTES])
}

/** The same host with every section populated, for the layout and phone checks. */
export async function mockShowcase(page: Page) {
  await serve(page, { included: true }, [SHOWCASE, ROUTES])
}
