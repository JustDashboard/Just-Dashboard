import { expect, test, type Locator, type Page, type Route } from "@playwright/test"
import { mockDatabases } from "./database-fixture"
import { mockImages } from "./docker-images-fixture"
import { admin, json as networkJson, mockNetwork } from "./network-fixture"
import { mockShowcase } from "./proxy-fixtures"
import { risen } from "./risen"

/**
 * The small things a thumb presses, on the pages the probes measured them on
 * (0.9.0, WP-38): the database switcher and the heads of a database's blocks,
 * the Images band's marks, the tool panel's earlier runs, Git's group
 * commands, Terminal's tab strip and the proxy's two bands.
 *
 * A target is the box a press lands in, not the box that is drawn: a control
 * that draws 16px and carries a `hit-area` is a 44px target under a finger.
 * So each one is measured as its own box grown by an absolutely placed
 * `::before` or `::after`, and its `<label>`, then cut to every ancestor that
 * clips what is outside it (an overflow other than visible), because a press
 * outside the clip lands on whatever is under it instead.
 *
 * A press box reaching past its line must not make a scroller scroll: a
 * strip of tabs that moves sideways a few pixels, or a title bar that scrolls
 * up and down under a thumb, is the cost of a press box with no room. Each
 * route also checks that no scroller is bigger with the press boxes than
 * without them.
 */

type Small = { what: string; width: number; height: number }

const PRESSABLE =
  'a[href], button, [role="button"], [role="tab"], [role="menuitem"], [role="checkbox"], [role="switch"], [role="radio"], [role="combobox"], input:not([type="hidden"]), select, textarea, summary'

/** The targets in `scope` whose press box is under `min` on either side. */
function smallTargets(scope: Locator, min = 24): Promise<Small[]> {
  return scope.evaluateAll(
    (roots, { min, selector }) => {
      type Box = { left: number; top: number; right: number; bottom: number }
      const px = (value: string) => parseFloat(value) || 0
      const union = (a: Box, b: Box): Box => ({
        left: Math.min(a.left, b.left),
        top: Math.min(a.top, b.top),
        right: Math.max(a.right, b.right),
        bottom: Math.max(a.bottom, b.bottom),
      })
      const meet = (a: Box, b: Box): Box => ({
        left: Math.max(a.left, b.left),
        top: Math.max(a.top, b.top),
        right: Math.min(a.right, b.right),
        bottom: Math.min(a.bottom, b.bottom),
      })
      /** An element's padding box, where its absolutely placed children are measured from. */
      const paddingBox = (el: Element): Box => {
        const box = el.getBoundingClientRect()
        const style = getComputedStyle(el)
        return {
          left: box.left + px(style.borderLeftWidth),
          top: box.top + px(style.borderTopWidth),
          right: box.right - px(style.borderRightWidth),
          bottom: box.bottom - px(style.borderBottomWidth),
        }
      }
      const placesChildren = (el: Element) => {
        const style = getComputedStyle(el)
        return style.position !== "static" || style.transform !== "none"
      }
      const pressBox = (el: HTMLElement): Box => {
        const own = el.getBoundingClientRect()
        let box: Box = { left: own.left, top: own.top, right: own.right, bottom: own.bottom }
        for (const pseudo of ["::before", "::after"]) {
          const style = getComputedStyle(el, pseudo)
          if (style.content === "none" || style.position !== "absolute") continue
          if ([style.left, style.top, style.right, style.bottom].includes("auto")) continue
          let holder: Element | null = el
          while (holder && holder !== document.body && !placesChildren(holder)) {
            holder = holder.parentElement
          }
          const frame = paddingBox(holder ?? document.body)
          // A scaled holder scales the insets with it, and a box centred with
          // a translate is moved by it.
          const scale = el.offsetWidth ? own.width / el.offsetWidth : 1
          // (`-translate-y-1/2` is the `translate` property, not `transform`).
          const moved = style.translate === "none" ? [] : style.translate.split(" ")
          const matrix =
            style.transform === "none" ? { e: 0, f: 0 } : new DOMMatrixReadOnly(style.transform)
          const length = (value: string | undefined, size: string) =>
            value?.endsWith("%") ? (parseFloat(value) / 100) * px(size) : px(value ?? "0")
          const shift = {
            e: matrix.e + length(moved[0], style.width),
            f: matrix.f + length(moved[1], style.height),
          }
          box = union(box, {
            left: frame.left + (px(style.left) + shift.e) * scale,
            top: frame.top + (px(style.top) + shift.f) * scale,
            right: frame.right - (px(style.right) - shift.e) * scale,
            bottom: frame.bottom - (px(style.bottom) - shift.f) * scale,
          })
        }
        const label = el.closest("label")
        if (label) {
          const at = label.getBoundingClientRect()
          box = union(box, { left: at.left, top: at.top, right: at.right, bottom: at.bottom })
        }
        // Cut to every ancestor that clips, up to the first scroller. A
        // scroller clips to everything it can scroll to, not only what is in
        // view.
        for (let up = el.parentElement; up && up !== document.body; up = up.parentElement) {
          const style = getComputedStyle(up)
          const clipsX = style.overflowX !== "visible"
          const clipsY = style.overflowY !== "visible"
          if (!clipsX && !clipsY) continue
          const frame = paddingBox(up)
          const scrolls = (overflow: string) => overflow === "auto" || overflow === "scroll"
          const clip: Box = {
            left: scrolls(style.overflowX) ? frame.left - up.scrollLeft : frame.left,
            right: scrolls(style.overflowX)
              ? frame.left - up.scrollLeft + up.scrollWidth
              : frame.right,
            top: scrolls(style.overflowY) ? frame.top - up.scrollTop : frame.top,
            bottom: scrolls(style.overflowY)
              ? frame.top - up.scrollTop + up.scrollHeight
              : frame.bottom,
          }
          box = meet(box, {
            left: clipsX ? clip.left : -Infinity,
            right: clipsX ? clip.right : Infinity,
            top: clipsY ? clip.top : -Infinity,
            bottom: clipsY ? clip.bottom : Infinity,
          })
          // What is outside a scroller's ancestors is scrolled into them first.
          if (scrolls(style.overflowX) || scrolls(style.overflowY)) break
        }
        return box
      }
      const shown = (el: HTMLElement) => {
        const box = el.getBoundingClientRect()
        if (box.width === 0 || box.height === 0) return false
        if (el.closest("[aria-hidden=true], [inert]")) return false
        const style = getComputedStyle(el)
        return style.visibility === "visible" && style.pointerEvents !== "none"
      }
      const name = (el: HTMLElement) => {
        const said =
          el.getAttribute("aria-label") || el.textContent?.trim() || el.getAttribute("title") || ""
        const slot = el.getAttribute("data-slot")
        return `${el.tagName.toLowerCase()}${slot ? `[${slot}]` : ""} "${said.slice(0, 40)}"`
      }
      const found: { what: string; width: number; height: number }[] = []
      const seen = new Set<Element>()
      for (const root of roots) {
        const all = [root, ...root.querySelectorAll(selector)].filter((el) => el.matches(selector))
        for (const el of all as HTMLElement[]) {
          if (seen.has(el) || !shown(el)) continue
          seen.add(el)
          const box = pressBox(el)
          const width = Math.round(box.right - box.left)
          const height = Math.round(box.bottom - box.top)
          if (Math.min(width, height) < min) found.push({ what: name(el), width, height })
        }
      }
      return found
    },
    { min, selector: PRESSABLE },
  )
}

/**
 * The scrollers on the page that are wider or taller with the press boxes in
 * `scope` than without them. Every `::after` in it placed past its control's
 * own box is pulled in to it, each scroller on the page is measured again, and
 * the two are compared.
 */
async function scrollersGrown(scope: Locator) {
  return scope.evaluateAll((roots) => {
    const reaching: Element[] = []
    for (const el of roots.flatMap((root) => [root, ...root.querySelectorAll("*")])) {
      const after = getComputedStyle(el, "::after")
      if (after.content === "none" || after.position !== "absolute") continue
      const insets = [after.left, after.top, after.right, after.bottom].map((v) => parseFloat(v))
      if (insets.some((v) => v < 0)) reaching.push(el)
    }
    const scrollers = [...document.querySelectorAll("body *")].filter((el) => {
      const style = getComputedStyle(el)
      return [style.overflowX, style.overflowY].some((v) => v === "auto" || v === "scroll")
    })
    const measure = () =>
      scrollers.map((el) => ({ width: el.scrollWidth, height: el.scrollHeight }))
    const withBoxes = measure()
    for (const el of reaching) el.setAttribute("data-press-box-off", "")
    const style = document.createElement("style")
    style.textContent = "[data-press-box-off]::after { inset: 0 !important; }"
    document.head.append(style)
    const without = measure()
    style.remove()
    for (const el of reaching) el.removeAttribute("data-press-box-off")
    const grown: string[] = []
    scrollers.forEach((el, index) => {
      const dw = withBoxes[index].width - without[index].width
      const dh = withBoxes[index].height - without[index].height
      if (dw > 0 || dh > 0) {
        const label =
          el.getAttribute("aria-label") ||
          el.getAttribute("data-slot") ||
          el.className.toString().slice(0, 60)
        grown.push(`${el.tagName.toLowerCase()} "${label}" +${dw}x+${dh}`)
      }
    })
    return grown
  })
}

/**
 * Every target in `scope` presses as 24px at least, and none makes a scroller
 * bigger. `handedOff` names targets that are short for a reason in a file
 * this pass does not own; each is a handoff in its outcome.
 */
async function expectTargets(scope: Locator, what: string, handedOff: RegExp[] = []) {
  await expect(scope.first()).toBeVisible()
  const small = (await smallTargets(scope)).filter(
    (target) => !handedOff.some((pattern) => pattern.test(target.what)),
  )
  expect(small, `${what}: press boxes under 24px`).toEqual([])
  expect(await scrollersGrown(scope), `${what}: scrollers grown by a press box`).toEqual([])
}

const now = new Date().toISOString()

test.describe("under a finger at 390", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test("a database's switcher and the heads of its blocks", async ({ page }) => {
    await mockDatabases(page)
    await page.goto("/databases/1")
    await risen(page)
    expect(await page.evaluate(() => matchMedia("(pointer: coarse)").matches)).toBe(true)

    // The switcher is the database's name in the header: 32px drawn under a
    // finger, where it was a 22px line of text.
    const switcher = page.getByRole("button", { name: /^Database: shop\. Switch database$/ })
    await expect(switcher).toBeVisible()
    expect((await switcher.boundingBox())!.height).toBeGreaterThanOrEqual(32)
    await expectTargets(switcher, "switcher")

    await expect(page.locator("main [data-slot=panel-header]").first()).toBeVisible()
    await expectTargets(page.locator("main [data-slot=panel-header]"), "block heads")
  })

  test("the Images band's marks are boxes, not slivers", async ({ page }) => {
    await mockImages(page)
    await page.goto("/docker/images")
    await risen(page)
    const band = page.locator("[data-slot=image-band]")
    const disk = page.getByRole("region", { name: "Docker on disk" })
    // The prune a mark opens is behind a 24px box, not a 16x2 one.
    const mark = disk.getByRole("button", { name: "Empty the build cache" })
    await expect(mark).toBeVisible()
    const box = (await mark.boundingBox())!
    expect(box.width).toBeGreaterThanOrEqual(24)
    expect(box.height).toBeGreaterThanOrEqual(24)
    // The kinds' names are `Explain` terms whose 44px press box the reading
    // line's name cuts to its own 20px (`truncate` in bar-reading.tsx).
    await expectTargets(band, "image band", [/^button\[explain\]/])
  })

  test("the tool panel's earlier runs are buttons of the shared height", async ({ page }) => {
    await mockNetwork(page, [], { session: admin })
    await page.route("**/api/v1/network/probe", (route) =>
      networkJson(route, {
        tool: "ping",
        target: "192.0.2.9",
        ok: true,
        output: "4 packets transmitted, 4 received",
        duration: "3s",
      }),
    )
    await page.route("**/api/v1/network/diagnostics/**", (route) => networkJson(route, []))
    await page.goto("/network/tools?tool=ping&target=192.0.2.9")
    await risen(page)
    const panel = page.locator("[data-slot=security-tools]")
    const run = panel.getByRole("button", { name: "Run", exact: true })
    await run.click()
    await expect(run).toBeEnabled()
    await run.click()
    const earlier = page.locator("[data-slot=tool-earlier]")
    await expect(earlier.getByRole("button")).not.toHaveCount(0)
    for (const chip of await earlier.getByRole("button").all()) {
      await expect(chip).toHaveAttribute("data-size", "xs")
    }
    await expectTargets(earlier, "earlier runs")
  })

  test("Git's group commands are buttons, not 20px words", async ({ page }) => {
    await mockGit(page)
    await page.goto("/git?repo=%2Fsrv%2Fapp")
    await risen(page)
    const changes = page.locator("[data-slot=changes-panel]")
    for (const name of ["Unstage all", "Discard all", "Stage all"]) {
      const command = changes.getByRole("button", { name, exact: true })
      // A Button (the tooltip's trigger takes over its data-slot).
      await expect(command).toHaveAttribute("data-size", "xs")
      await expect(command).toHaveAttribute("data-variant", "ghost")
    }
    await expectTargets(changes, "changes panel")
  })

  test("Terminal's tab strip: its arrows in their own slots, nothing scrolls from a press box", async ({
    page,
  }) => {
    await mockTerminal(page)
    await page.goto("/terminal")
    await risen(page)
    const strip = page.locator("[data-slot=window-strip]:visible")
    await expect(strip.locator("[data-window]").first()).toBeVisible()
    const left = strip.getByRole("button", { name: "Scroll tabs left" })
    const right = strip.getByRole("button", { name: "Scroll tabs right" })
    await expect(right).toBeVisible()
    // Each arrow is a 32px slot of its own, outside the tabs it scrolls.
    for (const arrow of [left, right]) {
      const box = (await arrow.boundingBox())!
      expect(Math.round(box.width)).toBeGreaterThanOrEqual(32)
      expect(Math.round(box.height)).toBeGreaterThanOrEqual(32)
    }
    const tabs = strip.locator("[aria-label='Terminal windows']")
    const scroller = (await tabs.boundingBox())!
    const arrowBox = (await right.boundingBox())!
    expect(arrowBox.x).toBeGreaterThanOrEqual(scroller.x + scroller.width)
    await expectTargets(strip, "window strip")
  })

  for (const [path, slot] of [
    ["/proxy", "overview-band"],
    ["/proxy/sites", "site-band"],
  ] as const) {
    test(`the proxy's ${slot}`, async ({ page }) => {
      await mockShowcase(page)
      await page.goto(path)
      await risen(page)
      await expectTargets(page.locator(`[data-slot=${slot}]`), slot)
    })
  }
})

/* ------------------------------------------------------------- fixtures -- */

const session = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

function reply(route: Route, body: unknown) {
  return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

/** A checkout with a file staged, one edited after staging and one never committed. */
async function mockGit(page: Page) {
  const repo = {
    path: "/srv/app",
    name: "app",
    branch: "main",
    upstream: "origin/main",
    remote: "https://github.com/acme/app.git",
    head: "abc1234",
    subject: "Ship the thing",
    author: "Ada",
    commitAt: now,
    dirty: true,
    changes: 3,
    staged: 2,
    untracked: 1,
    conflicts: 0,
    ahead: 1,
    behind: 2,
    detached: false,
  }
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    switch (path) {
      case "/auth/session":
        return reply(route, session)
      case "/git/":
        return reply(route, { available: true, repos: [repo] })
      case "/git/pull-requests":
        return reply(route, { available: true, repos: [] })
      case "/git/github/":
        return reply(route, { available: true, account: { loggedIn: false, gitConfigured: false } })
      case "/git/status":
        return reply(route, {
          repo,
          files: [
            {
              path: "a.txt",
              index: "M",
              worktree: "M",
              label: "modified",
              staged: true,
              unstaged: true,
            },
            {
              path: "b.txt",
              index: "A",
              worktree: "",
              label: "added",
              staged: true,
              unstaged: false,
            },
            {
              path: "new.txt",
              index: "?",
              worktree: "?",
              label: "untracked",
              staged: false,
              unstaged: true,
            },
          ],
          clean: false,
          stashes: 1,
          identity: { name: "Ada", email: "ada@example.com" },
        })
      case "/git/stashes":
        return reply(route, [
          { index: 0, sha: "s0", at: now, branch: "main", message: "parked work" },
        ])
      case "/git/diff":
        return reply(route, { diff: "diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-one\n+two\n" })
      case "/files/list":
        return reply(route, { path: "/srv/app", entries: [] })
      case "/updates/self":
        return reply(route, { current: "0.9.0", latest: "0.9.0" })
    }
    return reply(route, [])
  })
}

/** One session with five windows: more tabs than a phone's title bar holds. */
async function mockTerminal(page: Page) {
  const windows = ["window-a", "window-b", "window-c", "window-d", "window-e"]
  await page.addInitScript(() => {
    localStorage.setItem("jd.terminal.renderer", "dom")
  })
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "")
    if (path === "/auth/session") return reply(route, session)
    if (path === "/terminal/") {
      return reply(route, {
        enabled: true,
        persistent: true,
        login: { user: "operator", home: "/home/operator", shell: "/bin/bash" },
        folders: [],
        sessions: [
          {
            id: "session-a",
            title: "session-a",
            live: true,
            windows: windows.length,
            createdAt: "2026-09-01T00:00:00Z",
            current: { id: windows[0], name: windows[0] },
          },
        ],
      })
    }
    if (/^\/terminal\/[^/]+\/windows$/.test(path)) {
      return reply(
        route,
        windows.map((id, index) => ({ id, name: id, index, cwd: "/home/operator/project" })),
      )
    }
    if (/^\/terminal\/[^/]+\/cwd$/.test(path)) {
      return reply(route, { cwd: "/home/operator/project" })
    }
    return reply(route, {})
  })
  await page.routeWebSocket("**/api/v1/**", (socket) => {
    socket.send(Buffer.from("operator@host:~/project$ "))
  })
}
