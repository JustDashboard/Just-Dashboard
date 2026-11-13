import { expect, test, type Locator, type Page, type Request } from "@playwright/test"
import { risen } from "./risen"
import { mockSystemUsers, session } from "./system-users-fixture"

const write = (page: Page, match: (request: Request) => boolean) =>
  page.waitForRequest((request) => request.method() !== "GET" && match(request))

const rowsIn = (page: Page, region: string) =>
  page.getByRole("region", { name: region }).locator("[data-slot='row']")

const passwordRow = (page: Page) => rowsIn(page, "Sign-in").first()

const keyRows = (page: Page) => rowsIn(page, "Sign-in").filter({ hasText: "SHA256:" })

/** The rows of a list of things to open: accounts, groups. */
const cards = (page: Page) => page.locator("[data-slot='choice-row']")

const saveBar = (page: Page) => page.getByRole("region", { name: "Save changes" })

async function open(page: Page, path: string) {
  await mockSystemUsers(page)
  await page.goto(path)
  await page.waitForLoadState("networkidle")
}

const pathIs = (path: string) => (url: URL) =>
  url.pathname.replace(/\/$/, "") === `/api/v1${path}`.replace(/\/$/, "")

/** Answers one read with something else; writes and every other route fall through. */
async function answer(page: Page, path: string, body: unknown, status = 200) {
  await page.route(pathIs(path), (route) =>
    route.request().method() === "GET"
      ? route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
      : route.fallback(),
  )
}

/** Holds one read until `release` is called, so the page can be measured while it waits. */
async function hold(page: Page, path: string) {
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route(pathIs(path), async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    await gate
    return route.fallback()
  })
  return () => release()
}

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found as { x: number; y: number; width: number; height: number }
}

/**
 * Every row the same height, and each the same distance under the one before
 * it. `slack` is for a hairlined list, whose last row has no rule under it.
 */
async function expectOneHeight(rows: Locator, slack = 0.75) {
  const count = await rows.count()
  expect(count).toBeGreaterThan(1)
  const boxes = []
  for (let index = 0; index < count; index += 1) boxes.push(await box(rows.nth(index)))
  const height = boxes[0].height
  const pitch = boxes[1].y - boxes[0].y
  for (const [index, row] of boxes.entries()) {
    expect(Math.abs(row.height - height), `row ${index} is another height`).toBeLessThan(slack)
    if (index > 0) {
      expect(
        Math.abs(row.y - boxes[index - 1].y - pitch),
        `row ${index} is not where the pitch puts it`,
      ).toBeLessThan(0.75)
    }
  }
}

const noSidewaysScroll = (page: Page) =>
  page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)

const readOnly = {
  ...session,
  capabilities: ["read"],
  user: { ...session.user, role: "readonly" },
}
const refused = { error: { code: "forbidden", message: "your role does not permit this" } }

test.describe("the list", () => {
  test("opens on its identity line, a picture and rows, not on tiles or a table", async ({
    page,
  }) => {
    await open(page, "/system-users")
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
    await expect(page.getByRole("table")).toHaveCount(0)
    const line = page.locator("[data-slot='host-identity']")
    await expect(line).toHaveAttribute("data-layout", "project")
    await expect(line.locator("img[src*='ubuntu']")).toHaveCount(1)
    await expect(line).toContainText("4 can sign in")
    await expect(line.getByRole("link", { name: "New account" })).toHaveAttribute(
      "href",
      "/system-users/new",
    )
    await expect(page.getByRole("region", { name: "Who can be root" })).toBeVisible()
  })

  test("groups and defaults are the line's menu", async ({ page }) => {
    await open(page, "/system-users")
    await page.getByRole("button", { name: "Groups and defaults" }).click()
    await expect(page.getByRole("menuitem", { name: "Account defaults" })).toBeVisible()
    await page.getByRole("menuitem", { name: "Groups" }).click()
    await expect(page).toHaveURL(/\/system-users\/groups$/)
  })

  test("root is listed before the system switch is touched", async ({ page }) => {
    await open(page, "/system-users")
    const root = cards(page).first()
    await expect(root).toContainText("root")
    await expect(root).toContainText("uid 0")
    await expect(root).toContainText("Password")
    await expect(root).toContainText("Is root")
    await expect(root).toContainText("198.51.100.23")
    // It cannot be swept up in a selection.
    await expect(page.getByRole("checkbox", { name: "Select root" })).toHaveCount(0)
  })

  test("says what is wrong on this host, each with where to fix it", async ({ page }) => {
    await open(page, "/system-users")
    const findings = page.locator("[data-slot='account-findings']")
    await expect(findings).toContainText("mira signs in with no password")
    await expect(findings).toContainText("root signs in with a password")
    await expect(findings).toContainText("ubuntu can become root with no password")
    await expect(findings).toContainText("/etc/sudoers.d/90-cloud-init-users")
    await expect(findings).toContainText("ubuntu and zoro have no way in")
    await expect(findings.getByRole("link", { name: "Add a key" })).toHaveAttribute(
      "href",
      "/system-users/user/root",
    )
  })

  test("the picture draws every way to root, by how strong it is", async ({ page }) => {
    await open(page, "/system-users")
    const picture = page.getByRole("region", { name: "Who can be root" })
    const become = picture.getByRole("region", { name: "Become root" })
    await expect(become.locator("[data-node]")).toHaveCount(4)
    await expect(become.getByRole("link", { name: "Open ion" })).toHaveAttribute(
      "href",
      "/system-users/user/ion",
    )
    // `deploy` may run one command through sudo and is in docker: it is root.
    await expect(become.locator("[data-node='account:deploy']")).toContainText(
      "Root by another name",
    )
    await expect(become.locator("[data-node='account:ubuntu']")).toContainText("No password asked")
    await expect(become.locator("[data-node='account:zoro']")).toContainText(
      "Needs a password it lacks",
    )
    const direct = picture.getByRole("region", { name: "Sign in as root" })
    await expect(direct.locator("[data-node='root:password']")).toContainText("Signs in over SSH")
    await expect(direct.locator("[data-node='root:key']")).toContainText("It has no SSH keys.")
  })

  test("the two `sudo` accounts read differently, because they are", async ({ page }) => {
    await open(page, "/system-users")
    const ubuntu = cards(page).filter({ hasText: "uid 1000" })
    await expect(ubuntu).toContainText("No way in")
    await expect(ubuntu).toContainText("No password asked")
    await expect(ubuntu.locator("img[src='/logos/lxd.svg']")).toHaveCount(1)
    const zoro = cards(page).filter({ hasText: "uid 1001" })
    await expect(zoro).toContainText("Needs a password it lacks")
  })

  test("daemons are a shelf of their own, drawn as their products", async ({ page }) => {
    await open(page, "/system-users")
    await expect(cards(page)).toHaveCount(7)
    await page.getByRole("button", { name: "System accounts" }).click()
    await expect(cards(page)).toHaveCount(15)
    const shelf = page.getByRole("list", { name: "System accounts" })
    await expect(shelf.locator("[data-slot='choice-row']")).toHaveCount(8)
    const postgres = cards(page).filter({ hasText: "postgres" })
    await expect(postgres.locator("img[src*='postgresql']")).toHaveCount(1)
    await expect(postgres).toContainText("No login")
    await expect(page.getByRole("checkbox", { name: "Select postgres" })).toHaveCount(0)
  })

  test("a row opens the account, and a selection acts on all of it", async ({ page }) => {
    await open(page, "/system-users")
    await page.getByRole("checkbox", { name: "Select zoro" }).click()
    // A press on the box is not a press on the row.
    await expect(page).toHaveURL(/\/system-users$/)
    await page.getByRole("checkbox", { name: "Select ubuntu" }).click()
    const bar = page.locator("[data-slot='bulk-bar']")
    await expect(bar).toContainText("2 selected")
    await bar.getByRole("button", { name: "Disable" }).click()
    const sent = write(page, (r) => r.url().includes("/system-accounts/bulk"))
    await page.getByRole("dialog").getByRole("button", { name: "Disable" }).click()
    const body = (await sent).postDataJSON()
    expect(body.action).toBe("disable")
    expect(body.endSessions).toBe(true)
    expect(body.usernames.sort()).toEqual(["ubuntu", "zoro"])

    await cards(page).filter({ hasText: "uid 1002" }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/ion$/)
  })

  test("locking a selection sends one request however often it is pressed", async ({ page }) => {
    await open(page, "/system-users")
    let locks = 0
    await page.route(pathIs("/system-accounts/bulk"), async (route) => {
      locks += 1
      await new Promise((resolve) => setTimeout(resolve, 300))
      return route.fallback()
    })
    await page.getByRole("checkbox", { name: "Select zoro" }).click()
    const lock = page.locator("[data-slot='bulk-bar']").getByRole("button", {
      name: "Lock passwords",
    })
    await lock.click()
    await lock.click({ force: true }).catch(() => {})
    await expect(page.locator("[data-slot='bulk-bar']")).toHaveCount(0)
    expect(locks).toBe(1)
  })

  test("a row's menu goes to that account's views", async ({ page }) => {
    await open(page, "/system-users")
    await page.getByRole("button", { name: "More for ion", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Terminal" })).toBeVisible()
    await expect(page.getByRole("menuitem", { name: "Files" })).toBeVisible()
    await page.getByRole("menuitem", { name: "Settings" }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/ion\?tab=settings$/)
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
  })

  for (const width of [1280, 390]) {
    test(`every account is one height at ${width}, whatever it has`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await open(page, "/system-users")
      await risen(page)
      // root has no full name and no box, ubuntu never signed in, alex is disabled.
      await expectOneHeight(page.getByRole("list", { name: "Accounts" }).locator("> li"))
      await page.getByRole("button", { name: "System accounts" }).click()
      await expect(cards(page)).toHaveCount(15)
      await risen(page)
      await expectOneHeight(page.getByRole("list", { name: "System accounts" }).locator("> li"))
    })
  }

  test("the page has its shape while it waits, and nothing moves when the read lands", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    // Once through, so the page knows how tall its findings and its picture were.
    await open(page, "/system-users")
    await expect(cards(page)).toHaveCount(7)

    const release = await hold(page, "/system-users/")
    await page.goto("/system-users")
    const line = page.locator("[data-slot='host-identity']")
    const picture = page.getByRole("region", { name: "Who can be root" })
    const waiting = page.locator("[data-slot='choice-rows-waiting'] > li").first()
    await expect(waiting).toBeVisible()
    await risen(page)
    const before = {
      line: await box(line),
      picture: await box(picture),
      row: await box(waiting),
    }
    release()
    await expect(cards(page)).toHaveCount(7)
    await risen(page)
    const after = {
      line: await box(line),
      picture: await box(picture),
      row: await box(cards(page).first()),
    }
    expect(after.line.y).toBeCloseTo(before.line.y, 0)
    expect(after.line.height).toBeCloseTo(before.line.height, 0)
    expect(Math.abs(after.picture.y - before.picture.y)).toBeLessThan(2)
    expect(Math.abs(after.picture.height - before.picture.height)).toBeLessThan(2)
    expect(Math.abs(after.row.y - before.row.y)).toBeLessThan(2)
    expect(Math.abs(after.row.height - before.row.height)).toBeLessThan(2)
  })

  test("a read that failed can be asked for again", async ({ page }) => {
    await mockSystemUsers(page)
    let failing = true
    await page.route(pathIs("/system-users/"), (route) =>
      failing
        ? route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({ error: { code: "internal", message: "internal error" } }),
          })
        : route.fallback(),
    )
    await page.goto("/system-users")
    await expect(page.locator("[data-slot='error-state']")).toBeVisible()
    await expect(page.locator("[data-slot='host-identity']")).toContainText("Not read")
    failing = false
    await page.getByRole("button", { name: "Try again" }).click()
    await expect(cards(page)).toHaveCount(7)
  })

  test("nothing matching says so, with the way back to everyone", async ({ page }) => {
    await open(page, "/system-users")
    await page.getByRole("button", { name: /Disabled/ }).click()
    await expect(cards(page)).toHaveCount(1)
    await page.getByRole("button", { name: "System accounts" }).click()
    await expect(cards(page)).toHaveCount(1)
    await page.getByRole("button", { name: /Can sign in/ }).click()
    await page.getByRole("button", { name: "System accounts" }).click()
    await expect(cards(page)).toHaveCount(4)
  })
})

test.describe("an account", () => {
  test("the rail keeps System users lit inside it", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    await expect(page.getByRole("link", { name: "System users" }).first()).toHaveAttribute(
      "data-active",
      "true",
    )
  })

  test("opens on its line: a terminal as the command, the rest in the menu", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    const line = page.locator("[data-slot='host-identity']")
    await expect(line).toHaveAttribute("data-layout", "project")
    await expect(line).toContainText("Can sign in")
    await expect(line).toContainText("uid 1002")
    await line.getByRole("button", { name: "Terminal" }).click()
    await expect(page).toHaveURL(/\/terminal\?as=ion$/)
    await page.goBack()
    await page.getByRole("button", { name: "More for ion", exact: true }).click()
    for (const name of ["Files", "Disable", "End sessions", "Delete"]) {
      await expect(page.getByRole("menuitem", { name, exact: true })).toBeVisible()
    }
    await page.getByRole("menuitem", { name: "Files" }).click()
    await expect(page).toHaveURL(/\/files\?path=%2Fhome%2Fion$/)
  })

  test("its views are in the address", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    await expect(page.getByRole("tab", { name: "Overview" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/ion\?tab=settings$/)
    await expect(page.locator("[data-slot='account-settings']")).toBeVisible()
    await page.reload()
    await expect(page.getByRole("tab", { name: "Settings" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await page.getByRole("tab", { name: "History" }).click()
    await expect(page).toHaveURL(/\?tab=history$/)
    await page.getByRole("tab", { name: "Overview" }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/ion$/)
  })

  test("the accounts stand beside it, and the view goes with you", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/user/ion?tab=settings")
    const rail = page.locator("[data-slot='account-rail']")
    await expect(rail).toBeVisible()
    await expect(rail.locator("[data-workspace-item]")).toHaveCount(7)
    await expect(rail.locator("[aria-current='page']")).toHaveAttribute("aria-label", "ion")
    // Root leads, as it does on the list.
    await expect(rail.locator("[data-workspace-item]").first()).toHaveAttribute(
      "aria-label",
      "root",
    )
    await rail.getByRole("button", { name: "deploy", exact: true }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/deploy\?tab=settings$/)
    await expect(rail.locator("[aria-current='page']")).toHaveAttribute("aria-label", "deploy")
    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/system-users\/user\/ubuntu\?tab=settings$/)
    await page.getByRole("button", { name: "Previous account" }).click()
    await expect(page).toHaveURL(/\/system-users\/user\/deploy\?tab=settings$/)
  })

  test("the rail is narrowed by the list's own search", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/user/ion")
    const rail = page.locator("[data-slot='account-rail']")
    await rail.getByLabel("Search the accounts").fill("docker")
    await expect(rail.locator("[data-workspace-item]")).toHaveCount(2)
    await expect(rail).toContainText("2 of 7")
  })

  test("a bare `!` offers a password, never an Unlock that cannot work", async ({ page }) => {
    await open(page, "/system-users/user/ubuntu")
    const row = passwordRow(page)
    await expect(row).toContainText("None")
    await expect(row.getByRole("button", { name: "Set password" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Unlock" })).toHaveCount(0)
  })

  test("a password behind a `!` can really be unlocked", async ({ page }) => {
    await open(page, "/system-users/user/alex")
    const row = passwordRow(page)
    await expect(row).toContainText("Locked")
    const sent = write(page, (r) => r.url().endsWith("/system-users/alex"))
    await row.getByRole("button", { name: "Unlock" }).click()
    expect((await sent).postDataJSON()).toEqual({ locked: false })
  })

  test("setting a password sends it once and shows what it sent", async ({ page }) => {
    await open(page, "/system-users/user/zoro")
    await page.getByRole("button", { name: "Set password" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByLabel("Password", { exact: true }).fill("correct horse battery")
    const sent = write(page, (r) => r.url().endsWith("/zoro/password"))
    await dialog.getByRole("button", { name: "Set password" }).click()
    expect((await sent).postDataJSON()).toEqual({
      password: "correct horse battery",
      mustChange: false,
    })
  })

  test("keys are drawn as where they live, with what limits them and when they were used", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/user/ion")
    const keys = keyRows(page)
    await expect(keys).toHaveCount(4)
    const github = keys.filter({ hasText: "ion-dev" })
    await expect(github.locator("img[src='/logos/github.svg']")).toHaveCount(1)
    await expect(github).toContainText("100.101.4.12")
    await expect(keys.filter({ hasText: "ion@macbook" })).toContainText("Only from 100.64.0.0/10")
    const actions = keys.filter({ hasText: "github-actions" })
    await expect(actions).toContainText("No forwarding")
    await expect(actions).toContainText("One command")
    await expect(actions).toContainText("Expires 2027-01-01")
    const weak = keys.filter({ hasText: "old laptop" })
    await expect(weak).toContainText("Weak")
    await expect(weak).toContainText("too short to be safe")
  })

  for (const width of [1440, 390]) {
    test(`a key is one line at ${width}, whatever limits it`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await open(page, "/system-users/user/ion")
      await risen(page)
      // One has three restrictions, one is weak, one has nothing to say.
      await expectOneHeight(rowsIn(page, "Sign-in"), 1.25)
      await expect(keyRows(page).filter({ hasText: "github-actions" })).toContainText(
        "Expires 2027-01-01",
      )
    })
  }

  test("a generated private key is shown once and then is gone", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    await page.getByRole("button", { name: "Add key" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByRole("radio", { name: "Generate" }).click()
    await dialog.getByRole("button", { name: "Generate" }).click()
    const shown = page.locator("[data-slot='private-key-shown']")
    await expect(shown).toContainText("only time it is shown")
    await expect(shown.getByRole("textbox")).toHaveValue(/BEGIN OPENSSH PRIVATE KEY/)
    await page.getByRole("button", { name: "I have saved it" }).click()
    await expect(page.getByRole("dialog")).toHaveCount(0)
  })

  test("github import asks only for a username, under GitHub's mark", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    await page.getByRole("button", { name: "Add key" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog.locator("img[src='/logos/github.svg']")).toHaveCount(1)
    await dialog.getByRole("radio", { name: "GitHub" }).click()
    await dialog.getByLabel("GitHub username").fill("octocat")
    const sent = write(page, (r) => r.url().endsWith("/ion/keys/import"))
    await dialog.getByRole("button", { name: "Import" }).click()
    expect((await sent).postDataJSON()).toEqual({ githubUser: "octocat" })
  })

  test("root access names the file each rule is in, and what removes it", async ({ page }) => {
    await open(page, "/system-users/user/ubuntu")
    const routes = rowsIn(page, "Root access")
    const cloud = routes.filter({ hasText: "90-cloud-init-users" })
    await expect(cloud).toContainText("no password asked")
    await expect(cloud.getByRole("button", { name: "Remove" })).toBeVisible()
    const group = routes.filter({ hasText: "/etc/sudoers" }).filter({ hasText: "Through sudo" })
    await expect(group).toContainText("has no password")
    await expect(group.getByRole("button", { name: "Leave group" })).toBeVisible()
    await expect(
      routes.filter({ hasText: "lxd" }).locator("img[src='/logos/lxd.svg']"),
    ).toHaveCount(1)
  })

  test("the account's own sudo rule is one segmented choice", async ({ page }) => {
    await open(page, "/system-users/user/zoro")
    const sent = write(page, (r) => r.url().endsWith("/zoro/sudo"))
    await page.getByRole("radio", { name: "No password" }).click()
    expect((await sent).postDataJSON()).toEqual({ level: "nopassword", commands: [] })
  })

  test("a daemon's account is read-only until asked, and then says so to the server", async ({
    page,
  }) => {
    await open(page, "/system-users/user/postgres")
    // It has no shell, so its files are the command; nothing else is offered.
    const line = page.locator("[data-slot='host-identity']")
    await expect(line.getByRole("button", { name: "Files" })).toBeVisible()
    await expect(page.getByRole("button", { name: "More for postgres", exact: true })).toHaveCount(
      0,
    )
    await expect(page.getByRole("button", { name: "Add key" })).toHaveCount(0)
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page.getByLabel("Full name")).toBeDisabled()
    await expect(page.getByLabel("Password lasts")).toBeDisabled()

    await page.getByRole("button", { name: "Allow changes" }).click()
    await expect(page.getByLabel("Full name")).toBeEnabled()
    await page.getByRole("button", { name: "More for postgres", exact: true }).click()
    await page.getByRole("menuitem", { name: "Disable" }).click()
    const sent = write(page, (r) => r.url().includes("/system-users/postgres?"))
    await page.getByRole("dialog").getByRole("button", { name: "Disable" }).click()
    const request = await sent
    expect(new URL(request.url()).searchParams.get("allowSystem")).toBe("true")
    expect(request.postDataJSON()).toEqual({ disabled: true, endSessions: true })
  })

  test("root has no way to be deleted, disabled or locked from here", async ({ page }) => {
    await open(page, "/system-users/user/root")
    await page.getByRole("button", { name: "More for root", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Files" })).toBeVisible()
    for (const name of ["Disable", "End sessions", "Delete"]) {
      await expect(page.getByRole("menuitem", { name, exact: true })).toHaveCount(0)
    }
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "More for the password" }).click()
    await expect(page.getByRole("menuitem", { name: "Lock", exact: true })).toHaveCount(0)
  })

  test("the history is one line of time, narrowed by what happened", async ({ page }) => {
    await open(page, "/system-users/user/ion?tab=history")
    const rows = page.locator("[data-slot='history-row']")
    await expect(rows).toHaveCount(9)
    await expectOneHeight(rows, 1.25)
    await page.getByRole("button", { name: /Refused/ }).click()
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText("185.220.101.4")
    await page.getByRole("button", { name: /sudo/ }).click()
    await expect(rows).toHaveCount(3)
    await expect(rows.filter({ hasText: "3 incorrect password attempts" })).toHaveCount(1)
  })

  test("an account with nothing on record is drawn as itself, with one sentence", async ({
    page,
  }) => {
    await open(page, "/system-users/user/mira?tab=history")
    const empty = page.locator("[data-slot='empty-state']")
    await expect(empty).toContainText("Nothing on record for mira")
    await expect(empty.locator("[data-slot='initials-mark']")).toHaveCount(1)
    await expect(page.locator("[data-slot='history-row']")).toHaveCount(0)
  })

  test("sessions can be ended one at a time", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    const row = rowsIn(page, "Right now").filter({ hasText: "pts/1" })
    await row.getByRole("button", { name: "End" }).click()
    const sent = write(page, (r) => r.url().includes("/ion/sessions"))
    await page.getByRole("dialog").getByRole("button", { name: "End" }).click()
    expect(new URL((await sent).url()).searchParams.get("pid")).toBe("41220")
  })

  test("an idle account draws no Right now section at all", async ({ page }) => {
    await open(page, "/system-users/user/zoro")
    await expect(page.getByRole("region", { name: "Right now" })).toHaveCount(0)
  })

  test("an account that is not there says so, with the way back", async ({ page }) => {
    await open(page, "/system-users/user/nobody-here")
    await expect(page.locator("[data-slot='empty-state']")).toContainText(
      "No account named nobody-here on this host",
    )
    await expect(page.getByRole("link", { name: "Back to the accounts" })).toHaveAttribute(
      "href",
      "/system-users",
    )
  })

  test("its line is where it will be while the account is read", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockSystemUsers(page)
    const release = await hold(page, "/system-users/ion")
    await page.goto("/system-users/user/ion")
    const line = page.locator("[data-slot='host-identity']")
    await expect(line).toContainText("Reading ion")
    await risen(page)
    const before = await box(line)
    release()
    await expect(line).toContainText("uid 1002")
    await risen(page)
    const after = await box(line)
    expect(Math.abs(after.y - before.y)).toBeLessThan(1)
    expect(Math.abs(after.x - before.x)).toBeLessThan(1)
  })
})

test.describe("an account's settings", () => {
  test("are one column of sections under one bar", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/user/ion?tab=settings")
    const column = page.locator("[data-slot='account-settings']")
    for (const title of ["Profile", "Password rules and expiry", "Limits"]) {
      await expect(column.getByRole("heading", { name: title, exact: true })).toBeVisible()
    }
    await expect(page.locator("details")).toHaveCount(0)
    for (const name of ["Save profile", "Save rules", "Save limits"]) {
      await expect(page.getByRole("button", { name })).toHaveCount(0)
    }
    await expect(saveBar(page)).toHaveCount(0)
    const width = (await box(column)).width
    expect(width).toBeLessThanOrEqual(768)
  })

  test("two forms that hold an edit are two writes, each what it was", async ({ page }) => {
    await open(page, "/system-users/user/ion?tab=settings")
    await page.getByLabel("Full name").fill("Ion P.")
    await page.getByLabel("Memory").fill("4")
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    await expect(saveBar(page)).toContainText("Profile and Limits")
    const profile = write(
      page,
      (r) => r.method() === "PATCH" && r.url().endsWith("/system-users/ion"),
    )
    const limits = write(page, (r) => r.method() === "PUT" && r.url().endsWith("/ion/limits"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    expect((await profile).postDataJSON()).toEqual({ comment: "Ion P." })
    expect((await limits).postDataJSON()).toEqual({
      cpuPercent: 0,
      memoryBytes: 4 * 1024 ** 3,
      tasks: 0,
    })
  })

  test("the rules of an account that can sign in carry its expiry, as they did", async ({
    page,
  }) => {
    await open(page, "/system-users/user/ion?tab=settings")
    await page.getByLabel("Warn before it expires").fill("14")
    const sent = write(page, (r) => r.method() === "PATCH" && r.url().endsWith("/system-users/ion"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    expect((await sent).postDataJSON()).toEqual({
      expiresOn: "",
      aging: { maxDays: 180, minDays: 0, warnDays: 14, inactiveDays: -1 },
    })
  })

  test("saving the rules of a disabled account does not enable it", async ({ page }) => {
    await open(page, "/system-users/user/alex?tab=settings")
    // Its expiry is how the host keeps it disabled, so it is not this form's.
    await expect(page.getByLabel("Account expires")).toBeDisabled()
    await page.getByLabel("Warn before it expires").fill("14")
    const sent = write(
      page,
      (r) => r.method() === "PATCH" && r.url().endsWith("/system-users/alex"),
    )
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    const body = (await sent).postDataJSON()
    expect(body).not.toHaveProperty("expiresOn")
    expect(body.aging.warnDays).toBe(14)
  })

  test("an edit is still held, and its bar still up, while another view is looked at", async ({
    page,
  }) => {
    await open(page, "/system-users/user/ion?tab=settings")
    await page.getByLabel("Full name").fill("Ion P.")
    await page.getByRole("tab", { name: "Overview" }).click()
    await expect(page.locator("[data-slot='account-settings']")).toBeHidden()
    await expect(saveBar(page)).toContainText("1 unsaved change")
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page.getByLabel("Full name")).toHaveValue("Ion P.")
  })

  test("an emptied number holds the save rather than sending 0", async ({ page }) => {
    await open(page, "/system-users/user/ion?tab=settings")
    await page.getByLabel("Account number").fill("")
    await expect(page.getByText("A whole number, 0 or more.")).toBeVisible()
    await expect(saveBar(page).getByRole("button", { name: /Save/ })).toBeDisabled()
    await saveBar(page).getByRole("button", { name: "Discard" }).click()
    await expect(page.getByLabel("Account number")).toHaveValue("1002")
    await expect(saveBar(page)).toHaveCount(0)
  })
})

test.describe("deleting", () => {
  async function dialog(page: Page, name: string) {
    await open(page, `/system-users/user/${name}`)
    await page.getByRole("button", { name: `More for ${name}`, exact: true }).click()
    await page.getByRole("menuitem", { name: "Delete" }).click()
    return page.locator("[data-slot='account-removal']")
  }

  test("reads what would go before anything does", async ({ page }) => {
    const removal = await dialog(page, "ion")
    await expect(removal).toContainText("/home/ion")
    await expect(removal).toContainText("3.2 GB")
    await expect(removal).toContainText("12 processes")
    await expect(removal).toContainText("/srv/shop/uploads")
    await expect(removal).toContainText("It also owns files elsewhere")
  })

  test("removing a home takes the typed name; keeping it does not", async ({ page }) => {
    const removal = await dialog(page, "ion")
    const confirm = page.getByRole("button", { name: "Delete account" })
    await expect(confirm).toBeEnabled()
    await removal.getByRole("radio", { name: "Remove", exact: true }).click()
    await expect(confirm).toBeDisabled()
    await removal.getByLabel("Type ion to remove its home for good").fill("ion")
    await expect(confirm).toBeEnabled()
    const sent = write(
      page,
      (r) => r.method() === "DELETE" && r.url().includes("/system-users/ion"),
    )
    await confirm.click()
    const request = await sent
    expect(new URL(request.url()).searchParams.get("home")).toBe("delete")
    expect(request.headers()["x-confirm"]).toBe("ion")
  })

  test("keeping the home can hand its files to somebody", async ({ page }) => {
    const removal = await dialog(page, "ion")
    await removal.getByRole("combobox").click()
    await page.getByRole("option", { name: "deploy" }).click()
    const sent = write(page, (r) => r.method() === "DELETE")
    await page.getByRole("button", { name: "Delete account" }).click()
    const query = new URL((await sent).url()).searchParams
    expect(query.get("home")).toBe("keep")
    expect(query.get("transferTo")).toBe("deploy")
    expect(query.get("endSessions")).toBe("true")
  })
})

test("an account sharing another's number can only lose its name", async ({ page }) => {
  await mockSystemUsers(page)
  await page.route("**/api/v1/system-users/deploy/removal", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        username: "deploy",
        home: "/root",
        homeExists: true,
        homeBytes: 0,
        homeUnsafe: true,
        processes: 0,
        cronJobs: false,
        sshKeys: 0,
        sudoRules: [],
        elsewhere: [],
        elsewhereTruncated: false,
        sharesNumber: "root",
      }),
    }),
  )
  await page.goto("/system-users/user/deploy")
  await page.waitForLoadState("networkidle")
  await page.getByRole("button", { name: "More for deploy", exact: true }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const removal = page.locator("[data-slot='account-removal']")
  await expect(removal).toContainText("It has the same number as root")
  // Nothing here may be aimed at a number somebody else has.
  await expect(removal.getByRole("radio")).toHaveCount(0)
  const sent = write(page, (r) => r.method() === "DELETE")
  await page.getByRole("button", { name: "Delete account" }).click()
  const query = new URL((await sent).url()).searchParams
  expect(query.get("home")).toBe("keep")
  expect(query.get("endSessions")).toBe("false")
  expect(query.get("transferTo")).toBeNull()
})

test.describe("a new account", () => {
  test("the way in is the question, so it cannot make a zoro by accident", async ({ page }) => {
    await open(page, "/system-users/new")
    await expect(page.locator("[data-register='flow']")).toHaveCount(1)
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Who is this account for?")
    await page.getByLabel("Username").fill("zoro")
    const create = page.getByRole("button", { name: "Create account" })
    // A name is not enough: there is no key yet.
    await expect(create).toBeDisabled()
    await page.getByRole("radio", { name: "Nobody directly" }).click()
    await expect(create).toBeEnabled()
    const sent = write(page, (r) => r.url().endsWith("/system-users/"))
    await create.click()
    const body = (await sent).postDataJSON()
    expect(body.username).toBe("zoro")
    expect(body.noSignIn).toBe(true)
  })

  test("a template sets what it stands for, under real marks", async ({ page }) => {
    await open(page, "/system-users/new")
    const kinds = page.locator("[data-slot='choice-card']")
    await expect(kinds).toHaveCount(5)
    await expect(
      kinds.filter({ hasText: "Developer" }).locator("img[src='/logos/docker.svg']"),
    ).toHaveCount(1)
    await page.getByRole("button", { name: "Make a Administrator account" }).click()
    await expect(page.getByRole("radio", { name: "Asks for password" })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    // Its sudo asks for a password, so a password is the way in it starts with.
    await expect(page.getByRole("radio", { name: "Password", exact: true })).toHaveAttribute(
      "aria-checked",
      "true",
    )
    await expect(page.locator("#new-shell")).toContainText("/bin/bash")
    await page.getByRole("button", { name: "Make a Service account" }).click()
    await expect(page.getByText("How do they sign in?")).toHaveCount(0)
  })

  test("a username the host would refuse is said before the server is asked", async ({ page }) => {
    await open(page, "/system-users/new")
    await page.getByLabel("Username").fill("Bad Name")
    await expect(page.getByText("Lower-case letters")).toBeVisible()
    await page.getByRole("radio", { name: "Nobody directly" }).click()
    await expect(page.getByRole("button", { name: "Create account" })).toBeDisabled()
  })

  test("the picture beside the form says what the answers add up to", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await open(page, "/system-users/new")
    const picture = page.locator("[data-slot='new-account-picture']")
    const form = page.locator("[data-slot='flow-panel']")
    // One focused surface, and the picture in the column beside it.
    await expect(form).toHaveCount(1)
    const [formBox, pictureBox] = [await box(form), await box(picture)]
    expect(pictureBox.x).toBeGreaterThan(formBox.x + formBox.width)
    expect(Math.round(pictureBox.width)).toBe(352)

    await expect(picture).toContainText("An SSH key")
    await expect(picture).toContainText("not named yet")
    await expect(picture).toContainText("No root access")
    await page.getByLabel("Username").fill("mira")
    await expect(picture).toContainText("mira")
    await page.getByRole("radio", { name: "No password", exact: true }).click()
    await expect(picture).toContainText("Root, nothing asked")
    await page.getByRole("radio", { name: "None", exact: true }).click()
    await page.getByRole("checkbox", { name: "docker" }).click()
    await expect(picture).toContainText("Root by another name")
    await expect(picture).toContainText("through docker")
    await page.getByRole("radio", { name: "Nobody directly" }).click()
    await expect(picture).toContainText("Nobody directly")
    await page.getByRole("button", { name: "Make a Service account" }).click()
    await expect(picture).toContainText("a service's account has no login")
  })

  test("saving a template is beside the command, not instead of it", async ({ page }) => {
    await open(page, "/system-users/new")
    const actions = page.locator("[data-slot='flow-actions']")
    await expect(actions.getByRole("button", { name: "Create account" })).toBeVisible()
    await page.getByLabel("Username").fill("contractor")
    const sent = write(page, (r) => r.url().endsWith("/system-accounts/templates"))
    await actions.getByRole("button", { name: "Save as template" }).click()
    const body = (await sent).postDataJSON()
    expect(body).toHaveLength(1)
    expect(body[0]).toMatchObject({ id: "contractor", name: "contractor", builtin: false })
  })
})

test.describe("groups", () => {
  test("open on how many reach root, with New group as the command", async ({ page }) => {
    await open(page, "/system-users/groups")
    await expect(page.locator("[data-slot='stat-grid']")).toHaveCount(0)
    await expect(page.getByRole("table")).toHaveCount(0)
    const line = page.locator("[data-slot='host-identity']")
    await expect(line).toHaveAttribute("data-layout", "project")
    await expect(line).toContainText("3 reach root")
    await expect(line).toContainText("12 groups")
    await expect(line.getByRole("button", { name: "New group" })).toBeVisible()
  })

  test("what reaches root leads, and a private group is not on the list", async ({ page }) => {
    await open(page, "/system-users/groups")
    const rows = cards(page)
    await expect(rows).toHaveCount(4)
    await expect(
      rows.filter({ hasText: "docker" }).locator("img[src='/logos/docker.svg']"),
    ).toHaveCount(1)
    await expect(rows.first()).toContainText("Root")
    // `ion` the account's own group, not the member of `developers`.
    await expect(rows.filter({ hasText: "gid 1000" })).toHaveCount(0)
    await expect(rows.filter({ hasText: "developers" })).toContainText("mira")
  })

  test("the line's verdict narrows the rows to what reaches root", async ({ page }) => {
    await open(page, "/system-users/groups")
    await page.getByRole("button", { name: "Only the groups that reach root" }).click()
    await expect(cards(page)).toHaveCount(3)
    await expect(cards(page).filter({ hasText: "developers" })).toHaveCount(0)
    await page.getByRole("button", { name: /^All/ }).click()
    await expect(cards(page)).toHaveCount(4)
  })

  for (const width of [1280, 390]) {
    test(`every group is one height at ${width}, whoever is in it`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await open(page, "/system-users/groups")
      await page.getByRole("button", { name: "System groups" }).click()
      await expect(cards(page)).toHaveCount(12)
      await risen(page)
      // docker has a reach and two members, `ion` neither.
      await expectOneHeight(page.locator("[data-slot='choice-list'] > li"))
    })
  }

  test("the line and the first row are where they will be while the groups are read", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockSystemUsers(page)
    const release = await hold(page, "/system-accounts/groups")
    await page.goto("/system-users/groups")
    const line = page.locator("[data-slot='host-identity']")
    const waiting = page.locator("[data-slot='choice-rows-waiting'] > li").first()
    await expect(waiting).toBeVisible()
    await risen(page)
    const before = { line: await box(line), row: await box(waiting) }
    release()
    await expect(cards(page)).toHaveCount(4)
    await risen(page)
    const after = { line: await box(line), row: await box(cards(page).first()) }
    expect(Math.abs(after.line.y - before.line.y)).toBeLessThan(1)
    expect(Math.abs(after.line.height - before.line.height)).toBeLessThan(1)
    expect(Math.abs(after.row.y - before.row.y)).toBeLessThan(1)
    expect(Math.abs(after.row.height - before.row.height)).toBeLessThan(1)
  })

  test("a new group is a name, and opens on its members", async ({ page }) => {
    await open(page, "/system-users/groups")
    await page.getByRole("button", { name: "New group" }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByLabel("Name").fill("developers")
    const sent = write(page, (r) => r.url().endsWith("/system-accounts/groups"))
    await dialog.getByRole("button", { name: "Create" }).click()
    expect((await sent).postDataJSON()).toEqual({ name: "developers" })
    await expect(page).toHaveURL(/\/system-users\/groups\/developers\?tab=members$/)
  })

  test("a group with no package behind it can be deleted from its row", async ({ page }) => {
    await open(page, "/system-users/groups")
    // A package's group asks first, on its own page.
    await page.getByRole("button", { name: "More for docker", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
    await page.keyboard.press("Escape")
    await page.getByRole("button", { name: "More for developers", exact: true }).click()
    await page.getByRole("menuitem", { name: "Delete" }).click()
    const sent = write(
      page,
      (r) => r.method() === "DELETE" && r.url().endsWith("/system-accounts/groups/developers"),
    )
    await page.getByRole("dialog").getByRole("button", { name: "Delete" }).click()
    await sent
  })

  test("the address that named a group lands on that group's page", async ({ page }) => {
    await open(page, "/system-users/groups?group=developers")
    await expect(page).toHaveURL(/\/system-users\/groups\/developers$/)
    await expect(page.locator("[data-slot='host-identity']")).toContainText("developers")
  })
})

test.describe("a group", () => {
  test("is a page of its own: what it gives as a picture, and the rule that says so", async ({
    page,
  }) => {
    await open(page, "/system-users/groups")
    await cards(page).filter({ hasText: "sudo" }).click()
    await expect(page).toHaveURL(/\/system-users\/groups\/sudo$/)
    await expect(page.getByRole("dialog")).toHaveCount(0)
    const line = page.locator("[data-slot='host-identity']")
    await expect(line).toHaveAttribute("data-layout", "project")
    await expect(line).toContainText("Root, asks for a password")
    await expect(line).toContainText("gid 27")
    await expect(line).toContainText("the system depends on it")

    const picture = page.getByRole("region", { name: "What it gives" })
    const members = picture.getByRole("region", { name: "In it" })
    await expect(members.locator("[data-node]")).toHaveCount(3)
    await expect(members.getByRole("link", { name: "Open zoro" })).toHaveAttribute(
      "href",
      "/system-users/user/zoro",
    )
    await expect(picture.getByRole("region", { name: "Reaches" })).toContainText("Through sudo")
    const rule = page.locator("[data-slot='group-rule']")
    await expect(rule).toContainText("%sudo ALL=(ALL:ALL) ALL")
    await expect(rule).toContainText("/etc/sudoers:50")
  })

  test("a group that hands nothing over says so, and draws no rules", async ({ page }) => {
    await open(page, "/system-users/groups/developers")
    await expect(page.locator("[data-slot='host-identity']")).toContainText("Hands nothing over")
    await expect(page.getByRole("region", { name: "Reaches" })).toContainText(
      "Nothing beyond the files it owns.",
    )
    await expect(page.getByRole("region", { name: "Rules" })).toHaveCount(0)
  })

  test("its members are switches, staged until the page's Save", async ({ page }) => {
    await open(page, "/system-users/groups/developers?tab=members")
    await expect(page.getByRole("tab", { name: /Members/ })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    await expect(page.getByRole("switch", { name: "mira" })).toBeChecked()
    await expect(saveBar(page)).toHaveCount(0)
    await page.getByRole("switch", { name: "zoro" }).click()
    await expect(saveBar(page)).toContainText("1 unsaved change")
    const sent = write(page, (r) => r.url().includes("/system-accounts/groups/developers"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    const request = await sent
    expect(request.method()).toBe("PATCH")
    const body = request.postDataJSON()
    expect(Object.keys(body)).toEqual(["members"])
    expect(body.members.sort()).toEqual(["alex", "ion", "mira", "zoro"])
    expect(new URL(request.url()).searchParams.get("allowSystem")).toBeNull()
  })

  test("a member the people's list does not hold is shown, so it can be taken out", async ({
    page,
  }) => {
    await mockSystemUsers(page)
    await answer(page, "/system-accounts/groups", [
      {
        name: "developers",
        gid: 1100,
        members: ["ion", "www-data"],
        primary: ["caddy"],
        system: false,
        rootEquivalent: false,
        sudoRules: [],
        protected: false,
      },
    ])
    await page.goto("/system-users/groups/developers?tab=members")
    await expect(page.getByRole("switch", { name: "www-data" })).toBeChecked()
    // The account it is the main group of is in it by its own setting.
    const members = page.locator("[data-slot='group-members']")
    await expect(members).toContainText("caddy")
    await expect(page.getByRole("switch", { name: "caddy" })).toHaveCount(0)
    await expect(members.getByRole("link", { name: "its main group" })).toHaveAttribute(
      "href",
      "/system-users/user/caddy?tab=settings",
    )
  })

  test("its name and number are one write with its members, as they were", async ({ page }) => {
    await open(page, "/system-users/groups/developers?tab=members")
    await page.getByRole("switch", { name: "zoro" }).click()
    await page.getByRole("tab", { name: "Settings" }).click()
    await expect(page).toHaveURL(/\?tab=settings$/)
    await page.getByLabel("Name", { exact: true }).fill("devs")
    await page.getByLabel("Number", { exact: true }).fill("1200")
    await expect(saveBar(page)).toContainText("3 unsaved changes")
    const sent = write(page, (r) => r.url().includes("/system-accounts/groups/developers"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    const body = (await sent).postDataJSON()
    expect(body.members.sort()).toEqual(["alex", "ion", "mira", "zoro"])
    expect(body.gid).toBe(1200)
    expect(body.newName).toBe("devs")
    await expect(page).toHaveURL(/\/system-users\/groups\/devs\?tab=settings$/)
  })

  test("an emptied number holds the save rather than sending group 0", async ({ page }) => {
    await open(page, "/system-users/groups/developers?tab=settings")
    await page.getByLabel("Number", { exact: true }).fill("")
    await expect(page.getByText("A whole number, 0 or more.")).toBeVisible()
    await expect(saveBar(page).getByRole("button", { name: /Save/ })).toBeDisabled()
  })

  test("a package's group is held until its changes are allowed, and then says so", async ({
    page,
  }) => {
    await open(page, "/system-users/groups/docker?tab=settings")
    await expect(page.getByLabel("Name", { exact: true })).toBeDisabled()
    await expect(page.getByRole("button", { name: "Delete group" })).toBeDisabled()
    await expect(page.getByRole("button", { name: "More for docker", exact: true })).toHaveCount(0)
    await page.getByRole("button", { name: "Allow changes" }).click()
    await page.getByLabel("Number", { exact: true }).fill("990")
    const sent = write(page, (r) => r.url().includes("/system-accounts/groups/docker"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    const request = await sent
    expect(new URL(request.url()).searchParams.get("allowSystem")).toBe("true")
    expect(request.postDataJSON()).toEqual({ members: ["deploy", "ion"], gid: 990 })
  })

  test("a group the system depends on keeps its name, number and existence", async ({ page }) => {
    await open(page, "/system-users/groups/sudo?tab=settings")
    await expect(page.getByLabel("Name", { exact: true })).toBeDisabled()
    await expect(page.getByLabel("Number", { exact: true })).toBeDisabled()
    await expect(page.getByRole("button", { name: "Allow changes" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Delete group" })).toHaveCount(0)
  })

  test("deleting asks first, then goes back to the groups", async ({ page }) => {
    await open(page, "/system-users/groups/developers")
    await page.getByRole("button", { name: "More for developers", exact: true }).click()
    await page.getByRole("menuitem", { name: "Delete group" }).click()
    const dialog = page.getByRole("dialog")
    await expect(dialog).toContainText("Delete the group developers")
    const sent = write(
      page,
      (r) => r.method() === "DELETE" && r.url().includes("/system-accounts/groups/developers"),
    )
    await dialog.getByRole("button", { name: "Delete" }).click()
    await sent
    await expect(page).toHaveURL(/\/system-users\/groups$/)
  })

  test("the groups stand beside it, and the view goes with you", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/groups/docker?tab=members")
    const rail = page.locator("[data-slot='group-rail']")
    await expect(rail).toBeVisible()
    await expect(rail.locator("[data-workspace-item]")).toHaveCount(4)
    await expect(rail.locator("[aria-current='page']")).toHaveAttribute("aria-label", "docker")
    await page.keyboard.press("Alt+ArrowDown")
    await expect(page).toHaveURL(/\/system-users\/groups\/lxd\?tab=members$/)
    await rail.getByRole("button", { name: "developers", exact: true }).click()
    await expect(page).toHaveURL(/\/system-users\/groups\/developers\?tab=members$/)
    await page.getByRole("button", { name: "Previous group" }).click()
    await expect(page).toHaveURL(/\/system-users\/groups\/sudo\?tab=members$/)
    // The switch the list has for what packages made is the rail's too.
    await rail.getByRole("button", { name: "System groups" }).click()
    await expect(rail.locator("[data-workspace-item]")).toHaveCount(12)
  })

  test("a group opened from outside the list is pinned over it", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/groups/adm")
    const rail = page.locator("[data-slot='group-rail']")
    await expect(rail.locator("[data-workspace-item]")).toHaveCount(5)
    await expect(rail.locator("[data-workspace-item]").first()).toHaveAttribute("aria-label", "adm")
    await expect(page.locator("[data-slot='host-identity']")).toContainText("Reads the system logs")
  })

  test("a group that is not there says so, with the way back", async ({ page }) => {
    await open(page, "/system-users/groups/nothing")
    await expect(page.locator("[data-slot='empty-state']")).toContainText(
      "No group named nothing on this host",
    )
    await expect(page.getByRole("link", { name: "Back to the groups" })).toHaveAttribute(
      "href",
      "/system-users/groups",
    )
  })
})

test.describe("account defaults", () => {
  test("are one centred column whose heads name the file each writes", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await open(page, "/system-users/defaults")
    await expect(page.locator("[data-slot='host-identity']")).toContainText(
      "passwords never expire",
    )
    const sections = page.locator("[data-slot='setting']")
    await expect(sections.filter({ hasText: "New accounts" })).toContainText("/etc/default/useradd")
    const passwords = sections.filter({ hasText: "Passwords" })
    await expect(passwords).toContainText("/etc/login.defs")
    await expect(passwords).toContainText("/etc/security/pwquality.conf")
    await expect(page.locator("[data-slot='skel']")).toContainText(".bashrc")
    await expect(page.getByRole("link", { name: "/etc/skel" })).toHaveAttribute(
      "href",
      "/files?path=%2Fetc%2Fskel",
    )
    // No Save of its own, and no sentence under a field.
    await expect(page.getByRole("button", { name: "Save defaults" })).toHaveCount(0)
    await expect(page.getByText("Empty is forever", { exact: false })).toHaveCount(0)

    const form = page.getByRole("form", { name: "Account defaults" })
    const [formBox, pageBox] = [await box(form), await box(page.locator("[data-slot='page']"))]
    expect(formBox.width).toBeLessThanOrEqual(768)
    const left = formBox.x - pageBox.x
    const right = pageBox.x + pageBox.width - (formBox.x + formBox.width)
    expect(Math.abs(left - right)).toBeLessThan(2)
  })

  test("save together, from the one bar, what they always sent", async ({ page }) => {
    await open(page, "/system-users/defaults")
    await expect(saveBar(page)).toHaveCount(0)
    await page.getByLabel("Last for").fill("180")
    await page.getByRole("radio", { name: "3", exact: true }).click()
    await expect(saveBar(page)).toContainText("2 unsaved changes")
    const sent = write(page, (r) => r.url().endsWith("/system-accounts/defaults"))
    await saveBar(page).getByRole("button", { name: /Save/ }).click()
    const request = await sent
    expect(request.method()).toBe("PUT")
    expect(request.postDataJSON()).toEqual({
      shell: "/bin/bash",
      passwordMaxDays: 180,
      passwordMinDays: 0,
      passwordWarnDays: 7,
      inactiveDays: -1,
      minLength: 8,
      minClasses: 3,
    })
  })

  test("an emptied length holds the save rather than sending 0", async ({ page }) => {
    await open(page, "/system-users/defaults")
    await page.getByLabel("Shortest allowed").fill("")
    await expect(page.getByText("Between 6 and 128 characters.")).toBeVisible()
    await expect(saveBar(page).getByRole("button", { name: /Save/ })).toBeDisabled()
    await saveBar(page).getByRole("button", { name: "Discard" }).click()
    await expect(page.getByLabel("Shortest allowed")).toHaveValue("8")
  })

  test("the form is where it will be while the defaults are read", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 })
    await mockSystemUsers(page)
    const release = await hold(page, "/system-accounts/defaults")
    await page.goto("/system-users/defaults")
    const line = page.locator("[data-slot='host-identity']")
    const passwords = page.getByRole("heading", { name: "Passwords" })
    const box3 = page.getByLabel("Warn before expiry")
    await expect(box3).toBeDisabled()
    await risen(page)
    const before = { line: await box(line), head: await box(passwords), field: await box(box3) }
    release()
    await expect(box3).toBeEnabled()
    await expect(box3).toHaveValue("7")
    await risen(page)
    const after = { line: await box(line), head: await box(passwords), field: await box(box3) }
    expect(Math.abs(after.line.y - before.line.y)).toBeLessThan(1)
    expect(Math.abs(after.head.y - before.head.y)).toBeLessThan(1)
    expect(Math.abs(after.field.y - before.field.y)).toBeLessThan(1)
  })

  test("the operator's own templates are one line each, with the one thing to do", async ({
    page,
  }) => {
    await mockSystemUsers(page)
    const own = [
      {
        id: "contractor",
        name: "Contractor",
        builtin: false,
        shell: "/bin/bash",
        groups: ["developers"],
        sudo: "none",
        system: false,
        noHome: false,
      },
      {
        id: "ci",
        name: "CI runner",
        builtin: false,
        shell: "/bin/sh",
        groups: [],
        sudo: "none",
        system: false,
        noHome: false,
      },
    ]
    await answer(page, "/system-accounts/templates", own)
    await page.goto("/system-users/defaults")
    const rows = page.locator("[data-slot='templates'] > li")
    await expect(rows).toHaveCount(2)
    await expectOneHeight(rows)
    await page.getByRole("button", { name: "Remove Contractor" }).click()
    const sent = write(page, (r) => r.url().endsWith("/system-accounts/templates"))
    await page.getByRole("dialog").getByRole("button", { name: "Remove" }).click()
    expect((await sent).postDataJSON()).toEqual([own[1]])
  })
})

test.describe("without the administrator's role", () => {
  for (const path of [
    "/system-users",
    "/system-users/user/ion",
    "/system-users/new",
    "/system-users/groups",
    "/system-users/groups/docker",
    "/system-users/defaults",
  ]) {
    test(`${path} is a closed door, not a page of controls that fail`, async ({ page }) => {
      await mockSystemUsers(page)
      await answer(page, "/auth/session", readOnly)
      for (const read of [
        "/system-users/",
        "/system-accounts/groups",
        "/system-accounts/defaults",
      ]) {
        await answer(page, read, refused, 403)
      }
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const door = page.locator("[data-slot='empty-state']")
      await expect(door).toContainText("The host's accounts are an administrator's to read")
      await expect(door.getByRole("link", { name: "Back to the overview" })).toBeVisible()
      await expect(page.locator("[data-slot='error-state']")).toHaveCount(0)
      const main = page.locator("[data-slot='page']")
      await expect(main.getByRole("button")).toHaveCount(0)
      await expect(main.getByRole("textbox")).toHaveCount(0)
      await expect(main.getByRole("switch")).toHaveCount(0)
      await expect(main.getByRole("checkbox")).toHaveCount(0)
    })
  }

  test("a refusal that arrives on the read itself is the same door", async ({ page }) => {
    // The session still says administrator; the server has since changed its mind.
    await mockSystemUsers(page)
    await answer(page, "/system-users/", refused, 403)
    await page.goto("/system-users")
    await expect(page.locator("[data-slot='empty-state']")).toContainText(
      "The host's accounts are an administrator's to read",
    )
    await expect(page.locator("[data-slot='error-state']")).toHaveCount(0)
  })
})

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("an account's readings are under its name, none of them cut off", async ({ page }) => {
    await open(page, "/system-users")
    await expect(page.getByRole("columnheader")).toHaveCount(0)
    const ion = cards(page).filter({ hasText: "uid 1002" })
    await ion.scrollIntoViewIfNeeded()
    await expect(ion).toContainText("Asks for password")
    await expect(ion.getByText("Asks for password")).toBeInViewport()
    await expect(ion.locator("img[src='/logos/docker.svg']")).toBeInViewport()
    for (const slot of ["account-sign-in", "account-root"]) {
      const cut = await ion
        .locator(`[data-slot='${slot}']`)
        .evaluate((el) => el.scrollWidth > el.clientWidth + 1)
      expect(cut, `${slot} is cut off`).toBe(false)
    }
  })

  test("an account is one pane: its line, its views, no column of siblings", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    await expect(page.locator("[data-slot='account-rail']")).toBeHidden()
    await expect(page.getByRole("tab", { name: "History" })).toBeInViewport()
    await expect(page.getByRole("button", { name: "Next account" })).toBeVisible()
    await page.getByRole("tab", { name: "Settings" }).click()
    await page.getByLabel("Full name").fill("Ion P.")
    await expect(saveBar(page)).toBeInViewport()
  })

  test("a group is one pane, with its picture's words kept", async ({ page }) => {
    await open(page, "/system-users/groups/sudo")
    await expect(page.locator("[data-slot='group-rail']")).toBeHidden()
    const picture = page.getByRole("region", { name: "What it gives" })
    await expect(picture.getByText("zoro")).toBeVisible()
    await expect(picture.getByText("Root, asks for a password")).toBeVisible()
  })

  test("a group's reach takes the line under its name", async ({ page }) => {
    await open(page, "/system-users/groups")
    const docker = cards(page).filter({ hasText: "docker" })
    await expect(docker.getByText("Root by another name")).toBeInViewport()
    await expect(docker.locator("[data-slot='group-gives']")).toHaveCount(0)
  })

  for (const path of [
    "/system-users",
    "/system-users/user/ion",
    "/system-users/user/ion?tab=settings",
    "/system-users/user/ion?tab=history",
    "/system-users/new",
    "/system-users/groups",
    "/system-users/groups/docker",
    "/system-users/groups/developers?tab=members",
    "/system-users/groups/developers?tab=settings",
    "/system-users/defaults",
  ]) {
    test(`${path} does not scroll sideways`, async ({ page }) => {
      await open(page, path)
      expect(await noSidewaysScroll(page)).toBe(false)
    })
  }
})
