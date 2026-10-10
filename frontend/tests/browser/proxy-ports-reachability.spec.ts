import { expect, test, type Page } from "@playwright/test"
import { json, mockProxy } from "./proxy-fixtures"

/**
 * What the ports page can say about reaching a port from outside the host:
 * the provider's policy is not visible from here, so the sheet says so; an
 * enrolled external source's measurement is the proof, and checking from one
 * goes through the external-check owner; a port Docker publishes has a path
 * through its NAT that is traced layer by layer. The free-port search names
 * every claim it passed over and every source it could not read.
 */

const now = new Date().toISOString()

const published = {
  protocol: "tcp",
  family: "ipv4",
  address: "0.0.0.0",
  port: 5432,
  pid: 1883700,
  ppid: 1755428,
  process: "docker-proxy",
  cmdline:
    "/usr/bin/docker-proxy -proto tcp -host-ip 0.0.0.0 -host-port 5432 -container-ip 10.0.0.2 -container-port 5432",
  user: "root",
  scope: "all",
  reach: "all",
  network: "all",
  exposed: true,
  level: "critical",
  pastFirewall: "docker",
  service: "PostgreSQL",
  danger: "A database answering the internet is found by scanners within hours.",
  container: { id: "0123456789ab", name: "shop-db", image: "postgres:16", published: true },
}

const external = (pending = false) => ({
  checkedAt: now,
  evidence: [
    {
      checkId: pending ? "c-new" : "c1",
      vantageId: "a",
      source: "Probe A",
      location: "Region A",
      placement: "external_host",
      port: 5432,
      family: "inet",
      tls: false,
      address: "203.0.113.9",
      local: false,
      status: pending ? "queued" : "completed",
      state: pending ? "unknown" : "connected",
      basis: pending ? "unknown" : "measured",
      at: now,
    },
  ],
  scopes: [
    {
      vantageId: "a",
      source: "Probe A",
      location: "Region A",
      placement: "external_host",
      scopeId: "service",
      target: "shop.example",
      addresses: ["203.0.113.9"],
      ports: [5432],
      families: ["inet"],
    },
  ],
})

const path = {
  request: {
    sourceKind: "external",
    containerId: "0123456789ab",
    family: "inet",
    protocol: "tcp",
    port: 5432,
    target: "",
    measure: false,
  },
  scope: {
    vantage: "published_port",
    source: "Outside this host",
    target: "shop-db",
    address: "10.0.0.2",
    family: "inet",
    protocol: "tcp",
    port: 5432,
    limitations: [
      "Provider firewalls, security groups, upstream NAT and load balancers are not visible to this dashboard.",
    ],
  },
  startedAt: now,
  endedAt: now,
  addresses: ["10.0.0.2"],
  comparison:
    "Docker NAT: observed. Forwarded-leg firewall prediction: unknown. Provider policy: unknown. External measurement: connected from Probe A.",
  evidence: [
    {
      id: "dnat",
      title: "Docker NAT",
      basis: "observed",
      state: "observed",
      scope: "nat table, DOCKER chain",
      owner: "Docker",
      ownerPath: "/docker",
      checkedAt: now,
      summary: "Docker's rule translates host port 5432 to 10.0.0.2:5432.",
      facts: [{ label: "Translates to", value: "10.0.0.2:5432" }],
      limitations: [],
    },
    {
      id: "docker-user",
      title: "DOCKER-USER",
      basis: "observed",
      state: "empty",
      scope: "filter table, DOCKER-USER chain",
      owner: "Operator rules",
      checkedAt: now,
      summary: "DOCKER-USER holds no rule of the operator's.",
      facts: [{ label: "FORWARD policy", value: "DROP" }],
      limitations: [],
    },
    {
      id: "provider",
      title: "Provider policy",
      basis: "unknown",
      state: "unknown",
      scope: "upstream of this host",
      owner: "Provider",
      checkedAt: now,
      summary: "No provider adapter is configured.",
      facts: [
        {
          label: "Public address on this host",
          value: "none — inbound traffic reaches it through a provider's translation",
        },
      ],
      limitations: [],
    },
  ],
}

async function mockReachability(page: Page) {
  await mockProxy(page, { included: true })
  let pending = false
  const checks: unknown[] = []
  await page.route("**/api/v1/ports", (route) => json(route, [published]))
  await page.route("**/api/v1/ports/external", (route) => json(route, external(pending)))
  await page.route("**/api/v1/network/external/checks", (route) => {
    checks.push(route.request().postDataJSON())
    pending = true
    return json(route, { id: "c-new", status: "queued" })
  })
  await page.route("**/api/v1/docker/containers/0123456789ab/published/5432?**", (route) =>
    json(route, path),
  )
  return checks
}

test("a published port's sheet says provider policy is unseen, shows the external proof and checks again", async ({
  page,
}) => {
  const checks = await mockReachability(page)
  await page.goto("/proxy/ports?socket=tcp:0.0.0.0:5432")
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText(/Not visible — no provider adapter/)).toBeVisible()
  const proof = sheet.getByRole("list", { name: "External measurements" })
  await expect(proof.getByText("Connected to 203.0.113.9, not on this host")).toBeVisible()
  await expect(proof.getByText(/Probe A · Region A · external host · IPv4/)).toBeVisible()
  await sheet.getByRole("button", { name: "Check from Probe A (IPv4)", exact: true }).click()
  await expect(proof.getByText("Waiting for the source to measure")).toBeVisible()
  await expect(
    sheet.getByRole("button", { name: "Check from Probe A (IPv4)", exact: true }),
  ).toBeDisabled()
  expect(checks).toEqual([
    { vantageId: "a", scopeId: "service", family: "inet", port: 5432, tls: false },
  ])
})

test("a port Docker publishes is traced from outside through its NAT, unknown layers named", async ({
  page,
}) => {
  await mockReachability(page)
  await page.goto("/proxy/ports?socket=tcp:0.0.0.0:5432")
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("button", { name: "Trace the path", exact: true }).click()
  const report = sheet.getByRole("region", { name: "Connection path report" })
  await expect(report.getByRole("heading", { name: "Outside this host → shop-db" })).toBeVisible()
  await expect(report.getByText(/any outside address → 10.0.0.2/)).toBeVisible()
  await expect(report.getByRole("heading", { name: "Docker NAT", exact: true })).toBeVisible()
  await expect(report.getByRole("heading", { name: "Provider policy", exact: true })).toBeVisible()
  await expect(report.getByText("Provider policy: unknown", { exact: false })).toBeVisible()
})

test("an unenrolled port says reachability from outside is unproven, and a reader is offered no check", async ({
  page,
}) => {
  await mockReachability(page)
  await page.route("**/api/v1/ports/external", (route) =>
    json(route, { checkedAt: now, evidence: [], scopes: [] }),
  )
  await page.goto("/proxy/ports?socket=tcp:0.0.0.0:5432")
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText(/No enrolled source has measured port 5432/)).toBeVisible()
  await expect(sheet.getByText(/No enrolled source may check port 5432/)).toBeVisible()

  await page.route("**/api/v1/auth/session", (route) =>
    json(route, {
      authenticated: true,
      needsTotp: false,
      needsEnrollment: false,
      require2fa: false,
      capabilities: ["read"],
      user: {
        id: 2,
        username: "viewer",
        role: "readonly",
        totpEnabled: false,
        disabled: false,
        mustChangePassword: false,
        createdAt: now,
      },
    }),
  )
  await page.reload()
  await expect(
    page.getByRole("dialog").getByText(/Not visible — no provider adapter/),
  ).toBeVisible()
  await expect(page.getByRole("dialog").getByText("From outside", { exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Trace the path", exact: true })).toHaveCount(0)
})

test("the free-port search names what it passed over and the sources it could not read", async ({
  page,
}) => {
  await mockReachability(page)
  await page.route("**/api/v1/ports/free?**", (route) =>
    json(route, {
      ports: [8002],
      skipped: [],
      containersChecked: true,
      reservations: [
        {
          port: 8000,
          source: "deployments",
          detail:
            "leased to shop · production on 127.0.0.1 until 2026-10-09T13:00:00Z, while its release starts",
        },
        {
          port: 8001,
          source: "firewall",
          detail:
            "ufw rule 4 admits it from anywhere, so whatever binds it is reachable from there at once",
        },
      ],
      sources: [
        { key: "sockets", label: "Listening sockets", state: "checked" },
        { key: "containers", label: "Container publications", state: "checked" },
        { key: "deployments", label: "Deployment port leases", state: "checked" },
        {
          key: "gateway",
          label: "Gateway forwards",
          state: "unavailable",
          detail: "the gateway could not be read",
        },
        {
          key: "provider",
          label: "Provider reservations",
          state: "not_supplied",
          detail:
            "No provider adapter is configured, so ports a provider reserves or its firewall refuses are unknown.",
        },
      ],
    }),
  )
  await page.goto("/proxy/ports")
  await page.getByRole("button", { name: "Find a free port", exact: true }).click()
  await page.getByRole("button", { name: "Find", exact: true }).click()
  const passed = page.getByRole("list", { name: "Ports passed over" })
  await expect(passed.getByText(/leased to shop · production/)).toBeVisible()
  await expect(passed.getByText(/ufw rule 4 admits it from anywhere/)).toBeVisible()
  await expect(
    page.getByText(/Not supplied: Provider reservations — No provider adapter/),
  ).toBeVisible()
  await expect(
    page.getByText(/Not read: Gateway forwards — the gateway could not be read/),
  ).toBeVisible()
  await expect(page.getByRole("list", { name: "Free ports" }).getByText("8002")).toBeVisible()
})
