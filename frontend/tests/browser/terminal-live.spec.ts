import { existsSync, readFileSync } from "node:fs"
import { mkdir } from "node:fs/promises"
import { join } from "node:path"
import { expect, test, type Locator, type Page } from "@playwright/test"

type LiveReady = {
  url: string
  cwd: string
  windowId: string
  workspaceId: string
  tuiCommand: string
  realAgents: boolean
}
type Stream = {
  attaches: number
  output: string
  input: string
  sizes: { rows: number; cols: number }[]
}
type Creation = { id: string; sourceWindowId: string; agent?: "codex" | "claude" }

const readyFile = process.env.JD_TERMINAL_LIVE_READY
const ready: LiveReady | undefined =
  readyFile && existsSync(readyFile) ? JSON.parse(readFileSync(readyFile, "utf8")) : undefined
const renderer = process.env.JD_TERMINAL_LIVE_RENDERER === "dom" ? "dom" : "webgl"

test.use({
  // The isolated harness serves native terminal sockets on a second loopback port.
  bypassCSP: true,
  video: process.env.JD_TERMINAL_EVIDENCE
    ? { mode: "on", size: { width: 1440, height: 900 } }
    : "off",
})
test.skip(!ready, "Set JD_TERMINAL_LIVE_READY to the isolated terminal harness ready.json")

const pane = (page: Page, id: string) => page.locator(`[data-terminal-window="${id}"]`)
const shellQuote = (text: string) => "'" + text.replaceAll("'", "'\\''") + "'"

async function focusPane(page: Page, id: string) {
  await pane(page, id)
    .locator(".xterm-screen")
    .click({ position: { x: 25, y: 25 } })
  await expect(pane(page, id)).toHaveAttribute("data-focused", "true")
  await expect(pane(page, id).locator(".xterm-helper-textarea")).toBeFocused()
}

async function drag(page: Page, divider: Locator, dx: number, dy: number, name: string) {
  const box = (await divider.boundingBox())!
  const directory = process.env.JD_TERMINAL_EVIDENCE
  if (directory) await mkdir(directory, { recursive: true })
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  const moving = page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, {
    steps: 40,
  })
  if (directory) {
    // Capture while the pointer remains held and the divider motion is in flight.
    await Promise.all([
      moving,
      page.screenshot({ path: join(directory, name), animations: "disabled" }),
    ])
  } else await moving
  await page.mouse.up()
}

async function evidence(page: Page, name: string, preservePointer = false) {
  const directory = process.env.JD_TERMINAL_EVIDENCE
  if (!directory) return
  await mkdir(directory, { recursive: true })
  if (!preservePointer) await page.mouse.move(0, 0)
  await page.screenshot({ path: join(directory, name), animations: "disabled" })
  // Leave each verified frame visible long enough to review in the recording.
  await page.waitForTimeout(650)
}

async function assertNativeGrids(page: Page, streams: Map<string, Stream>) {
  for (const terminal of await page.locator("[data-terminal-window]:visible").all()) {
    const id = (await terminal.getAttribute("data-terminal-window"))!
    const host = terminal.locator("[data-terminal-renderer]")
    await expect(host).toHaveAttribute("data-terminal-renderer", renderer)
    await expect
      .poll(
        async () => {
          const grid = await host.evaluate((element) => ({
            cols: Number(element.getAttribute("data-terminal-cols")),
            rows: Number(element.getAttribute("data-terminal-rows")),
          }))
          const reported = Array.from(
            (streams.get(id)?.output ?? "").matchAll(/PTY grid: (\d+) columns x (\d+) rows/g),
          ).at(-1)
          return Boolean(
            reported && Number(reported[1]) === grid.cols && Number(reported[2]) === grid.rows,
          )
        },
        { timeout: 10_000 },
      )
      .toBe(true)
    const hostBox = (await host.boundingBox())!
    const screen = (await terminal.locator(".xterm-screen").boundingBox())!
    expect(screen.x).toBeGreaterThanOrEqual(hostBox.x - 1)
    expect(screen.y).toBeGreaterThanOrEqual(hostBox.y - 1)
    expect(screen.x + screen.width).toBeLessThanOrEqual(hostBox.x + hostBox.width + 1)
    expect(screen.y + screen.height).toBeLessThanOrEqual(hostBox.y + hostBox.height + 1)
    expect(streams.get(id)?.attaches).toBe(1)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
}

async function waitForShellPrompt(streams: Map<string, Stream>, id: string) {
  // Socket attachment and cwd availability precede the login shell's rc
  // files. Wait until it can accept a command, especially on a busy host.
  await expect
    .poll(() => (streams.get(id)?.output ?? "").replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, ""), {
      timeout: 10_000,
    })
    .toMatch(/\r?\n> /)
}

async function liveTerminal(page: Page) {
  if (!ready) throw new Error("The isolated live terminal harness is unavailable")
  const streams = new Map<string, Stream>()
  const creates: Creation[] = []
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  page.on("websocket", (socket) => {
    const id = new URL(socket.url()).pathname.match(/\/terminal\/([^/]+)\/attach$/)?.[1]
    if (!id) return
    const stream = streams.get(id) ?? { attaches: 0, output: "", input: "", sizes: [] }
    stream.attaches++
    streams.set(id, stream)
    const decoder = new TextDecoder()
    socket.on("framereceived", ({ payload }) => {
      if (typeof payload !== "string") stream.output += decoder.decode(payload, { stream: true })
    })
    socket.on("framesent", ({ payload }) => {
      if (typeof payload !== "string") stream.input += Buffer.from(payload).toString("utf8")
      else {
        const control = JSON.parse(payload)
        if (control.type === "resize") stream.sizes.push(control)
      }
    })
  })
  await page.addInitScript(
    ({ ready, renderer }) => {
      localStorage.setItem("jd.terminal.renderer", renderer)
      localStorage.setItem(
        "jd.view.state",
        JSON.stringify({
          "shell.sidebar": false,
          "terminal.rail": false,
          "terminal.tools": false,
          "terminal.session": ready.workspaceId,
          "terminal.windows": { [ready.workspaceId]: ready.windowId },
          "terminal.layouts": {},
        }),
      )
      const NativeWebSocket = window.WebSocket
      window.WebSocket = class extends NativeWebSocket {
        constructor(url: string | URL, protocols?: string | string[]) {
          const target = new URL(String(url), window.location.href)
          if (target.pathname.startsWith("/api/v1/terminal/")) {
            const backend = new URL(ready.url)
            target.host = backend.host
            target.protocol = backend.protocol === "https:" ? "wss:" : "ws:"
          }
          super(target, protocols)
        }
      }
    },
    { ready, renderer },
  )
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace("/api/v1", "")
    if (path.startsWith("/terminal/")) {
      const response = await route.fetch({ url: ready.url + url.pathname + url.search })
      if (route.request().method() === "POST" && path.endsWith("/windows") && response.ok()) {
        const body = route.request().postDataJSON() as Omit<Creation, "id">
        creates.push({ ...body, ...(await response.json()) })
      }
      return route.fulfill({ response })
    }
    if (path === "/auth/session") {
      return route.fulfill({
        json: {
          authenticated: true,
          user: { username: "tester", role: "admin" },
          capabilities: ["read", "terminal", "destructive"],
        },
      })
    }
    if (path === "/system/metrics") {
      return route.fulfill({
        status: 503,
        json: {
          error: { code: "unavailable", message: "Isolated live terminal harness" },
        },
      })
    }
    return route.fulfill({ json: {} })
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto("/terminal")
  return { streams, creates, errors }
}

test("native PTYs inherit the live directory and resize every focused split", async ({ page }) => {
  if (!ready) return
  test.setTimeout(process.env.JD_TERMINAL_EVIDENCE ? 300_000 : 120_000)
  const { streams, creates, errors } = await liveTerminal(page)
  const source = ready.windowId
  const currentDir = join(ready.cwd, "working folder")
  await expect(pane(page, source).locator(".xterm-helper-textarea")).toBeFocused({
    timeout: 15_000,
  })
  try {
    await page.keyboard.press("Control+C")
    await page.keyboard.type(
      `cd ${shellQuote(ready.cwd)} && mkdir -p 'working folder' && cd 'working folder'`,
    )
    await page.keyboard.press("Enter")
    const cwd = async (id: string) => {
      const response = await page.request.get(`${ready.url}/api/v1/terminal/${id}/cwd`)
      return (await response.json()).cwd as string
    }
    await expect.poll(() => cwd(source), { timeout: 10_000 }).toBe(currentDir)
    await page.keyboard.type(ready.tuiCommand)
    await page.keyboard.press("Enter")
    await expect.poll(() => streams.get(source)?.output).toContain("Command: jd-resize-tui")
    await assertNativeGrids(page, streams)

    for (const agent of ["codex", "claude"] as const) {
      if (agent === "claude") {
        await page.locator(`[data-window="${source}"]`).getByRole("button").first().click()
        await focusPane(page, source)
      }
      const before = creates.length
      await page
        .getByRole("button", { name: agent === "codex" ? "Codex" : "Claude", exact: true })
        .click()
      await expect.poll(() => creates.length).toBe(before + 1)
      const created = creates.at(-1)!
      expect(created.sourceWindowId).toBe(source)
      expect(created.agent).toBe(agent)
      await expect(pane(page, created.id).locator(".xterm-helper-textarea")).toBeFocused()
      await expect.poll(() => cwd(created.id), { timeout: 10_000 }).toBe(currentDir)
      if (!ready.realAgents) {
        const command = agent === "codex" ? "codex --yolo" : "claude --dangerously-skip-permissions"
        await expect.poll(() => streams.get(created.id)?.output).toContain(`Command: ${command}`)
        await expect
          .poll(() => streams.get(created.id)?.output)
          .toContain(`Directory: ${currentDir}`)
        await assertNativeGrids(page, streams)
      }
      await evidence(
        page,
        `native-${agent}-${ready.realAgents ? "real-tool" : "fixture-agent"}.png`,
      )
    }

    await page.locator(`[data-window="${source}"]`).getByRole("button").first().click()
    await focusPane(page, source)
    await page.keyboard.press("Control+Alt+Shift+ArrowRight")
    await expect.poll(() => creates.length).toBe(3)
    const top = creates[2].id
    await expect(pane(page, top).locator(".xterm-helper-textarea")).toBeFocused()
    await expect.poll(() => cwd(top), { timeout: 10_000 }).toBe(currentDir)
    await waitForShellPrompt(streams, top)
    await page.keyboard.type(ready.tuiCommand)
    await page.keyboard.press("Enter")
    await expect.poll(() => streams.get(top)?.output).toContain("Command: jd-resize-tui")
    await page.keyboard.press("Control+Alt+Shift+ArrowDown")
    await expect.poll(() => creates.length).toBe(4)
    const bottom = creates[3].id
    await expect(pane(page, bottom).locator(".xterm-helper-textarea")).toBeFocused()
    await expect.poll(() => cwd(bottom), { timeout: 10_000 }).toBe(currentDir)
    await waitForShellPrompt(streams, bottom)
    await page.keyboard.type(ready.tuiCommand)
    await page.keyboard.press("Enter")
    await expect.poll(() => streams.get(bottom)?.output).toContain("Command: jd-resize-tui")
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
    await assertNativeGrids(page, streams)
    await evidence(page, "native-three-pty-splits.png")

    const width = page.getByRole("separator", { name: "Terminal split width", exact: true })
    const height = page.getByRole("separator", { name: "Terminal split height", exact: true })
    await drag(page, width, 85, 0, "native-drag-width.png")
    await assertNativeGrids(page, streams)
    await drag(page, height, 0, 60, "native-drag-height.png")
    await assertNativeGrids(page, streams)
    await evidence(page, "native-resized-splits.png")
    await width.focus()
    const widthBefore = Number(await width.getAttribute("aria-valuenow"))
    await page.keyboard.press("ArrowLeft")
    await expect
      .poll(async () => Number(await width.getAttribute("aria-valuenow")))
      .toBeLessThan(widthBefore)
    await page.keyboard.press("Home")
    await expect(width).toHaveAttribute("aria-valuenow", "50")
    await height.focus()
    const heightBefore = Number(await height.getAttribute("aria-valuenow"))
    await page.keyboard.press("ArrowUp")
    await expect
      .poll(async () => Number(await height.getAttribute("aria-valuenow")))
      .toBeLessThan(heightBefore)
    await page.keyboard.press("Home")
    await expect(height).toHaveAttribute("aria-valuenow", "50")
    await assertNativeGrids(page, streams)

    const visible = [source, top, bottom]
    for (const [index, id] of visible.entries()) {
      await focusPane(page, id)
      const label = ["live-left", "live-top-right", "live-bottom-right"][index]
      await page.keyboard.type(label)
      await expect.poll(() => streams.get(id)?.output).toContain(`Last input: ${label}`)
      for (const other of visible.filter((item) => item !== id)) {
        expect(streams.get(other)?.input).not.toContain(label)
      }
    }
    const beforeNavigation = visible.map((id) => streams.get(id)?.input)
    await page.keyboard.press("Control+Alt+KeyI")
    await expect(pane(page, top).locator(".xterm-helper-textarea")).toBeFocused()
    await page.keyboard.press("Control+Alt+KeyH")
    await expect(pane(page, source).locator(".xterm-helper-textarea")).toBeFocused()
    await page.keyboard.press("Control+Alt+KeyL")
    await expect(pane(page, top).locator(".xterm-helper-textarea")).toBeFocused()
    await page.keyboard.press("Control+Alt+KeyK")
    await expect(pane(page, bottom).locator(".xterm-helper-textarea")).toBeFocused()
    await page.keyboard.press("Control+Alt+KeyP")
    await expect(pane(page, source).locator(".xterm-helper-textarea")).toBeFocused()
    expect(visible.map((id) => streams.get(id)?.input)).toEqual(beforeNavigation)
    await evidence(page, "native-focused-input.png")
    await page.getByRole("button", { name: "Fullscreen", exact: true }).click()
    await assertNativeGrids(page, streams)
    await evidence(page, "native-fullscreen.png")
    await page.getByRole("button", { name: /^Leave fullscreen/ }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    await assertNativeGrids(page, streams)
    await evidence(page, "native-mobile.png")
    expect(errors).toEqual([])
  } finally {
    for (const created of creates) {
      await page.request.delete(
        `${ready.url}/api/v1/terminal/${ready.workspaceId}/windows/${created.id}`,
      )
    }
    if (!page.isClosed() && (await pane(page, source).isVisible())) {
      await focusPane(page, source)
      await page.keyboard.press("Control+C")
    }
  }
})

test("native panes detach and dock through live directional overlays", async ({ page }) => {
  if (!ready) return
  test.setTimeout(180_000)
  const { streams, creates, errors } = await liveTerminal(page)
  const source = ready.windowId
  const strip = page.getByLabel("Terminal windows")
  const startTUI = async (id: string) => {
    await expect(pane(page, id).locator(".xterm-helper-textarea")).toBeFocused()
    await waitForShellPrompt(streams, id)
    await page.keyboard.type(ready.tuiCommand)
    await page.keyboard.press("Enter")
    await expect.poll(() => streams.get(id)?.output).toContain("Command: jd-resize-tui")
  }
  const hoverDrop = async (
    dragged: string,
    target: string,
    direction: "left" | "right" | "up" | "down",
    start = true,
  ) => {
    if (start) {
      const tab = (await strip.locator(`[data-window="${dragged}"]`).boundingBox())!
      await page.mouse.move(tab.x + tab.width / 3, tab.y + tab.height / 2)
      await page.mouse.down()
      await page.mouse.move(tab.x + tab.width / 3 + 12, tab.y + tab.height / 2 + 12, {
        steps: 4,
      })
    }
    const box = (await pane(page, target).boundingBox())!
    const x = direction === "left" ? 0.1 : direction === "right" ? 0.9 : 0.5
    const y = direction === "up" ? 0.1 : direction === "down" ? 0.9 : 0.5
    await page.mouse.move(box.x + box.width * x, box.y + box.height * y, { steps: 16 })
    await page.mouse.move(box.x + box.width * x + 1, box.y + box.height * y + 1)
    await expect(pane(page, target).locator(`[data-terminal-drop="${direction}"]`)).toBeVisible()
    await evidence(page, `native-drop-overlay-${direction}.png`, true)
  }
  try {
    await startTUI(source)
    await page.keyboard.press("Control+Alt+Shift+ArrowRight")
    await expect.poll(() => creates.length).toBe(1)
    const split = creates[0].id
    await startTUI(split)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(strip.locator("[data-window]")).toHaveCount(1)
    await expect(strip.locator(`[data-window="${split}"]`)).toHaveCount(0)
    await assertNativeGrids(page, streams)
    await evidence(page, "native-split-hidden-from-windows.png")

    await pane(page, split)
      .getByRole("button", { name: /as separate window$/ })
      .click()
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
    await expect(strip.locator("[data-window]")).toHaveCount(2)
    await expect(strip.locator(`[data-window="${split}"]`)).toHaveAttribute("data-active", "true")
    await assertNativeGrids(page, streams)
    await evidence(page, "native-detached-window.png")
    await strip.locator(`[data-window="${source}"]`).getByRole("button").first().click()

    await hoverDrop(split, source, "left")
    for (const direction of ["up", "right", "down"] as const) {
      await hoverDrop(split, source, direction, false)
    }
    await page.mouse.up()
    await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(strip.locator("[data-window]")).toHaveCount(1)
    await assertNativeGrids(page, streams)
    await page.keyboard.type("live-dropped-below")
    await expect.poll(() => streams.get(split)?.output).toContain("Last input: live-dropped-below")
    expect(streams.get(source)?.input).not.toContain("live-dropped-below")
    await evidence(page, "native-dropped-below.png")

    // Detach the original group owner as well, then dock it beside the
    // surviving window. Both shells must keep their live TUI and socket.
    await pane(page, source)
      .getByRole("button", { name: /as separate window$/ })
      .click()
    await expect(strip.locator("[data-window]")).toHaveCount(2)
    await strip.locator(`[data-window="${split}"]`).getByRole("button").first().click()
    await hoverDrop(source, split, "right")
    await page.mouse.up()
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(strip.locator("[data-window]")).toHaveCount(1)
    await assertNativeGrids(page, streams)
    await page.keyboard.type("live-dropped-right")
    await expect.poll(() => streams.get(source)?.output).toContain("Last input: live-dropped-right")
    expect(streams.get(split)?.input).not.toContain("live-dropped-right")
    await evidence(page, "native-dropped-right.png")
    for (const id of [source, split]) expect(streams.get(id)?.attaches).toBe(1)
    expect(creates).toHaveLength(1)
    expect(errors).toEqual([])
  } finally {
    for (const created of creates) {
      await page.request.delete(
        `${ready.url}/api/v1/terminal/${ready.workspaceId}/windows/${created.id}`,
      )
    }
    if (!page.isClosed() && (await pane(page, source).isVisible())) {
      await focusPane(page, source)
      await page.keyboard.press("Control+C")
    }
  }
})
