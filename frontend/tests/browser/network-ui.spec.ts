import { expect, test, type Page } from "@playwright/test"
import { mockNetwork, type Mutation } from "./network-fixture"

/**
 * The Network section against a mocked API (`network-fixture.ts`): every page
 * renders its readings from the shapes `netx` writes, the forms send what the
 * routes take, and the design system's structural rules hold. Nothing here
 * needs a network to change: the checks are about what the pages do with the
 * answers, which is the part a Go test cannot see.
 */

const PAGES = [
  "/network",
  "/network/interfaces",
  "/network/routing",
  "/network/vpn",
  "/network/firewall",
  "/network/connections",
  "/network/tools",
]

/**
 * Review screenshots, for the eyes the checks do not have. Written only when
 * asked for, into the directory named — never into the tree.
 */
test.describe("screenshots", () => {
  const dir = process.env.JD_NETWORK_SHOTS
  test.skip(!dir, "set JD_NETWORK_SHOTS to a directory to capture them")

  for (const width of [390, 1440]) {
    test.describe(`${width} wide`, () => {
      test.use({ viewport: { width, height: 1000 } })
      for (const path of PAGES) {
        test(`${path} at ${width}`, async ({ page }) => {
          await mockNetwork(page)
          await page.goto(path)
          await page.waitForLoadState("networkidle")
          await page.waitForTimeout(1500)
          const name = path.replace(/^\/network\/?/, "") || "overview"
          await page.screenshot({ path: `${dir}/${name}-${width}.png`, fullPage: true })
          for (let part = 1; part <= 6; part++) {
            const moved = await page.locator("[data-slot=page]").evaluate((element) => {
              let parent = element.parentElement
              while (parent && !/auto|scroll/.test(getComputedStyle(parent).overflowY)) {
                parent = parent.parentElement
              }
              if (!parent || parent.scrollTop + parent.clientHeight >= parent.scrollHeight - 1)
                return false
              parent.scrollTop += parent.clientHeight - 80
              return true
            })
            if (!moved) break
            await page.waitForTimeout(300)
            await page.screenshot({ path: `${dir}/${name}-${width}-scroll-${part}.png` })
          }
        })
      }
    })
  }
})

/** Every visible button with no text of its own and no name from anywhere else. */
function unnamedControls(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null) continue
      if ((el.textContent ?? "").trim()) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
}

/** Fully rounded, filled labels: the pill the design system removed (§4). */
function filledPills(page: Page) {
  return page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("span, div")) {
      if (el.dataset.slot === "user-avatar") continue
      const s = getComputedStyle(el)
      const h = el.getBoundingClientRect().height
      if (!h || h > 32 || parseFloat(s.borderTopLeftRadius) < h / 2) continue
      const filled = s.backgroundColor !== "rgba(0, 0, 0, 0)" && s.backgroundColor !== "transparent"
      if (filled && (el.textContent ?? "").trim()) bad.push(el.outerHTML.slice(0, 140))
    }
    return bad
  })
}

/** A framed panel that is not a table: everything but a table stays plain (§2). */
function framedNonTables(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("[data-slot=page] [data-slot=panel]:not([data-plain])"))
      .filter((el) => !el.querySelector("[data-slot=table-container]"))
      .map((el) => el.outerHTML.slice(0, 120)),
  )
}

for (const path of PAGES) {
  test(`${path} keeps the design system's structural rules`, async ({ page }) => {
    await mockNetwork(page)
    await page.goto(path)
    await page.waitForLoadState("networkidle")
    await page.waitForSelector("[data-slot=page]")
    expect(await unnamedControls(page), "icon-only controls without a name").toEqual([])
    expect(await filledPills(page), "filled pills").toEqual([])
    expect(await framedNonTables(page), "a framed block that is not a table").toEqual([])
  })
}

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } })
  for (const path of PAGES) {
    test(`${path} never scrolls sideways`, async ({ page }) => {
      await mockNetwork(page)
      await page.goto(path)
      await page.waitForLoadState("networkidle")
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow, `horizontal overflow on ${path}`).toBeLessThanOrEqual(0)
    })
  }
})

test("the old Security addresses lead to the Network pages, keeping their questions", async ({
  page,
}) => {
  await mockNetwork(page)
  await page.goto("/security/tools?tool=dns&target=example.com")
  await expect(page).toHaveURL(/\/network\/tools\?tool=dns&target=example\.com$/)
  await page.goto("/security/network")
  await expect(page).toHaveURL(/\/network\/interfaces$/)
  await page.goto("/security/connections")
  await expect(page).toHaveURL(/\/network\/connections$/)
})

test("the overview draws the topology, the readings and the attention list", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network")
  const picture = page.getByRole("region", { name: "Outside" })
  await expect(picture.getByText("The internet")).toBeVisible()
  await expect(picture.getByText("Tailnet")).toBeVisible()
  await expect(picture.getByText("4 of 7 online")).toBeVisible()
  await expect(page.getByRole("region", { name: "Inside" }).getByText("web")).toBeVisible()
  await expect(page.getByText("7 changes kept for boot")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Attention" })).toBeVisible()
  // A finding opens the page that fixes it.
  await page.getByRole("button", { name: /ens3 dropped or failed/ }).click()
  await page.getByRole("button", { name: "Open", exact: true }).click()
  await expect(page).toHaveURL(/\/network\/interfaces$/)
})

test("a device opens its sheet, and only what may change offers to", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "Open ens3" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("carries the default route")).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Set down" })).toBeDisabled()
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Open jd-lab" }).click()
  await expect(page.getByRole("dialog").getByRole("button", { name: "Delete" })).toBeEnabled()
})

test("a new VLAN sends exactly what the create route takes", async ({ page }) => {
  const mutations: Mutation[] = []
  await mockNetwork(page, mutations)
  await page.goto("/network/interfaces")
  await page.getByRole("button", { name: "New device" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Make a VLAN" }).click()
  await dialog.getByRole("combobox", { name: "Rides on" }).click()
  await page.getByRole("option", { name: /ens3/ }).click()
  await dialog.getByRole("textbox", { name: "VLAN id" }).fill("40")
  await dialog.getByRole("textbox", { name: "Address" }).fill("10.40.0.1/24")
  await dialog.getByRole("button", { name: "Create vlan40" }).click()
  await expect.poll(() => mutations.length).toBe(1)
  expect(mutations[0]).toEqual({
    method: "POST",
    path: "/network/links",
    body: {
      name: "vlan40",
      kind: "vlan",
      up: true,
      parent: "ens3",
      vlanId: 40,
      addresses: ["10.40.0.1/24"],
    },
  })
})

test("the routing page lights the rule that answers this browser", async ({ page }) => {
  await mockNetwork(page)
  await page.goto("/network/routing")
  await expect(page.getByText("5270 · your replies")).toBeVisible()
  await expect(page.getByText("table 52 · answers you")).toBeVisible()
  // Forwarding needed by something cannot be switched off from here.
  await expect(page.getByRole("switch", { name: "IPv4 forwarding" })).toBeDisabled()
})

test("a peer opens its sheet and shows its QR code again", async ({ page }) => {
  await mockNetwork(page, [], {
    overrides: {
      "/network/vpn/wireguard/wg0/peers/1/config": {
        name: "Ana's phone",
        config: "[Interface]\nPrivateKey = redacted\n",
        qr: "data:image/png;base64,iVBORw0KGgo=",
      },
    },
  })
  await page.goto("/network/vpn")
  await page.getByRole("button", { name: "Open Ana's phone" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Show QR code" }).click()
  await expect(page.getByRole("img", { name: /QR code for Ana's phone/ })).toBeVisible()
})

test("every control in the forms has a name", async ({ page }) => {
  await mockNetwork(page)
  const opened = [
    { path: "/network/interfaces", button: "New device" },
    { path: "/network/interfaces", button: "New namespace" },
    { path: "/network/routing", button: "Add route" },
    { path: "/network/routing", button: "Add rule" },
    { path: "/network/vpn", button: "Add a device" },
  ]
  for (const { path, button } of opened) {
    await page.goto(path)
    await page.getByRole("button", { name: button, exact: true }).first().click()
    await expect(page.getByRole("dialog")).toBeVisible()
    expect(await unnamedControls(page), `unnamed controls in ${button}`).toEqual([])
    await page.keyboard.press("Escape")
  }
})
