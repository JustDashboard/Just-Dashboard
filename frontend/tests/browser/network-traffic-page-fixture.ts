import type { Page } from "@playwright/test"
import { admin, json, type Mutation } from "./network-fixture"
import { ebpf, openMaturity } from "./network-traffic-maturity-fixture"

// What the Traffic page's own specs need beyond the maturity package's
// shapes: accounts that may do less, reads that wait or fail, and an eBPF
// inventory large enough to have something attached and something to filter.

/** An account that may read and change nothing. */
export const reader = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, role: "readonly" },
}

/** An administrator without the destructive capability: may set, may not clear. */
export const cautious = {
  ...admin,
  capabilities: admin.capabilities.filter((c) => c !== "destructive"),
}

/** The inventory with a program on the uplink, so the attachments block is drawn. */
export const ebpfAttached = {
  ...ebpf,
  total: 4,
  programs: [
    ...ebpf.programs,
    {
      id: 77,
      type: "xdp",
      name: "xdp_drop_bad",
      uid: 0,
      bytesXlated: 512,
      bytesJited: 400,
      memlock: 4096,
      mapIds: [],
      owners: ["cilium-agent"],
    },
  ],
  byType: [...ebpf.byType, { type: "xdp", count: 1 }],
  attachments: [
    { device: "ens3", kind: "xdp", programId: 77, name: "xdp_drop_bad", mode: "driver" },
  ],
}

export type Gate = { release: () => void }

/**
 * The Traffic page's mocks. `session` signs another account in; `fail` makes
 * the named reads answer 500; `hold` keeps every read of this section waiting
 * until the returned gate is released, which is how a spec looks at the page
 * before its data lands and again after.
 */
export async function openTraffic(
  page: Page,
  options: {
    mutations?: Mutation[]
    overrides?: Record<string, unknown>
    session?: typeof admin
    fail?: string[]
    hold?: boolean
  } = {},
): Promise<Gate> {
  await openMaturity(page, options.mutations ?? [], options.overrides)
  if (options.session) {
    await page.route("**/api/v1/auth/session", (route) => json(route, options.session))
  }
  for (const path of options.fail ?? []) {
    await page.route(
      (url) => url.pathname === `/api/v1${path}`,
      (route) =>
        route.request().method() === "GET"
          ? json(
              route,
              { error: { code: "read_failed", message: `${path} could not be read` } },
              500,
            )
          : route.fallback(),
    )
  }
  return options.hold ? holdReads(page) : { release: () => {} }
}

/** Keeps every read of the Network section waiting from here on, until released. */
export async function holdReads(page: Page): Promise<Gate> {
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/network/**", async (route) => {
    await gate
    await route.fallback()
  })
  return { release }
}
