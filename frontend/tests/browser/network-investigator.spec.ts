import { expect, test, type Page } from "@playwright/test"
import { admin, json, mockNetwork } from "./network-fixture"
import type { PathRequest, PathResult } from "../../src/lib/network-investigator-types"

const id = "a".repeat(64)
const evidence = (
  id: string,
  title: string,
  basis: "observed" | "modeled" | "measured" | "unknown",
  state: string,
  summary: string,
) => ({
  id,
  title,
  basis,
  state,
  summary,
  scope: "selected source",
  owner: "native owner",
  checkedAt: new Date().toISOString(),
  facts: [],
  limitations: [],
})

function report(request: PathRequest): PathResult {
  return {
    request,
    scope: {
      vantage: request.sourceKind === "host" ? "dashboard_host" : "container_network_namespace",
      source: request.sourceKind === "host" ? "Dashboard host" : "web",
      sourceAddress:
        request.sourceAddress || (request.family === "inet" ? "192.0.2.3" : "2001:db8::3"),
      target: request.target,
      address: request.address || (request.family === "inet" ? "192.0.2.8" : "2001:db8::8"),
      family: request.family,
      protocol: request.protocol,
      port: request.port,
      limitations: ["Provider policy and remote service ownership remain unknown."],
    },
    startedAt: new Date().toISOString(),
    endedAt: new Date().toISOString(),
    addresses:
      request.family === "inet" ? ["192.0.2.8", "192.0.2.9"] : ["2001:db8::8", "2001:db8::9"],
    evidence: [
      evidence("dns", "DNS", "measured", "observed", "Native resolver answered this private name."),
      evidence("rules", "Policy rules", "observed", "observed", "Ordered native candidates."),
      evidence(
        "route",
        "Kernel route",
        "observed",
        "observed",
        "The kernel selected eth0; no connectivity is implied.",
      ),
      evidence(
        "firewall",
        "Firewall",
        "modeled",
        "modeled",
        "UFW OUT default models deny for this tuple.",
      ),
      evidence("nat", "NAT", "unknown", "unknown", "Foreign and provider NAT remain unknown."),
      evidence(
        "tunnel",
        "Tunnel / interface",
        "observed",
        "observed",
        "Native interface metadata identifies eth0.",
      ),
      evidence(
        "proxy",
        "Proxy",
        "unknown",
        "unknown",
        "Remote or foreign proxy configuration is unknown.",
      ),
      evidence(
        "owner",
        "Destination owner",
        "unknown",
        "unknown",
        "Remote ownership remains unknown.",
      ),
      evidence(
        "probe",
        "Connection measurement",
        request.measure ? "measured" : "unknown",
        request.measure ? "connected" : "not_requested",
        request.measure
          ? "TCP connected from this source at this time."
          : "No TCP connection was attempted.",
      ),
    ],
    comparison: request.measure
      ? "Host firewall adapter prediction: deny. Selected-source TCP measurement: connected. Unknown foreign/provider/application layers remain unknown."
      : "No connection measurement was requested.",
  }
}

async function setup(page: Page, readonly = false) {
  const calls: PathRequest[] = []
  await mockNetwork(page, [], {
    ...(readonly
      ? { session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } } }
      : {}),
  })
  await page.route("**/api/v1/network/investigate/sources", (route) =>
    json(route, { containers: [{ id, name: "web" }] }),
  )
  await page.route("**/api/v1/network/investigate", (route) => {
    const request = route.request().postDataJSON() as PathRequest
    calls.push(request)
    return json(route, report(request))
  })
  return calls
}

test("keeps model and selected-source measurement distinct with provider layers unknown", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await page.getByRole("checkbox", { name: "Measure a TCP connection" }).check()
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  const result = page.getByRole("region", { name: "Connection path report" })
  await expect(result).toContainText("prediction: deny")
  await expect(result).toContainText("TCP connected from this source at this time")
  await expect(result).toContainText("Unknown foreign/provider/application layers remain unknown")
  await expect(result).not.toContainText("Allowed")
  expect(calls).toHaveLength(1)
  expect(calls[0]).toMatchObject({
    sourceKind: "host",
    target: "private.corp",
    family: "inet",
    protocol: "tcp",
    port: 443,
    measure: true,
  })
  await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
  await expect(page.locator('[data-slot="panel"]:not([data-plain])')).toHaveCount(0)
})

test("pins a chosen native DNS candidate on the next investigation", async ({ page }) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  await page.getByLabel("Chosen DNS address", { exact: true }).click()
  await page.getByRole("option", { name: "192.0.2.9", exact: true }).click()
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  await expect.poll(() => calls.length).toBe(2)
  expect(calls[1].address).toBe("192.0.2.9")
  expect(calls[1].measure).toBe(false)
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "192.0.2.3 → 192.0.2.9",
  )
})

test("container UDP investigation retains source/family/port and sends no TCP substitute", async ({
  page,
}) => {
  const calls = await setup(page)
  await page.goto("/network/investigate")
  await page.getByLabel("Source", { exact: true }).click()
  await page.getByRole("option", { name: "web", exact: true }).click()
  await page.getByLabel("Family", { exact: true }).click()
  await page.getByRole("option", { name: "IPv6", exact: true }).click()
  await page.getByLabel("Protocol", { exact: true }).click()
  await page.getByRole("option", { name: "UDP", exact: true }).click()
  await page.getByLabel("Destination", { exact: true }).fill("2001:db8::8")
  await page.getByLabel("Port", { exact: true }).fill("53")
  await expect(page.getByRole("checkbox", { name: "Measure a TCP connection" })).toBeDisabled()
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  await expect.poll(() => calls.length).toBe(1)
  expect(calls[0]).toEqual({
    sourceKind: "container",
    containerId: id,
    target: "2001:db8::8",
    family: "inet6",
    protocol: "udp",
    port: 53,
    measure: false,
  })
})

test("unavailable private DNS shows skipped later evidence and retains a failed next draft", async ({
  page,
}) => {
  await setup(page)
  let attempts = 0
  await page.route("**/api/v1/network/investigate", (route) => {
    attempts++
    if (attempts > 1)
      return route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "source_unavailable", message: "Container identity changed" },
        }),
      })
    const result = report(route.request().postDataJSON())
    result.scope.address = undefined
    result.addresses = []
    result.evidence = [
      evidence(
        "dns",
        "DNS",
        "unknown",
        "unavailable",
        "Native private suffix resolver is unavailable; no public fallback was queried.",
      ),
      evidence(
        "route",
        "Kernel route",
        "unknown",
        "skipped",
        "No selected destination exists; route lookup was not queried.",
      ),
      evidence(
        "probe",
        "Connection measurement",
        "unknown",
        "skipped",
        "No connection probe was attempted.",
      ),
    ]
    result.comparison = "No selected address exists; connectivity was not measured."
    return json(route, result)
  })
  await page.goto("/network/investigate")
  await page.getByLabel("Destination", { exact: true }).fill("private.corp")
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "no public fallback",
  )
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "not measured",
  )
  await page.getByLabel("Destination", { exact: true }).fill("next.private")
  await page.getByRole("button", { name: "Investigate", exact: true }).click()
  await expect(page.getByRole("alert")).toContainText("Container identity changed")
  await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("next.private")
  await expect(page.getByRole("region", { name: "Connection path report" })).toContainText(
    "private.corp",
  )
})

test("read-only accounts cannot start diagnostics or read source attribution", async ({ page }) => {
  const calls = await setup(page, true)
  let inventories = 0
  await page.route("**/api/v1/network/investigate/sources", (route) => {
    inventories++
    return json(route, { containers: [] })
  })
  await page.goto("/network/investigate")
  await expect(page.getByRole("button", { name: "Investigate", exact: true })).toBeDisabled()
  await expect(page.getByLabel("Destination", { exact: true })).toBeDisabled()
  await expect(
    page.getByRole("status").filter({ hasText: "system administrator access" }),
  ).toBeVisible()
  expect(calls).toHaveLength(0)
  expect(inventories).toBe(0)
})
