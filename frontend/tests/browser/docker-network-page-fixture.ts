import type { Page } from "@playwright/test"
import {
  CTS,
  NETS,
  mockNetworks,
  networkDetail,
  user,
  type NetworkMocks,
} from "./docker-networks-fixture"

/**
 * The server behind a network's own page: `docker-networks-fixture`'s host
 * (eleven networks, sixteen containers), with a network's inspect answered by
 * its name as well as its id, as the Engine resolves either, and each member's
 * other networks listed as the backend lists them.
 */

const fulfil = (body: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
})

export const netId = (name: string) => NETS.find((n) => n.name === name)!.id
export const ctId = (name: string) => CTS.find((c) => c.name === name)!.id

/** A network's inspect as the backend writes it: its members with the other networks each is on. */
export function inspectOf(key: string) {
  const net = NETS.find((n) => n.name === key || n.id === key)
  if (!net) return undefined
  const detail = networkDetail(net)
  return {
    ...detail,
    members: detail.members.map((member) => ({
      ...member,
      networks: Object.keys(CTS.find((c) => c.id === member.id)!.on)
        .filter((name) => name !== net.name)
        .sort(),
    })),
  }
}

export const readOnly = {
  ...user,
  capabilities: ["read"],
  user: { ...user.user, role: "readonly" },
}

export async function mockNetworkPage(
  page: Page,
  options: Parameters<typeof mockNetworks>[1] & {
    session?: object
    /** Replaces a network's inspect, by its name. */
    inspect?: Record<string, object>
  } = {},
): Promise<NetworkMocks> {
  const mocks = await mockNetworks(page, options)
  if (options.session) {
    await page.route("**/api/v1/auth/session", (route) => route.fulfill(fulfil(options.session)))
  }
  await page.route(/\/api\/v1\/docker\/networks\/[^/?]+$/, (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    const key = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop()!)
    if (key === "prune" || key === "drivers") return route.fallback()
    const detail = inspectOf(key)
    if (!detail) {
      return route.fulfill(
        fulfil({ error: { code: "not_found", message: `network ${key} not found` } }, 404),
      )
    }
    return route.fulfill(fulfil(options.inspect?.[detail.name] ?? detail))
  })
  return mocks
}
