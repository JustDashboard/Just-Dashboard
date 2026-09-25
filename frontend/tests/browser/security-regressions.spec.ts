import { expect, test, type Page, type Route } from "@playwright/test"

const signedIn = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  capabilities: ["read", "service.control", "file.write", "destructive"],
  user: { id: 1, username: "operator", role: "operator" },
}
const columns = [
  { name: "id", type: "bigint", nullable: false },
  { name: "name", type: "text", nullable: false },
]
const result = (rows: unknown[][]) => ({
  columns: ["id", "name"],
  types: ["bigint", "text"],
  rows,
  rowCount: rows.length,
  rowsAffected: 0,
  duration: "1ms",
})
const fulfill = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

for (const admin of [false, true]) {
  test(`Compose controls ${admin ? "remain available to administrators" : "require administrator access"}`, async ({
    page,
  }) => {
    const stack = {
      name: "permission-test",
      workingDir: "/srv/permission-test",
      configPath: "/srv/permission-test/compose.yaml",
      configFiles: ["/srv/permission-test/compose.yaml"],
      services: [
        { name: "web", image: "nginx", ports: [], state: "running", container: "abc" },
        { name: "worker", image: "worker", ports: [], state: "missing", missing: true },
      ],
      managed: true,
      running: 1,
      total: 2,
      orphans: [],
      state: "partial",
      summary: "Partly running",
    }
    const mutations: string[] = []
    await page.route("**/api/v1/**", async (route) => {
      const path = new URL(route.request().url()).pathname.slice(7)
      if (route.request().method() !== "GET") mutations.push(path)
      if (path === "/auth/session")
        return fulfill(route, {
          ...signedIn,
          capabilities: [...signedIn.capabilities, ...(admin ? ["system.admin"] : [])],
        })
      if (path === "/docker/ping")
        return fulfill(route, { available: true, serverVersion: "29.8.0" })
      if (path === "/docker/stacks/") return fulfill(route, [stack])
      if (path === "/docker/stacks/permission-test") return fulfill(route, stack)
      if (path === "/docker/stacks/permission-test/config")
        return fulfill(route, {
          path: stack.configPath,
          content: "services:\n  web:\n    image: nginx\n",
        })
      return route.fulfill({ status: 503, contentType: "application/json", body: "{}" })
    })
    await page.goto("/docker/stacks")
    await expect(page.getByRole("button", { name: "permission-test", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Create stack", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    // A stack is a page of its own, so its controls are checked there.
    await page.getByRole("button", { name: "permission-test", exact: true }).click()
    await page.waitForURL(/\/docker\/stacks\/permission-test$/)
    const panel = page.getByRole("main")
    await expect(panel.getByText("worker", { exact: true })).toBeVisible()
    await expect(panel.getByRole("button", { name: "Deploy", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    await expect(panel.getByRole("button", { name: "Recreate service", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    await expect(panel.getByRole("button", { name: "Create it", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    if (!admin) await expect(panel.getByText("Administrator access required")).toBeVisible()
    await panel.getByRole("tab", { name: "Compose file", exact: true }).click()
    await expect(panel.getByText(stack.configPath, { exact: true })).toBeVisible()
    await expect(panel.getByRole("button", { name: "Check", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    await expect(panel.getByRole("button", { name: "Save", exact: true })).toHaveCount(
      admin ? 1 : 0,
    )
    expect(mutations).toEqual([])
  })
}

async function databaseFixture(page: Page) {
  const mutations: unknown[] = [],
    held: Route[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname.slice(7)
    let body: unknown = []
    if (path === "/auth/session") body = signedIn
    if (path === "/dashboard/update") body = { current: "0.6.7", latest: "0.6.7", releases: [] }
    if (path === "/databases/")
      body = [
        {
          id: 1,
          name: "Test database",
          driver: "postgres",
          host: "localhost",
          port: 5432,
          database: "test",
          username: "test",
          createdAt: new Date().toISOString(),
        },
      ]
    if (path === "/databases/drivers")
      body = [{ id: "postgres", name: "Postgres", sql: true, ddl: true, filterOps: ["eq"] }]
    if (path === "/databases/1/tables")
      body = ["items", "other"].map((name) => ({
        schema: "public",
        name,
        type: "table",
        estimatedRows: 2,
      }))
    if (path === "/databases/1/table")
      body = { columns, primaryKey: ["id"], foreignKeys: [], indexes: [] }
    if (path === "/databases/1/browse") {
      if (url.searchParams.get("table") === "other") {
        held.push(route)
        return
      }
      body = result(
        url.searchParams.has("orderBy")
          ? [
              ["2", "B"],
              ["1", "A"],
            ]
          : [
              ["1", "A"],
              ["2", "B"],
            ],
      )
    }
    if (path === "/databases/1/rows") {
      mutations.push(request.postDataJSON())
      body = {}
    }
    await fulfill(route, body)
  })
  await page.goto("/databases/browse?conn=1&schema=public&table=items")
  await expect(page.getByRole("checkbox", { name: "Select row 1", exact: true })).toBeVisible()
  return { mutations, held }
}

test("database selections cannot follow a different sort or a loading table", async ({ page }) => {
  const { mutations, held } = await databaseFixture(page)
  await page.getByRole("checkbox", { name: "Select row 1", exact: true }).click()
  await expect(page.getByRole("button", { name: "Delete 1", exact: true })).toBeVisible()
  await page.getByTitle("Sort by id", { exact: true }).click()
  await expect(page.getByRole("checkbox", { name: "Select row 1", exact: true })).not.toBeChecked()
  await expect(page.getByRole("button", { name: "Delete 1", exact: true })).toHaveCount(0)
  await page.getByRole("checkbox", { name: "Select row 1", exact: true }).click()
  await page.getByRole("button", { name: /^other\b/ }).click()
  await expect(page).toHaveURL(/table=other/)
  await expect.poll(() => held.length).toBeGreaterThan(0)
  await expect(page.getByRole("checkbox", { name: "Select row 1", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Delete 1", exact: true })).toHaveCount(0)
  expect(mutations).toEqual([])
})

test("the row editor submits exact BIGINT digits", async ({ page }) => {
  const { mutations } = await databaseFixture(page)
  await page.getByRole("button", { name: "Insert", exact: true }).click()
  await page.locator('[id="f-id"]').fill("9007199254740993")
  await page.locator('[id="f-name"]').fill("precision")
  await page.getByRole("dialog").getByRole("button", { name: "Insert", exact: true }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(mutations).toMatchObject([{ values: { id: "9007199254740993", name: "precision" } }])
})

test("slow polling has only one request in flight", async ({ page }) => {
  await page.clock.install()
  const pending: Route[] = []
  let initial = true
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.slice(7)
    // The last day's readings poll on their own cadence; the trail's own
    // request is the one this is about.
    if (path === "/audit/" && url.searchParams.has("since")) {
      return fulfill(route, { entries: [], total: 0 })
    }
    if (path === "/audit/") {
      if (initial) {
        initial = false
        return fulfill(route, {
          entries: [
            {
              id: 1,
              ts: new Date().toISOString(),
              username: "operator",
              action: "read",
              target: "INITIAL_RESULT",
              success: true,
              status: 200,
            },
          ],
          total: 1,
        })
      }
      pending.push(route)
      return
    }
    await fulfill(route, path === "/auth/session" ? signedIn : [])
  })
  await page.goto("/audit")
  await expect(page.getByRole("table").getByText("INITIAL_RESULT", { exact: true })).toBeVisible()
  await page.clock.runFor(15001)
  await expect.poll(() => pending.length).toBe(1)
  await page.clock.runFor(60000)
  expect(pending).toHaveLength(1)
  await fulfill(pending[0], {
    entries: [
      {
        id: 2,
        ts: new Date().toISOString(),
        username: "operator",
        action: "read",
        target: "FRESH_RESULT",
        success: true,
        status: 200,
      },
    ],
    total: 1,
  })
  await expect(page.getByRole("table").getByText("FRESH_RESULT", { exact: true })).toBeVisible()
})

test("a reset password is changed only after the account's second factor", async ({ page }) => {
  const changes: unknown[] = []
  let status: unknown = { authenticated: false, needsTotp: false, needsEnrollment: false }
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.slice(7)
    if (path === "/auth/login")
      status = { ...signedIn, authenticated: false, needsTotp: true, needsPasswordChange: false }
    if (path === "/auth/2fa/verify")
      status = { ...signedIn, authenticated: false, needsPasswordChange: true }
    if (path === "/account/password") {
      changes.push(route.request().postDataJSON())
      status = { authenticated: false, needsTotp: false, needsEnrollment: false }
      return fulfill(route, {})
    }
    await fulfill(route, status)
  })
  await page.goto("/login")
  await page.getByLabel("Username", { exact: true }).fill("operator")
  await page.getByLabel("Password", { exact: true }).fill("temporary-password")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Two-factor code", exact: true })).toBeVisible()
  await expect(page.getByLabel("New password", { exact: true })).toHaveCount(0)
  await page.getByLabel("Verification code", { exact: true }).fill("123456")
  await page.getByRole("button", { name: "Verify", exact: true }).click()
  await expect(
    page.getByRole("heading", { name: "Change your password", exact: true }),
  ).toBeVisible()
  await page.getByLabel("New password", { exact: true }).fill("replacement-password")
  await page.getByLabel("Repeat new password", { exact: true }).fill("replacement-password")
  await page.getByRole("button", { name: "Change password", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Sign in", exact: true })).toBeVisible()
  expect(changes).toEqual([
    { currentPassword: "temporary-password", newPassword: "replacement-password" },
  ])
})

test("same-name PM2 applications use trusted daemon and process identities", async ({ page }) => {
  const actions: URL[] = [],
    sockets: URL[] = []
  await page.routeWebSocket("**/api/v1/pm2/**", (ws) => {
    sockets.push(new URL(ws.url()))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url()),
      path = url.pathname.slice(7)
    if (path === "/system/metrics")
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: { code: "unavailable", message: "Metrics unavailable in this test" },
        }),
      })
    let body: unknown = []
    if (path === "/auth/session") body = signedIn
    if (path === "/pm2/")
      body = {
        available: true,
        daemons: ["alice", "bob"].map((account) => ({ account, home: `/home/${account}` })),
        processes: ["alice", "bob"].map((daemonId) => ({
          daemonId,
          logsAvailable: daemonId === "bob",
          logsUnavailableReason: "Add this daemon's log directory to JD_LOG_ROOTS.",
          id: 0,
          name: "same-name",
          user: "untrusted-metadata",
          status: "stopped",
          scriptPath: "/app.js",
          cpu: 0,
          memory: 0,
          restarts: 0,
          unstableRestarts: 0,
          uptimeMs: 0,
        })),
      }
    if (path === "/processes/inventory")
      body = {
        processes: [],
        total: 0,
        available: 0,
        truncated: false,
        ratesReady: false,
        users: [],
        states: [],
        managers: [],
      }
    if (path.endsWith("/start")) {
      actions.push(url)
      body = {}
    }
    await fulfill(route, body)
  })
  // Two daemons, so each row names its account: the name alone is ambiguous
  // by design here, and the account is what the row must key its verbs on.
  await page.goto("/processes/pm2")
  const row = page.getByRole("row").filter({ hasText: "bob" })
  await expect(row).toBeVisible()
  await row.hover()
  await row.getByRole("button", { name: "Start", exact: true }).click()
  await expect.poll(() => actions.length).toBe(1)
  expect(actions[0].searchParams.get("user")).toBe("bob")
  expect(actions[0].searchParams.get("id")).toBe("0")
  await row.getByRole("button", { name: "same-name", exact: true }).click()
  await expect(page.getByRole("dialog")).toContainText("bob #0")
  await page.getByRole("tab", { name: "Logs", exact: true }).click()
  await expect.poll(() => sockets.length).toBe(1)
  expect(sockets[0].searchParams.get("user")).toBe("bob")
  expect(sockets[0].searchParams.get("id")).toBe("0")
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  const alice = page.getByRole("row").filter({ hasText: "alice" })
  await alice.getByRole("button", { name: "same-name", exact: true }).click()
  await expect(page.getByRole("dialog")).toContainText("alice #0")
  await page.getByRole("tab", { name: "Logs", exact: true }).click()
  await expect(
    page.getByText("Add this daemon's log directory to JD_LOG_ROOTS.", { exact: true }),
  ).toBeVisible()
  expect(sockets).toHaveLength(1)
})

test("Redis scan cursors retain all unsigned 64-bit digits in requests", async ({ page }) => {
  const cursors: string[] = []
  const cursor = "18446744073709551615"
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url()),
      path = url.pathname.slice(7)
    let body: unknown = []
    if (path === "/auth/session") body = signedIn
    if (path === "/databases/")
      body = [
        {
          id: 1,
          name: "Redis",
          driver: "redis",
          host: "localhost",
          port: 6379,
          database: "0",
          username: "",
          createdAt: new Date().toISOString(),
        },
      ]
    if (path === "/databases/drivers") body = [{ id: "redis", name: "Redis", sql: false }]
    if (path === "/databases/1/schemas") body = [{ name: "0", size: 2 }]
    if (path === "/databases/1/keys") {
      cursors.push(url.searchParams.get("cursor") ?? "")
      body = {
        keys: [{ key: "example", type: "string", ttl: -1, size: 1 }],
        cursor,
        done: url.searchParams.get("cursor") === cursor,
      }
    }
    await fulfill(route, body)
  })
  await page.goto("/databases/browse?conn=1")
  await expect(page.getByRole("button", { name: /example/ })).toBeVisible()
  await page.getByRole("button", { name: "Next", exact: true }).click()
  await expect.poll(() => cursors).toEqual(["0", cursor])
  await page.getByRole("button", { name: "Previous", exact: true }).click()
  await expect.poll(() => cursors).toEqual(["0", cursor, "0"])
})

test("resource filters keep focus while stale responses are discarded", async ({ page }) => {
  const pending: Route[] = []
  const entries = (target: string) => ({
    entries: [
      {
        id: 1,
        ts: new Date().toISOString(),
        username: "operator",
        action: "read",
        target,
        success: true,
        status: 200,
      },
    ],
    total: 1,
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url()),
      path = url.pathname.slice(7)
    if (path === "/audit/") {
      if (url.searchParams.get("action")) {
        pending.push(route)
        return
      }
      return fulfill(route, entries("INITIAL_FILTER_RESULT"))
    }
    await fulfill(route, path === "/auth/session" ? signedIn : [])
  })
  await page.goto("/audit")
  await expect(
    page.getByRole("table").getByText("INITIAL_FILTER_RESULT", { exact: true }),
  ).toBeVisible()
  const action = page.getByPlaceholder("Action, e.g. docker.container")
  await action.pressSequentially("docker", { delay: 30 })
  await expect(action).toHaveValue("docker")
  await expect(action).toBeFocused()
  await expect(
    page.getByRole("table").getByText("INITIAL_FILTER_RESULT", { exact: true }),
  ).toHaveCount(0)
  await expect.poll(() => pending.at(-1)?.request().url()).toContain("action=docker")
  await fulfill(pending.at(-1)!, entries("CURRENT_FILTER_RESULT"))
  await expect(
    page.getByRole("table").getByText("CURRENT_FILTER_RESULT", { exact: true }),
  ).toBeVisible()
  await fulfill(pending[0], entries("STALE_FILTER_RESULT"))
  await expect(
    page.getByRole("table").getByText("CURRENT_FILTER_RESULT", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByRole("table").getByText("STALE_FILTER_RESULT", { exact: true }),
  ).toHaveCount(0)
})

test("saving enrollment recovery codes advances to the required password change", async ({
  page,
}) => {
  let status: unknown = { authenticated: false, needsTotp: false, needsEnrollment: true }
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.slice(7)
    if (path === "/auth/2fa/setup")
      return fulfill(route, { secret: "ABCDEFGH", otpauthUrl: "otpauth://totp/example" })
    if (path === "/auth/2fa/enable") {
      status = { ...signedIn, authenticated: false, needsPasswordChange: true }
      return fulfill(route, { recoveryCodes: ["one-time-code"] })
    }
    await fulfill(route, status)
  })
  await page.goto("/login")
  await page.getByRole("button", { name: "Generate a secret", exact: true }).click()
  await page.getByLabel("Code from your app", { exact: true }).fill("123456")
  await page.getByRole("button", { name: "Enable two-factor", exact: true }).click()
  await expect(
    page.getByRole("heading", { name: "Save your recovery codes", exact: true }),
  ).toBeVisible()
  await expect(page.getByLabel("New password", { exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "I have saved them", exact: true }).click()
  await expect(
    page.getByRole("heading", { name: "Change your password", exact: true }),
  ).toBeVisible()
})
