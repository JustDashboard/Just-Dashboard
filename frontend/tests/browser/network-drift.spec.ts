import { expect, test } from "@playwright/test"
import { json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import type { DriftReport } from "../../src/lib/network-drift"

const resource = "/etc/just-dashboard/network/links.ip"
const observation = {
  id: `render:${resource}`,
  domain: "render",
  resource,
  status: "drift" as const,
  coverage: "configuration",
  owned: true,
  repairable: true,
  reason: "The owned file differs from the saved configuration.",
  expected: { sha256: "expected-render" },
  observed: { sha256: "changed-render" },
}
const report: DriftReport = {
  checkedAt: "2026-10-08T12:00:00Z",
  finishedAt: "2026-10-08T12:00:01Z",
  status: "drift",
  consistent: true,
  savedGeneration: "saved-generation",
  canonicalGeneration: "canonical-generation",
  spec: {
    ...observation,
    id: "spec",
    resource: "spec.json",
    status: "matching",
    repairable: false,
  },
  journal: {
    ...observation,
    id: "journal",
    resource: "change.json",
    status: "not_required",
    repairable: false,
  },
  files: [observation],
  runtime: [],
  blocklists: [],
  boot: {
    status: "missing",
    unit: "just-dashboard-network.service",
    owned: true,
    repairable: true,
    reason: "The unit is not loaded.",
    execution: {
      status: "unrecorded",
      source: "systemd",
      scope: "current boot, last activation",
      bootTrigger: "unknown",
      reason: "No measured activation is available.",
      commands: [],
    },
  },
  repairPlan: {
    generation: "saved-generation",
    createdAt: "2026-10-08T12:00:01Z",
    status: "review_required",
    executable: false,
    blockers: [],
    excluded: [],
    preconditions: ["Saved generation must still match."],
    items: [
      {
        id: `repair:${observation.id}`,
        observationId: observation.id,
        domain: "render",
        resource,
        action: "regenerate_owned_file",
        reason: observation.reason,
        preconditions: ["Refuse foreign replacements."],
      },
    ],
  },
}

test("drift reports observed evidence and does not offer execution for an advisory plan", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: { "/network/drift": report } })
  await page.goto("/network/drift")
  await loaded(page)
  await expect(page.getByRole("heading", { name: "Network drift", exact: true })).toBeVisible()
  await expect(page.getByText("Reboot attribution is unknown", { exact: false })).toBeVisible()
  await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
  await page.getByRole("button", { name: "Review selected (1)" }).click()
  await expect(page.getByRole("dialog")).toContainText("No changes will be applied")
  await expect(page.getByRole("button", { name: /^Apply/ })).toHaveCount(0)
  expect(mutations).toEqual([])
})

test("a changed render invalidates reviewed evidence even with the same saved generation", async ({
  page,
}) => {
  await mockNetwork(page, [], { overrides: { "/network/drift": report } })
  await page.goto("/network/drift")
  const checkbox = page.getByRole("checkbox", { name: `Select repair for ${resource}` })
  await checkbox.check()
  await page.getByRole("button", { name: "Review selected (1)" }).click()
  await page.route("**/api/v1/network/drift", (route) =>
    json(route, {
      ...report,
      files: [{ ...observation, expected: { sha256: "new-cache-render" } }],
    }),
  )
  await page.getByRole("button", { name: "Close review" }).click()
  await page.getByRole("button", { name: "Inspect again" }).click()
  await expect(checkbox).not.toBeChecked()
  await expect(page.getByRole("button", { name: "Review selected (0)" })).toBeDisabled()
})

test("a failed refresh retains dated evidence and blocks repair review", async ({ page }) => {
  await mockNetwork(page, [], { overrides: { "/network/drift": report } })
  await page.goto("/network/drift")
  await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
  await page.route("**/api/v1/network/drift", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { message: "Inspection unavailable" } }),
    }),
  )
  await page.getByRole("button", { name: "Inspect again" }).click()
  await expect(page.getByText("Last known evidence", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Review selected (1)" })).toBeDisabled()
  await expect(page.getByRole("checkbox", { name: `Select repair for ${resource}` })).toBeDisabled()
  await expect(page.locator("time").first()).toHaveAttribute("datetime", report.checkedAt)
  await expect(page.getByRole("button", { name: "Refresh", exact: true })).toBeVisible()
})

test.describe("mobile evidence", () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test("long resource identities stay readable without horizontal overflow", async ({ page }) => {
    await mockNetwork(page, [], { overrides: { "/network/drift": report } })
    await page.goto("/network/drift")
    await loaded(page)
    await expect(page.getByText(resource, { exact: true }).first()).toBeVisible()
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    if (process.env.JD_NETWORK_SHOTS) {
      await page.screenshot({
        path: `${process.env.JD_NETWORK_SHOTS}/drift-390.png`,
        fullPage: true,
      })
    }
  })
})
