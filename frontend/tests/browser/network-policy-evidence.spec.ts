import { expect, test, type Page } from "@playwright/test"
import { admin, iso, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import { gateway, overrides, protection } from "./network-gateway-fixture"

/** The API supplies evidence; the page must preserve its scope and uncertainty. */
const reader = {
  ...admin,
  capabilities: ["read"],
  user: { ...admin.user, role: "readonly" },
}

type AdmissionStatus = "present" | "absent" | "unsupported" | "unreadable"

const admission = (status: AdmissionStatus, reason = "") => ({
  needed: true,
  present: status === "present",
  checkedAt: iso(1),
  chains: ["inet", "inet6"].flatMap((family) =>
    ["FORWARD", "INPUT", "DOCKER-USER"].map((chain) => ({
      family,
      tool: family === "inet" ? "iptables" : "ip6tables",
      chain,
      needed: true,
      status: family === "inet6" && chain === "INPUT" ? status : "present",
      reason: family === "inet6" && chain === "INPUT" ? reason : "",
    })),
  ),
})

const openGateway = async (
  page: Page,
  status: AdmissionStatus,
  options: { reason?: string; session?: typeof admin; mutations?: Mutation[] } = {},
) => {
  const data: Record<string, unknown> = {
    ...overrides,
    "/network/gateway": { ...gateway, admission: admission(status, options.reason) },
  }
  await mockNetwork(page, options.mutations, { overrides: data, session: options.session })
  await page.goto("/network/gateway")
  await loaded(page)
  return data
}

const chainReading = (page: Page, label: string) =>
  page
    .locator("dt")
    .filter({ hasText: new RegExp(`^${label}$`) })
    .locator("xpath=following-sibling::dd[1]")

test("a missing IPv6 INPUT rule is identified independently and repaired only after review", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const data = await openGateway(page, "absent", {
    mutations,
    reason: "The owned mark rule is missing from ip6tables INPUT.",
  })
  await page.route("**/api/v1/network/gateway/admission/repair", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    mutations.push({ method: "POST", path: "/network/gateway/admission/repair", body: null })
    data["/network/gateway"] = { ...gateway, admission: admission("present") }
    return json(route, { repaired: true })
  })

  await expect(
    page.getByText("Owned admission rules need attention", { exact: true }),
  ).toBeVisible()
  await expect(chainReading(page, "IPv6 INPUT")).toContainText("absent")
  await expect(chainReading(page, "IPv6 INPUT")).toContainText("ip6tables INPUT")
  for (const label of ["IPv4 INPUT", "IPv4 FORWARD", "IPv6 FORWARD", "IPv6 DOCKER-USER"]) {
    await expect(chainReading(page, label)).toContainText("Owned rule present")
  }
  await expect(
    page.locator("time").filter({ hasText: admission("absent").checkedAt }),
  ).toBeVisible()
  await expect(
    page.getByText("Provider policy and end-to-end reachability remain unverified."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Repair owned rules", exact: true }).click()
  const review = page.getByRole("dialog")
  await expect(review).toContainText("This may admit traffic previously refused")
  expect(mutations).toEqual([])
  await review.getByRole("button", { name: "Repair rules", exact: true }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toMatchObject({ method: "POST", path: "/network/gateway/admission/repair" })
  await expect(chainReading(page, "IPv6 INPUT")).toContainText("Owned rule present")
  await expect(page.getByRole("button", { name: "Repair owned rules", exact: true })).toHaveCount(0)
})

for (const [status, reason] of [
  ["unreadable", "ip6tables INPUT could not be read: permission denied"],
  ["unsupported", "The required ip6tables tool is unavailable"],
] as const) {
  test(`${status} admission evidence keeps its reason without offering an absent-rule repair`, async ({
    page,
  }) => {
    const mutations: Mutation[] = []
    await openGateway(page, status, { reason, mutations })
    await expect(chainReading(page, "IPv6 INPUT")).toContainText(status)
    await expect(chainReading(page, "IPv6 INPUT")).toContainText(reason)
    await expect(page.getByRole("button", { name: "Repair owned rules", exact: true })).toHaveCount(
      0,
    )
    expect(mutations).toEqual([])
  })
}

test("a reader can inspect absent admission evidence while repair remains unavailable", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await openGateway(page, "absent", { session: reader, mutations })
  await expect(chainReading(page, "IPv6 INPUT")).toContainText("absent")
  await expect(page.getByRole("button", { name: "Repair owned rules", exact: true })).toHaveCount(0)
  await expect(
    page.getByRole("button", { name: "Forward a port", exact: true }).first(),
  ).toBeDisabled()
  expect(mutations).toEqual([])
})

test("an explicit INPUT drop remains visible beside an accept policy and unverified layers", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, {
    overrides: {
      ...overrides,
      "/network/gateway": {
        ...gateway,
        admission: admission("absent"),
        capability: {
          ...gateway.capability,
          writable: false,
          reachability: "unknown",
          reason: "An explicit drop in ip6 filter INPUT refuses the owned marked traffic.",
          blocker: { family: "ip6", table: "filter", chain: "INPUT", rule: "owned mark accept" },
          layers: [
            {
              family: "ip6",
              table: "filter",
              chain: "INPUT",
              hook: "input",
              policy: "accept",
              status: "blocked",
              reason: "Explicit drop precedes the owned mark admission.",
            },
          ],
          unknownLayers: ["Provider firewall", "End-to-end reachability"],
        },
      },
    },
  })
  await page.goto("/network/gateway")
  await loaded(page)
  await expect(
    page.getByText("This firewall does not let the gateway write", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText("An explicit drop in ip6 filter INPUT refuses the owned marked traffic."),
  ).toBeVisible()
  await page.getByText("Checked policy layers", { exact: true }).click()
  await expect(page.getByText("ip6 filter INPUT: blocked", { exact: false })).toBeVisible()
  await expect(
    page.getByText("Explicit drop precedes the owned mark admission.", { exact: false }),
  ).toBeVisible()
  await expect(
    page.getByText("Unverified: Provider firewall; End-to-end reachability."),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Repair owned rules", exact: true })).toHaveCount(0)
  await expect(
    page.getByRole("button", { name: "Forward a port", exact: true }).first(),
  ).toBeDisabled()
  expect(mutations).toEqual([])
})

const list = protection.blocklists[0]
const cacheGeneration = "a".repeat(64)
const loadedGeneration = "b".repeat(64)

const openBlocklist = async (
  page: Page,
  evidence: Record<string, unknown>,
  session = admin,
  mutations: Mutation[] = [],
) => {
  await mockNetwork(page, mutations, {
    session,
    overrides: {
      ...overrides,
      "/network/protection": {
        ...protection,
        blocklists: [{ ...list, ...evidence }],
      },
    },
  })
  await page.goto("/network/protection")
  await loaded(page)
  return page.getByLabel(`${list.name} enforcement evidence`, { exact: true })
}

for (const [status, error] of [
  ["missing", "The cached blocklist is missing."],
  ["unreadable", "The cached blocklist could not be read: permission denied."],
  ["invalid", "The cached blocklist contains an invalid network."],
] as const) {
  test(`${status} cache reports zero current networks separately from retained kernel data`, async ({
    page,
  }) => {
    const evidence = await openBlocklist(page, {
      count: 0,
      savedCount: 2,
      cache: { status, count: 0, generation: "", error },
      renderedGeneration: loadedGeneration,
      runtime: { status: "present", count: 2, generation: loadedGeneration },
      enforcement: "degraded",
    })
    await expect(page.getByText("Degraded enforcement", { exact: true })).toBeVisible()
    await expect(evidence).toContainText(
      `Cache ${status}: 0 networks · last fetched count 2 · kernel present: 2 networks`,
    )
    await expect(evidence.getByRole("alert")).toHaveText(error)
    await expect(page.getByText("Verified set", { exact: true })).toHaveCount(0)
    await evidence.getByText("Policy generations", { exact: true }).click()
    await expect(evidence).toContainText("Cache: unavailable")
    await expect(evidence).toContainText(`Kernel: ${loadedGeneration}`)
  })
}

test("current cache, rendered policy, and drifted kernel generations remain separate", async ({
  page,
}) => {
  const evidence = await openBlocklist(page, {
    count: 2,
    savedCount: 2,
    cache: { status: "ready", count: 2, generation: cacheGeneration },
    renderedGeneration: cacheGeneration,
    runtime: { status: "present", count: 1, generation: loadedGeneration },
    enforcement: "degraded",
  })
  await expect(page.getByText("Degraded enforcement", { exact: true })).toBeVisible()
  await expect(evidence).toContainText("Cache ready: 2 networks · kernel present: 1 networks")
  await evidence.getByText("Policy generations", { exact: true }).click()
  await expect(evidence).toContainText(`Cache: ${cacheGeneration}`)
  await expect(evidence).toContainText(`Render: ${cacheGeneration}`)
  await expect(evidence).toContainText(`Kernel: ${loadedGeneration}`)
  await expect(page.getByText("Verified set", { exact: true })).toHaveCount(0)
})

test("an unreadable kernel set reports an unknown count and retains reader access to evidence", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  const evidence = await openBlocklist(
    page,
    {
      count: 2,
      cache: { status: "ready", count: 2, generation: cacheGeneration },
      renderedGeneration: cacheGeneration,
      runtime: { status: "unreadable", count: null, error: "nft list set: permission denied" },
      enforcement: "unknown",
    },
    reader,
    mutations,
  )
  await expect(page.getByText("Enforcement unknown", { exact: true })).toBeVisible()
  await expect(evidence).toContainText("kernel unreadable: unknown networks")
  await expect(evidence).not.toContainText("kernel unreadable: 0 networks")
  await expect(evidence.getByRole("alert")).toHaveText("nft list set: permission denied")
  await expect(
    page.getByRole("switch", { name: `${list.name} in force`, exact: true }),
  ).toBeDisabled()
  await expect(
    page.getByRole("button", { name: `Refresh ${list.name}`, exact: true }),
  ).toBeDisabled()
  await expect(page.getByRole("button", { name: "New blocklist", exact: true })).toBeDisabled()
  expect(mutations).toEqual([])
})

test("a verified normalized address union may contain fewer kernel intervals than cached networks", async ({
  page,
}) => {
  const evidence = await openBlocklist(page, {
    count: 3,
    savedCount: 3,
    cache: { status: "ready", count: 3, generation: cacheGeneration },
    renderedGeneration: cacheGeneration,
    runtime: { status: "present", count: 2, generation: cacheGeneration },
    enforcement: "verified",
  })
  await expect(page.getByText("Verified set", { exact: true })).toBeVisible()
  await expect(evidence).toContainText("Cache ready: 3 networks · kernel present: 2 networks")
  await expect(page.getByText("Degraded enforcement", { exact: true })).toHaveCount(0)
})
