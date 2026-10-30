import { expect, test, type Page, type Request } from "@playwright/test"
import { mockSystemUsers } from "./system-users-fixture"

const write = (page: Page, match: (request: Request) => boolean) =>
  page.waitForRequest((request) => request.method() !== "GET" && match(request))

const rowsIn = (page: Page, region: string) =>
  page.getByRole("region", { name: region }).locator("[data-slot='row']")

const passwordRow = (page: Page) => rowsIn(page, "Sign-in").first()

const keyRows = (page: Page) => rowsIn(page, "Sign-in").filter({ hasText: "SHA256:" })

async function open(page: Page, path: string) {
  await mockSystemUsers(page)
  await page.goto(path)
  await page.waitForLoadState("networkidle")
}

test.describe("the list", () => {
  test("opens on a table with named columns, not on report blocks", async ({ page }) => {
    await open(page, "/system-users")
    await expect(page.locator("[data-slot='access-band']")).toHaveCount(0)
    await expect(page.locator("[data-slot='stat-tile']")).toHaveCount(0)
    for (const name of ["Account", "Sign-in", "Root access", "Last sign-in"]) {
      await expect(page.getByRole("columnheader", { name, exact: true })).toBeVisible()
    }
    await expect(page.locator("[data-slot='host-identity'] img[src*='ubuntu']")).toHaveCount(1)
  })

  test("root is listed before the system switch is touched", async ({ page }) => {
    await open(page, "/system-users")
    const root = page.locator("[data-slot='account-row']").filter({ hasText: "root" }).first()
    await expect(root).toContainText("Password")
    await expect(root).toContainText("Is root")
    await expect(root).toContainText("198.51.100.23")
    // It leads, as the account everything else is measured against.
    await expect(page.locator("[data-slot='account-row']").first()).toContainText("root")
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

  test("the two `sudo` accounts read differently, because they are", async ({ page }) => {
    await open(page, "/system-users")
    const rows = page.locator("[data-slot='account-row']")
    const ubuntu = rows.filter({ hasText: "ubuntu" })
    await expect(ubuntu).toContainText("No way in")
    await expect(ubuntu).toContainText("No password asked")
    await expect(ubuntu.locator("img[src='/logos/lxd.svg']")).toHaveCount(1)
    const zoro = rows.filter({ hasText: "zoro" })
    await expect(zoro).toContainText("Needs a password it lacks")
  })

  test("daemons are a shelf of their own, drawn as their products", async ({ page }) => {
    await open(page, "/system-users")
    await expect(page.locator("[data-slot='account-row']")).toHaveCount(7)
    await page.getByRole("button", { name: "System accounts" }).click()
    await expect(page.locator("[data-slot='account-row']")).toHaveCount(15)
    const postgres = page.locator("[data-slot='account-row']").filter({ hasText: "postgres" })
    await expect(postgres.locator("img[src*='postgresql']")).toHaveCount(1)
    await expect(postgres).toContainText("No login")
    await expect(page.getByRole("checkbox", { name: "Select postgres" })).toHaveCount(0)
  })

  test("a row opens the account, and a selection acts on all of it", async ({ page }) => {
    await open(page, "/system-users")
    await page.getByRole("checkbox", { name: "Select zoro" }).click()
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

    await page.locator("[data-slot='account-row']").filter({ hasText: "ion" }).first().click()
    await expect(page).toHaveURL(/\/system-users\/user\/ion$/)
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
    await expect(page.getByRole("button", { name: "Disable" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "Add key" })).toHaveCount(0)
    await expect(page.getByText("Profile", { exact: true })).toHaveCount(0)
    await page.getByRole("button", { name: "Allow changes" }).click()
    await page.getByRole("button", { name: "Disable" }).click()
    const sent = write(page, (r) => r.url().includes("/system-users/postgres?"))
    await page.getByRole("dialog").getByRole("button", { name: "Disable" }).click()
    const request = await sent
    expect(new URL(request.url()).searchParams.get("allowSystem")).toBe("true")
    expect(request.postDataJSON()).toEqual({ disabled: true, endSessions: true })
  })

  test("root has no way to be deleted, disabled or locked from here", async ({ page }) => {
    await open(page, "/system-users/user/root")
    await expect(page.getByRole("button", { name: "Disable" })).toHaveCount(0)
    await expect(page.getByRole("button", { name: "More for root", exact: true })).toHaveCount(0)
  })

  test("the history is one line of time, narrowed by what happened", async ({ page }) => {
    await open(page, "/system-users/user/ion")
    const rows = page.locator("[data-slot='history-row']")
    await expect(rows).toHaveCount(9)
    await page.getByRole("button", { name: /Refused/ }).click()
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText("185.220.101.4")
    await page.getByRole("button", { name: /sudo/ }).click()
    await expect(rows).toHaveCount(3)
    await expect(rows.filter({ hasText: "3 incorrect password attempts" })).toHaveCount(1)
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
    const cards = page.locator("[data-slot='choice-card']")
    await expect(cards).toHaveCount(5)
    await expect(
      cards.filter({ hasText: "Developer" }).locator("img[src='/logos/docker.svg']"),
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
})

test.describe("groups", () => {
  test("what reaches root leads, and a private group is not on the list", async ({ page }) => {
    await open(page, "/system-users/groups")
    const rows = page.locator("[data-slot='group-row']")
    await expect(
      rows.filter({ hasText: "docker" }).locator("img[src='/logos/docker.svg']"),
    ).toHaveCount(1)
    await expect(rows.first()).toContainText("Root")
    // `ion` the account's own group, not the member of `developers`.
    await expect(rows.filter({ hasText: "gid 1000" })).toHaveCount(0)
    await expect(rows.filter({ hasText: "developers" })).toContainText("mira")
  })

  test("a group's members are edited in its sheet", async ({ page }) => {
    await open(page, "/system-users/groups")
    await page.locator("[data-slot='group-row']").filter({ hasText: "developers" }).click()
    const sheet = page.getByRole("dialog")
    await sheet.getByRole("switch", { name: "zoro" }).click()
    const sent = write(page, (r) => r.url().includes("/system-accounts/groups/developers"))
    await sheet.getByRole("button", { name: "Save" }).click()
    expect((await sent).postDataJSON().members.sort()).toEqual(["alex", "ion", "mira", "zoro"])
  })
})

test("defaults say which file each writes and save together", async ({ page }) => {
  await open(page, "/system-users/defaults")
  await expect(page.getByText("/etc/default/useradd")).toBeVisible()
  await expect(page.getByText(".bashrc", { exact: false })).toBeVisible()
  await page.getByLabel("Last for").fill("180")
  const sent = write(page, (r) => r.url().endsWith("/system-accounts/defaults"))
  await page.getByRole("button", { name: "Save defaults" }).click()
  const body = (await sent).postDataJSON()
  expect(body.passwordMaxDays).toBe(180)
  expect(body.minLength).toBe(8)
})

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test("an account's readings stack under its name, none of them cut off", async ({ page }) => {
    await open(page, "/system-users")
    await expect(page.getByRole("columnheader")).toHaveCount(0)
    const ion = page.locator("[data-slot='account-row']").filter({ hasText: "ion" }).first()
    await expect(ion).toContainText("Asks for password")
    await expect(ion.getByText("Asks for password")).toBeInViewport()
    await expect(ion.locator("img[src='/logos/docker.svg']")).toBeInViewport()
  })

  for (const path of [
    "/system-users",
    "/system-users/user/ion",
    "/system-users/new",
    "/system-users/groups",
    "/system-users/defaults",
  ]) {
    test(`${path} does not scroll sideways`, async ({ page }) => {
      await open(page, path)
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow).toBe(false)
    })
  }
})
