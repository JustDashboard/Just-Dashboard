import { expect, test, type Page } from "@playwright/test"
import type { DiagnosticComparison, DiagnosticRun } from "../../src/lib/network-diagnostics"
import { admin, json, mockNetwork } from "./network-fixture"

const now = "2026-10-08T12:00:00Z"
function run(id = "baseline", overrides: Partial<DiagnosticRun> = {}): DiagnosticRun {
  return {
    id,
    name: "IPv6 DNS baseline",
    request: { tool: "dns", target: "example.test", record: "AAAA" },
    scope: {
      vantage: "dashboard_host",
      target: "example.test",
      family: "resolved_at_execution",
      protocol: "resolver_selected",
      limitations: [
        "This host observation does not establish inbound or provider-policy reachability.",
      ],
    },
    status: "completed",
    outcome: "completed",
    outcomeSource: "tool_result",
    createdAt: now,
    startedAt: now,
    endedAt: now,
    updatedAt: now,
    createdBy: "operator",
    jobId: `job-${id}`,
    stages: [
      { id: "validation", status: "completed", startedAt: now, endedAt: now, outcome: "validated" },
      { id: "probe", status: "completed", startedAt: now, endedAt: now, outcome: "completed" },
      { id: "recording", status: "completed", startedAt: now, endedAt: now, outcome: "saved" },
    ],
    hasResult: true,
    resultTruncated: false,
    result: {
      tool: "dns",
      target: "example.test",
      ok: true,
      records: ["2001:db8::1"],
      output: "retained raw tool output",
      duration: "2ms",
    },
    ...overrides,
  }
}

async function fixture(page: Page, records: DiagnosticRun[] = [run()], session = admin) {
  await mockNetwork(page, [], { session })
  const state = {
    runs: new Map(records.map((record) => [record.id, structuredClone(record)])),
    calls: [] as { method: string; path: string; query: URLSearchParams; body: unknown }[],
    failCreate: 0,
    failSave: 0,
    failPolicy: 0,
    failRead: "",
    next: 0,
    policy: { maxRuns: 100, maxAgeHours: 168 },
  }
  await page.route("**/api/v1/network/diagnostics/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace("/api/v1/network/diagnostics", "")
    const method = request.method()
    const body = request.postData() ? request.postDataJSON() : undefined
    state.calls.push({ method, path, query: url.searchParams, body })
    const fail = (status: number) =>
      json(
        route,
        {
          error: {
            code: "diagnostic_conflict",
            message: "The diagnostic request could not be saved. Try again.",
          },
        },
        status,
      )
    if (path === "/policy") {
      if (method === "GET") return json(route, state.policy)
      if (state.failPolicy) {
        const status = state.failPolicy
        state.failPolicy = 0
        return fail(status)
      }
      state.policy = body
      return json(route, state.policy)
    }
    if (path === "/compare") {
      const comparison: DiagnosticComparison = {
        beforeId: url.searchParams.get("before")!,
        afterId: url.searchParams.get("after")!,
        request: records[0]?.request ?? run().request,
        status: { before: "completed", after: "completed" },
        outcome: { before: "completed", after: "completed" },
        duration: { before: "2ms", after: "5ms" },
        durationDeltaMs: 3,
        records: {
          added: ["2001:db8::2"],
          removed: ["2001:db8::1"],
          unchanged: 0,
          truncated: false,
        },
        output: { added: ["new answer"], removed: ["old answer"], unchanged: 0, truncated: false },
        partial: false,
        limitations: ["Line order and duplicate counts are ignored."],
      }
      return json(route, comparison)
    }
    if (path === "/") {
      if (method === "GET" && state.failRead === "list")
        return json(
          route,
          {
            error: {
              code: "diagnostics_unavailable",
              message: "Recording is unavailable",
              retryable: true,
            },
          },
          503,
        )
      if (method === "GET")
        return json(
          route,
          [...state.runs.values()].reverse().map((record) => ({ ...record, result: undefined })),
        )
      if (state.failCreate) {
        const status = state.failCreate
        state.failCreate = 0
        return fail(status)
      }
      const record = run(`created-${++state.next}`, {
        name: body.name,
        request: body.request,
        status: "queued",
        outcome: undefined,
        outcomeSource: undefined,
        startedAt: undefined,
        endedAt: undefined,
        hasResult: false,
        result: undefined,
        stages: [
          { id: "validation", status: "completed", endedAt: now },
          { id: "probe", status: "queued" },
          { id: "recording", status: "queued" },
        ],
      })
      state.runs.set(record.id, record)
      return json(route, record, 202)
    }
    const [id, verb] = path.slice(1).split("/")
    const record = state.runs.get(id)
    if (!record) return json(route, { error: { code: "not_found", message: "Run not found" } }, 404)
    if (method === "GET") {
      if (id === state.failRead)
        return json(
          route,
          {
            error: {
              code: "diagnostics_unavailable",
              message: "Recording is unavailable",
              retryable: true,
            },
          },
          503,
        )
      if (verb === "history")
        return json(route, {
          request: record.request,
          limitations: ["Only runs still inside the retention policy are listed."],
          points: [...state.runs.values()]
            .filter((other) => JSON.stringify(other.request) === JSON.stringify(record.request))
            .map((other) => ({
              id: other.id,
              name: other.name,
              createdAt: other.createdAt,
              status: other.status,
              outcome: other.outcome,
              metrics: other.result?.metrics ?? [],
            })),
        })
      return json(route, verb === "export" ? { version: 1, run: record } : record)
    }
    if (method === "PATCH") {
      if (state.failSave) {
        const status = state.failSave
        state.failSave = 0
        return fail(status)
      }
      record.name = body.name
      return json(route, record)
    }
    if (method === "DELETE") {
      state.runs.delete(id)
      return route.fulfill({ status: 204 })
    }
    if (verb === "cancel") {
      record.status = "cancelling"
      record.endedAt = undefined
      return json(route, record, 202)
    }
    if (verb === "rerun") {
      const next = run(`rerun-${++state.next}`, {
        name: record.name,
        request: record.request,
        rerunOf: record.id,
        createdAt: "2026-10-08T12:05:00Z",
        result: { ...record.result!, records: ["2001:db8::2"], duration: "5ms" },
      })
      state.runs.set(next.id, next)
      return json(route, next, 202)
    }
    return fail(400)
  })
  return state
}

for (const status of [409, 500]) {
  test(`a ${status} launch keeps the named IPv6 tool draft and runs only on retry`, async ({
    page,
  }) => {
    const state = await fixture(page, [])
    state.failCreate = status
    let quick = 0
    await page.route("**/api/v1/network/probe", async (route) => {
      quick++
      await json(route, run().result)
    })
    await page.goto("/network/tools?tool=port&target=2001:db8::9")
    await page.getByRole("textbox", { name: "Port", exact: true }).fill("8443")
    await page.getByRole("button", { name: "Run and save", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Save a diagnostic run" })
    await dialog.getByRole("textbox", { name: "Run name" }).fill("IPv6 private listener")
    expect(state.calls.filter((call) => call.method === "POST")).toHaveLength(0)
    await dialog.getByRole("button", { name: "Run and save", exact: true }).click()
    await expect(dialog.getByText(/could not be saved/)).toBeVisible()
    await expect(dialog.getByRole("textbox", { name: "Run name" })).toHaveValue(
      "IPv6 private listener",
    )
    await expect(page.locator("#tool-port-port")).toHaveValue("8443")
    await dialog.getByRole("button", { name: "Run and save", exact: true }).click()
    await expect(page).toHaveURL(/\/network\/runs\?run=created-1$/)
    await expect(page.getByRole("region", { name: "Saved diagnostic" })).toContainText("Queued")
    const creates = state.calls.filter((call) => call.method === "POST")
    expect(creates).toHaveLength(2)
    expect(creates[1].body).toEqual({
      name: "IPv6 private listener",
      request: { tool: "port", target: "2001:db8::9", port: 8443 },
    })
    await page.reload()
    await expect(page.getByRole("textbox", { name: "Saved run name" })).toHaveValue(
      "IPv6 private listener",
    )
    expect(state.calls.filter((call) => call.method === "POST")).toHaveLength(2)
    expect(quick).toBe(0)
  })
}

test("a cancellation stays pending until cleanup is observed", async ({ page }) => {
  const record = run("running", {
    status: "running",
    outcome: undefined,
    endedAt: undefined,
    hasResult: false,
    result: undefined,
  })
  const state = await fixture(page, [record])
  await page.goto("/network/runs?run=running")
  await expect(page.getByRole("region", { name: "Saved diagnostic" })).toContainText("Running")
  await expect(
    page.getByRole("region", { name: "Saved diagnostic" }).locator(".animate-breathe"),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "Cancel run", exact: true }).click()
  await expect(page.getByRole("button", { name: "Stopping…", exact: true })).toBeDisabled()
  await expect(page.getByText("Waiting for the diagnostic to stop")).toBeVisible()
  await expect(page.getByRole("region", { name: "Saved diagnostic" })).not.toContainText(
    "Cancelled",
  )
  Object.assign(state.runs.get("running")!, {
    status: "cancelled",
    outcome: "cancelled",
    outcomeSource: "context",
    endedAt: now,
  })
  await expect(page.getByRole("region", { name: "Saved diagnostic" })).toContainText("Cancelled")
  await expect(page.getByRole("button", { name: "Rerun", exact: true })).toBeVisible()
  expect(
    state.calls.filter((call) => call.method === "POST" && call.path.endsWith("/cancel")),
  ).toHaveLength(1)
})

test("saved names survive failed writes, explicit reruns compare and export, and deletion confirms", async ({
  page,
}) => {
  const state = await fixture(page)
  state.failSave = 500
  await page.goto("/network/runs?run=baseline")
  const name = page.getByRole("textbox", { name: "Saved run name" })
  await name.fill("DNS after resolver repair")
  await page.getByRole("button", { name: "Save name", exact: true }).click()
  await expect(page.getByText(/could not be saved/)).toBeVisible()
  await expect(name).toHaveValue("DNS after resolver repair")
  await page.getByRole("button", { name: "Save name", exact: true }).click()
  await expect(page.getByRole("button", { name: "Save name", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "Rerun", exact: true }).click()
  await expect(page).toHaveURL(/run=rerun-1$/)
  await page.getByRole("combobox", { name: "Baseline run", exact: true }).click()
  await page.getByRole("option").filter({ hasText: "DNS after resolver repair" }).click()
  await page.getByRole("button", { name: "Compare", exact: true }).click()
  await expect(page.getByLabel("Run comparison result")).toContainText("+3 ms")
  await expect(page.getByLabel("Structured records", { exact: true })).toContainText(
    "1 added · 1 removed",
  )
  const compare = state.calls.find((call) => call.path === "/compare")!
  expect(compare.query.get("before")).toBe("baseline")
  expect(compare.query.get("after")).toBe("rerun-1")
  const download = page.waitForEvent("download")
  await page.getByRole("button", { name: "Export JSON", exact: true }).click()
  expect((await download).suggestedFilename()).toBe("network-diagnostic-rerun-1.json")
  await expect(page.getByText("retained raw tool output", { exact: true })).not.toBeVisible()
  await page.getByText("Bounded tool evidence", { exact: true }).click()
  await expect(page.getByText("retained raw tool output", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Delete", exact: true }).click()
  expect(state.calls.filter((call) => call.method === "DELETE")).toHaveLength(0)
  await page
    .getByRole("dialog", { name: "Delete saved run" })
    .getByRole("button", { name: "Delete run", exact: true })
    .click()
  await expect(page).toHaveURL(/\/network\/runs$/)
  expect(state.runs.has("rerun-1")).toBe(false)
  expect(state.runs.has("baseline")).toBe(true)
})

test("restart interruption remains saved across reload and never launches itself", async ({
  page,
}) => {
  const state = await fixture(page, [
    run("interrupted", {
      status: "interrupted",
      outcome: "interrupted",
      outcomeSource: "recovery",
      hasResult: false,
      result: undefined,
      error: "Backend restarted before this run finished; final cleanup was not observed.",
    }),
  ])
  await page.goto("/network/runs?run=interrupted")
  await expect(page.getByText("Interrupted at backend restart or shutdown")).toBeVisible()
  await expect(page.getByText(/No answer was retained/)).toBeVisible()
  await page.reload()
  await expect(page.getByText("Interrupted at backend restart or shutdown")).toBeVisible()
  expect(state.calls.filter((call) => call.method !== "GET")).toHaveLength(0)
  await expect(page.getByRole("combobox", { name: "Baseline run" })).toHaveCount(0)
})

for (const [outcome, label] of Object.entries({
  timed_out: "Timed out",
  refused: "Connection refused",
  dns_failure: "DNS failure",
  permission_denied: "Permission denied",
  unsupported: "Unsupported on this host",
  invalid_certificate: "Invalid certificate",
  completed_with_findings: "Completed with findings",
})) {
  test(`retained ${outcome} is a typed outcome with expandable evidence`, async ({ page }) => {
    await fixture(page, [
      run("outcome", {
        status: outcome === "completed_with_findings" ? "completed" : "failed",
        outcome,
        outcomeSource: "error_text",
        error: "Retained tool error",
        resultTruncated: true,
      }),
    ])
    await page.goto("/network/runs?run=outcome")
    await expect(page.getByRole("region", { name: "Saved diagnostic" })).toContainText(label)
    await expect(page.getByText(/category is inferred from the tool/)).toBeVisible()
    await expect(page.getByText("Retained result is truncated")).toBeVisible()
    await expect(page.getByText("Retained tool error", { exact: true })).not.toBeVisible()
    await page.getByText("Bounded tool evidence", { exact: true }).click()
    await expect(page.getByText("Retained tool error", { exact: true })).toBeVisible()
  })
}

test("initial list and result failures offer retry without inventing an empty inventory", async ({
  page,
}) => {
  const state = await fixture(page)
  state.failRead = "list"
  await page.goto("/network/runs")
  await expect(page.getByText("Recording is unavailable", { exact: true })).toBeVisible()
  await expect(page.getByText(/No saved runs/)).toHaveCount(0)
  state.failRead = "baseline"
  const failedDetail = page.waitForResponse(
    (response) => response.url().endsWith("/diagnostics/baseline") && response.status() === 503,
  )
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  await failedDetail
  await expect(page.getByText("Recording is unavailable", { exact: true })).toBeVisible()
  state.failRead = ""
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::1")
})

test("a failed later result read retains dated evidence and refresh retries", async ({ page }) => {
  const state = await fixture(page)
  await page.goto("/network/runs?run=baseline")
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::1")
  state.failRead = "baseline"
  await expect(page.getByText("Showing the last known diagnostic result")).toBeVisible()
  await expect(page.getByText(/Last successful read:/)).toBeVisible()
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::1")
  state.failRead = ""
  state.runs.get("baseline")!.result!.records = ["2001:db8::2"]
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::2")
  await expect(page.getByText("Showing the last known diagnostic result")).toHaveCount(0)
})

test("retention is bounded and confirms deletion, keeping the draft after refusal", async ({
  page,
}) => {
  const state = await fixture(page)
  state.failPolicy = 500
  await page.goto("/network/runs")
  await page.getByText("Retention policy", { exact: true }).click()
  await page.getByRole("textbox", { name: "Finished runs to retain" }).fill("257")
  await expect(page.getByRole("button", { name: "Save retention", exact: true })).toBeDisabled()
  await page.getByRole("textbox", { name: "Finished runs to retain" }).fill("2")
  await page.getByRole("textbox", { name: "Retention hours" }).fill("24")
  await page.getByRole("button", { name: "Save retention", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Change diagnostic retention" })
  expect(state.calls.filter((call) => call.method === "PUT")).toHaveLength(0)
  await dialog.getByRole("button", { name: "Save retention", exact: true }).click()
  await expect(dialog).toBeVisible()
  await expect(
    page.getByRole("textbox", { name: "Finished runs to retain", includeHidden: true }),
  ).toHaveValue("2")
  await dialog.getByRole("button", { name: "Save retention", exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(state.policy).toEqual({ maxRuns: 2, maxAgeHours: 24 })
})

test("read access opens local tools without reading or starting retained runs", async ({
  page,
}) => {
  const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
  const state = await fixture(page, [], reader)
  await page.goto("/network/runs")
  await expect(page.getByText("Saved diagnostics need the admin capability")).toBeVisible()
  expect(state.calls).toHaveLength(0)
  await page.getByRole("link", { name: "Quick tools", exact: true }).click()
  await expect(page.getByRole("button", { name: "Run and save", exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Saved runs", exact: true })).toHaveCount(0)
  expect(state.calls).toHaveLength(0)
})

for (const width of [375, 1280, 1720]) {
  test(`saved runs fit a ${width}px reporting page`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixture(page)
    await page.goto("/network/runs?run=baseline")
    await expect(page.getByRole("textbox", { name: "Saved run name" })).toBeVisible()
    await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
    await page.screenshot({ path: info.outputPath(`saved-runs-${width}.png`), fullPage: true })
  })
}
