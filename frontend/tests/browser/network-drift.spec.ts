import { expect, test } from "@playwright/test"
import { admin, json, loaded, mockNetwork, type Mutation } from "./network-fixture"
import type { DriftReport } from "../../src/lib/network-drift"

const resource = "/etc/just-dashboard/network/links.batch"
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

const executableReport: DriftReport = {
  ...report,
  repairPlan: {
    ...report.repairPlan,
    executable: true,
    items: [
      {
        ...report.repairPlan.items[0],
        executable: true,
        reviewToken: "reviewed-token",
        effect: "boot_files",
        before: "# Generated owned render\n# old boot input\n",
        after: "# Generated owned render\n# reviewed boot input\n",
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

test("an administrator reviews exact changes with pending confirmation when the global preference is off", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, {
    overrides: {
      "/network/drift": executableReport,
      "/network/changes/current": { available: true, owned: true, change: null },
    },
  })
  await page.route("**/api/v1/network/drift/repairs", async (route) => {
    const request = route.request()
    expect(request.headers()["x-jd-network-apply"]).toBe("pending")
    mutations.push({
      method: request.method(),
      path: "/network/drift/repairs",
      body: request.postDataJSON(),
    })
    return json(route, {
      phase: "awaiting_confirmation",
      watchdog: "armed",
      persistence: "written",
      runtime: "not_applied",
      boot: "not_verified",
    })
  })
  await page.goto("/network/drift")
  const confirmationPreference = page.getByRole("switch", {
    name: "Require network reconnection confirmation",
  })
  await expect(confirmationPreference).toBeChecked()
  await confirmationPreference.uncheck()
  await expect(confirmationPreference).not.toBeChecked()
  await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
  await page.getByRole("button", { name: "Review selected (1)" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("old boot input")
  await expect(dialog).toContainText("reviewed boot input")
  await dialog.getByRole("button", { name: "Apply selected repairs" }).click()
  await expect(dialog).toHaveCount(0)
  expect(mutations).toEqual([
    {
      method: "POST",
      path: "/network/drift/repairs",
      body: {
        generation: executableReport.savedGeneration,
        selections: [
          { id: executableReport.repairPlan.items[0].id, reviewToken: "reviewed-token" },
        ],
      },
    },
  ])
})

test("a fresh preflight detects shifted review evidence before any repair request", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, { overrides: { "/network/drift": executableReport } })
  await page.goto("/network/drift")
  await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
  await page.getByRole("button", { name: "Review selected (1)" }).click()
  await page.route("**/api/v1/network/drift", (route) =>
    json(route, {
      ...executableReport,
      files: [
        { ...observation, observed: { sha256: "foreign-replacement", identity: "new-inode" } },
      ],
    }),
  )
  await page.getByRole("button", { name: "Apply selected repairs" }).click()
  await expect(page.getByRole("dialog")).toContainText("No repair request was sent.")
  expect(mutations).toEqual([])
})

test("read-only accounts can inspect executable proposals without an apply control", async ({
  page,
}) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations, {
    session: { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } },
    overrides: { "/network/drift": executableReport },
  })
  await page.goto("/network/drift")
  await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
  await page.getByRole("button", { name: "Review selected (1)" }).click()
  await expect(page.getByRole("dialog")).toContainText("No changes will be applied")
  await expect(page.getByRole("button", { name: "Apply selected repairs" })).toHaveCount(0)
  expect(mutations).toEqual([])
})

test.describe("mobile evidence", () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test("long resource identities stay readable without horizontal overflow", async ({ page }) => {
    const mutations: Mutation[] = []
    await mockNetwork(page, mutations, { overrides: { "/network/drift": executableReport } })
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
    await page.getByRole("checkbox", { name: `Select repair for ${resource}` }).check()
    await page.getByRole("button", { name: "Review selected (1)" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("old boot input")
    await expect(dialog).toContainText("reviewed boot input")
    expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(
      true,
    )
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
    expect(mutations).toEqual([])
    if (process.env.JD_NETWORK_SHOTS) {
      await page.screenshot({ path: `${process.env.JD_NETWORK_SHOTS}/drift-review-390.png` })
    }
  })
})
