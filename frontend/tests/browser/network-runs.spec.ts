import { expect, test, type Page } from "@playwright/test"
import type { DiagnosticComparison, DiagnosticRun } from "../../src/lib/network-diagnostics"
import { admin, json, mockNetwork } from "./network-fixture"
import { risen } from "./risen"

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
    hold: false,
    release: () => {},
    next: 0,
    policy: { maxRuns: 100, maxAgeHours: 168 },
  }
  const held = new Promise<void>((resolve) => (state.release = resolve))
  await page.route("**/api/v1/network/diagnostics/**", async (route) => {
    if (state.hold) await held
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

const detail = (page: Page) => page.getByRole("region", { name: "Saved diagnostic", exact: true })
const actions = (page: Page) => page.getByRole("button", { name: "Run actions", exact: true })

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
    // Tools sends the reader to the address that used to select the run
    // beside the list; it opens the run's own page.
    await expect(page).toHaveURL(/\/network\/runs\/created-1$/)
    await expect(detail(page)).toContainText("Queued")
    const creates = state.calls.filter((call) => call.method === "POST")
    expect(creates).toHaveLength(2)
    expect(creates[1].body).toEqual({
      name: "IPv6 private listener",
      request: { tool: "port", target: "2001:db8::9", port: 8443 },
    })
    await page.reload()
    await expect(detail(page)).toContainText("IPv6 private listener")
    await expect(detail(page)).toContainText("2001:db8::9:8443")
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
  await expect(page).toHaveURL(/\/network\/runs\/running$/)
  await expect(detail(page)).toContainText("Running")
  await expect(detail(page).locator(".animate-breathe")).toHaveCount(0)
  await page.getByRole("button", { name: "Cancel run", exact: true }).click()
  await expect(page.getByRole("button", { name: "Stopping…", exact: true })).toBeDisabled()
  await expect(page.getByText("Waiting for the diagnostic to stop")).toBeVisible()
  await expect(detail(page)).not.toContainText("Cancelled")
  Object.assign(state.runs.get("running")!, {
    status: "cancelled",
    outcome: "cancelled",
    outcomeSource: "context",
    endedAt: now,
  })
  await expect(detail(page)).toContainText("Cancelled")
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
  await page.goto("/network/runs/baseline")
  await actions(page).click()
  await page.getByRole("menuitem", { name: "Rename", exact: true }).click()
  const rename = page.getByRole("dialog", { name: "Rename saved run" })
  const name = rename.getByRole("textbox", { name: "Saved run name" })
  await name.fill("DNS after resolver repair")
  await rename.getByRole("button", { name: "Save name", exact: true }).click()
  await expect(rename.getByText(/could not be saved/)).toBeVisible()
  await expect(name).toHaveValue("DNS after resolver repair")
  await rename.getByRole("button", { name: "Save name", exact: true }).click()
  await expect(rename).toHaveCount(0)
  await expect(detail(page)).toContainText("DNS after resolver repair")
  await page.getByRole("button", { name: "Rerun", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/runs\/rerun-1$/)
  await page.getByRole("tab", { name: /^Compare/ }).click()
  await expect(page).toHaveURL(/\/network\/runs\/rerun-1\?tab=compare$/)
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
  await actions(page).click()
  await page.getByRole("menuitem", { name: "Export JSON", exact: true }).click()
  expect((await download).suggestedFilename()).toBe("network-diagnostic-rerun-1.json")
  await expect(page.getByText("retained raw tool output", { exact: true })).toHaveCount(0)
  await page.getByRole("tab", { name: "Details", exact: true }).click()
  await expect(page.getByText("retained raw tool output", { exact: true })).toBeVisible()
  await expect(detail(page).getByRole("link", { name: "baseline", exact: true })).toHaveAttribute(
    "href",
    "/network/runs/baseline",
  )
  await actions(page).click()
  await page.getByRole("menuitem", { name: "Delete", exact: true }).click()
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
  // With no answer, what the tool said is the result.
  await expect(page.getByLabel("Probe error")).toContainText("Backend restarted")
  await page.reload()
  await expect(page.getByText("Interrupted at backend restart or shutdown")).toBeVisible()
  expect(state.calls.filter((call) => call.method !== "GET")).toHaveLength(0)
  await expect(page.getByRole("tab", { name: /^Compare/ })).toHaveCount(0)
  await expect(page.getByRole("tab", { name: "History", exact: true })).toHaveCount(0)
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
  test(`retained ${outcome} is a typed outcome with its evidence a view away`, async ({ page }) => {
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
    await expect(detail(page)).toContainText(label)
    await expect(page.getByText(/category is inferred from the tool/)).toBeVisible()
    await expect(page.getByText("Retained result is truncated")).toBeVisible()
    await expect(page.getByText("Retained tool error", { exact: true })).toHaveCount(0)
    await page.getByRole("tab", { name: "Details", exact: true }).click()
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
  await expect(page.getByRole("region", { name: "About saved runs" })).toContainText("Not read")
  state.failRead = "baseline"
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  const failedDetail = page.waitForResponse(
    (response) => response.url().endsWith("/diagnostics/baseline") && response.status() === 503,
  )
  await page.getByRole("link", { name: "Open IPv6 DNS baseline" }).click()
  await failedDetail
  await expect(page.getByText("Recording is unavailable", { exact: true })).toBeVisible()
  state.failRead = ""
  await page.getByRole("button", { name: "Try again", exact: true }).click()
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::1")
})

test("a failed later result read retains dated evidence and refresh retries", async ({ page }) => {
  const state = await fixture(page)
  await page.goto("/network/runs/baseline")
  await expect(page.getByLabel("Structured result")).toContainText("2001:db8::1")
  state.failRead = "baseline"
  // A finished run is read again only now and then; the page's refresh key
  // reads it at once.
  await page.keyboard.press("F5")
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
  const line = page.getByRole("region", { name: "About saved runs" })
  await expect(line).toContainText("keeps 100 finished for 7 days")
  await page.getByRole("button", { name: "Saved run actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Retention policy", exact: true }).click()
  const editor = page.getByRole("dialog", { name: "Retention policy" })
  await editor.getByRole("textbox", { name: "Finished runs to retain" }).fill("257")
  await expect(editor.getByRole("button", { name: "Save retention", exact: true })).toBeDisabled()
  await editor.getByRole("textbox", { name: "Finished runs to retain" }).fill("2")
  await editor.getByRole("textbox", { name: "Retention hours" }).fill("24")
  await editor.getByRole("button", { name: "Save retention", exact: true }).click()
  const dialog = page.getByRole("dialog", { name: "Change diagnostic retention" })
  expect(state.calls.filter((call) => call.method === "PUT")).toHaveLength(0)
  await dialog.getByRole("button", { name: "Save retention", exact: true }).click()
  await expect(dialog).toBeVisible()
  await expect(
    page.getByRole("textbox", { name: "Finished runs to retain", includeHidden: true }),
  ).toHaveValue("2")
  await dialog.getByRole("button", { name: "Save retention", exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await expect(editor).toHaveCount(0)
  expect(state.policy).toEqual({ maxRuns: 2, maxAgeHours: 24 })
  await expect(line).toContainText("keeps 2 finished for 1 day")
})

test("read access opens local tools without reading or starting retained runs", async ({
  page,
}) => {
  const reader = { ...admin, capabilities: ["read"], user: { ...admin.user, role: "readonly" } }
  const state = await fixture(page, [run()], reader)
  for (const path of ["/network/runs/baseline", "/network/runs?run=baseline", "/network/runs"]) {
    await page.goto(path)
    await expect(page.getByText("Saved diagnostics need the admin capability")).toBeVisible()
    await expect(page.getByRole("button", { name: "Rerun", exact: true })).toHaveCount(0)
    await expect(page.getByRole("button", { name: /actions/i })).toHaveCount(0)
  }
  expect(state.calls).toHaveLength(0)
  await page.getByRole("link", { name: "Run a tool", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/tools/)
  await expect(page.getByRole("button", { name: "Run and save", exact: true })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Saved runs", exact: true })).toHaveCount(0)
  expect(state.calls).toHaveLength(0)
})

test("an account that may not delete is offered no Delete on a run or its row", async ({
  page,
}) => {
  const keeper = { ...admin, capabilities: ["read", "system.admin"] }
  await fixture(page, [run()], keeper)
  await page.goto("/network/runs")
  await page.getByRole("button", { name: "Actions for IPv6 DNS baseline" }).click()
  await expect(page.getByRole("menuitem", { name: "Rerun", exact: true })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Delete", exact: true })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.goto("/network/runs/baseline")
  await actions(page).click()
  await expect(page.getByRole("menuitem", { name: "Export JSON", exact: true })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Delete", exact: true })).toHaveCount(0)
})

for (const width of [375, 1280, 1720]) {
  test(`saved runs fit a ${width}px reporting page`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixture(page)
    for (const path of ["/network/runs", "/network/runs?run=baseline"]) {
      await page.goto(path)
      await expect(page.getByText("IPv6 DNS baseline").first()).toBeVisible()
      await expect(page.locator('[data-slot="page"]')).toHaveAttribute("data-register", "reading")
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        width,
      )
    }
    await expect(page).toHaveURL(/\/network\/runs\/baseline$/)
    // The list beside the run is the wide page's; narrower, the run is the one pane.
    await expect(page.getByRole("navigation", { name: "Saved runs" })).toHaveCount(
      width >= 1280 ? 1 : 0,
    )
    await page.screenshot({ path: info.outputPath(`saved-runs-${width}.png`), fullPage: true })
  })
}

const mixed = () => [
  run("plain", { name: "Listeners", request: { tool: "listeners", target: "" } }),
  run("failed", {
    name: "A long name for a port check that will not fit a phone's row whole",
    request: { tool: "port", target: "2001:db8:100:200:300:400:500:600", port: 8443 },
    status: "failed",
    outcome: "unsupported",
    hasResult: false,
    result: undefined,
  }),
  run("busy", {
    name: "Traceroute",
    request: { tool: "traceroute", target: "mirror.example.test" },
    status: "running",
    outcome: undefined,
    endedAt: undefined,
    hasResult: false,
    result: undefined,
  }),
]

for (const width of [390, 1280]) {
  test(`run rows keep one height whatever they hold at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await fixture(page, mixed())
    await page.goto("/network/runs")
    const rows = page.getByRole("list", { name: "Saved runs" }).getByRole("listitem")
    await expect(rows).toHaveCount(3)
    await risen(page)
    const boxes = await Promise.all([0, 1, 2].map((index) => rows.nth(index).boundingBox()))
    expect(new Set(boxes.map((box) => box!.height)).size).toBe(1)
    // Each row starts where the one before it ends, plus the list's one gap.
    expect(boxes[2]!.y - boxes[1]!.y).toBe(boxes[1]!.y - boxes[0]!.y)
    // The state is on the row at either width: a column wide, the second line's lead on a phone.
    await expect(
      rows
        .filter({ hasText: "A long name" })
        .getByText("Unsupported on this host")
        .locator("visible=true"),
    ).toHaveCount(1)
    // No row of figures opens the page any more.
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true)
  })
}

test("the opening line stands in one place while the runs load and once they have", async ({
  page,
}) => {
  const state = await fixture(page)
  state.hold = true
  await page.goto("/network/runs")
  const line = page.getByRole("region", { name: "About saved runs" })
  const title = page.getByRole("region", { name: "Runs", exact: true })
  await expect(line).toBeVisible()
  await risen(page)
  const [lineLoading, titleLoading] = [await line.boundingBox(), await title.boundingBox()]
  state.release()
  const first = page.getByRole("list", { name: "Saved runs" }).getByRole("listitem").first()
  await expect(first).toBeVisible()
  await risen(page)
  expect(await line.boundingBox()).toEqual(lineLoading)
  expect((await title.boundingBox())!.y).toBe(titleLoading!.y)
})

test("the chips narrow the list, and the rail walks what is left with the view kept", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await fixture(page, [
    run("first", { name: "First DNS" }),
    run("second", { name: "Second DNS" }),
    run("refused", { name: "Refused port", status: "failed", outcome: "refused" }),
  ])
  await page.goto("/network/runs")
  const chips = page.getByRole("group", { name: "Run states" })
  await expect(chips.getByRole("button", { name: /^Completed/ })).toContainText("2")
  await expect(chips.getByRole("button", { name: /^Failed/ })).toContainText("1")
  await expect(chips.getByRole("button", { name: /^Interrupted/ })).toHaveCount(0)
  await chips.getByRole("button", { name: /^Completed/ }).click()
  const rows = page.getByRole("list", { name: "Saved runs" }).getByRole("listitem")
  await expect(rows).toHaveCount(2)
  await page.getByRole("link", { name: "Open Second DNS" }).click()
  await expect(page).toHaveURL(/\/network\/runs\/second$/)
  // The rail is the list as it was left: the chip still narrows it.
  const rail = page.getByRole("navigation", { name: "Saved runs" })
  await expect(rail.getByRole("link")).toHaveCount(2)
  await expect(rail.getByRole("link", { name: "Second DNS" })).toHaveAttribute(
    "aria-current",
    "page",
  )
  await page.getByRole("tab", { name: "Details", exact: true }).click()
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(/\/network\/runs\/first\?tab=details$/)
  await expect(detail(page)).toContainText("First DNS")
  await expect(page.getByRole("button", { name: "Next run", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "Previous run", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/runs\/second\?tab=details$/)
  await rail.getByRole("button", { name: /^All/ }).click()
  await expect(rail.getByRole("link")).toHaveCount(3)
})

test("a run that is gone says so and leads back to the list", async ({ page }) => {
  await fixture(page)
  await page.goto("/network/runs/expired")
  await expect(page.getByText("No saved run with that ID is kept")).toBeVisible()
  await page.getByRole("link", { name: "All saved runs", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/runs$/)
})

test("nothing watched is a mark, a sentence and the step that makes one", async ({ page }) => {
  await fixture(page, [])
  await page.route("**/api/v1/certificates/watched**", (route) => json(route, []))
  await page.goto("/network/runs")
  await expect(page.getByText("No saved runs", { exact: true })).toBeVisible()
  const panel = page.getByRole("region", { name: "Watched probes" })
  await expect(panel).toContainText("No probe is watched")
  await expect(panel.getByRole("link", { name: "Port check", exact: true })).toHaveAttribute(
    "href",
    "/network/tools?tool=port",
  )
})

test("watched probes are the watch list's own, checked and stopped from the runs page", async ({
  page,
}) => {
  await fixture(page)
  let rows = [
    {
      id: 7,
      domain: "db.example.test",
      port: 5432,
      kind: "tcp",
      checkedAt: new Date(Date.now() - 60_000).toISOString(),
      probe: { ok: true, state: "connected", ms: 3, address: "10.0.0.5:5432" },
    },
    {
      id: 8,
      domain: "cache.example.test",
      port: 6379,
      kind: "tcp",
      checkedAt: new Date(Date.now() - 60_000).toISOString(),
      probe: {
        ok: false,
        state: "refused",
        error: "dial tcp 10.0.0.6:6379: connect: connection refused",
      },
    },
    { id: 9, domain: "app.example.test", port: 443, kind: "tls" },
  ]
  const calls: string[] = []
  await page.route("**/api/v1/certificates/watched**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    calls.push(`${request.method()} ${path}`)
    if (path.endsWith("/history")) return json(route, [])
    if (request.method() === "DELETE") {
      rows = rows.filter((row) => !path.endsWith(`/${row.id}`))
      return route.fulfill({ status: 204 })
    }
    return json(route, rows)
  })
  await page.goto("/network/runs")
  const panel = page.getByRole("region", { name: "Watched probes" })
  await expect(panel.getByText("db.example.test:5432")).toBeVisible()
  await expect(panel.getByText("connects in 3 ms")).toBeVisible()
  await expect(panel.getByText("refused", { exact: true })).toBeVisible()
  await expect(panel.getByText("connect: connection refused")).toBeVisible()
  // A TLS watch is the certificates page's, not a probe.
  await expect(panel.getByText("app.example.test")).toHaveCount(0)
  await expect(panel.getByRole("link", { name: "Watch list" })).toHaveAttribute(
    "href",
    "/proxy/certificates",
  )

  await panel.getByRole("button", { name: "Check now" }).click()
  await expect.poll(() => calls.includes("POST /api/v1/certificates/watched/check")).toBe(true)
  await panel.getByRole("button", { name: "Stop watching cache.example.test:6379" }).click()
  await expect(panel.getByText("cache.example.test:6379")).toHaveCount(0)
  expect(calls).toContain("DELETE /api/v1/certificates/watched/8")
})
