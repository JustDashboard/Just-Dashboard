import type { Page, Route } from "@playwright/test"
import { mockProject } from "./deploy-fixture"
import {
  history,
  host,
  json,
  now,
  snapshot,
  storage,
  user as admin,
  windowOf,
} from "./host-fixture"
import {
  backups,
  certificates,
  containers,
  databases,
  events,
  exposure,
  packageUpdates,
  processes,
  repos,
  selfUpdate,
  traffic,
} from "./overview-fixture"
import { showcaseHandler } from "./proxy-fixtures"

/**
 * A working server and one account whose answer to the first-run question
 * the spec controls.
 *
 * The world is three fixtures layered so that each answers what it owns and
 * hands the rest down: the deployment project's pages (`mockProject`), the
 * proxy showcase for when a link lands in the proxy, and the host and
 * Overview's reads on top. That is what lets a spec walk the one thing this
 * change promises — a link from a deployment's domain into a section the
 * account hid — instead of opening each page cold.
 *
 * The account is signed out until `/auth/login`, and every PUT to
 * `/account/workspace` becomes its answer the way the server stores it:
 * sorted, without repeats, and no longer owing the question.
 */
export async function mockOnboarding(
  page: Page,
  {
    signedIn = true,
    needsOnboarding = true,
    hiddenSections = [] as string[],
  }: { signedIn?: boolean; needsOnboarding?: boolean; hiddenSections?: string[] } = {},
) {
  await mockProject(page)

  await page.route("**/api/v1/**", (route) => {
    const handler = showcaseHandler(apiPath(route))
    return handler ? handler(route) : route.fallback()
  })

  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => {
    socket.send(JSON.stringify({ type: "host", data: host }))
    socket.send(JSON.stringify({ type: "metrics", data: snapshot }))
  })
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url())
    const path = apiPath(route)
    const body = hostRead(path, url)
    return body === undefined ? route.fallback() : json(route, body)
  })

  const account = { signedIn, needsOnboarding, hiddenSections: [...hiddenSections] }
  const saved: string[][] = []
  const me = () => ({
    ...admin.user,
    username: "ion",
    displayName: "Ion",
    avatarVersion: 0,
    needsOnboarding: account.needsOnboarding,
    hiddenSections: account.hiddenSections,
  })
  const session = () =>
    account.signedIn
      ? { ...admin, user: me() }
      : { authenticated: false, needsTotp: false, needsEnrollment: false, require2fa: false }

  await page.route("**/api/v1/auth/session", (route) => json(route, session()))
  await page.route("**/api/v1/auth/login", (route) => {
    account.signedIn = true
    return json(route, session())
  })
  await page.route("**/api/v1/auth/logout", (route) => {
    account.signedIn = false
    return route.fulfill({ status: 204 })
  })
  await page.route("**/api/v1/account/workspace", (route) => {
    const { hiddenSections: sections } = route.request().postDataJSON() as {
      hiddenSections: string[]
    }
    saved.push(sections)
    account.hiddenSections = [...new Set(sections)].sort()
    account.needsOnboarding = false
    return json(route, me())
  })

  return { account, saved }
}

function apiPath(route: Route) {
  return new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
}

/** The host's readings and the Overview's module reads, or `undefined` for anything else. */
function hostRead(path: string, url: URL): unknown {
  switch (path) {
    case "/system/host":
      return host
    case "/system/metrics":
      return snapshot
    case "/system/metrics/history": {
      const w = windowOf(url)
      return history(w.from, w.to, w.explicit && w.to <= now - 3_000_000 ? 10 : 20)
    }
    case "/system/metrics/storage": {
      const w = windowOf(url)
      return storage(w.from, w.to)
    }
    case "/system/metrics/events": {
      const range = url.searchParams.get("range") ?? "1h"
      const hours = range.endsWith("d")
        ? Number(range.slice(0, -1)) * 24
        : Number(range.slice(0, -1))
      return events.filter((event) => Date.parse(event.ts) >= now - hours * 3_600_000).reverse()
    }
    case "/processes/":
      return processes
    case "/docker/containers/":
      return containers
    case "/databases/":
      return databases
    case "/certificates/":
      return certificates
    case "/backups/":
      return backups
    case "/git/":
      return { available: true, repos }
    case "/packages/updates":
      return packageUpdates
    case "/exposure":
      return exposure
    case "/deploy/traffic":
      return traffic
    case "/dashboard/update":
      return selfUpdate
  }
  return undefined
}
