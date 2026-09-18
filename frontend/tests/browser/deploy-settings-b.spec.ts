import { expect, test } from "@playwright/test"
import { deployment, json, mockProject, now, project, run, steps } from "./deploy-fixture"

/**
 * Settings part B — Domains, Storage, Databases & backups, Automation, and
 * the Danger zone. `mockProject` gives every test a working project 7 /
 * environment 12; each test adds only the routes its own screen needs that
 * the shared fixture does not already answer.
 */

test.describe("Domains", () => {
  test("the configured domain shows route and certificate evidence, and edits save", async ({
    page,
  }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/domains")

    const card = page
      .locator('[data-slot="panel"]')
      .filter({ has: page.getByRole("heading", { name: "Domains", exact: true }) })
    await expect(card).toBeVisible()
    await expect(card.getByText("api.example.test", { exact: true })).toBeVisible()
    await expect(page.getByText("Routed here", { exact: true })).toBeVisible()
    await expect(page.getByText("Certificate valid", { exact: true })).toBeVisible()
    // The default bind address is loopback, so the firewall notice is absent.
    await expect(page.getByText(/binds a public address/)).toHaveCount(0)

    await page.getByRole("switch", { name: "HTTPS" }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].domains).toEqual([
      { hostname: "api.example.test", https: false, ownership: "managed" },
    ])
  })

  test("adding a domain checks the hostname and saves it", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/hostname**", (route) =>
      json(route, {
        hostname: "next.example.test",
        covered: true,
        certificateName: "next.example.test",
        method: "custom",
        detail: "Already covered.",
      }),
    )
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/domains")

    await page.getByRole("button", { name: "Add domain", exact: true }).click()
    const sheet = page.getByRole("dialog", { name: "Add domain" })
    await expect(sheet).toBeVisible()
    await sheet.getByRole("textbox", { name: "Hostname" }).fill("next.example.test")
    await sheet.getByRole("textbox", { name: "Hostname" }).press("Tab")
    await expect(sheet.getByText("HTTPS is ready for this name")).toBeVisible()
    await sheet.getByRole("button", { name: "Save", exact: true }).click()

    await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].domains).toEqual([
      { hostname: "api.example.test", https: true, ownership: "managed" },
      { hostname: "next.example.test", https: true, ownership: "managed" },
    ])
  })

  test("a domain refusal is shown on its own row, not just as a toast", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_plan",
            message: "hostname is already routed elsewhere",
            field: "domains[0]",
          },
        }),
      })
    })
    await page.goto("/deploy/7/settings/domains")
    await page.getByRole("switch", { name: "HTTPS" }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(
      page.getByText("hostname is already routed elsewhere", { exact: true }),
    ).toBeVisible()
    await expect(page.getByText("Could not save domains")).toHaveCount(0)
  })

  test("a public bind address warns about the firewall", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        revision: 3,
        build: { method: "recipe" },
        runtime: {
          internalPort: 3000,
          hostPort: 0,
          bindAddress: "0.0.0.0",
          strategy: "blue_green",
          mounts: [],
        },
        variables: [],
        dependencies: [],
        checks: [],
        domains: [{ hostname: "api.example.test", https: true, ownership: "managed" }],
        pending: { pending: false, desiredRevision: 3, changes: [] },
      })
    })
    await page.goto("/deploy/7/settings/domains")
    await expect(page.getByText("This environment binds a public address")).toBeVisible()
    await expect(page.getByRole("link", { name: "firewall" })).toHaveAttribute(
      "href",
      "/security/firewall",
    )
  })
})

test.describe("Storage", () => {
  test("mounts render, a new one saves, and evidence reflects operations", async ({ page }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/storage")

    await expect(page.getByRole("heading", { name: "Persistent mounts" })).toBeVisible()
    await expect(page.getByRole("textbox", { name: "Source" })).toHaveValue("api-data")
    await expect(page.getByRole("textbox", { name: "Container path" })).toHaveValue("/data")

    await page.getByRole("button", { name: "Add mount", exact: true }).click()
    const sources = page.getByRole("textbox", { name: "Source" })
    const targets = page.getByRole("textbox", { name: "Container path" })
    await sources.nth(1).fill("cache")
    await targets.nth(1).fill("/cache")
    await page.getByRole("switch", { name: "Read only" }).nth(1).click()

    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Storage saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].runtime).toMatchObject({
      mounts: [
        { source: "api-data", target: "/data", ownership: "linked" },
        { source: "cache", target: "/cache", ownership: "linked", readOnly: true },
      ],
    })

    await expect(page.getByRole("heading", { name: "Storage evidence" })).toBeVisible()
    const evidence = page.getByRole("list", { name: "Storage evidence" })
    await expect(evidence.getByText("/data", { exact: true })).toBeVisible()
    await expect(evidence.getByText("Present", { exact: true })).toBeVisible()
    await expect(evidence.getByText("volume", { exact: true })).toBeVisible()
    await expect(evidence.getByRole("link", { name: "Open the volume" })).toHaveAttribute(
      "href",
      "/docker/volumes?volume=api-data",
    )
  })

  test("a mount refusal is shown on its own row, not just as a toast", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_plan",
            message: "target path must be absolute",
            field: "runtime.mounts[0]",
          },
        }),
      })
    })
    await page.goto("/deploy/7/settings/storage")
    await page.getByRole("textbox", { name: "Container path" }).fill("relative/path")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("target path must be absolute", { exact: true })).toBeVisible()
    await expect(page.getByText("Could not save storage")).toHaveCount(0)
  })
})

test.describe("Databases & backups", () => {
  test("linking an existing database saves the dependency and the variable, then can be removed", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/databases/", (route) =>
      json(route, [{ id: 9, name: "orders-db", driver: "postgres" }]),
    )
    await page.route("**/api/v1/databases/9/url**", (route) =>
      json(route, { id: 9, name: "orders-db", driver: "postgres", reference: "${{database.9}}" }),
    )
    let links: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/database-links", (route) =>
      json(route, links),
    )
    const configPuts: Record<string, unknown>[] = []
    const variablePuts: { url: string; body: Record<string, unknown> }[] = []
    page.on("request", (request) => {
      if (request.method() !== "PUT") return
      if (request.url().endsWith("/configuration")) configPuts.push(request.postDataJSON())
      else if (request.url().includes("/variables/"))
        variablePuts.push({ url: request.url(), body: request.postDataJSON() })
    })
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByRole("heading", { name: "Databases & backups" })).toBeVisible()
    await expect(page.getByText("No database is linked", { exact: false })).toBeVisible()

    await page.getByRole("button", { name: "Add database", exact: true }).click()
    await page.getByRole("button", { name: "Use existing", exact: true }).click()
    await page.getByRole("combobox", { name: "Existing database" }).click()
    await page.getByRole("option", { name: "orders-db · postgres" }).click()
    // The link now exists on the server; the row appears once the page refreshes.
    links = [
      {
        connectionId: 9,
        name: "orders-db",
        network: "jd-e12-db-abc",
        hostname: "db-9.jd.internal",
        status: "connected",
        checkedAt: now,
      },
    ]
    await page.getByRole("button", { name: "Connect database", exact: true }).click()

    await expect(page.getByText("Database linked", { exact: true })).toBeVisible()
    await expect.poll(() => configPuts.length).toBe(1)
    expect(configPuts[0].dependencies).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          kind: "database",
          ownership: "linked",
          resourceKind: "database_connection",
          resourceId: "9",
        }),
        expect.objectContaining({ kind: "backup" }),
      ]),
    )
    await expect.poll(() => variablePuts.length).toBe(1)
    expect(variablePuts[0].url).toContain("/variables/DATABASE_URL")
    expect(variablePuts[0].body).toMatchObject({
      value: "${{database.9}}",
      sensitivity: "secret",
      scopes: ["runtime"],
    })

    const orders = page.getByRole("link", { name: "orders-db", exact: true })
    await expect(orders).toHaveAttribute("href", "/databases?conn=9")
    await expect(page.getByText("db-9.jd.internal", { exact: true })).toBeVisible()
    await expect(page.getByText("Connected", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Remove database", exact: true }).click()
    await expect(page.getByText("Dependencies saved", { exact: true })).toBeVisible()
    await expect.poll(() => configPuts.length).toBe(2)
    expect(configPuts[1].dependencies).toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "backup" })]),
    )
    expect(configPuts[1].dependencies).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "database" })]),
    )
  })

  test("backup dependency fields save, and gate evidence renders from the latest run", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [
        { id: 4, name: "Nightly snapshot" },
        { id: 5, name: "Weekly full" },
      ]),
    )
    await page.route("**/api/v1/deploy/7/runs/84", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        run,
        steps: steps.map((step) =>
          step.key === "backup_gate"
            ? {
                ...step,
                evidence: {
                  backups: [
                    {
                      jobId: 4,
                      runId: 201,
                      status: "success",
                      fresh: true,
                      restoreTested: false,
                      endedAt: now,
                    },
                  ],
                },
              }
            : step,
        ),
      })
    })
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByRole("combobox", { name: "Backup job" })).toHaveText("Nightly snapshot")
    const requiredSwitch = page.getByRole("switch", { name: "Backup before deploy" })
    await expect(requiredSwitch).toBeChecked()
    const restoreSwitch = page.getByRole("switch", { name: "Require restore evidence" })
    await expect(restoreSwitch).not.toBeChecked()
    await expect(page.getByRole("spinbutton", { name: "Maximum age (hours)" })).toHaveValue("24")

    await restoreSwitch.click()
    await page.getByRole("spinbutton", { name: "Maximum age (hours)" }).fill("12")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Dependencies saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].dependencies).toEqual([
      expect.objectContaining({
        kind: "backup",
        resourceId: "4",
        config: expect.objectContaining({ requireRestoreTest: true, maxAgeSeconds: 43200 }),
      }),
    ])

    await expect(page.getByRole("heading", { name: "Latest backup gate evidence" })).toBeVisible()
    await expect(page.getByText("#4", { exact: true })).toBeVisible()
    await expect(page.getByText("#201", { exact: true })).toBeVisible()

    await expect(page.getByRole("heading", { name: "Backups", exact: true })).toBeVisible()
    const backupEvidence = page.getByRole("list", { name: "Backup evidence" })
    await expect(backupEvidence.getByText("Backup job 4", { exact: true })).toBeVisible()
    await expect(backupEvidence.getByRole("link", { name: "Open the backup job" })).toHaveAttribute(
      "href",
      "/backups",
    )
  })

  test("a volume dependency can be added and saved", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/docker/volumes/", (route) =>
      json(route, [
        {
          name: "media-cache",
          driver: "local",
          mountpoint: "/var/lib/docker/volumes/media-cache/_data",
          createdAt: now,
          scope: "local",
          labels: {},
          size: 0,
          refCount: 0,
          inUse: true,
        },
      ]),
    )
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/databases")

    await page.getByRole("button", { name: "Add volume", exact: true }).click()
    await page.getByRole("combobox", { name: "Volume" }).click()
    await page.getByRole("option", { name: "media-cache", exact: true }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()

    await expect(page.getByText("Dependencies saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].dependencies).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          kind: "storage",
          ownership: "linked",
          resourceKind: "docker_volume",
          resourceId: "media-cache",
        }),
      ]),
    )
  })

  test("removing a database does not commit an unsaved, half-filled dependency row", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        revision: 3,
        build: { method: "recipe" },
        runtime: {
          internalPort: 3000,
          hostPort: 0,
          bindAddress: "127.0.0.1",
          strategy: "blue_green",
          mounts: [],
        },
        variables: [],
        dependencies: [
          {
            kind: "database",
            ownership: "linked",
            resourceKind: "database_connection",
            resourceId: "9",
            config: {},
          },
        ],
        checks: [],
        domains: [],
        pending: { pending: false, desiredRevision: 3, changes: [] },
      })
    })
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/databases")

    // An unfinished "Add backup" row — never saved.
    await page.getByRole("button", { name: "Add backup", exact: true }).click()
    await expect(page.getByRole("combobox", { name: "Backup job" })).toBeVisible()

    await page.getByRole("button", { name: "Remove database", exact: true }).click()
    await expect(page.getByText("Dependencies saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].dependencies).toEqual([])
  })
})

test.describe("Databases & backups field errors", () => {
  test("a dependency refusal is shown on its own row, not just as a toast", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [{ id: 4, name: "Nightly snapshot" }]),
    )
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_plan",
            message: "maximum age must be positive",
            field: "dependencies[0]",
          },
        }),
      })
    })
    await page.goto("/deploy/7/settings/databases")
    await page.getByRole("spinbutton", { name: "Maximum age (hours)" }).fill("0")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("maximum age must be positive", { exact: true })).toBeVisible()
    await expect(page.getByText("Could not save dependencies")).toHaveCount(0)
  })
})

test.describe("Automation", () => {
  test("creating a webhook shows its one-time secret, then it can be toggled and removed", async ({
    page,
  }) => {
    await mockProject(page)
    let triggers: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/triggers**", async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
      if (path === "/deploy/7/environments/12/triggers" && method === "GET") {
        return json(route, triggers)
      }
      if (path === "/deploy/7/environments/12/triggers" && method === "POST") {
        const input = request.postDataJSON() as Record<string, unknown>
        const trigger = {
          id: 31,
          environmentId: 12,
          projectId: 7,
          hookId: "provider-hook",
          lastStatus: "",
          ...input,
        }
        triggers = [trigger]
        return json(route, { trigger, secret: "one-time-provider-secret" })
      }
      const match = path.match(/^\/deploy\/7\/environments\/12\/triggers\/(\d+)$/)
      if (match && method === "PUT") {
        const input = request.postDataJSON() as Record<string, unknown>
        triggers = triggers.map((item) =>
          String(item.id) === match[1] ? { ...item, ...input } : item,
        )
        return json(route, triggers[0])
      }
      if (match && method === "DELETE") {
        triggers = []
        return route.fulfill({ status: 204 })
      }
      return route.fallback()
    })
    await page.goto("/deploy/7/settings/automation")

    await expect(page.getByRole("heading", { name: "Webhooks", exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Add webhook", exact: true }).click()
    const sheet = page.getByRole("dialog", { name: "Add webhook" })
    await sheet.getByRole("textbox", { name: "Name" }).fill("Deploy hook")
    await sheet.getByRole("textbox", { name: "Repository" }).fill("Wayy01/storefront")
    await sheet.getByRole("textbox", { name: "Branch" }).fill("main")
    await sheet.getByRole("button", { name: "Create webhook" }).click()

    await expect(page.getByText("Webhook created", { exact: true })).toBeVisible()
    await expect(page.getByText("Copy this secret now")).toBeVisible()
    // Absolute — an operator pastes this straight into GitHub, which has no
    // notion of the dashboard's own origin.
    await expect(
      page.getByText(`${new URL(page.url()).origin}/api/v1/hooks/providers/github/provider-hook`),
    ).toBeVisible()
    await expect(page.getByText("one-time-provider-secret")).toBeVisible()
    await page.getByRole("button", { name: "Dismiss", exact: true }).click()
    await expect(page.getByText("Copy this secret now")).toHaveCount(0)

    await expect(page.getByText("Deploy hook", { exact: true })).toBeVisible()
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Disable", exact: true }).click()
    await expect(page.getByText("Webhook disabled", { exact: true })).toBeVisible()
    await expect(page.getByText("Disabled", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Deploy hook actions" }).click()
    await page.getByRole("menuitem", { name: "Remove" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Remove webhook" }).click()
    await expect(page.getByText("Deploy hook", { exact: true })).toHaveCount(0)
  })

  test("creating a schedule with a backup action can be viewed, paused and removed", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [{ id: 4, name: "Nightly snapshot" }]),
    )
    let schedules: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/schedules**", async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
      if (path === "/deploy/7/environments/12/schedules" && method === "GET") {
        return json(route, schedules)
      }
      if (path === "/deploy/7/environments/12/schedules" && method === "POST") {
        const input = request.postDataJSON() as Record<string, unknown>
        const schedule = {
          id: 41,
          environmentId: 12,
          nextRunAt: "2026-09-08T03:00:00Z",
          ...input,
        }
        schedules = [schedule]
        return json(route, schedule)
      }
      if (path === "/deploy/7/environments/12/schedules/41/runs" && method === "GET") {
        return json(route, { runs: [{ ...run, id: 91, runNumber: 4, state: "succeeded" }] })
      }
      const match = path.match(/^\/deploy\/7\/environments\/12\/schedules\/(\d+)$/)
      if (match && method === "PUT") {
        const input = request.postDataJSON() as Record<string, unknown>
        schedules = schedules.map((item) =>
          String(item.id) === match[1] ? { ...item, ...input } : item,
        )
        return json(route, schedules[0])
      }
      if (match && method === "DELETE") {
        schedules = []
        return route.fulfill({ status: 204 })
      }
      return route.fallback()
    })
    await page.goto("/deploy/7/settings/automation")

    await page.getByRole("button", { name: "Add schedule", exact: true }).click()
    const sheet = page.getByRole("dialog", { name: "Add schedule" })
    await sheet.getByRole("textbox", { name: "Name" }).fill("Nightly backup")
    await sheet.getByRole("combobox", { name: "Action" }).click()
    await page.getByRole("option", { name: "Backup", exact: true }).click()
    await sheet.getByRole("combobox", { name: "Backup job" }).click()
    await page.getByRole("option", { name: "Nightly snapshot", exact: true }).click()
    await sheet.getByRole("button", { name: "Create schedule" }).click()

    await expect(page.getByText("Schedule created", { exact: true })).toBeVisible()
    await expect(page.getByText("Nightly backup", { exact: true })).toBeVisible()
    await expect(page.getByText("Enabled", { exact: true }).first()).toBeVisible()

    await page.getByRole("button", { name: "Runs", exact: true }).click()
    const runsSheet = page.getByRole("dialog", { name: "Nightly backup runs" })
    await expect(runsSheet).toBeVisible()
    await expect(runsSheet.getByText("#4 Deploy", { exact: true })).toBeVisible()
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Pause", exact: true }).click()
    await expect(page.getByText("Schedule paused", { exact: true })).toBeVisible()
    await expect(page.getByText("Paused", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Nightly backup actions" }).click()
    await page.getByRole("menuitem", { name: "Remove" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Remove schedule" }).click()
    await expect(page.getByText("Nightly backup", { exact: true })).toHaveCount(0)
  })

  test("an approval can be reviewed and approved, opening its variables sheet", async ({
    page,
  }) => {
    await mockProject(page)
    const approval = {
      id: 9,
      triggerId: 31,
      providerRef: "43",
      revision: "deadbeefcafefeed0000",
      repository: "Wayy01/storefront",
      headRepository: "Wayy01/storefront",
      author: "octocat",
      state: "pending",
      configured: false,
      updatedAt: now,
    }
    await page.route("**/api/v1/deploy/7/previews/approvals", (route) =>
      route.request().method() === "GET" ? json(route, [approval]) : route.fallback(),
    )
    await page.route("**/api/v1/deploy/7/previews/approvals/9/approve", (route) =>
      json(route, {
        preview: {
          id: 60,
          triggerId: 31,
          providerRef: "43",
          environmentId: 53,
          environmentSlug: "pr-43",
          state: "open",
          updatedAt: now,
        },
      }),
    )
    await page.goto("/deploy/7/settings/automation")

    await expect(page.getByText("PR 43", { exact: false }).first()).toBeVisible()
    await expect(page.getByText("Awaiting approval")).toBeVisible()
    await page.getByRole("button", { name: "Review revision" }).click()
    const dialog = page.getByRole("dialog", { name: "Approve PR 43" })
    await expect(dialog).toContainText("octocat")
    await expect(dialog).toContainText("deadbeefcafefeed0000")
    await dialog.getByRole("button", { name: "Approve and configure" }).click()

    await expect(page.getByRole("dialog", { name: "Preview 43 variables" })).toBeVisible()
  })

  test("preview isolation blocks deploy with the exact sentences, and an open preview deploys", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/previews", (route) =>
      json(route, [
        {
          id: 51,
          triggerId: 31,
          providerRef: "42",
          environmentId: 52,
          environmentSlug: "pr-42",
          state: "open",
          updatedAt: now,
        },
        {
          id: 52,
          triggerId: 31,
          providerRef: "40",
          environmentId: 54,
          environmentSlug: "pr-40",
          state: "open",
          updatedAt: now,
          isolationStatus: "pending",
        },
        {
          id: 53,
          triggerId: 31,
          providerRef: "39",
          environmentId: 55,
          environmentSlug: "pr-39",
          state: "open",
          updatedAt: now,
          isolationStatus: "quarantined",
        },
      ]),
    )
    let deployed = 0
    await page.route("**/api/v1/deploy/7/environments/52/runs", (route) => {
      deployed++
      return json(route, { ...run, id: 92, operation: "deploy", state: "queued" })
    })
    await page.goto("/deploy/7/settings/automation")

    await expect(page.getByText("This older preview may share production resources")).toBeVisible()
    await expect(
      page.getByText("The previous runtime is stopped and stored data is retained"),
    ).toBeVisible()
    await expect(page.getByText("Isolation incomplete", { exact: true })).toBeVisible()
    await expect(page.getByText("Stopped for isolation", { exact: true })).toBeVisible()

    const openRow = page.getByText("pr-42", { exact: true }).locator("..").locator("..")
    await openRow.getByRole("button", { name: "Deploy preview" }).click()
    await expect(page.getByText("Preview deployment queued", { exact: true })).toBeVisible()
    expect(deployed).toBe(1)

    await expect(page.getByRole("link", { name: "Notification channels" })).toHaveAttribute(
      "href",
      "/deploy/notifications",
    )
  })

  test("a webhook's deliveries can be reviewed and its secret rotated", async ({ page }) => {
    await mockProject(page)
    const trigger = {
      id: 31,
      projectId: 7,
      environmentId: 12,
      name: "Deploy hook",
      kind: "github",
      provider: "github",
      config: { repository: "Wayy01/storefront", ref: "main" },
      hookId: "provider-hook",
      enabled: true,
      lastStatus: "accepted",
    }
    let rotated = 0
    await page.route("**/api/v1/deploy/7/environments/12/triggers**", async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
      if (path === "/deploy/7/environments/12/triggers" && method === "GET") {
        return json(route, [trigger])
      }
      if (path === "/deploy/7/environments/12/triggers/31/deliveries" && method === "GET") {
        return json(route, [
          {
            deliveryId: "d1",
            event: "push",
            ref: "refs/heads/main",
            decision: "accepted",
            runId: 84,
            receivedAt: now,
          },
          {
            deliveryId: "d2",
            event: "push",
            ref: "refs/heads/chore",
            decision: "suppressed",
            reason: "no watched path changed",
            receivedAt: now,
          },
          {
            deliveryId: "d3",
            event: "pull_request",
            decision: "rejected",
            reason: "preview approval required",
            receivedAt: now,
          },
        ])
      }
      if (path === "/deploy/7/environments/12/triggers/31/rotate-secret" && method === "POST") {
        rotated++
        return json(route, { secret: "rotated-webhook-secret" })
      }
      return route.fallback()
    })
    await page.goto("/deploy/7/settings/automation")

    await expect(page.getByText("Deploy hook", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Deploy hook actions" }).click()
    await page.getByRole("menuitem", { name: "Deliveries" }).click()

    await expect(page.getByRole("heading", { name: "Deliveries · Deploy hook" })).toBeVisible()
    const sheet = page.getByRole("dialog")
    await expect(sheet.getByText("Accepted", { exact: true })).toBeVisible()
    await expect(sheet.getByText("Ignored", { exact: true })).toBeVisible()
    await expect(sheet.getByText("Refused", { exact: true })).toBeVisible()
    await expect(sheet.getByText(/no watched path changed/)).toBeVisible()
    await expect(sheet.getByRole("link", { name: "View run" })).toHaveAttribute(
      "href",
      "/deploy/7/runs/84",
    )
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Deploy hook actions" }).click()
    await page.getByRole("menuitem", { name: "Rotate secret" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Rotate secret" }).click()
    await expect(page.getByText("Copy the new secret for Deploy hook now")).toBeVisible()
    await expect(page.getByText("rotated-webhook-secret", { exact: true })).toBeVisible()
    expect(rotated).toBe(1)
  })

  test("a pending approval can be rejected instead of reviewed", async ({ page }) => {
    await mockProject(page)
    const approval = {
      id: 9,
      triggerId: 31,
      providerRef: "43",
      revision: "deadbeefcafefeed0000",
      repository: "Wayy01/storefront",
      headRepository: "Wayy01/storefront",
      author: "octocat",
      state: "pending",
      configured: false,
      updatedAt: now,
    }
    let rejectBody: Record<string, unknown> | undefined
    await page.route("**/api/v1/deploy/7/previews/approvals", (route) =>
      route.request().method() === "GET" ? json(route, [approval]) : route.fallback(),
    )
    await page.route("**/api/v1/deploy/7/previews/approvals/9/reject", (route) => {
      rejectBody = route.request().postDataJSON()
      approval.state = "rejected"
      return json(route, { ...approval })
    })
    await page.goto("/deploy/7/settings/automation")

    await expect(page.getByText("Awaiting approval")).toBeVisible()
    await page.getByRole("button", { name: "Reject", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Reject PR 43" })
    await dialog.getByRole("button", { name: "Reject", exact: true }).click()

    await expect(page.getByText("Rejected", { exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "Review revision" })).toHaveCount(0)
    expect(rejectBody).toEqual({ revision: "deadbeefcafefeed0000" })
  })

  test("legacy compose projects show only the legacy hook", async ({ page }) => {
    await mockProject(page, { normalized: false })
    await page.goto("/deploy/7/settings/automation")

    await expect(
      page.getByRole("heading", { name: "Legacy deployment hook", exact: true }),
    ).toBeVisible()
    await expect(page.getByText(project.hookUrl, { exact: true })).toBeVisible()
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Webhooks", exact: true })).toHaveCount(0)
    await expect(page.getByRole("heading", { name: "Schedules", exact: true })).toHaveCount(0)
  })
})

test.describe("Runtime", () => {
  test("a field-level refusal is shown beside the field it names, not only as a toast", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      await route.fulfill({
        status: 422,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "invalid_plan",
            message: "runtime port must be between 1 and 65535",
            field: "runtime.internalPort",
          },
        }),
      })
    })
    await page.goto("/deploy/7/settings/runtime")

    const card = page
      .locator('[data-slot="panel"]')
      .filter({ has: page.getByRole("heading", { name: "Runtime", exact: true }) })
    const port = card.getByRole("spinbutton", { name: "Application port" })
    await port.fill("99999")
    await card.getByRole("button", { name: "Save", exact: true }).click()

    await expect(port).toHaveAttribute("aria-invalid", "true")
    await expect(
      page.getByText("runtime port must be between 1 and 65535", { exact: true }),
    ).toBeVisible()
    await expect(page.getByText("Could not save runtime settings")).toHaveCount(0)
  })
})

test.describe("Settings cards keep independent unsaved edits", () => {
  test("saving the Build card does not discard an unsaved release task", async ({ page }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/build")

    await page.getByRole("button", { name: "Add release task", exact: true }).click()
    const taskCommand = page.getByRole("textbox", { name: "Release task 1 command" })
    await taskCommand.fill("./bin/migrate --force")

    const buildCard = page
      .locator('[data-slot="panel"]')
      .filter({ has: page.getByRole("heading", { name: "Build", exact: true }) })
    await buildCard.getByRole("textbox", { name: "Root directory" }).fill("apps/api")
    await buildCard.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Build settings saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)

    await expect(taskCommand).toHaveValue("./bin/migrate --force")
  })
})

test.describe("Danger zone", () => {
  test("stop posts the stop operation for a live deployment", async ({ page }) => {
    const dashboard = await mockProject(page)
    await page.goto("/deploy/7/settings/danger")

    await expect(page.getByRole("heading", { name: "Stop the application" })).toBeVisible()
    await page.getByRole("button", { name: "Stop", exact: true }).click()
    await expect(page).toHaveURL(/\/deploy\/7\/runs\/\d+$/)
    expect(dashboard.actions()).toEqual(["stop"])
  })

  test("start replaces stop once the live release is stopped", async ({ page }) => {
    const dashboard = await mockProject(page)
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        project,
        running: false,
        deployment: { ...deployment, buildMethod: "recipe", activeRun: undefined, stopped: true },
      })
    })
    await page.goto("/deploy/7/settings/danger")

    const card = page
      .locator('[data-slot="panel"]')
      .filter({ has: page.getByRole("heading", { name: "Start the application" }) })
    await expect(card).toBeVisible()
    await card.getByRole("button", { name: "Start", exact: true }).click()
    await expect(page).toHaveURL(/\/deploy\/7\/runs\/\d+$/)
    expect(dashboard.actions()).toEqual(["start"])
  })

  test("archiving replaces the archive card with resource removal", async ({ page }) => {
    await mockProject(page)
    let archived = 0
    await page.route("**/api/v1/deploy/7/archive", (route) => {
      archived++
      return json(route, { ...project, archivedAt: now })
    })
    await page.goto("/deploy/7/settings/danger")

    await expect(page.getByRole("heading", { name: "Archive this deployment" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Remove managed resources" })).toHaveCount(0)
    await page.getByRole("button", { name: "Archive", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Archive deployment" })
    await expect(dialog).toContainText("Triggers will be disabled")
    await dialog.getByRole("button", { name: "Archive deployment" }).click()

    expect(archived).toBe(1)
    await expect(page.getByRole("heading", { name: "Archive this deployment" })).toHaveCount(0)
    await expect(page.getByRole("heading", { name: "Remove managed resources" })).toBeVisible()
  })

  test("an archived project can remove a typed-confirmation target, then be deleted permanently", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/deploy/7", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      await json(route, {
        project: { ...project, archivedAt: now },
        running: false,
        deployment: { ...deployment, buildMethod: "recipe", activeRun: undefined },
      })
    })
    await page.route("**/api/v1/deploy/7/removal-plan", (route) =>
      json(route, {
        deploymentId: 7,
        archived: true,
        targets: [
          {
            id: "docker_volume:api-data",
            kind: "docker_volume",
            resourceId: "api-data",
            displayName: "api-data",
            owner: "docker",
            ownership: "managed",
            data: true,
            requiresAdmin: true,
            confirmationType: "typed",
            confirmationPhrase: "api-data",
          },
        ],
        digest: `sha256:${"9".repeat(64)}`,
        generatedAt: now,
      }),
    )
    let removeBody: Record<string, unknown> | undefined
    let removeConfirm: string | undefined
    await page.route("**/api/v1/deploy/7/remove-managed", async (route) => {
      removeBody = route.request().postDataJSON()
      removeConfirm = (await route.request().headerValue("x-confirm")) ?? undefined
      await json(route, { deploymentId: 7, removed: [], remaining: [] })
    })
    let purged = 0
    await page.route("**/api/v1/deploy/7/permanent", (route) => {
      purged++
      return route.fulfill({ status: 204 })
    })
    await page.goto("/deploy/7/settings/danger")

    await expect(page.getByRole("heading", { name: "Archive this deployment" })).toHaveCount(0)
    await page.getByRole("button", { name: "Preview targets" }).click()
    await expect(page.getByText("api-data", { exact: true }).first()).toBeVisible()
    await expect(page.getByText(/typed confirmation/)).toBeVisible()

    await page.getByRole("button", { name: "Remove", exact: true }).click()
    const removeDialog = page.getByRole("dialog", { name: "Remove api-data" })
    await removeDialog.getByRole("textbox").fill("api-data")
    await removeDialog.getByRole("button", { name: "Remove managed resource" }).click()

    await expect.poll(() => removeBody).toBeDefined()
    expect(removeBody).toMatchObject({ targetIds: ["docker_volume:api-data"] })
    expect(removeConfirm).toBe("api-data")

    // The confirmation's own success toast sits bottom-right and can overlap
    // the next button while it is showing; waiting it out is the honest fix
    // rather than forcing a click an operator could not have made either.
    await page.locator("[data-sonner-toast]").first().waitFor({ state: "detached", timeout: 15000 })
    await page.getByRole("button", { name: "Delete permanently", exact: true }).first().click()
    const purgeDialog = page.getByRole("dialog", { name: /Delete .* permanently/ })
    await purgeDialog.getByRole("button", { name: "Delete permanently" }).click()
    await expect(page).toHaveURL(/\/deploy\?view=archived$/)
    expect(purged).toBe(1)
  })
})

test.describe("Screenshots", () => {
  const screens: { name: string; path: string }[] = [
    { name: "domains", path: "/deploy/7/settings/domains" },
    { name: "storage", path: "/deploy/7/settings/storage" },
    { name: "databases", path: "/deploy/7/settings/databases" },
    { name: "automation", path: "/deploy/7/settings/automation" },
    { name: "danger", path: "/deploy/7/settings/danger" },
  ]

  for (const screen of screens) {
    test(`${screen.name} at 1280 and 390`, async ({ page }, testInfo) => {
      await mockProject(page)
      await page.route("**/api/v1/backups/", (route) =>
        json(route, [{ id: 4, name: "Nightly snapshot" }]),
      )
      await page.route("**/api/v1/docker/volumes/", (route) => json(route, []))
      // The shell scrolls an inner div (`h-svh overflow-hidden` on the
      // outer), not the document, so a `fullPage` shot only ever captures one
      // viewport's worth: making the viewport itself tall enough is what
      // actually gets the whole page into the frame.
      await page.setViewportSize({ width: 1280, height: 2400 })
      await page.goto(screen.path)
      await page.waitForLoadState("networkidle")
      await page.screenshot({
        path: testInfo.outputPath(`${screen.name}-1280.png`),
        fullPage: true,
      })
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)

      await page.setViewportSize({ width: 390, height: 2600 })
      await page.waitForTimeout(150)
      await page.screenshot({
        path: testInfo.outputPath(`${screen.name}-390.png`),
        fullPage: true,
      })
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
      ).toBe(true)
    })
  }
})

test.describe("Password protection", () => {
  test("a domain can be put behind a password, keeps the hash on later saves, and can be opened again", async ({
    page,
  }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/domains")
    await expect(page.getByRole("link", { name: "api.example.test" })).toBeVisible()

    // Turning protection on asks for the two fields; the password leaves the
    // browser exactly once, as a password.
    await page.getByRole("switch", { name: "Password" }).click()
    await page.getByLabel("User name").fill("team")
    await page.locator("#domain-password-0").fill("correct horse battery")
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].domains).toEqual([
      {
        hostname: "api.example.test",
        https: true,
        ownership: "managed",
        protection: { username: "team", password: "correct horse battery" },
      },
    ])

    // Once the server holds a hash, saving something else keeps it and
    // sends no password.
    await page.goto("/deploy/7/settings/domains")
    await expect(page.getByRole("switch", { name: "Password" })).toBeChecked()
    await expect(page.getByLabel("User name")).toHaveValue("team")
    await expect(page.locator("#domain-password-0")).toHaveAttribute("placeholder", "Unchanged")
    await page.getByRole("switch", { name: "HTTPS" }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(2)
    expect(puts[1].domains).toEqual([
      {
        hostname: "api.example.test",
        https: false,
        ownership: "managed",
        protection: {
          username: "team",
          hash: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy",
        },
      },
    ])

    // Turning it off sends no protection at all.
    await page.getByRole("switch", { name: "Password" }).click()
    await expect(page.getByLabel("User name")).toHaveCount(0)
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect.poll(() => puts.length).toBe(3)
    expect((puts[2].domains as Record<string, unknown>[])[0]).not.toHaveProperty("protection")
  })
})
