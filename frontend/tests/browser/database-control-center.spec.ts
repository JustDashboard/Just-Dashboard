import { expect, test, type Page } from "@playwright/test"
import { hold } from "./database-fixture"
import { dockerlessInventory, mockFleet, refusal } from "./database-fleet-fixture"

/**
 * The section-wide pages of Databases: the control center, the map and Add a
 * database, against a mocked machine that runs Docker.
 *
 * Each test is a claim one of the pages makes and a way it has been, or
 * could be, got wrong: a figure that could not be read drawn as a zero, a
 * second concern hidden behind the first, a stopped server offered a Stop, a
 * failed poll taking the fleet away, a new database published to the internet
 * by default, a connection saved without having been dialled.
 */

const where = (page: Page) => new URL(page.url()).pathname + new URL(page.url()).search
const card = (page: Page, name: string) =>
  page
    .locator("[data-card=database]")
    .filter({ has: page.getByRole("link", { name: `Open ${name}` }) })
const tile = (page: Page, label: string) =>
  page.locator("[data-slot=stat-tile]").filter({ has: page.getByText(label, { exact: true }) })
const foundRow = (page: Page, name: string) =>
  page.locator("[data-slot=choice-row]").filter({ hasText: name }).first()
const scroller = (page: Page) => page.locator("[data-slot=page]").first().locator("xpath=..")

test.describe("the control center", () => {
  test("reads the fleet as five figures and cards in their engine's words, shelved by where they run", async ({
    page,
  }) => {
    await mockFleet(page, { dumps: { 4: null, 5: 60 } })
    await page.goto("/databases")

    await expect(tile(page, "Databases")).toContainText("5 engines")
    await expect(tile(page, "Running")).toContainText("of 5")
    // The file reported no size: the sum says how many did, and is not a zero.
    await expect(tile(page, "Stored")).toContainText("1 of 5 did not report a size")
    await expect(tile(page, "Sessions")).toContainText("14")
    await expect(tile(page, "Backed up")).toContainText("3")
    await expect(tile(page, "Backed up")).toContainText("1 never backed up")

    for (const [shelf, names] of [
      ["Containers", ["blog", "cache", "shop"]],
      ["Files", ["notes"]],
      ["Elsewhere", ["app"]],
    ] as const) {
      const list = page.getByRole("list", { name: shelf })
      for (const name of names) {
        await expect(list.getByRole("link", { name: `Open ${name}` })).toBeVisible()
      }
    }

    // MariaDB behind the mysql driver is drawn and named as itself.
    await expect(card(page, "blog")).toContainText("MariaDB 11.8.9")
    await expect(card(page, "blog").locator("img").first()).toHaveAttribute("src", /mariadb/)
    await expect(card(page, "cache")).toContainText("Keys")
    await expect(card(page, "cache")).toContainText("Clients")
    await expect(card(page, "app")).toContainText("Collections")
    // The figure nobody reported is a dash on the card, and the dump age is said.
    await expect(card(page, "shop")).toContainText("backed up 3h ago")
    await expect(card(page, "cache")).toContainText("never backed up")
    await expect(card(page, "shop")).toContainText("shop-db")

    // The two work pages of each engine, under its own names.
    await expect(card(page, "shop").getByRole("link", { name: "Data of shop" })).toHaveAttribute(
      "href",
      "/databases/1/data",
    )
    await expect(
      card(page, "cache").getByRole("link", { name: "Console of cache" }),
    ).toHaveAttribute("href", "/databases/4/query")
    await expect(
      card(page, "app").getByRole("link", { name: "Aggregations of app" }),
    ).toHaveAttribute("href", "/databases/5/query")

    // The whole card is the way in; a control on it is its own press.
    await card(page, "shop").getByText("Stored").click()
    await expect(page).toHaveURL(/\/databases\/1$/)
  })

  test("a reading narrows the fleet, the address says which, and Back undoes it", async ({
    page,
  }) => {
    await mockFleet(page, {
      fleet: {
        2: { ok: false, state: "stopped" },
        4: {
          ok: false,
          state: "unreachable",
          error: "dial tcp 127.0.0.1:6379: connection refused",
        },
      },
    })
    await page.goto("/databases")
    await expect(tile(page, "Running")).toContainText("1 stopped · 1 not answering")

    await page.getByRole("button", { name: "Show the databases that are not running" }).click()
    expect(where(page)).toBe("/databases?show=down")
    await expect(page.locator("[data-card=database]")).toHaveCount(2)
    await expect(card(page, "shop")).toHaveCount(0)
    // What the server said stands where the figures would be.
    await expect(card(page, "cache")).toContainText("connection refused")

    // A pasted link is the same view.
    await page.reload()
    await expect(page.locator("[data-card=database]")).toHaveCount(2)
    await expect(page.getByRole("button", { name: "Show every database again" })).toHaveAttribute(
      "aria-pressed",
      "true",
    )

    await page.goBack()
    await expect(page.locator("[data-card=database]")).toHaveCount(5)
    await page.goForward()
    await page.getByRole("button", { name: /Not running/ }).click()
    expect(where(page)).toBe("/databases")
  })

  test("engines and words narrow it too, and nothing left offers the way back", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: /^MariaDB/ }).click()
    expect(where(page)).toBe("/databases?engine=mariadb")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    await page.getByRole("button", { name: /^All/ }).click()

    await page.getByLabel("Filter databases").fill("cache")
    await expect(page.locator("[data-card=database]")).toHaveCount(1)
    await page.getByLabel("Filter databases").fill("nothing-like-this")
    await expect(page.getByText("No database matches")).toBeVisible()
    await page
      .locator("[data-slot=empty-state]")
      .getByRole("button", { name: "Show every database" })
      .click()
    await expect(page.locator("[data-card=database]")).toHaveCount(5)
    expect(where(page)).toBe("/databases")
  })

  test("the shelves and the table are the reader's own arrangement, kept and not in the address", async ({
    page,
  }) => {
    await mockFleet(page, {
      rows: { 1: { environment: "production" }, 2: { environment: "staging" } },
    })
    await page.goto("/databases")

    await page.getByRole("radio", { name: "Engine" }).click()
    await expect(page.getByRole("list", { name: "MariaDB" })).toBeVisible()
    await page.getByRole("radio", { name: "Environment" }).click()
    await expect(page.getByRole("list", { name: "production" })).toBeVisible()
    await expect(page.getByRole("list", { name: "No environment" })).toBeVisible()

    await page.getByRole("radio", { name: "Table" }).click()
    const table = page.getByRole("table")
    // The figures to compare stay on screen at this width; the engine and
    // the place, which the name's mark and the shelves say, join them on a
    // wider one.
    await expect(table.getByRole("columnheader")).toHaveText([
      "Status",
      "Name",
      "Size",
      "Objects",
      "Sessions",
      "Feeds",
      "Last backup",
      "Reachable from",
      "Actions",
    ])
    await page.setViewportSize({ width: 1720, height: 900 })
    await expect(table.getByRole("columnheader")).toHaveText([
      "Status",
      "Name",
      "Engine",
      "Where",
      "Size",
      "Objects",
      "Sessions",
      "Feeds",
      "Last backup",
      "Reachable from",
      "Actions",
    ])
    await expect(table.getByRole("row", { name: /shop/ })).toContainText("shop-db")
    // The file's size was not reported: a dash, never 0 B.
    await expect(table.getByRole("row", { name: /notes/ })).toContainText("—")
    await table.getByRole("button", { name: "Size" }).click()
    await expect(table.getByRole("columnheader", { name: "Size" })).toHaveAttribute(
      "aria-sort",
      "descending",
    )
    await expect(table.getByRole("row").nth(1)).toContainText("shop")
    await expect(table.getByRole("link", { name: "Open shop" })).toHaveAttribute(
      "href",
      "/databases/1",
    )
    expect(where(page)).toBe("/databases")

    await page.reload()
    await expect(page.getByRole("table")).toBeVisible()
    await page.getByRole("radio", { name: "Cards" }).click()
    await page.getByRole("radio", { name: "Where it runs" }).click()
  })

  test("what needs attention lists every concern of a database, each with its fix", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      dumps: { 1: null, 4: null, 5: 400 },
      fleet: {
        1: { exposure: "public" },
        2: { ok: false, state: "stopped", consumers: 2 },
        4: { ok: false, state: "unreachable", error: "NOAUTH Authentication required" },
      },
    })
    await page.goto("/databases")
    const attention = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Needs attention" }),
    })
    const finding = (title: RegExp) => attention.getByRole("button", { name: title })

    // The worst stands first, and a public port does not hide the missing dump.
    await expect(attention.getByRole("button").first()).toContainText("cache cannot be reached")
    await expect(finding(/shop is reachable from the internet/)).toBeVisible()
    await expect(finding(/2 databases have never been backed up/)).toBeVisible()
    await expect(finding(/app was last backed up/)).toBeVisible()
    await expect(finding(/postgresql@17-main is running here and needs a password/)).toBeVisible()

    // Stopped while two deployments use it: the fix is Start, and it runs.
    await finding(/blog is stopped and 2 deployment environments use it/).click()
    await attention.getByRole("button", { name: "Start", exact: true }).click()
    await expect.poll(() => server.bodies("POST /databases/2/power")).toEqual([{ action: "start" }])

    // Public, in a container of its own: the fix asks first and names it.
    await finding(/shop is reachable from the internet/).click()
    await attention.getByRole("button", { name: "Restrict to this server" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Recreates shop-db")
    await expect(dialog).toContainText("PostgreSQL 16.4")
    await dialog.getByRole("button", { name: "Restrict" }).click()
    await expect
      .poll(() => server.bodies("PUT /databases/1/access"))
      .toEqual([{ exposure: "local" }])

    // Never dumped: each can be dumped from the finding, and the job is watched.
    await finding(/2 databases have never been backed up/).click()
    // One that does not answer cannot be dumped, and is offered no dump.
    await expect(attention.getByRole("button", { name: "Back up cache now" })).toHaveCount(0)
    await attention.getByRole("button", { name: "Back up shop now" }).click()
    await expect(page.getByText("shop backed up")).toBeVisible()
    expect(server.bodies("POST /databases/1/backup")).toEqual([{}])
  })

  test("a stopped server keeps its card with Start, and says it is starting until it has", async ({
    page,
  }) => {
    const started = hold()
    const server = await mockFleet(page, { fleet: { 2: { ok: false, state: "stopped" } } })
    server.answer("POST /databases/2/power", async () => {
      await started.until
    })
    await page.goto("/databases")

    await expect(card(page, "blog").locator("[data-slot=database-status]")).toHaveText("stopped")
    // Nothing to stop, nothing to open: Start is its verb.
    await expect(card(page, "blog").getByRole("link", { name: "Data of blog" })).toHaveCount(0)
    await card(page, "blog").getByRole("button", { name: "Actions for blog" }).click()
    await expect(page.getByRole("menuitem", { name: "Start" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Stop" })).toHaveCount(0)
    await page.keyboard.press("Escape")

    await card(page, "blog").getByRole("button", { name: "Start blog" }).click()
    await expect(card(page, "blog")).toContainText("Starting…")
    await expect(card(page, "blog").getByRole("button", { name: "Start blog" })).toHaveCount(0)
    started.release()
    await expect(page.getByText("blog started")).toBeVisible()
    expect(server.bodies("POST /databases/2/power")).toEqual([{ action: "start" }])
  })

  test("stopping asks first, names the container and what depends on it", async ({ page }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await expect(page.getByRole("menuitem")).toHaveText([
      "Stop",
      "Restart",
      "Back up now",
      "Settings",
      "Forget",
    ])
    await page.getByRole("menuitem", { name: "Stop" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Stops shop-db")
    await expect(dialog).toContainText("2 deployment environments are bound to it")
    expect(server.sent).toEqual([])
    await dialog.getByRole("button", { name: "Stop" }).click()
    await expect(page.getByText("shop stopped")).toBeVisible()
    expect(server.bodies("POST /databases/1/power")).toEqual([{ action: "stop" }])

    // A server somewhere else has no power here, and a file none at all.
    await card(page, "app").getByRole("button", { name: "Actions for app" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Back up now", "Settings", "Forget"])
  })

  test("forgetting a connection a deployment is linked to says what to undo first", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("DELETE /databases/1", () =>
      refusal(
        409,
        "database_linked",
        "remove the deployment's managed database network before forgetting this linked connection",
      ),
    )
    await page.goto("/databases")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("The database itself and its data are not touched")
    await dialog.getByRole("button", { name: "Forget" }).click()
    await expect(
      page.getByText(/shop is linked to a deployment through a managed database network/),
    ).toBeVisible()
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(card(page, "shop")).toBeVisible()

    // One nothing is linked to goes, and its card with it.
    await card(page, "cache").getByRole("button", { name: "Actions for cache" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    await page.getByRole("dialog").getByRole("button", { name: "Forget" }).click()
    await expect(page.getByText("Forgot cache")).toBeVisible()
    await expect(card(page, "cache")).toHaveCount(0)
  })

  test("a role that only reads is drawn no command it cannot use and asks for no inventory", async ({
    page,
  }) => {
    const server = await mockFleet(page, {
      viewer: true,
      fleet: { 2: { ok: false, state: "stopped" } },
    })
    await page.goto("/databases")

    await expect(card(page, "shop")).toBeVisible()
    await expect(page.getByRole("link", { name: "Add a database" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Scan this server again" })).toHaveCount(0)
    await expect(page.getByRole("heading", { name: "Found on this server" })).toHaveCount(0)
    await expect(card(page, "blog").getByRole("button", { name: "Start blog" })).toHaveCount(0)
    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await expect(page.getByRole("menuitem")).toHaveText(["Settings"])
    expect(server.asked).not.toContain("GET /databases/inventory")
  })

  test("it waits in the shape it will have, says a failure with a way to ask again, and a failed poll takes nothing away", async ({
    page,
  }) => {
    const first = hold()
    let answer: "held" | "failing" | "given" = "held"
    const server = await mockFleet(page, {
      // One server is down, so the fleet is read again every five seconds.
      fleet: { 4: { ok: false, state: "unreachable", error: "connection refused" } },
    })
    server.answer("GET /databases/fleet", async () => {
      if (answer === "held") await first.until
      if (answer === "failing") return refusal(500, "internal", "the fleet could not be read", true)
    })
    await page.goto("/databases")
    await expect(page.getByRole("status", { name: "Loading databases" })).toBeVisible()
    await expect(page.locator("[data-slot=stat-grid]")).toContainText("Backed up")

    answer = "failing"
    first.release()
    await expect(
      page.getByRole("alert").filter({ hasText: "the fleet could not be read" }),
    ).toBeVisible()
    answer = "given"
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(card(page, "shop")).toBeVisible()

    answer = "failing"
    await expect(page.getByText("the last check did not finish").first()).toBeVisible({
      timeout: 15_000,
    })
    await expect(card(page, "shop")).toBeVisible()
    await expect(tile(page, "Running")).toContainText("of 5")
  })

  test("an empty server says so, offers the way to add one, and lists what was found under it", async ({
    page,
  }) => {
    await mockFleet(page, { empty: true })
    await page.goto("/databases")

    await expect(page.getByText("No databases yet")).toBeVisible()
    await expect(
      page.locator("[data-slot=empty-state]").getByRole("link", { name: "Add a database" }),
    ).toHaveAttribute("href", "/databases/new")
    await expect(page.getByRole("heading", { name: "Found on this server" })).toBeVisible()
    await expect(foundRow(page, "orders-db")).toBeVisible()
  })
})

test.describe("found on this server", () => {
  test("lists what is not connected with the one thing to do about each", async ({ page }) => {
    await mockFleet(page)
    await page.goto("/databases")
    const found = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Found on this server" }),
    })

    // Connected ones are not listed again.
    await expect(found.getByText("shop-db")).toHaveCount(0)
    await expect(found.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Connect postgresql@17-main" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Start old-mysql" })).toBeVisible()
    await expect(foundRow(page, "old-mysql")).toContainText("the container is exited")
    await expect(found.getByRole("button", { name: "Start redis-server" })).toBeVisible()
    await expect(found.getByRole("button", { name: "Open data.db" })).toBeVisible()

    // Declared and never created: the stack that declares it is where to go.
    await expect(found.getByRole("link", { name: "Open stack search-db" })).toHaveAttribute(
      "href",
      "/docker/stacks/shop",
    )
    // Seen, and nothing here opens it: it says so and links to what runs it.
    await expect(foundRow(page, "memcached")).toContainText("no driver yet")
    await expect(found.getByRole("link", { name: "Open container memcached" })).toHaveAttribute(
      "href",
      "/docker/containers/c-mc",
    )
    await expect(foundRow(page, "sales.duckdb")).toContainText("no driver for DuckDB")
    await expect(found.getByText("2 directories are outside the file roots")).toBeVisible()

    // The dashboard's own store is counted behind a fold, with no way to connect it.
    await found.getByText("Kept by other programs").click()
    await expect(foundRow(page, "vpsd.db")).toContainText("never connected")
    await expect(found.getByRole("button", { name: "Open vpsd.db" })).toHaveCount(0)
  })

  test("connects by key, asks for a password only when the server refuses what it states", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    let tried = 0
    server.answer("POST /databases/inventory/connect", ({ body }) => {
      if (body.key !== "docker:orders-db") return
      tried += 1
      if (!body.password) {
        return refusal(
          409,
          "sign_in_failed",
          "orders-db did not accept the credentials found for it (password authentication failed); type the password it actually uses",
        )
      }
      if (body.password !== "right") {
        return refusal(400, "sign_in_failed", 'password authentication failed for user "orders"')
      }
    })
    await page.goto("/databases")

    await page.getByRole("button", { name: "Connect orders-db" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("did not accept the credentials found for it")
    await expect(dialog.getByLabel("User")).toHaveValue("orders")
    await dialog.getByLabel("Password").fill("wrong")
    await dialog.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(dialog).toContainText("It refused the sign-in")
    await expect(dialog).toContainText('password authentication failed for user "orders"')
    await dialog.getByLabel("Password").fill("right")
    await dialog.getByRole("button", { name: "Connect", exact: true }).click()

    await expect(page.getByText("Connected orders-db")).toBeVisible()
    await expect(card(page, "orders-db")).toBeVisible()
    expect(tried).toBe(3)
    expect(server.bodies("POST /databases/inventory/connect").at(-1)).toEqual({
      key: "docker:orders-db",
      user: "orders",
      password: "right",
      database: "orders",
    })
  })

  test("a native server offers an account made from this machine, with its password shown before it is used", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: "Connect postgresql@17-main" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("postgresql@17-main.service · 127.0.0.1:5438")
    await expect(
      dialog.getByRole("button", { name: "Make an account from this server" }),
    ).toHaveAttribute("aria-pressed", "true")
    const generated = await dialog.getByLabel("Its password").inputValue()
    expect(generated).toMatch(/^[A-Za-z0-9]{30}$/)
    await dialog.getByRole("button", { name: "Make the account and connect" }).click()

    await expect(page.getByText("Connected postgres 17 main on this host")).toBeVisible()
    expect(server.bodies("POST /databases/host/grant")).toEqual([
      {
        driver: "postgres",
        host: "127.0.0.1",
        port: 5438,
        user: "just_dashboard",
        password: generated,
        database: "postgres",
        name: "",
        superuser: true,
      },
    ])
  })

  test("starts what is down, opens a file, ignores and brings back, and scans on request", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases")

    await page.getByRole("button", { name: "Start old-mysql" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /docker/containers/c-old/start")
    await expect(foundRow(page, "old-mysql")).toContainText("Starting…")
    await page.getByRole("button", { name: "Start redis-server" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /systemd/redis-server.service/start")

    await page.getByRole("button", { name: "Open data.db" }).click()
    await expect(page.getByText("Connected data.db")).toBeVisible()
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([
      { key: "file:/srv/app/data.db" },
    ])

    await page.getByRole("button", { name: "Ignore sessions" }).click()
    await expect
      .poll(() => server.bodies("POST /databases/inventory/ignore"))
      .toEqual([{ key: "docker:sessions", ignored: true }])
    await page.getByText("Ignored", { exact: true }).click()
    await expect(page.getByRole("button", { name: "Stop ignoring sessions" })).toBeVisible()
    // A key whose server is gone can still be taken off the list.
    await page.getByRole("button", { name: "Stop ignoring docker:long-gone" }).click()
    await expect
      .poll(() => server.bodies("POST /databases/inventory/ignore").at(-1))
      .toEqual({
        key: "docker:long-gone",
        ignored: false,
      })

    await page.getByRole("button", { name: "Scan this server again" }).click()
    await expect
      .poll(() => server.sent.map((one) => one.call))
      .toContain("POST /databases/inventory/scan")
  })

  test("a Docker that did not answer is a stated silence, not an empty list", async ({ page }) => {
    await mockFleet(page, { inventory: dockerlessInventory() })
    await page.goto("/databases")

    await expect(page.getByText("Not looked at: containers and compose services.")).toBeVisible()
    await expect(page.getByText(/Docker did not answer/)).toBeVisible()
    await expect(page.getByRole("button", { name: "Connect postgresql@17-main" })).toBeVisible()
  })
})

test.describe("the map", () => {
  test("draws what feeds what, with its figures, and narrows to an engine in the address", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases/map")

    await expect(tile(page, "Databases")).toContainText("5 engines")
    await expect(tile(page, "Readers")).toContainText("over 6 links")
    await expect(tile(page, "Sessions")).toContainText("12")
    await expect(tile(page, "Links wrong")).toContainText("broken or stale")

    const picture = page.locator("[data-slot=wiring]")
    await expect(
      picture.getByRole("list", { name: "Databases" }).getByRole("listitem"),
    ).toHaveCount(5)
    await expect(picture.getByRole("link", { name: "Open blog" })).toHaveAttribute(
      "href",
      "/databases/2",
    )
    // The engine that answered, not the driver that dials it.
    await expect(picture.getByRole("list", { name: "Databases" })).toContainText("MariaDB")
    await expect(page.getByRole("list", { name: "How to read the map" })).toContainText(
      "link broken",
    )

    const readers = page.locator("[data-slot=row-list]")
    await expect(readers.locator("[data-slot=row]")).toHaveCount(4)
    // The broken link stands first, and says so.
    await expect(readers.locator("[data-slot=row]").first()).toContainText("worker")
    await expect(readers.locator("[data-slot=row]").first()).toContainText("link broken")
    await expect(readers).toContainText("shop — linked by its deployment · 3 open sessions")

    await page.getByRole("button", { name: /^Redis/ }).click()
    expect(where(page)).toBe("/databases/map?engine=redis")
    await expect(
      picture.getByRole("list", { name: "Databases" }).getByRole("listitem"),
    ).toHaveCount(1)
    await expect(readers.locator("[data-slot=row]")).toHaveCount(2)
    await page.goBack()
    await expect(readers.locator("[data-slot=row]")).toHaveCount(4)
  })

  test("says when it could not be read, and when there is nothing to map", async ({ page }) => {
    const server = await mockFleet(page)
    let failing = true
    server.answer("GET /databases/topology", () => {
      if (failing) return refusal(500, "internal", "the map could not be drawn", true)
    })
    await page.goto("/databases/map")
    await expect(
      page.getByRole("alert").filter({ hasText: "the map could not be drawn" }),
    ).toBeVisible()
    failing = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(page.locator("[data-slot=wiring]")).toBeVisible()

    server.answer("GET /databases/topology", () => ({
      body: { nodes: [], edges: [], checkedAt: new Date().toISOString() },
    }))
    await page.reload()
    await expect(page.getByText("Nothing to map yet")).toBeVisible()
  })
})

test.describe("adding a database", () => {
  test("the way in and the engine are in the address, and Back steps out of each", async ({
    page,
  }) => {
    await mockFleet(page)
    await page.goto("/databases")
    await page.getByRole("link", { name: "Add a database" }).last().click()
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Where is the database?")
    const way = (name: string) => page.getByRole("button", { name, exact: true })
    await expect(way("Start a new one here")).toHaveAttribute("aria-pressed", "true")

    await way("Connect to one").click()
    expect(where(page)).toBe("/databases/new?mode=connect")
    await page.getByRole("button", { name: /^Redis/ }).click()
    expect(where(page)).toBe("/databases/new?mode=connect&engine=redis")
    await expect(page.locator("[data-slot=flow-panel]")).toContainText("Redis")
    await page.goBack()
    await expect(page.locator("[data-slot=flow-panel]")).toHaveCount(0)
    await page.goBack()
    await expect(way("Start a new one here")).toHaveAttribute("aria-pressed", "true")

    // A link to one way in opens on it.
    await page.goto("/databases/new?mode=found")
    await expect(way("Found on this server")).toHaveAttribute("aria-pressed", "true")
    await expect(page.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
  })

  test("a new server answers on this server only unless the reader says otherwise, and lands on its home", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    let adoptions = 0
    server.answer("POST /databases/adopt", () => {
      adoptions += 1
      // The container is up before the engine inside it answers.
      if (adoptions === 1)
        return refusal(409, "sign_in_failed", "the database system is starting up")
    })
    await page.goto("/databases/new")

    for (const shelf of ["SQL", "Documents", "Key–value", "Analytics"]) {
      await expect(page.getByRole("group", { name: shelf })).toBeVisible()
    }
    await expect(page.getByRole("group", { name: "Analytics" })).toContainText("ClickHouse")
    await expect(page.getByRole("group", { name: "SQL" })).toContainText("TimescaleDB")

    await page.getByRole("button", { name: /^PostgreSQL/ }).click()
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toContainText("postgres:16-alpine")
    await panel.getByRole("radio", { name: "17" }).click()
    await expect(panel).toContainText("postgres:17-alpine")
    await expect(panel).toContainText("It will answer on this server only.")
    await panel.getByLabel("Name").fill("orders db")
    await expect(panel.getByRole("alert")).toContainText("Letters, digits, dots")
    await expect(panel.getByRole("button", { name: "Create" })).toBeDisabled()
    await panel.getByLabel("Name").fill("orders-db")
    await panel.getByLabel("Database").fill("orders")
    await panel.getByRole("button", { name: "Create" }).click()

    await expect(panel.locator("[data-slot=flow-steps]")).toBeVisible()
    await expect(page).toHaveURL(/\/databases\/20$/, { timeout: 15_000 })
    expect(server.bodies("POST /databases/provision")).toEqual([
      {
        engine: "postgres",
        name: "orders-db",
        version: "17",
        database: "orders",
        exposure: "local",
      },
    ])
    expect(server.bodies("POST /databases/adopt")).toEqual([
      { container: "orders-db" },
      { container: "orders-db" },
    ])
  })

  test("publishing a new server is an explicit choice, an engine with no accounts is sent none, and a refusal is shown where it was asked", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/provision", () =>
      refusal(409, "container_exists", "a container named jd-valkey already exists"),
    )
    await page.goto("/databases/new?mode=start&engine=valkey")
    const panel = page.locator("[data-slot=flow-panel]")

    await expect(panel.getByLabel("Database")).toHaveCount(0)
    await panel.getByText("Account", { exact: true }).click()
    await expect(panel.getByLabel("User")).toHaveCount(0)
    const publish = panel.getByRole("switch")
    await expect(publish).not.toBeChecked()
    await publish.click()
    await expect(panel).toContainText("It will answer on every address this server has.")
    await panel.getByRole("button", { name: "Create" }).click()

    await expect(panel).toContainText("It was not started")
    await expect(panel).toContainText("a container named jd-valkey already exists")
    expect(server.bodies("POST /databases/provision")).toEqual([
      { engine: "valkey", version: "8", exposure: "public" },
    ])
    // The answers are still there to change.
    await expect(publish).toBeChecked()
  })

  test("a server with no Docker says so before anything is asked of it", async ({ page }) => {
    await mockFleet(page, { inventory: dockerlessInventory() })
    await page.goto("/databases/new?mode=start&engine=postgres")
    const panel = page.locator("[data-slot=flow-panel]")
    await expect(panel).toContainText("There is no Docker to start it in")
    await expect(panel.getByRole("button", { name: "Create" })).toBeDisabled()
  })

  test("a connection typed by hand is saved only once the string on screen has answered", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/test", ({ body }) => {
      if (!String(body.dsn).includes(":right@")) {
        return { body: { ok: false, error: 'password authentication failed for user "app"' } }
      }
    })
    await page.goto("/databases/new?mode=connect&engine=postgres")
    const panel = page.locator("[data-slot=flow-panel]")
    const connect = panel.getByRole("button", { name: "Connect", exact: true })

    await expect(panel.getByRole("button", { name: "Test" })).toBeDisabled()
    await panel.getByLabel("Host").fill("db.example.com")
    await panel.getByLabel("User").fill("app")
    await panel.getByLabel("Password").fill("wrong")
    await panel.getByLabel("Database", { exact: true }).fill("shop")
    // What will be saved is shown with the password hidden.
    await expect(panel.locator("[data-slot=connection-preview]")).toHaveText(
      "postgres://app:••••••@db.example.com:5432/shop?sslmode=disable",
    )
    await expect(connect).toBeDisabled()
    await panel.getByRole("button", { name: "Test" }).click()
    await expect(panel).toContainText("It refused the connection")
    await expect(connect).toBeDisabled()

    await panel.getByLabel("Password").fill("right")
    await panel.getByRole("radio", { name: "Verified" }).click()
    await panel.getByRole("button", { name: "Test" }).click()
    await expect(panel.locator("[data-slot=test-result]")).toHaveText("Answered: PostgreSQL 16.4")
    await expect(connect).toBeEnabled()
    // Changing the address after the test is a string nobody has dialled.
    await panel.getByLabel("Port").fill("6543")
    await expect(connect).toBeDisabled()
    await panel.getByRole("button", { name: "Test" }).click()
    await expect(connect).toBeEnabled()

    await panel.getByRole("radio", { name: "Production" }).click()
    await panel.getByRole("switch", { name: /Protect it/ }).click()
    await connect.click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/")).toEqual([
      {
        name: "shop-2",
        driver: "postgres",
        dsn: "postgres://app:right@db.example.com:6543/shop?sslmode=verify-full",
        probe: true,
        environment: "production",
        readOnly: true,
      },
    ])
  })

  test("a string can be pasted whole, a server that is down saved untested, and the server's refusal stays on the form", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    server.answer("POST /databases/", ({ body }) => {
      if (body.name === "taken")
        return refusal(400, "bad_request", "a connection named taken already exists")
    })
    await page.goto("/databases/new?mode=connect&engine=mysql")
    const panel = page.locator("[data-slot=flow-panel]")

    await panel.getByRole("radio", { name: "URL" }).click()
    await panel.getByLabel("Connection string").fill("redis://:pw@cache.example.com:6379/0")
    await expect(panel.getByRole("alert")).toContainText("That is a Redis address.")
    await panel.getByLabel("Connection string").fill("mysql://app:p%40ss@db.example.com:3307/blog")
    await expect(panel.locator("[data-slot=connection-preview]")).toHaveText(
      "app:••••••@tcp(db.example.com:3307)/blog",
    )
    await panel.getByLabel("Name").fill("taken")
    await panel.getByRole("switch", { name: /Save it without testing/ }).click()
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(panel).toContainText("It was not saved")
    await expect(panel).toContainText("a connection named taken already exists")
    expect(where(page)).toBe("/databases/new?mode=connect&engine=mysql")

    await panel.getByLabel("Name").fill("blog-remote")
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/").at(-1)).toEqual({
      name: "blog-remote",
      driver: "mysql",
      dsn: "app:p@ss@tcp(db.example.com:3307)/blog",
      probe: false,
    })
  })

  test("a file-based engine asks for a path and says where it may be", async ({ page }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=connect&engine=sqlite")
    const panel = page.locator("[data-slot=flow-panel]")

    await expect(panel.getByLabel("Host")).toHaveCount(0)
    await expect(panel.getByRole("radio", { name: "URL" })).toHaveCount(0)
    await expect(panel).toContainText("inside /srv or /home")
    await panel.getByLabel("Database file").fill("/srv/app/reports.db")
    await panel.getByRole("button", { name: "Test" }).click()
    await expect(panel.locator("[data-slot=test-result]")).toBeVisible()
    await panel.getByRole("button", { name: "Connect", exact: true }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/")).toEqual([
      { name: "reports", driver: "sqlite", dsn: "/srv/app/reports.db", probe: true },
    ])
  })

  test("a found server connected from here lands on its home, and a link that names one opens on its password", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto(
      `/databases/new?mode=found&key=${encodeURIComponent("host:postgresql@17-main.service")}`,
    )
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Connect postgresql@17-main")
    await dialog.getByRole("button", { name: "Cancel" }).click()
    await expect(dialog).toHaveCount(0)

    await page.getByRole("button", { name: "Connect sessions" }).click()
    await expect(page).toHaveURL(/\/databases\/20$/)
    expect(server.bodies("POST /databases/inventory/connect")).toEqual([{ key: "docker:sessions" }])
  })

  test("every server that states its credentials can be connected in one request", async ({
    page,
  }) => {
    const server = await mockFleet(page)
    await page.goto("/databases/new?mode=found")
    await page.getByRole("button", { name: "Connect all 2" }).click()
    await expect(page.getByText("Connected 2 databases")).toBeVisible()
    expect(server.sent.map((one) => one.call)).toEqual(["POST /databases/sync"])
  })

  test("a role that cannot add one is told so and is sent nowhere", async ({ page }) => {
    const server = await mockFleet(page, { viewer: true })
    await page.goto("/databases/new?mode=found")
    await expect(page.getByText("Adding a database needs an administrator")).toBeVisible()
    expect(server.asked).not.toContain("GET /databases/inventory")
    expect(server.asked).not.toContain("GET /databases/provision/options")
  })
})

/**
 * The design system's structural rules, held on these pages in the states
 * that draw the most: the full control center, its table, its dialogs, the
 * map, and each way of adding a database with its form open.
 */

/** Every visible button with no text of its own and no name from anywhere else. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
      const text = (el.textContent ?? "").trim()
      if (text.length > 0) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels. */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const r = parseFloat(s.borderTopLeftRadius)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || r < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      const text = (el.textContent ?? "").trim()
      if (filled && text.length > 0) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

/** Text a centred row sets off its own centre line. */
function offCentreText(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const item of document.querySelectorAll<HTMLElement>("body *")) {
      const row = item.parentElement
      if (!row) continue
      const rowStyle = getComputedStyle(row)
      if (!rowStyle.display.endsWith("flex") || !rowStyle.flexDirection.startsWith("row")) continue
      const style = getComputedStyle(item)
      const align = ["auto", "normal"].includes(style.alignSelf)
        ? rowStyle.alignItems
        : style.alignSelf
      if (align !== "center" || style.display !== "block" || style.position === "absolute") continue
      const box = item.getBoundingClientRect()
      if (box.height < 2 || box.width < 2) continue
      const blocks = [...item.children].some((child) => {
        const display = getComputedStyle(child).display
        return display !== "none" && !display.startsWith("inline")
      })
      if (blocks) continue
      const range = document.createRange()
      range.selectNodeContents(item)
      const ink = [...range.getClientRects()].filter((r) => r.width > 0 && r.height > 0)
      if (ink.length === 0) continue
      const top = Math.min(...ink.map((r) => r.top))
      const bottom = Math.max(...ink.map((r) => r.bottom))
      const offset = (top + bottom) / 2 - (box.top + box.bottom) / 2
      if (Math.abs(offset) >= 1.5) {
        bad.push(`${offset.toFixed(1)}px ${item.outerHTML.slice(0, 140)}`)
      }
    }
    return bad
  })
}

async function keepsTheRules(page: Page, what: string, register: "reading" | "flow") {
  await page.waitForLoadState("networkidle")
  await expect(page.locator("[data-slot=page]").first()).toBeVisible()
  expect(await unnamedControls(page), `unlabelled icon-only controls on ${what}`).toEqual([])
  expect(await offCentreText(page), `text off its row's centre line on ${what}`).toEqual([])
  expect(await filledPills(page), `fully rounded filled chips on ${what}`).toEqual([])
  const registers = await page
    .locator("[data-slot=page]")
    .evaluateAll((pages) => pages.map((el) => (el as HTMLElement).dataset.register))
  expect(registers, `the register of ${what}`).toEqual([register])
  const panels = await page.locator("[data-slot=flow-panel]").count()
  expect(panels, `flow panels on ${what}`).toBeLessThanOrEqual(register === "flow" ? 1 : 0)
  const sideways = await scroller(page).evaluate((el) => el.scrollWidth > el.clientWidth + 1)
  expect(sideways, `${what} scrolls sideways`).toBe(false)
}

const BUSY = {
  dumps: { 4: null, 5: 400 },
  rows: { 1: { environment: "production", readOnly: true }, 2: { environment: "eu-west staging" } },
  fleet: {
    1: { exposure: "public" },
    2: { ok: false, state: "stopped", consumers: 1 },
    4: {
      ok: false,
      state: "unreachable",
      error: "dial tcp 127.0.0.1:6379: connect: connection refused",
    },
  },
}

for (const [label, viewport] of [
  ["", { width: 1280, height: 900 }],
  [" at a phone's width", { width: 390, height: 844 }],
] as const) {
  test(`the control center, the map and Add a database keep the design system's rules${label}`, async ({
    page,
  }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    await mockFleet(page, BUSY)

    await page.goto("/databases")
    await expect(card(page, "shop")).toBeVisible()
    await keepsTheRules(page, "/databases", "reading")
    await page
      .getByRole("button", { name: /have never been backed up|has never been backed up/ })
      .click()
    await page.getByText("Ignored", { exact: true }).click()
    await page.getByText("Kept by other programs").click()
    await keepsTheRules(page, "/databases with its folds open", "reading")

    await card(page, "shop").getByRole("button", { name: "Actions for shop" }).click()
    await page.getByRole("menuitem", { name: "Forget" }).click()
    await keepsTheRules(page, "the forget confirmation", "reading")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()
    await page.getByRole("button", { name: "Connect postgresql@17-main" }).click()
    await keepsTheRules(page, "the password form of a found server", "reading")
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click()

    if (viewport.width >= 1024) {
      await page.getByRole("radio", { name: "Table" }).click()
      await keepsTheRules(page, "/databases as a table", "reading")
      await page.getByRole("radio", { name: "Cards" }).click()
    } else {
      // Eleven columns do not fit a phone: the cards are the rows drawn down.
      await expect(page.getByRole("radio", { name: "Table" })).toHaveCount(0)
    }

    await page.goto("/databases/map")
    await expect(page.locator("[data-slot=wiring]")).toBeVisible()
    await keepsTheRules(page, "/databases/map", "reading")

    await page.goto("/databases/new")
    await expect(page.getByRole("group", { name: "SQL" })).toBeVisible()
    await keepsTheRules(page, "/databases/new", "flow")
    await page.goto("/databases/new?mode=start&engine=postgres")
    await expect(page.locator("[data-slot=flow-panel]")).toBeVisible()
    await page.locator("[data-slot=flow-panel]").getByText("Account", { exact: true }).click()
    await keepsTheRules(page, "starting a PostgreSQL", "flow")
    await page.goto("/databases/new?mode=connect&engine=mongodb")
    await expect(page.locator("[data-slot=flow-panel]")).toBeVisible()
    await page.locator("[data-slot=flow-panel]").getByLabel("Host").fill("db.example.com")
    await keepsTheRules(page, "connecting a MongoDB", "flow")
    await page.goto("/databases/new?mode=found")
    await expect(page.getByRole("button", { name: "Connect orders-db" })).toBeVisible()
    await keepsTheRules(page, "the found servers", "flow")
  })
}
