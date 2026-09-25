import { expect, test } from "@playwright/test"
import {
  backupJob,
  deployment,
  json,
  mockProject,
  now,
  project,
  run,
  steps,
} from "./deploy-fixture"

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

    const form = page.getByRole("form", { name: "Domains" })
    await expect(form).toBeVisible()
    await expect(form.getByText("api.example.test", { exact: true })).toBeVisible()
    await expect(page.getByText("Routed here", { exact: true })).toBeVisible()
    await expect(page.getByText("Certificate valid", { exact: true })).toBeVisible()
    // The certificate's days left, and the figures over the list.
    await expect(form.getByText("70 days left", { exact: true })).toBeVisible()
    await expect(page.getByText("1 of 1", { exact: true })).toBeVisible()
    await expect(page.getByRole("link", { name: "Open api.example.test" })).toHaveAttribute(
      "href",
      "https://api.example.test/",
    )
    // The default bind address is loopback, so the firewall notice is absent.
    await expect(page.getByText(/binds a public address/)).toHaveCount(0)

    const https = page.getByRole("button", { name: "HTTPS on api.example.test" })
    await expect(https).toHaveAttribute("aria-pressed", "true")
    await https.click()
    await expect(page.getByText("1 unsaved change", { exact: true })).toBeVisible()
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
    await expect(sheet.getByText("HTTPS ready", { exact: true })).toBeVisible()
    await sheet.getByRole("button", { name: "Add domain", exact: true }).click()

    await expect(page.getByText("Domains saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].domains).toEqual([
      { hostname: "api.example.test", https: true, ownership: "managed" },
      { hostname: "next.example.test", https: true, ownership: "managed" },
    ])
  })

  test("adding a domain says it also saves the list's unsaved edits, and Cancel forgets the sheet", async ({
    page,
  }) => {
    await mockProject(page)
    await page.goto("/deploy/7/settings/domains")

    // Turning HTTPS off is a draft the row answers for: the live certificate
    // stops being its answer, and it says what the next deployment does.
    await page.getByRole("button", { name: "HTTPS on api.example.test" }).click()
    await expect(page.getByText("HTTP only on deploy", { exact: true })).toBeVisible()
    await expect(page.getByText("Certificate valid", { exact: true })).toHaveCount(0)

    await page.getByRole("button", { name: "Add domain", exact: true }).click()
    const sheet = page.getByRole("dialog", { name: "Add domain" })
    await expect(sheet.getByText("Also saves 1 unsaved change", { exact: true })).toBeVisible()
    await sheet.getByRole("textbox", { name: "Hostname" }).fill("next.example.test")
    await sheet.getByRole("switch", { name: "Ask visitors for a password" }).click()
    await sheet.getByLabel("Password", { exact: true }).fill("a-typed-secret")
    await sheet.getByRole("button", { name: "Cancel", exact: true }).click()
    await expect(sheet).toHaveCount(0)

    await page.getByRole("button", { name: "Add domain", exact: true }).click()
    await expect(sheet.getByRole("textbox", { name: "Hostname" })).toHaveValue("")
    await expect(
      sheet.getByRole("switch", { name: "Ask visitors for a password" }),
    ).not.toBeChecked()
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
    await page.getByRole("button", { name: "HTTPS on api.example.test" }).click()
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(
      page
        .getByRole("list", { name: "Domains" })
        .getByText("hostname is already routed elsewhere", { exact: true }),
    ).toBeVisible()
    await expect(page.getByText("Not saved", { exact: true })).toBeVisible()
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
    // Read-only is the binary inside the container path's own edge.
    await page.getByRole("button", { name: "Read only" }).nth(1).click()
    // The source says which kind of thing it names as it is typed.
    await expect(
      page.getByRole("list", { name: "Mounts" }).getByText("volume", { exact: true }),
    ).toHaveCount(2)

    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Storage saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].runtime).toMatchObject({
      mounts: [
        { source: "api-data", target: "/data", ownership: "linked" },
        { source: "cache", target: "/cache", ownership: "linked", readOnly: true },
      ],
    })

    await expect(page.getByRole("heading", { name: "In the live release" })).toBeVisible()
    const evidence = page.getByRole("list", { name: "In the live release" })
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

    await expect(page.getByRole("heading", { name: "Linked databases" })).toBeVisible()
    await expect(page.getByText("No database is linked", { exact: false })).toBeVisible()

    await page.getByRole("button", { name: "Add database", exact: true }).click()
    await page.getByRole("button", { name: "Use existing", exact: true }).click()
    // The link now exists on the server; the row appears once the page refreshes.
    links = [
      {
        connectionId: 9,
        name: "orders-db",
        driver: "postgres",
        database: "orders",
        network: "jd-e12-db-abc",
        hostname: "db-9.jd.internal",
        status: "connected",
        checkedAt: now,
      },
    ]
    // A saved connection is a row you take, drawn as its engine; taking it is the advance.
    await page.getByRole("button", { name: "Connect orders-db", exact: true }).click()

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
    // A new variable reaches the build too, the way a database linked while
    // creating the project does: a static env import or Prisma's config reads it there.
    expect(variablePuts[0].body).toMatchObject({
      value: "${{database.9}}",
      sensitivity: "secret",
      scopes: ["runtime", "build"],
    })

    const orders = page.getByRole("link", { name: "orders-db", exact: true })
    await expect(orders).toHaveAttribute("href", "/databases/overview?conn=9")
    await expect(page.getByText("db-9.jd.internal", { exact: true })).toBeVisible()
    await expect(page.getByText("Connected", { exact: true })).toBeVisible()
    // The engine is spelled as the picture above spells it, not as the driver key.
    await expect(
      page.getByRole("list", { name: "Linked databases" }).getByText("PostgreSQL", { exact: true }),
    ).toBeVisible()
    // How the application reaches it, drawn as a picture once it is bound.
    await expect(
      page.getByRole("list", { name: "How the application reaches its databases" }),
    ).toBeVisible()

    await page.getByRole("button", { name: "Actions for orders-db" }).click()
    await page.getByRole("menuitem", { name: "Remove database" }).click()
    // The fixture's configuration echoes no `reference` for the variable that
    // was just written, so the page sees a bound database it cannot name a
    // carrier for and asks before removing the link alone.
    await expect(page.getByText("This page finds no variable referencing orders-db")).toBeVisible()
    await page.getByRole("button", { name: "Remove the link", exact: true }).click()
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
        backupJob({ id: 4, name: "Nightly snapshot", lastSuccessAt: now, nextRun: now }),
        backupJob({ id: 5, name: "Weekly full" }),
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

    // A chosen job is the Backups page's own card, which opens the job.
    await expect(
      page.getByRole("link", { name: "Open the backup job Nightly snapshot" }),
    ).toHaveAttribute("href", "/backups/4")
    const requiredSwitch = page.getByRole("switch", { name: "Backup before deploy" })
    await expect(requiredSwitch).toBeChecked()
    const restoreSwitch = page.getByRole("switch", { name: "Require restore evidence" })
    await expect(restoreSwitch).not.toBeChecked()
    await expect(page.getByRole("spinbutton", { name: "Maximum age" })).toHaveValue("24")

    await restoreSwitch.click()
    await page.getByRole("spinbutton", { name: "Maximum age" }).fill("12")
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
    // The gate names the job rather than its number now that the page holds
    // the job list it is gating on.
    await expect(page.getByText("Nightly snapshot").first()).toBeVisible()
    await expect(page.getByText("#201", { exact: true })).toBeVisible()

    // The job's own facts are on its row, and the live release's observation
    // of it folds onto the same row rather than into a second panel below.
    await expect(page.getByText("Observed · last run success")).toBeVisible()
    await expect(page.getByRole("link", { name: "Open the backup job" })).toHaveAttribute(
      "href",
      "/backups/4",
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

    await page.getByRole("button", { name: "Actions for Database 9" }).click()
    await page.getByRole("menuitem", { name: "Remove database" }).click()
    await expect(page.getByText("Dependencies saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].dependencies).toEqual([])
  })
})

/**
 * The configuration a linked project is in: one database dependency, one
 * variable carrying it as a typed reference, and the binding observed.
 */
async function mockLinkedDatabase(
  page: import("@playwright/test").Page,
  options: {
    status?: string
    detail?: string
    variables?: Record<string, unknown>[]
    dependencies?: Record<string, unknown>[]
  } = {},
) {
  await mockProject(page)
  await page.route("**/api/v1/deploy/7/environments/12/database-links", (route) =>
    json(route, [
      {
        connectionId: 9,
        name: "orders-db",
        driver: "postgres",
        database: "orders",
        network: "jd-e12-db-abc",
        hostname: "db-9.jd.internal",
        status: options.status ?? "connected",
        detail: options.detail,
        checkedAt: now,
      },
    ]),
  )
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
      variables: options.variables ?? [
        {
          name: "DATABASE_URL",
          revision: 1,
          sensitivity: "secret",
          scopes: ["runtime"],
          masked: "••••••••",
          valueDigest: `sha256:${"1".repeat(64)}`,
          reference: { kind: "database", target: "9" },
          pending: false,
          createdBy: "operator",
          createdAt: now,
          environmentId: 12,
          desiredRevision: 3,
        },
      ],
      dependencies: options.dependencies ?? [
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
}

test.describe("Databases & backups evidence", () => {
  test("the readings report the link, the policy and the dump coverage", async ({ page }) => {
    await mockLinkedDatabase(page)
    await page.route("**/api/v1/backups/", (route) => json(route, []))
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByText("Linked databases").first()).toBeVisible()
    await expect(page.getByText("All connected", { exact: true })).toBeVisible()
    // A linked database with no backup policy at all is the reading that
    // earns its space; the gate would refuse nothing, and nothing is kept.
    await expect(page.getByText("None", { exact: true })).toBeVisible()
    await expect(page.getByText("A linked database with no backup policy")).toBeVisible()
    await expect(page.getByText("0 of 1", { exact: true })).toBeVisible()
    await expect(page.getByRole("link", { name: "Back up orders-db" })).toHaveAttribute(
      "href",
      "/backups?database=9",
    )
  })

  test("the variable that carries a database is named, and removal takes it with the link", async ({
    page,
  }) => {
    await mockLinkedDatabase(page)
    await page.route("**/api/v1/backups/", (route) => json(route, []))
    await page.route("**/api/v1/deploy/7/environments/12/variables/**", async (route) => {
      if (route.request().method() !== "DELETE") return route.fallback()
      await json(route, { desiredRevision: 4 })
    })
    const configPuts: Record<string, unknown>[] = []
    const variableDeletes: string[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        configPuts.push(request.postDataJSON())
      }
      if (request.method() === "DELETE" && request.url().includes("/variables/")) {
        variableDeletes.push(request.url())
      }
    })
    await page.goto("/deploy/7/settings/databases")

    // The variable that carries it is the row's own link into Variables.
    await expect(
      page
        .getByRole("list", { name: "Linked databases" })
        .getByRole("link", { name: "DATABASE_URL", exact: true }),
    ).toHaveAttribute("href", "/deploy/7/settings/variables")

    await page.getByRole("button", { name: "Actions for orders-db" }).click()
    await page.getByRole("menuitem", { name: "Remove database" }).click()

    // Runtime activation attaches a database by reading the variable, so a
    // removal that left the variable behind reattached it on the next deploy.
    await expect(page.getByText("DATABASE_URL carries this database")).toBeVisible()
    await page.getByRole("button", { name: "Remove the link and its variables" }).click()

    await expect.poll(() => variableDeletes.length).toBe(1)
    expect(variableDeletes[0]).toContain("/variables/DATABASE_URL")
    await expect.poll(() => configPuts.length).toBe(1)
    expect(configPuts[0].dependencies).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ kind: "database" })]),
    )
    // The delete advanced the environment's desired revision, so the write
    // after it has to send the one the delete handed back or be refused.
    expect(configPuts[0].revision).toBe(4)
  })

  test("a job that takes no dump of a linked database says so, and can be given one", async ({
    page,
  }) => {
    await mockLinkedDatabase(page, {
      dependencies: [
        {
          kind: "database",
          ownership: "linked",
          resourceKind: "database_connection",
          resourceId: "9",
          config: {},
        },
        {
          kind: "backup",
          ownership: "linked",
          resourceKind: "backup_job",
          resourceId: "4",
          config: { requiredBeforeDeploy: true, maxAgeSeconds: 86400 },
        },
      ],
    })
    let dumps: number[] = []
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [backupJob({ id: 4, name: "Nightly snapshot", databaseDumps: dumps })]),
    )
    const jobPuts: Record<string, unknown>[] = []
    await page.route("**/api/v1/backups/4", async (route) => {
      if (route.request().method() !== "PUT") return route.fallback()
      const body = route.request().postDataJSON() as Record<string, unknown>
      jobPuts.push(body)
      dumps = body.databaseDumps as number[]
      await json(route, backupJob({ id: 4, name: "Nightly snapshot", databaseDumps: dumps }))
    })
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByText("Nightly snapshot takes no native dump of orders-db")).toBeVisible()
    await expect(page.getByText("0 of 1", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Add the dump to Nightly snapshot" }).click()
    await expect(page.getByText("Nightly snapshot now dumps this database")).toBeVisible()
    await expect.poll(() => jobPuts.length).toBe(1)
    expect(jobPuts[0].databaseDumps).toEqual([9])
    // The destination's keys are preserved precisely by not being sent.
    expect(jobPuts[0]).not.toHaveProperty("secrets")
    expect(jobPuts[0]).toMatchObject({ name: "Nightly snapshot", schedule: "0 3 * * *" })

    await expect(page.getByText("1 of 1", { exact: true })).toBeVisible()
    await expect(page.getByText("Nightly snapshot takes no native dump of orders-db")).toHaveCount(
      0,
    )
  })

  test("a binding that could not be repaired reports the reason reconciliation recorded", async ({
    page,
  }) => {
    await mockLinkedDatabase(page, {
      status: "unavailable",
      detail: "the saved database port belongs to a different container; reconnect it explicitly",
    })
    await page.route("**/api/v1/backups/", (route) => json(route, []))
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByText("orders-db needs reconnection")).toBeVisible()
    await expect(
      page.getByText("the saved database port belongs to a different container"),
    ).toBeVisible()
    await expect(page.getByText("1 needs attention", { exact: true })).toBeVisible()
  })

  test("a linked database with no variable carrying it is called out", async ({ page }) => {
    await mockLinkedDatabase(page, { variables: [] })
    await page.route("**/api/v1/backups/", (route) => json(route, []))
    // No observed binding either: nothing is holding this database's address.
    await page.route("**/api/v1/deploy/7/environments/12/database-links", (route) =>
      json(route, []),
    )
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByText("No variable carries this database")).toBeVisible()
    await expect(page.getByText("No variable carries a linked database.")).toBeVisible()
  })

  test("a database bound by a literal address is not reported as uncarried", async ({ page }) => {
    // An install from before typed references stores the URL itself, so the
    // reference this page reads is absent while the database is plainly bound.
    // Calling that "no variable carries it" would be a false alarm — and the
    // page must not overclaim either: a binding outlives the variable that
    // made it, so it reports what it observed, not a variable it cannot see.
    await mockLinkedDatabase(page, { variables: [] })
    await page.route("**/api/v1/backups/", (route) => json(route, []))
    await page.goto("/deploy/7/settings/databases")

    await expect(page.getByText("No variable carries this database")).toHaveCount(0)
    await expect(
      page.getByText("Bound on the managed network; no variable names one by reference."),
    ).toBeVisible()

    await page.getByRole("button", { name: "Actions for orders-db" }).click()
    await page.getByRole("menuitem", { name: "Remove database" }).click()
    await expect(page.getByText("This page finds no variable referencing orders-db")).toBeVisible()
  })
})

test.describe("Databases & backups field errors", () => {
  test("a dependency refusal is shown on its own row, not just as a toast", async ({ page }) => {
    await mockProject(page)
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [backupJob({ id: 4, name: "Nightly snapshot" })]),
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
    await page.getByRole("spinbutton", { name: "Maximum age" }).fill("0")
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
    // With none yet, the section offers the senders themselves, and the head
    // says what already deploys it.
    await expect(page.getByText(/already deploy it — a webhook adds another sender/)).toBeVisible()
    // The picture's empty mark and the section's button are one act, named alike.
    await page.getByRole("button", { name: "Add webhook", exact: true }).first().click()
    const sheet = page.getByRole("dialog", { name: "Add webhook" })
    // The sender is picked by its card; GitHub is the one chosen to start.
    await expect(
      sheet.getByRole("group", { name: "Provider" }).getByRole("button", { name: /^GitHub/ }),
    ).toHaveAttribute("aria-pressed", "true")
    await sheet.getByRole("textbox", { name: "Name" }).fill("Deploy hook")
    await sheet.getByRole("textbox", { name: "Repository" }).fill("Wayy01/storefront")
    await sheet.getByRole("textbox", { name: "Branch" }).fill("main")
    await sheet.getByRole("button", { name: "Add webhook" }).click()

    await expect(page.getByText("Webhook created", { exact: true })).toBeVisible()
    await expect(page.getByText("Copy this secret now")).toBeVisible()
    // Absolute — an operator pastes this straight into GitHub, which has no
    // notion of the dashboard's own origin.
    await expect(
      page.getByText(`${new URL(page.url()).origin}/api/v1/hooks/providers/github/provider-hook`),
    ).toBeVisible()
    await expect(page.getByText("one-time-provider-secret")).toBeVisible()
    // The sheet became its own result: where on GitHub the two values go.
    await expect(
      sheet.getByText("Open the repository's Settings → Webhooks → Add webhook."),
    ).toBeVisible()
    await sheet.getByRole("button", { name: "Done", exact: true }).click()
    await expect(page.getByText("Copy this secret now")).toHaveCount(0)

    await expect(page.getByText("Deploy hook", { exact: true })).toBeVisible()
    await expect(page.getByText("Enabled", { exact: true })).toBeVisible()
    await page.getByRole("button", { name: "Disable", exact: true }).click()
    await expect(page.getByText("Webhook disabled", { exact: true })).toBeVisible()
    await expect(page.getByText("Disabled", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Actions for Deploy hook" }).click()
    await page.getByRole("menuitem", { name: "Remove" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Remove webhook" }).click()
    await expect(page.getByText("Deploy hook", { exact: true })).toHaveCount(0)
  })

  test("creating a schedule with a backup action can be viewed, paused and removed", async ({
    page,
  }) => {
    await mockProject(page)
    await page.route("**/api/v1/backups/", (route) =>
      json(route, [backupJob({ id: 4, name: "Nightly snapshot" })]),
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

    // The picture's empty mark and the section's button are one act, named alike.
    await page.getByRole("button", { name: "Add schedule", exact: true }).first().click()
    const sheet = page.getByRole("dialog", { name: "Add schedule" })
    await sheet.getByRole("textbox", { name: "Name" }).fill("Nightly backup")
    // When it fires is built from words, not typed as cron fields.
    await expect(sheet.getByRole("combobox", { name: "Repeats" })).toHaveText(/Every day/)
    await sheet
      .getByRole("group", { name: "Action" })
      .getByRole("button", { name: /^Backup/ })
      .click()
    await sheet.getByRole("combobox", { name: "Backup job" }).click()
    await page.getByRole("option", { name: "Nightly snapshot", exact: true }).click()
    await sheet.getByRole("button", { name: "Add schedule" }).click()

    await expect(page.getByText("Schedule created", { exact: true })).toBeVisible()
    // An untouched builder still posts the nightly expression, with the job.
    expect(schedules[0]).toMatchObject({
      expression: "0 3 * * *",
      steps: [{ action: "backup", config: { jobId: 4 } }],
    })
    await expect(page.getByText("Nightly backup", { exact: true })).toBeVisible()
    const scheduleList = page.getByRole("list", { name: "Schedules" })
    await expect(scheduleList.getByText(/^Every day at 03:00 · /)).toBeVisible()
    await expect(page.getByText("Enabled", { exact: true }).first()).toBeVisible()

    await page.getByRole("button", { name: "Runs", exact: true }).click()
    const runsSheet = page.getByRole("dialog", { name: "Nightly backup runs" })
    await expect(runsSheet).toBeVisible()
    await expect(runsSheet.getByText("#4 Deploy", { exact: true })).toBeVisible()
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Pause", exact: true }).click()
    await expect(page.getByText("Schedule paused", { exact: true })).toBeVisible()
    await expect(page.getByText("Paused", { exact: true })).toBeVisible()

    await page.getByRole("button", { name: "Actions for Nightly backup" }).click()
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

    const openRow = page.getByRole("listitem").filter({ hasText: "pr-42" })
    // A preview blocked for isolation cannot be deployed from its card.
    await expect(
      page.getByRole("listitem").filter({ hasText: "pr-40" }).getByRole("button", {
        name: "Deploy preview",
      }),
    ).toBeDisabled()
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
    await page.getByRole("button", { name: "Actions for Deploy hook" }).click()
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
    // Counted by what was decided, and narrowed to one decision by its chip.
    await sheet.getByRole("button", { name: /^Refused/ }).click()
    await expect(sheet.getByText(/preview approval required/)).toBeVisible()
    await expect(sheet.getByText(/no watched path changed/)).toHaveCount(0)
    await page.keyboard.press("Escape")

    await page.getByRole("button", { name: "Actions for Deploy hook" }).click()
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

    const card = page.getByRole("form", { name: "Runtime" })
    const port = card.getByRole("spinbutton", { name: "Application port" })
    await port.fill("99999")
    await card.getByRole("button", { name: "Save", exact: true }).click()

    await expect(port).toHaveAttribute("aria-invalid", "true")
    await expect(
      page.getByText("runtime port must be between 1 and 65535", { exact: true }),
    ).toBeVisible()
    // The rail head of the section holding the field says the save was refused.
    await expect(card.getByText("Not saved", { exact: true })).toBeVisible()
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

    const buildCard = page.getByRole("form", { name: "Build" })
    await buildCard.getByRole("textbox", { name: "Root directory" }).fill("apps/api")
    await buildCard.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Build settings saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)

    await expect(taskCommand).toHaveValue("./bin/migrate --force")
  })

  // Each draft is keyed on its own section's saved value, not the page's
  // revision, so the save beside it bumping the revision restarts nothing.
  test("saving release tasks does not discard an unsaved Build edit", async ({ page }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/build")

    const root = page.getByRole("form", { name: "Build" }).getByRole("textbox", {
      name: "Root directory",
    })
    await root.fill("apps/web")

    const tasks = page.getByRole("form", { name: "Release tasks" })
    await tasks.getByRole("button", { name: "Add release task", exact: true }).click()
    await tasks.getByLabel("Release task 1 name").fill("Migrate")
    await tasks.getByLabel("Release task 1 command").fill("./bin/migrate")
    await tasks.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Release tasks saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    // The release tasks' save wrote the saved build, not the unsaved draft.
    expect((puts[0].build as Record<string, unknown>).rootDirectory).toBeUndefined()

    await expect(root).toHaveValue("apps/web")
  })

  test("saving health checks does not discard an unsaved Runtime edit", async ({ page }) => {
    await mockProject(page)
    const puts: Record<string, unknown>[] = []
    page.on("request", (request) => {
      if (request.method() === "PUT" && request.url().endsWith("/configuration")) {
        puts.push(request.postDataJSON())
      }
    })
    await page.goto("/deploy/7/settings/runtime")

    const memory = page.getByRole("form", { name: "Runtime" }).getByLabel("Memory limit")
    await memory.fill("768")

    const checks = page.getByRole("form", { name: "Health checks" })
    await checks.getByRole("button", { name: "Add check", exact: true }).click()
    await checks.getByRole("button", { name: "Save", exact: true }).click()
    await expect(page.getByText("Health checks saved", { exact: true })).toBeVisible()
    await expect.poll(() => puts.length).toBe(1)
    expect((puts[0].runtime as Record<string, unknown>).memoryMb).toBeUndefined()

    await expect(memory).toHaveValue("768")
  })
})

test.describe("Danger zone", () => {
  test("stop posts the stop operation for a live deployment", async ({ page }) => {
    const dashboard = await mockProject(page)
    await page.goto("/deploy/7/settings/danger")

    await expect(page.getByRole("heading", { name: "Stop the application" })).toBeVisible()
    await page.getByRole("button", { name: "Stop", exact: true }).click()
    // The header menu's own verb, so it asks before the containers go down.
    await page.getByRole("dialog").getByRole("button", { name: "Stop", exact: true }).click()
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

  test("archiving turns the archive row into a restore and opens resource removal", async ({
    page,
  }) => {
    await mockProject(page)
    let archived = 0
    await page.route("**/api/v1/deploy/7/archive", (route) => {
      archived++
      return json(route, { ...project, archivedAt: now })
    })
    await page.goto("/deploy/7/settings/danger")

    await expect(page.getByRole("heading", { name: "Archive this deployment" })).toBeVisible()
    // Removal and deletion are drawn from the start, in order, each saying
    // what has to come first rather than offering a button for it.
    await expect(page.getByRole("heading", { name: "Remove managed resources" })).toBeVisible()
    await expect(page.getByText("Archive the deployment first")).toHaveCount(2)
    await expect(page.getByRole("button", { name: "Delete permanently" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Refresh" })).toHaveCount(0)

    await page.getByRole("button", { name: "Archive", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Archive api-production" })
    await expect(dialog).toContainText("It leaves the active list")
    await dialog.getByRole("button", { name: "Archive deployment" }).click()

    expect(archived).toBe(1)
    await expect(page.getByRole("heading", { name: "Archive this deployment" })).toHaveCount(0)
    await expect(page.getByRole("heading", { name: "Restore this deployment" })).toBeVisible()
    await expect(page.getByRole("heading", { name: "Remove managed resources" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Refresh" })).toBeVisible()
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
    // The plan is read on arrival: each target as the thing it is, marked
    // where it holds data and where its name has to be typed.
    await expect(page.getByText("api-data", { exact: true }).first()).toBeVisible()
    await expect(page.getByText(/type its name to remove/)).toBeVisible()
    await expect(page.getByText("holds data", { exact: true })).toBeVisible()

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
    // Rare and unrecoverable, so the deployment's name is typed first.
    await expect(purgeDialog.getByRole("button", { name: "Delete permanently" })).toBeDisabled()
    await purgeDialog.getByRole("textbox").fill(project.name)
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
        json(route, [backupJob({ id: 4, name: "Nightly snapshot" })]),
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
    await expect(page.getByRole("link", { name: "Open api.example.test" })).toBeVisible()
    const password = page.getByRole("button", { name: "Password on api.example.test" })
    const https = page.getByRole("button", { name: "HTTPS on api.example.test" })

    // Turning protection on asks for the two fields; the password leaves the
    // browser exactly once, as a password.
    await password.click()
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
    await expect(password).toHaveAttribute("aria-pressed", "true")
    await expect(page.getByLabel("User name")).toHaveValue("team")
    await expect(page.locator("#domain-password-0")).toHaveAttribute("placeholder", "Unchanged")
    await https.click()
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
    await password.click()
    await expect(page.getByLabel("User name")).toHaveCount(0)
    await page.getByRole("button", { name: "Save", exact: true }).click()
    await expect.poll(() => puts.length).toBe(3)
    expect((puts[2].domains as Record<string, unknown>[])[0]).not.toHaveProperty("protection")
  })
})
