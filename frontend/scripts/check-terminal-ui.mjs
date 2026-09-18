import { chromium } from "playwright"
import assert from "node:assert/strict"

// Run against a disposable frontend: bun dev --hostname 127.0.0.1 --port 3107
// Then: node scripts/check-terminal-ui.mjs (or set JD_BROWSER_BASE_URL).
// Disposable API and socket fixtures: this check never reaches a host shell.
const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
const errors = []
const mutations = []
const creates = []
const input = []
const controls = []
let scrollOffset = 40
let sessionPresent = true
let closedWindows = 0
let closedSessions = 0
page.on("pageerror", (error) => errors.push(error.message))
let windows = ["codex", "server logs", "shell"].map((name, index) => ({
  id: `window-${index}`,
  index,
  name,
  cwd: "/home/ubuntu/Just-Dashboard",
}))
await page.addInitScript(() => {
  localStorage.removeItem("jd.terminal.settings")
  localStorage.setItem(
    "jd.view.state",
    JSON.stringify({ "terminal.rail": false, "terminal.tools": false, "shell.sidebar": false }),
  )
})
if (process.env.JD_TERMINAL_RENDERER) {
  await page.addInitScript((renderer) => {
    localStorage.setItem("jd.terminal.renderer", renderer)
  }, process.env.JD_TERMINAL_RENDERER)
}

await page.route("**/api/v1/**", async (route) => {
  const path = new URL(route.request().url()).pathname.replace("/api/v1", "")
  let data = {}
  if (route.request().method() === "POST") mutations.push(path)
  if (path === "/terminal/" && route.request().method() === "POST") {
    creates.push(route.request().postDataJSON())
    sessionPresent = true
    return route.fulfill({ json: { id: "preview", windowId: windows[0].id } })
  }
  if (path === "/terminal/preview" && route.request().method() === "DELETE") {
    sessionPresent = false
    closedSessions++
  }
  if (/\/windows\/[^/]+$/.test(path) && route.request().method() === "DELETE") {
    closedWindows++
    windows = windows.filter((win) => win.id !== path.split("/").at(-1))
    windows = windows.map((win, index) => ({ ...win, index }))
  }
  if (path === "/system/metrics")
    return route.fulfill({
      status: 503,
      json: { error: { code: "unavailable", message: "Preview" } },
    })
  if (path === "/system/health") data = { status: "ok" }
  if (path === "/auth/session")
    data = {
      authenticated: true,
      user: { username: "ubuntu", role: "admin" },
      capabilities: ["read", "terminal", "file.write"],
    }
  else if (path === "/terminal/")
    data = {
      enabled: true,
      login: { user: "ubuntu", home: "/home/ubuntu", shell: "/bin/bash" },
      folders: [],
      sessions: sessionPresent
        ? [
            {
              id: "preview",
              title: "Workspace",
              user: "ubuntu",
              live: true,
              windows: windows.length,
              cwd: "/home/ubuntu/Just-Dashboard",
              createdAt: new Date().toISOString(),
            },
          ]
        : [],
    }
  else if (path.endsWith("/windows") && route.request().method() === "POST") {
    const created = {
      id: `window-${windows.length}`,
      index: windows.length,
      name: "shell",
      cwd: "/home/ubuntu/Just-Dashboard",
    }
    windows.push(created)
    return route.fulfill({ status: 201, json: created })
  } else if (path.endsWith("/windows")) data = windows
  else if (/\/windows\/[^/]+$/.test(path) && route.request().method() === "PATCH") {
    const id = path.split("/").at(-1)
    const body = route.request().postDataJSON()
    if (body.name) windows = windows.map((w) => (w.id === id ? { ...w, name: body.name } : w))
  } else if (path === "/git/detect") data = { found: false }
  else if (path === "/files/list") data = { entries: [], path: "/home/ubuntu/Just-Dashboard" }
  await route.fulfill({ json: data })
})
await page.routeWebSocket("**/api/v1/**", (socket) => {
  if (socket.url().includes("/attach")) {
    socket.send(
      Buffer.from(
        "\x1b[32m~/Just-Dashboard\x1b[0m\r\n> git status\r\nOn branch main\r\nYour branch is up to date with origin/main.\r\n\r\nnothing to commit, working tree clean\r\n\r\n\x1b[32m~/Just-Dashboard\x1b[0m\r\n> ",
      ),
    )
    // Exercise alternate-screen enter/leave, cursor visibility, clearing and
    // a scroll region before returning to the shell buffer.
    socket.send(Buffer.from("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[2;20r\x1b[Hadvanced TUI\x1b[r"))
    socket.send(Buffer.from("\x1b[?25h\x1b[?1049l"))
    // Split a multibyte glyph between WebSocket messages. xterm's Uint8Array
    // decoder must carry the incomplete UTF-8 sequence into the next write.
    const unicode = Buffer.from(
      "\r\n╭────────╮  ▄▀█ █▀▄ ▌▐\r\n│ test   │  █▄█ █▄▀ ▐▌  ● ⏺ ✻ → … ✓ ⚠\r\n╰────────╯  ▀ ▀ ▀ ▀ ▀▀\r\n",
    )
    const split = unicode.indexOf(Buffer.from("⏺")) + 1
    socket.send(unicode.subarray(0, split))
    socket.send(unicode.subarray(split))
    socket.onMessage((message) => {
      if (typeof message === "string" && message.startsWith("{")) {
        const control = JSON.parse(message)
        controls.push(control)
        if (control.type === "exit-copy") scrollOffset = 0
        if (control.type === "scroll-to") scrollOffset = control.offset
        if (["sync-copy", "exit-copy", "scroll-to"].includes(control.type))
          socket.send(
            JSON.stringify({
              type: "copy-mode",
              data: { active: scrollOffset > 0, offset: scrollOffset, history: 100, height: 30 },
            }),
          )
      }
      if (typeof message !== "string") {
        const decoded = Buffer.from(message).toString("utf8")
        input.push(decoded)
        socket.send(Buffer.from(message))
      }
    })
  }
})
try {
  await page.goto(`${process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:3107"}/terminal`)
  await page.locator(".xterm-screen").waitFor()
  await page.waitForFunction(
    () =>
      document
        .querySelector("[data-terminal-rows][data-terminal-cols]")
        ?.getAttribute("data-terminal-unicode") === "11",
  )
  const terminalGeometry = await page.locator("[data-terminal-rows]").evaluate((host) => ({
    rows: Number(host.getAttribute("data-terminal-rows")),
    cols: Number(host.getAttribute("data-terminal-cols")),
  }))
  assert.equal(
    await page.locator("[data-terminal-renderer]").getAttribute("data-terminal-renderer"),
    process.env.JD_TERMINAL_RENDERER === "dom" ? "dom" : "webgl",
    "the requested xterm renderer must actually be active",
  )

  const initialResize = controls.filter((control) => control.type === "resize").at(-1)
  assert.deepEqual(
    { rows: initialResize.rows, cols: initialResize.cols },
    terminalGeometry,
    "the resize control must match xterm's measured grid",
  )
  await page.getByRole("button", { name: "Show the sessions rail", exact: true }).click()
  const directCreated = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.endsWith("/api/v1/terminal/") &&
      response.request().method() === "POST",
  )
  await page.getByRole("button", { name: "New session", exact: true }).click()
  await directCreated
  assert.equal("persist" in creates.at(-1), false, "session creation must always use a direct PTY")
  assert.equal(await page.getByText("Persistent session", { exact: true }).count(), 0)
  await page.getByRole("button", { name: "Hide the sessions rail", exact: true }).click()
  assert.equal(await page.getByRole("textbox", { name: "Command draft" }).count(), 0)
  assert.equal(await page.getByRole("button", { name: "Focus", exact: true }).count(), 0)
  assert.equal(await page.getByRole("slider", { name: "Terminal scrollback" }).count(), 0)
  await page.locator(".xterm-helper-textarea").press("a")
  await page.locator(".xterm-helper-textarea").press("Tab")
  assert(input.includes("a"), "native typing must reach the shell")
  assert(input.includes("\t"), "Tab must reach native shell completion")
  assert.equal(await page.locator('[aria-label="Terminal workspace"]').count(), 0)
  assert.equal(await page.getByText("/home/ubuntu/Just-Dashboard", { exact: true }).count(), 0)
  for (const tab of await page.locator("[data-window]").all()) {
    const box = await tab.boundingBox()
    assert(box.width >= 112 && box.height >= 32)
  }
  assert.equal(
    await page.getByRole("button", { name: "Close window codex", exact: true }).count(),
    1,
  )
  await page.getByRole("button", { name: "server logs", exact: true }).click()
  await page.locator('[data-window="window-1"][data-active="true"]').waitFor()
  await page.getByRole("button", { name: "More for window server logs" }).click()
  await page.getByRole("menuitem", { name: "Rename window", exact: true }).click()
  const rename = page.locator('[aria-label="Terminal windows"] input')
  await rename.fill("build output")
  await rename.press("Enter")
  await page.getByRole("button", { name: "build output", exact: true }).waitFor()
  assert.equal(await page.getByText("Split side by side", { exact: true }).count(), 0)
  await page.getByRole("button", { name: "Terminal settings", exact: true }).click()
  const settingsPanel = page.locator('[data-slot="popover-content"]')
  assert.equal(
    await settingsPanel.getByRole("combobox", { name: "Font family", exact: true }).count(),
    0,
  )
  assert.equal(
    await settingsPanel.getByRole("combobox", { name: "Cursor style", exact: true }).count(),
    0,
  )
  assert.equal(
    await settingsPanel.getByRole("slider", { name: "Line height", exact: true }).count(),
    0,
  )
  assert.equal(
    await settingsPanel.getByRole("slider", { name: "Letter spacing", exact: true }).count(),
    0,
  )
  assert.equal(
    await settingsPanel.getByRole("switch", { name: "Blinking cursor", exact: true }).count(),
    0,
  )
  assert.equal(
    await settingsPanel.getByRole("button", { name: "Larger text", exact: true }).count(),
    0,
  )
  assert.equal(await settingsPanel.getByRole("button", { name: "Reset", exact: true }).count(), 0)
  await settingsPanel.getByRole("switch", { name: "Confirm multi-line paste", exact: true }).click()
  const savedSettings = await page.evaluate(() =>
    JSON.parse(localStorage.getItem("jd.terminal.settings")),
  )
  assert.equal(savedSettings.confirmMultilinePaste, false)
  assert.equal("fontFamily" in savedSettings, false)
  assert.equal("cursorStyle" in savedSettings, false)
  assert.equal("lineHeight" in savedSettings, false)
  assert.equal("letterSpacing" in savedSettings, false)
  assert.equal("cursorBlink" in savedSettings, false)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Terminal actions", exact: true }).click()
  await page.getByRole("menuitem", { name: "Save scrollback", exact: true }).waitFor()
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: /Search scrollback/ }).click()
  await page.getByRole("textbox", { name: "Find in scrollback" }).fill("branch")
  await page.getByRole("button", { name: "Close search", exact: true }).click()
  await page.getByRole("button", { name: "Show the sessions rail", exact: true }).click()
  await page.getByRole("button", { name: "Hide the sessions rail", exact: true }).click()
  await page.getByRole("button", { name: "Show files & git", exact: true }).click()
  await page.getByRole("button", { name: "Hide files & git", exact: true }).click()
  await page.getByRole("button", { name: "Show files & git", exact: true }).waitFor()
  await page.screenshot({ animations: "disabled", path: "/tmp/terminal-desktop.png" })
  await page.evaluate(() => {
    document.documentElement.className = "light"
    document.documentElement.style.colorScheme = "light"
    window.dispatchEvent(new Event("just-dashboard:themechange"))
  })
  await page.screenshot({ animations: "disabled", path: "/tmp/terminal-light.png" })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.waitForFunction(
    ([rows, cols]) => {
      const host = document.querySelector("[data-terminal-rows]")
      return (
        Number(host?.getAttribute("data-terminal-rows")) !== rows ||
        Number(host?.getAttribute("data-terminal-cols")) !== cols
      )
    },
    [terminalGeometry.rows, terminalGeometry.cols],
  )
  const mobileGeometry = await page.locator("[data-terminal-rows]").evaluate((host) => ({
    rows: Number(host.getAttribute("data-terminal-rows")),
    cols: Number(host.getAttribute("data-terminal-cols")),
  }))
  const mobileResize = controls.filter((control) => control.type === "resize").at(-1)
  assert.deepEqual({ rows: mobileResize.rows, cols: mobileResize.cols }, mobileGeometry)
  await page.locator('[data-window="window-2"] button').first().click()
  await page.locator('[data-window="window-2"][data-active="true"]').waitFor()
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
  await page.screenshot({ animations: "disabled", path: "/tmp/terminal-mobile.png" })
  const screen = await page.locator(".xterm-screen").boundingBox()
  const host = await page.locator(".xterm-screen").locator("../..").boundingBox()
  assert(screen.y + screen.height <= host.y + host.height + 1, "terminal rows must fit inside host")
  const beforeLaunch = mutations.filter((path) => path === "/terminal/").length
  await page.goto(
    `${process.env.JD_BROWSER_BASE_URL ?? "http://127.0.0.1:3107"}/terminal?cwd=%2Fhome%2Fubuntu&folder=work&keep=1#shell`,
  )
  await page.waitForURL((url) => !url.searchParams.has("cwd"))
  await page.locator(".xterm-screen").waitFor()
  assert.equal(mutations.filter((path) => path === "/terminal/").length, beforeLaunch + 1)
  assert(page.url().includes("keep=1#shell"))
  for (let i = 0; i < 2; i++) {
    await page.reload()
    await page.locator(".xterm-screen").waitFor()
  }
  assert.deepEqual(errors, [], "terminal mount/reconnect must not raise browser errors")
  assert.equal(
    mutations.filter((path) => path === "/terminal/").length,
    beforeLaunch + 1,
    "refresh must not create sessions",
  )
  // The visible X uses confirmation, and the last window closes the session
  // rather than calling the backend's deliberately refused last-window delete.
  while (windows.length > 1) {
    const name = windows[0].name
    const beforeClose = closedWindows
    await page.getByRole("button", { name: `Close window ${name}`, exact: true }).click()
    await page.getByRole("dialog").waitFor()
    assert.equal(closedWindows, beforeClose)
    await page.getByRole("button", { name: "Cancel", exact: true }).click()
    assert.equal(closedWindows, beforeClose)
    await page.getByRole("button", { name: `Close window ${name}`, exact: true }).click()
    await page.getByRole("button", { name: "Close window", exact: true }).click()
    await page
      .getByRole("button", { name: `Close window ${name}`, exact: true })
      .waitFor({ state: "detached" })
  }
  await page.getByRole("button", { name: `Close window ${windows[0].name}`, exact: true }).click()
  await page.getByRole("button", { name: "Close session", exact: true }).click()
  await page.getByText("No sessions yet", { exact: true }).waitFor()
  assert.equal(closedSessions, 1)
  await page.reload()
  await page.getByText("No sessions yet", { exact: true }).waitFor()
  assert.equal(
    mutations.filter((path) => path === "/terminal/").length,
    beforeLaunch + 1,
    "closed sessions must stay closed after refresh",
  )
  assert.deepEqual(errors, [])
  console.log(
    "PASS: direct PTY creation, native typing and Tab, compact window actions and close, integrated title bar, one-time launch, refresh after closing, bounded layout and dark/light/mobile",
  )
} finally {
  await browser.close()
}
