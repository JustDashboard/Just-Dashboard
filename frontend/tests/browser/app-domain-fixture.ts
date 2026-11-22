import type { Page, Route } from "@playwright/test"

/**
 * This server's just-dashboard.com address (`/dashboard/link/domain`, docs/internal/backend/
 * app-domains.md) as a small state machine: a claim holds the handle and starts issuing the
 * wildcard, a few reads later the wildcard is valid, a release lets the handle go, and a
 * refusal set on the world is answered once in place of the next mutation. A test sets
 * `world.state` directly to play the site suspending or detaching the address.
 *
 * Addresses are documentation ranges; the zone is the site's, which is public.
 */

export type DomainState = {
  enabled: boolean
  linked: boolean
  available: boolean | null
  zone: string | null
  reason: string | null
  publicAddress: string | null
  autoAssign: boolean
  domain: {
    handle: string
    hostname: string
    wildcard: string
    address: string
    status: "active" | "suspended" | "detached"
    reason: string | null
    updatedAt: number
  } | null
  certificate: {
    name: string
    state: "missing" | "issuing" | "valid" | "expiring" | "failed"
    expiresAt: number | null
    error: string | null
  } | null
  usedBy: {
    projectId: number
    project: string
    environmentId: number
    environment: string
    hostname: string
  }[]
}

export const ZONE = "just-dashboard.com"
export const ADDRESS = "203.0.113.10"
// About 80 days from now, so "Valid until" names a real date.
const EXPIRES = Math.floor(Date.now() / 1000) + 80 * 86_400

const base: DomainState = {
  enabled: true,
  linked: true,
  available: true,
  zone: ZONE,
  reason: null,
  publicAddress: ADDRESS,
  autoAssign: true,
  domain: null,
  certificate: null,
  usedBy: [],
}

export const notLinked: DomainState = {
  ...base,
  linked: false,
  available: null,
  zone: null,
  reason: "This server is not linked to a just-dashboard.com account.",
}

export const notOffered: DomainState = {
  ...base,
  available: false,
  zone: null,
  reason: "Addresses are not set up on this site yet.",
}

export const unreachable: DomainState = {
  ...base,
  available: null,
  zone: null,
  reason: "just-dashboard.com did not answer within 15s",
}

export const readyToClaim = (zone = ZONE): DomainState => ({ ...base, zone })

export const turnedOff: DomainState = { ...base, enabled: false, available: null, zone: null }

export const shopUse = {
  projectId: 7,
  project: "api-production",
  environmentId: 12,
  environment: "production",
  hostname: `shop.acme.${ZONE}`,
}

export const blogUse = {
  projectId: 9,
  project: "blog",
  environmentId: 21,
  environment: "staging",
  hostname: `blog.acme.${ZONE}`,
}

export function held(
  status: "active" | "suspended" | "detached" = "active",
  extra: Partial<DomainState> = {},
  handle = "acme",
): DomainState {
  return {
    ...base,
    domain: {
      handle,
      hostname: `${handle}.${ZONE}`,
      wildcard: `*.${handle}.${ZONE}`,
      address: ADDRESS,
      status,
      reason: null,
      updatedAt: 1795000000,
    },
    certificate:
      status === "detached"
        ? null
        : { name: `jd-app-domain-${handle}`, state: "valid", expiresAt: EXPIRES, error: null },
    ...extra,
  }
}

export type Refusal = { status: number; code: string; message: string; retryAfter?: number }

export async function mockAppDomain(page: Page, initial: DomainState) {
  const world = {
    state: initial,
    calls: [] as { method: string; path: string; body?: unknown }[],
    refusal: undefined as Refusal | undefined,
    /** Reads left before an issuing certificate settles as valid. */
    issuingReads: 2,
  }
  const respond = (route: Route, body: unknown, status = 200, headers = {}) =>
    route.fulfill({ status, contentType: "application/json", headers, body: JSON.stringify(body) })

  await page.route("**/api/v1/dashboard/link/domain**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method === "GET") {
      if (world.state.certificate?.state === "issuing" && --world.issuingReads <= 0) {
        world.state = {
          ...world.state,
          certificate: { ...world.state.certificate, state: "valid", expiresAt: EXPIRES },
        }
      }
      return respond(route, world.state)
    }
    world.calls.push({ method, path, body: request.postDataJSON() ?? undefined })
    if (world.refusal) {
      const { status, code, message, retryAfter } = world.refusal
      world.refusal = undefined
      return respond(
        route,
        { error: { code, message } },
        status,
        retryAfter ? { "Retry-After": String(retryAfter) } : {},
      )
    }
    if (path === "/dashboard/link/domain" && method === "PUT") {
      const { handle } = request.postDataJSON() as { handle: string }
      world.issuingReads = 2
      world.state = {
        ...held(
          "active",
          { usedBy: world.state.usedBy, autoAssign: world.state.autoAssign },
          handle,
        ),
        certificate: {
          name: `jd-app-domain-${handle}`,
          state: "issuing",
          expiresAt: null,
          error: null,
        },
      }
      return respond(route, world.state)
    }
    if (path === "/dashboard/link/domain" && method === "DELETE") {
      if (world.state.usedBy.length) {
        return respond(
          route,
          { error: { code: "domain_in_use", message: "deployments use the address" } },
          409,
        )
      }
      world.state = readyToClaim()
      return route.fulfill({ status: 204 })
    }
    if (path === "/dashboard/link/domain/certificate" && method === "POST") {
      world.issuingReads = 2
      world.state = {
        ...world.state,
        certificate: {
          name: world.state.certificate?.name ?? "jd-app-domain-acme",
          state: "issuing",
          expiresAt: world.state.certificate?.expiresAt ?? null,
          error: null,
        },
      }
      return respond(route, world.state)
    }
    if (path === "/dashboard/link/domain/settings" && method === "PUT") {
      const { autoAssign } = request.postDataJSON() as { autoAssign: boolean }
      world.state = { ...world.state, autoAssign }
      return respond(route, world.state)
    }
    return respond(route, { error: { code: "not_found", message: "no such route" } }, 404)
  })
  return world
}

/** The suggestion `GET /deploy/hostname?name=…&method=app-domain` answers. */
export function appDomainSuggestion(label: string, covered = true) {
  return {
    hostname: `${label}.acme.${ZONE}`,
    base: `acme.${ZONE}`,
    covered,
    certificateName: covered ? "jd-app-domain-acme" : undefined,
    certificateMethod: covered ? undefined : "app-domain",
    method: "app-domain",
    address: ADDRESS,
    detail: covered
      ? "The jd-app-domain-acme certificate already covers it."
      : "No certificate covers it yet; the release issues the wildcard for *.acme.just-dashboard.com.",
  }
}
