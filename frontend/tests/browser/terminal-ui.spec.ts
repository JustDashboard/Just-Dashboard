import { mkdir } from "node:fs/promises"
import { join } from "node:path"
import { expect, test, type Locator, type Page, type WebSocketRoute } from "@playwright/test"

test.use({
  video: process.env.JD_TERMINAL_EVIDENCE
    ? { mode: "on", size: { width: 1440, height: 900 } }
    : "off",
})
const TERMINAL_TIMEOUT = process.env.JD_TERMINAL_EVIDENCE ? 90_000 : 60_000
test.setTimeout(TERMINAL_TIMEOUT)

type WindowCreation = {
  id: string
  session: string
  sourceWindowId?: string
  agent?: "codex" | "claude"
}

type Connection = {
  socket: WebSocketRoute
  closed: boolean
  input: string[]
  sizes: { rows: number; cols: number }[]
  /** How many times the page said this window was the one on screen. */
  focus: number
}

async function terminalFixture(
  page: Page,
  renderer: "dom" | "webgl",
  persistenceError?: string,
  redraw = false,
  layouts?: Record<string, unknown> | null,
) {
  const connections = new Map<string, Connection[]>()
  const typed = new Map<string, string>()
  const errors: string[] = []
  const creates: WindowCreation[] = []
  const windows = new Map([
    ["session-a", ["window-a", "window-b"]],
    ["session-b", ["window-c"]],
  ])
  page.on("pageerror", (error) => errors.push(error.message))
  await page.addInitScript(
    ({ renderer, layouts }) => {
      localStorage.setItem("jd.terminal.renderer", renderer)
      localStorage.setItem(
        "jd.view.state",
        JSON.stringify({
          "terminal.rail": true,
          "terminal.tools": false,
          "shell.sidebar": false,
          "terminal.layouts": layouts,
        }),
      )
    },
    { renderer, layouts },
  )
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "")
    if (path === "/auth/session") {
      return route.fulfill({
        json: {
          authenticated: true,
          user: { username: "operator", role: "admin" },
          capabilities: ["read", "terminal", "destructive"],
        },
      })
    }
    if (path === "/terminal/") {
      return route.fulfill({
        json: {
          enabled: true,
          persistent: !persistenceError,
          persistenceError,
          login: { user: "operator", home: "/home/operator", shell: "/bin/bash" },
          folders: [],
          sessions: Array.from(windows, ([id, items]) => ({
            id,
            title: id,
            live: true,
            windows: items.length,
            createdAt: "2026-09-01T00:00:00Z",
            current: { id: items[0], name: items[0] },
          })),
        },
      })
    }
    const match = path.match(/^\/terminal\/([^/]+)\/windows(?:\/([^/]+))?$/)
    if (match) {
      const [, session, id] = match
      if (route.request().method() === "POST") {
        const body = route.request().postDataJSON() as Omit<WindowCreation, "id" | "session">
        const created = { id: `window-new-${creates.length + 1}`, session, ...body }
        creates.push(created)
        windows.set(session, [...(windows.get(session) ?? []), created.id])
        return route.fulfill({ status: 201, json: { id: created.id } })
      }
      if (route.request().method() === "DELETE") {
        windows.set(
          session,
          windows.get(session)!.filter((window) => window !== id),
        )
        return route.fulfill({ json: {} })
      }
      return route.fulfill({
        json: (windows.get(session) ?? []).map((id, index) => ({
          id,
          name: id,
          index,
          cwd: "/home/operator/project",
        })),
      })
    }
    if (/^\/terminal\/[^/]+\/cwd$/.test(path)) {
      return route.fulfill({ json: { cwd: "/home/operator/project" } })
    }
    if (path.startsWith("/terminal/") && route.request().method() === "DELETE") {
      const id = path.split("/").at(-1)!
      windows.delete(id)
      return route.fulfill({ json: {} })
    }
    if (path === "/system/metrics") {
      return route.fulfill({
        status: 503,
        json: { error: { code: "unavailable", message: "Terminal fixture" } },
      })
    }
    return route.fulfill({ json: {} })
  })
  await page.routeWebSocket("**/api/v1/**", (socket) => {
    const id = new URL(socket.url()).pathname.match(/\/terminal\/([^/]+)\/attach$/)?.[1]
    if (!id) return
    const connection: Connection = { socket, closed: false, input: [], sizes: [], focus: 0 }
    connections.set(id, [...(connections.get(id) ?? []), connection])
    socket.onClose(() => {
      connection.closed = true
    })
    socket.onMessage((message) => {
      if (typeof message === "string") {
        const control = JSON.parse(message)
        if (control.type === "resize") {
          connection.sizes.push(control)
          if (redraw) {
            socket.send(
              Buffer.from(
                terminalFrame(
                  id,
                  control.rows,
                  control.cols,
                  creates.find((item) => item.id === id),
                  typed.get(id),
                ),
              ),
            )
          }
        }
        if (control.type === "focus") connection.focus++
      } else {
        const text = Buffer.from(message).toString("utf8")
        connection.input.push(text)
        const grid = connection.sizes.at(-1)
        if (redraw && grid && /^[\x20-\x7e]+$/.test(text)) {
          typed.set(id, (typed.get(id) ?? "") + text)
          const content = `Input: ${typed.get(id)}`.slice(0, Math.max(0, grid.cols - 2))
          socket.send(Buffer.from(`\x1b[${Math.max(2, grid.rows - 2)};2H${content}`))
        }
      }
    })
    // These cells are painted once, like a TUI's unchanged frame. Later output
    // contains only cursor updates and cannot reconstruct them after a remount.
    socket.send(
      Buffer.from(
        `shell history ${id}\r\n\x1b[?1049h\x1b[?25l\x1b[2J\x1b[HSession frame: ${id}\x1b[3;1HReady ▀█\x1b[6n`,
      ),
    )
    const agent = creates.find((item) => item.id === id)?.agent
    if (redraw) {
      socket.send(
        JSON.stringify({
          type: "state",
          data: agent
            ? { busy: true, process: agent, title: agent === "codex" ? "Codex" : "Claude Code" }
            : { busy: false },
        }),
      )
    }
  })
  await page.goto("/terminal")
  await expect(page.locator("[data-terminal-renderer]:visible")).toHaveAttribute(
    "data-terminal-renderer",
    renderer,
    { timeout: 15_000 },
  )
  await expect
    .poll(() => connections.get("window-a")?.[0].input.length, { timeout: 15_000 })
    .toBeGreaterThan(0)
  // Window tabs live in the strip. An unnamed session's rail row carries its
  // current window's label as well, so a page-wide lookup by name finds both.
  const strip = page.getByLabel("Terminal windows")
  return { connections, errors, strip, creates }
}

// A full-screen program repaints for the PTY's reported grid, rather than for
// browser pixels. Drawing its border at that edge exposes stale resize controls.
function terminalFrame(
  id: string,
  rows: number,
  cols: number,
  creation?: WindowCreation,
  input?: string,
) {
  const width = Math.max(2, cols)
  const line = (row: number, text: string) =>
    row < rows ? `\x1b[${row};2H${text.slice(0, Math.max(0, width - 3))}` : ""
  const agent = creation?.agent
  const command = agent === "codex" ? "codex --yolo" : "claude --dangerously-skip-permissions"
  let frame = "\x1b[?25l\x1b[2J\x1b[H\x1b[38;2;110;170;205m"
  frame += "┌" + "─".repeat(width - 2) + "┐"
  for (let row = 2; row < rows; row++) {
    frame += `\x1b[${row};1H│\x1b[${row};${width}H│`
  }
  if (rows > 1) frame += `\x1b[${rows};1H└${"─".repeat(width - 2)}┘`
  frame += "\x1b[0m"
  frame += line(2, agent ? (agent === "codex" ? "Codex" : "Claude Code") : "Project terminal")
  frame += line(4, "/home/operator/project")
  frame += line(6, agent ? `$ ${command}` : "$ bun test src")
  frame += line(8, agent ? "Ready for your next instruction." : "✓ 1,286 tests passed")
  frame += line(10, `PTY ${cols} columns × ${rows} rows`)
  frame += line(12, id)
  frame += line(14, "Unicode: ✓ ▀█ ━┳━")
  if (input) frame += line(Math.max(2, rows - 2), `Input: ${input}`)
  return frame
}

test("explains unavailable restart protection while keeping existing terminals usable", async ({
  page,
}) => {
  const persistenceError = "The host is not running systemd"
  const { connections, errors } = await terminalFixture(page, "dom", persistenceError)
  await expect(
    page.getByRole("alert").filter({ hasText: "New terminals are unavailable" }),
  ).toContainText(persistenceError)
  await expect(
    page.getByText("Existing sessions keep running on the server.", { exact: false }),
  ).toBeVisible()
  expect(connections.get("window-a")?.[0].closed).toBe(false)

  await page.route("**/api/v1/terminal/", (route) =>
    route.fulfill({
      json: {
        enabled: true,
        persistent: false,
        persistenceError,
        login: { user: "operator", home: "/home/operator", shell: "/bin/bash" },
        sessions: [],
      },
    }),
  )
  await page.reload()
  await expect(
    page.getByText("Restore restart protection before opening a terminal."),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Open session", exact: true })).toBeDisabled()
  expect(errors).toEqual([])
})

async function scrollback(page: Page) {
  await page.getByRole("button", { name: "Terminal actions", exact: true }).click()
  const download = page.waitForEvent("download")
  await page.getByRole("menuitem", { name: "Save scrollback" }).click()
  const stream = await (await download).createReadStream()
  const chunks: Buffer[] = []
  for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
  await expect(page.getByRole("menuitem", { name: "Save scrollback" })).not.toBeVisible()
  await expect(page.getByRole("button", { name: "Terminal actions", exact: true })).toBeFocused()
  // Export notifications overlap the terminal and would change its screenshot.
  const notice = page.locator("[data-sonner-toast]").filter({ hasText: /Saved [\d,]+ lines/ })
  await notice.getByRole("button", { name: "Close toast", exact: true }).click()
  await expect(notice).toHaveCount(0)
  return Buffer.concat(chunks).toString("utf8")
}

for (const renderer of ["dom", "webgl"] as const) {
  test(`preserves busy terminal windows and sessions with ${renderer}`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, errors, strip } = await terminalFixture(page, renderer)
    const first = connections.get("window-a")![0]
    const originalSize = first.sizes.at(-1)
    first.socket.send(Buffer.from("\x1b[5;1H\x1b[38;2;"))
    await strip.getByRole("button", { name: "window-b", exact: true }).click()
    await expect(page.locator(".xterm-screen")).toHaveCount(2)
    await expect(page.locator(".xterm-helper-textarea:visible")).toBeFocused()
    expect(first.closed).toBe(false)

    // Cross the server's 128 KiB replay limit while hidden, including split ANSI
    // and UTF-8 sequences. A background terminal must keep parsing and answering.
    const unicode = Buffer.from("42;200;100mBackground complete ✓\x1b[0m")
    const split = unicode.indexOf(Buffer.from("✓")) + 1
    first.socket.send(unicode.subarray(0, split))
    first.socket.send(unicode.subarray(split))
    first.socket.send(Buffer.from("\x1b[0m".repeat(40_000) + "\x1b[6n"))
    await expect.poll(() => first.input.length).toBe(2)
    await page.setViewportSize({ width: 1200, height: 800 })
    await expect
      .poll(() => connections.get("window-b")?.[0].sizes.at(-1)?.cols)
      .not.toBe(originalSize?.cols)
    expect(first.sizes.at(-1)).toEqual(originalSize)

    await strip.getByRole("button", { name: "window-a", exact: true }).click()
    await expect(page.locator(".xterm-helper-textarea:visible")).toBeFocused()
    await expect.poll(() => first.sizes.at(-1)?.cols).not.toBe(originalSize?.cols)
    expect(connections.get("window-a")).toHaveLength(1)
    const output = await scrollback(page)
    expect(output).toContain("Session frame: window-a")
    expect(output).toContain("Ready ▀█")
    expect(output).toContain("Background complete ✓")

    await page.locator(".xterm-helper-textarea:visible").focus()
    await page.keyboard.type("only-a")
    await expect.poll(() => first.input.join("")).toContain("only-a")
    expect(connections.get("window-b")![0].input.join("")).not.toContain("only-a")

    const screen = page.locator(".xterm-screen:visible")
    await page.mouse.move(0, 0)
    const before = await screen.screenshot()
    await page.locator('[data-session="session-b"]').getByRole("button").first().click()
    await expect(page.locator(".xterm-screen")).toHaveCount(3)
    await expect(page.locator(".xterm-helper-textarea:visible")).toBeFocused()
    await page.keyboard.type("only-c")
    await expect.poll(() => connections.get("window-c")![0].input.join("")).toContain("only-c")
    expect(first.input.join("")).not.toContain("only-c")
    await page.locator('[data-session="session-a"]').getByRole("button").first().click()
    await expect(page.locator(".xterm-helper-textarea:visible")).toBeFocused()
    expect(connections.get("window-a")).toHaveLength(1)
    expect(await scrollback(page)).toBe(output)
    await page.locator(".xterm-helper-textarea:visible").focus()
    await page.mouse.move(0, 0)
    await expect.poll(async () => (await screen.screenshot()).equals(before)).toBe(true)

    // Background panes keep their sockets until their window/session is closed.
    await strip.getByRole("button", { name: "Close window window-b", exact: true }).click()
    await expect(page.locator(".xterm-screen")).toHaveCount(2)
    await expect.poll(() => connections.get("window-b")![0].closed).toBe(true)
    await page.locator('[data-session="session-b"]').getByRole("button", { name: /close/i }).click()
    await expect(page.locator(".xterm-screen")).toHaveCount(1)
    await expect.poll(() => connections.get("window-c")![0].closed).toBe(true)
    const replies = first.input.length
    first.socket.send(Buffer.from("\x1b[?1049l\x1b[6n"))
    await expect.poll(() => first.input.length).toBeGreaterThan(replies)
    expect(await scrollback(page)).toContain("shell history window-a")
    await page.getByRole("link", { name: "Overview", exact: true }).click()
    await expect(page.locator(".xterm-screen")).toHaveCount(0)
    await expect.poll(() => first.closed).toBe(true)
    expect(errors).toEqual([])
  })
}

// A tab is named after what its shell is doing and marked while something
// runs; the rail names a session after the window it is on. And the page
// remembers which session and window were on screen across a navigation.
test("names windows after their work and remembers where you were", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { connections, errors, strip } = await terminalFixture(page, "dom")
  const first = connections.get("window-a")![0]
  const tabA = page.locator('[data-window="window-a"]')
  const rowA = page.locator('[data-session="session-a"]')
  const state = (data: Record<string, unknown>) =>
    first.socket.send(JSON.stringify({ type: "state", data }))

  // A program that set no title, open but doing nothing: the tab and the rail
  // show its name, and their dots say idle.
  state({ busy: true, process: "claude" })
  await expect(tabA).toHaveAttribute("data-busy", "true")
  await expect(tabA.getByRole("button", { name: "claude", exact: true })).toBeVisible()
  await expect(rowA).toContainText("claude")
  await expect(tabA).not.toHaveAttribute("data-working", "true")
  await expect(rowA).not.toHaveAttribute("data-working", "true")
  await expect(tabA.locator("[data-activity=idle] > span")).toHaveClass(/bg-warning/)
  await expect(rowA.locator("[data-activity=idle] > span")).toHaveClass(/bg-warning/)
  // The dot sits at the end of the tab, beside its close, where the rail puts
  // it on a row.
  const dot = (await tabA.locator("[data-activity]").boundingBox())!
  const name = (await tabA.getByText("claude", { exact: true }).boundingBox())!
  const close = (await tabA.getByRole("button", { name: /^Close window/ }).boundingBox())!
  expect(dot.x).toBeGreaterThanOrEqual(name.x + name.width)
  expect(dot.x + dot.width).toBeLessThanOrEqual(close.x)
  // A session is opened and closed, and that is all: nothing to file it in,
  // rename it or pin it with.
  await expect(page.getByRole("button", { name: "New folder" })).toHaveCount(0)
  await expect(rowA.getByRole("button", { name: /^More for/ })).toHaveCount(0)
  await expect(tabA.getByRole("button", { name: /^Rename/ })).toHaveCount(0)
  await tabA.getByRole("button", { name: "claude", exact: true }).dblclick()
  await expect(page.getByLabel("Terminal windows").locator("input")).toHaveCount(0)
  // It names itself and starts working: the title wins over the process, the
  // glyph the agent spins in front of its name is dropped — the mark says
  // that — and the tab and the row are marked.
  state({ busy: true, process: "claude", title: "✳ Claude Code", working: true })
  // The mark's label joins the button's accessible name, which is what a
  // screen reader should hear.
  await expect(tabA.getByRole("button", { name: "Claude Code Working", exact: true })).toBeVisible()
  await expect(tabA).not.toContainText("✳")
  await expect(rowA).toContainText("Claude Code")
  await expect(rowA).not.toContainText("✳")
  await expect(tabA).toHaveAttribute("data-working", "true")
  await expect(rowA).toHaveAttribute("data-working", "true")
  await expect(tabA.getByRole("img", { name: "Working" })).toBeVisible()
  // Working is a still green dot: nothing on the mark moves.
  await expect(tabA.locator("[data-activity=working] > span")).toHaveClass(/bg-success/)
  await expect(tabA.locator("[data-activity] .animate-breathe")).toHaveCount(0)
  // It stops: the tab and the row go straight back to idle — there is no
  // "finished" state lingering in green — and the tab does not change width.
  const workingWidth = (await tabA.boundingBox())!.width
  state({ busy: true, process: "claude", title: "✳ Claude Code", finishedAt: Date.now() })
  await expect(tabA).not.toHaveAttribute("data-working", "true")
  await expect(rowA).not.toHaveAttribute("data-working", "true")
  await expect(tabA.locator("[data-activity=idle] > span")).toHaveClass(/bg-warning/)
  await expect(rowA.locator("[data-activity=idle] > span")).toHaveClass(/bg-warning/)
  await expect(tabA.locator("[data-activity] svg")).toHaveCount(0)
  expect((await tabA.boundingBox())!.width).toBe(workingWidth)
  // Back at the prompt, which titles the window after its directory.
  state({ busy: false, title: "~" })
  await expect(tabA).not.toHaveAttribute("data-busy", "true")
  await expect(tabA.getByRole("button", { name: "~", exact: true })).toBeVisible()
  await expect(rowA).toContainText("~")

  // Pick window-b, leave the page, come back: window-b is still the one on
  // screen, and the server was told which window that was.
  await strip.getByRole("button", { name: "window-b", exact: true }).click()
  await expect(page.locator('[data-window="window-b"]')).toHaveAttribute("data-active", "true")
  await expect.poll(() => connections.get("window-b")?.[0].focus).toBeGreaterThan(0)
  await page.getByRole("link", { name: "Overview", exact: true }).click()
  await expect(page.locator(".xterm-screen")).toHaveCount(0)
  await page.getByRole("link", { name: "Terminal", exact: true }).click()
  await expect(page.locator('[data-window="window-b"]')).toHaveAttribute("data-active", "true")
  await expect(page.locator(".xterm-helper-textarea:visible")).toBeFocused()

  // Same for the session.
  await page.locator('[data-session="session-b"]').getByRole("button").first().click()
  await expect(page.locator('[data-session="session-b"]')).toHaveAttribute("data-active", "true")
  await page.getByRole("link", { name: "Overview", exact: true }).click()
  await expect(page.locator(".xterm-screen")).toHaveCount(0)
  await page.getByRole("link", { name: "Terminal", exact: true }).click()
  await expect(page.locator('[data-session="session-b"]')).toHaveAttribute("data-active", "true")
  await expect(page.locator('[data-window="window-c"]')).toHaveAttribute("data-active", "true")
  expect(errors).toEqual([])
})

// A window whose socket in this browser drops is red on its tab and on its
// session's row, goes back to its session by itself — the session is still
// running on the server — and stops being red once it is attached again.
test("a dropped connection is red until it is back", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { connections, errors } = await terminalFixture(page, "dom")
  const tabA = page.locator('[data-window="window-a"]')
  const rowA = page.locator('[data-session="session-a"]')

  await page.clock.install({ time: new Date("2026-10-04T00:00:00Z") })
  await page.clock.pauseAt(new Date("2026-10-04T01:00:00Z"))
  connections.get("window-a")![0].socket.close()
  await expect(tabA).toHaveAttribute("data-disconnected", "true")
  await expect(rowA).toHaveAttribute("data-disconnected", "true")
  await expect(tabA.getByRole("img", { name: "Disconnected" })).toBeVisible()
  await expect(tabA.locator("[data-activity=disconnected] > span")).toHaveClass(/bg-destructive/)
  await expect(page.locator('[data-session="session-b"]')).not.toHaveAttribute(
    "data-disconnected",
    "true",
  )

  await expect(
    page.getByText("Connection lost. The session is still running on the server — reconnecting."),
  ).toBeVisible()
  // Nobody presses anything. The server sends the window's state the moment a
  // socket attaches.
  await page.clock.runFor(1100)
  await page.clock.resume()
  await expect.poll(() => connections.get("window-a")?.length).toBe(2)
  connections
    .get("window-a")![1]
    .socket.send(JSON.stringify({ type: "state", data: { busy: false, title: "~" } }))
  await expect(tabA).not.toHaveAttribute("data-disconnected", "true")
  await expect(rowA).not.toHaveAttribute("data-disconnected", "true")
  expect(errors).toEqual([])
})

const terminalPane = (page: Page, id: string) => page.locator(`[data-terminal-window="${id}"]`)

async function focusTerminal(page: Page, id: string) {
  const pane = terminalPane(page, id)
  await pane.locator(".xterm-screen").click({ position: { x: 30, y: 30 } })
  await expect(pane).toHaveAttribute("data-focused", "true")
  await expect(pane.locator(".xterm-helper-textarea")).toBeFocused()
}

async function splitTerminal(page: Page, direction: "left" | "right" | "up" | "down") {
  await page.getByRole("button", { name: "Split terminal", exact: true }).click()
  await page.getByRole("menuitem", { name: `Split ${direction}`, exact: true }).click()
}

async function assertTerminalGrids(page: Page, connections: Map<string, Connection[]>) {
  const panes = page.locator("[data-terminal-window]:visible")
  expect(await panes.count()).toBeGreaterThan(0)
  const renderer = await panes
    .first()
    .locator("[data-terminal-renderer]")
    .getAttribute("data-terminal-renderer")
  for (const pane of await panes.all()) {
    const id = (await pane.getAttribute("data-terminal-window"))!
    const host = pane.locator("[data-terminal-renderer]")
    await expect(host).toHaveAttribute("data-terminal-renderer", renderer!)
    await expect
      .poll(async () => {
        const grid = await host.evaluate((element) => ({
          rows: Number(element.getAttribute("data-terminal-rows")),
          cols: Number(element.getAttribute("data-terminal-cols")),
        }))
        const reported = connections.get(id)?.at(-1)?.sizes.at(-1)
        return (
          grid.rows > 0 &&
          grid.cols > 0 &&
          reported?.rows === grid.rows &&
          reported.cols === grid.cols
        )
      })
      .toBe(true)
    await expect
      .poll(async () => {
        const hostBox = await host.boundingBox()
        const screenBox = await pane.locator(".xterm-screen").boundingBox()
        return Boolean(
          hostBox &&
          screenBox &&
          screenBox.x >= hostBox.x - 1 &&
          screenBox.y >= hostBox.y - 1 &&
          screenBox.x + screenBox.width <= hostBox.x + hostBox.width + 1 &&
          screenBox.y + screenBox.height <= hostBox.y + hostBox.height + 1,
        )
      })
      .toBe(true)
    for (const canvas of await pane.locator(".xterm-screen canvas").all()) {
      await expect
        .poll(async () =>
          canvas.evaluate((element) => {
            const screen = element.closest(".xterm-screen")!.getBoundingClientRect()
            const box = element.getBoundingClientRect()
            return (
              box.width > 0 &&
              box.height > 0 &&
              Math.abs(box.width - screen.width) < 2 &&
              Math.abs(box.height - screen.height) < 2
            )
          }),
        )
        .toBe(true)
    }
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  )
}

async function dragDivider(page: Page, divider: Locator, dx: number, dy: number) {
  const box = (await divider.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { steps: 8 })
  await page.mouse.up()
}

async function splitEvidence(page: Page, filename: string) {
  const directory = process.env.JD_TERMINAL_EVIDENCE
  if (!directory) return
  await mkdir(directory, { recursive: true })
  await page.mouse.move(0, 0)
  await page.screenshot({ path: join(directory, filename), animations: "disabled" })
}

async function hoverWindowDrop(
  page: Page,
  source: string,
  target: string,
  direction: "left" | "right" | "up" | "down",
  start = true,
) {
  await expect(terminalPane(page, target)).toBeVisible()
  if (start) {
    const tab = (await page.locator(`[data-window="${source}"]`).boundingBox())!
    await page.mouse.move(tab.x + tab.width / 3, tab.y + tab.height / 2)
    await page.mouse.down()
    await page.mouse.move(tab.x + tab.width / 3 + 12, tab.y + tab.height / 2 + 12, {
      steps: 4,
    })
  }
  const pane = (await terminalPane(page, target).boundingBox())!
  const x = direction === "left" ? 0.1 : direction === "right" ? 0.9 : 0.5
  const y = direction === "up" ? 0.1 : direction === "down" ? 0.9 : 0.5
  await page.mouse.move(pane.x + pane.width * x, pane.y + pane.height * y, { steps: 12 })
  // Some browsers only begin sending dragover after the next pointer move.
  await page.mouse.move(pane.x + pane.width * x + 1, pane.y + pane.height * y + 1)
}

async function dragEvidence(page: Page, name: string) {
  const directory = process.env.JD_TERMINAL_EVIDENCE
  if (!directory) return
  await mkdir(directory, { recursive: true })
  await page.screenshot({ path: join(directory, name), animations: "disabled" })
  await page.waitForTimeout(650)
}

test.describe("terminal splits", () => {
  test("rejects same-group and undersized drops and ignores unrelated drag types", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors, strip } = await terminalFixture(page, "dom")
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await hoverWindowDrop(page, "window-a", creates[0].id, "left")
    await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
    await page.mouse.up()
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)

    await page.getByRole("button", { name: "Hide the sessions rail", exact: true }).click()
    await page.setViewportSize({ width: 650, height: 844 })
    await hoverWindowDrop(page, "window-b", "window-a", "right")
    const overlay = page.locator('[data-terminal-drop="right"]')
    await expect(overlay).toBeVisible()
    await expect(overlay).toHaveAttribute("data-drop-blocked", "true")
    await expect(overlay).toContainText("Not enough space")
    await page.mouse.up()
    await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(strip.locator('[data-window="window-b"]')).toHaveCount(1)

    const transfer = await page.evaluateHandle(() => {
      const data = new DataTransfer()
      data.setData("text/plain", "window-b")
      return data
    })
    await terminalPane(page, "window-a").dispatchEvent("dragover", { dataTransfer: transfer })
    await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
    await terminalPane(page, "window-a").dispatchEvent("drop", { dataTransfer: transfer })
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    expect(connections.get("window-a")).toHaveLength(1)
    expect(creates).toHaveLength(1)
    expect(errors).toEqual([])
  })

  test("recovers a null saved layout before opening a split", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors } = await terminalFixture(
      page,
      "webgl",
      undefined,
      true,
      null,
    )
    await expect(terminalPane(page, "window-a").locator(".xterm-helper-textarea")).toBeFocused()
    await page.getByRole("button", { name: "Split terminal", exact: true }).click()
    await expect(page.getByRole("menuitem", { name: "Split right", exact: true })).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(terminalPane(page, "window-a").locator(".xterm-helper-textarea")).toBeFocused()
    expect(creates).toHaveLength(0)
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await assertTerminalGrids(page, connections)
    expect(connections.get("window-a")).toHaveLength(1)
    expect(errors).toEqual([])
  })

  test("keeps a divider drag when a pending split is created before pointer release", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors } = await terminalFixture(page, "webgl", undefined, true)
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    let releaseCreation = () => {}
    const released = new Promise<void>((resolve) => {
      releaseCreation = resolve
    })
    let held = false
    await page.route("**/api/v1/terminal/session-a/windows", async (route) => {
      if (route.request().method() !== "POST") return route.fallback()
      held = true
      await released
      return route.fallback()
    })
    await splitTerminal(page, "down")
    await expect.poll(() => held).toBe(true)
    const divider = page.getByRole("separator", { name: "Terminal split width", exact: true })
    const box = (await divider.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + 80, box.y + box.height / 2, { steps: 8 })
    await expect
      .poll(async () => Number(await divider.getAttribute("aria-valuenow")))
      .toBeGreaterThan(55)
    const draggedRatio = await divider.getAttribute("aria-valuenow")
    releaseCreation()
    await expect.poll(() => creates.length).toBe(2)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
    await expect(divider).toHaveAttribute("aria-valuenow", draggedRatio!)
    await page.mouse.up()
    await expect(divider).toHaveAttribute("aria-valuenow", draggedRatio!)
    await assertTerminalGrids(page, connections)
    for (const id of ["window-a", ...creates.map((item) => item.id)])
      expect(connections.get(id)).toHaveLength(1)
    await splitEvidence(page, "split-during-divider-drag.png")
    expect(errors).toEqual([])
  })

  test("ignores an older in-flight listing after creating a split", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors } = await terminalFixture(page, "webgl", undefined, true)
    let releaseListing = () => {}
    const released = new Promise<void>((resolve) => {
      releaseListing = resolve
    })
    let holdNextRead = true
    let held = false
    let reads = 0
    await page.route("**/api/v1/terminal/session-a/windows", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      reads++
      if (!holdNextRead) return route.fallback()
      holdNextRead = false
      held = true
      await released
      return route.fulfill({
        headers: { "X-Terminal-Stale-Listing": "1" },
        json: ["window-a", "window-b"].map((id, index) => ({
          id,
          name: id,
          index,
          cwd: "/home/operator/project",
        })),
      })
    })
    await expect.poll(() => held, { timeout: 10_000 }).toBe(true)
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    const created = creates[0].id
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(terminalPane(page, created).locator(".xterm-helper-textarea")).toBeFocused()
    await expect.poll(() => reads).toBeGreaterThan(1)
    const staleResponse = page.waitForResponse(
      (response) => response.headers()["x-terminal-stale-listing"] === "1",
    )
    const readsBeforeRelease = reads
    releaseListing()
    await (await staleResponse).finished()
    // Wait through the following authoritative poll as well: a stale response
    // must never briefly unmount the pane and reconstruct it from byte replay.
    await expect.poll(() => reads, { timeout: 10_000 }).toBeGreaterThan(readsBeforeRelease)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(terminalPane(page, created)).toHaveAttribute("data-focused", "true")
    expect(creates).toHaveLength(1)
    expect(connections.get("window-a")).toHaveLength(1)
    expect(connections.get(created)).toHaveLength(1)
    await assertTerminalGrids(page, connections)
    expect(errors).toEqual([])
  })

  test("keeps a successfully created split when the following window listing fails", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors } = await terminalFixture(page, "webgl", undefined, true)
    let recovered = false
    let failedReads = 0
    let healthyReads = 0
    await page.route("**/api/v1/terminal/session-a/windows", async (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      if (recovered) {
        healthyReads++
        return route.fallback()
      }
      failedReads++
      return route.fulfill({
        status: 503,
        json: { error: { code: "unavailable", message: "Transient window listing failure" } },
      })
    })
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    await expect.poll(() => failedReads).toBeGreaterThan(0)
    const created = creates[0].id
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(terminalPane(page, created)).toHaveAttribute("data-focused", "true")
    await expect(terminalPane(page, created).locator(".xterm-helper-textarea")).toBeFocused()
    await assertTerminalGrids(page, connections)
    recovered = true
    await expect.poll(() => healthyReads, { timeout: 10_000 }).toBeGreaterThan(0)
    await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
    await expect(terminalPane(page, created)).toHaveAttribute("data-focused", "true")
    expect(creates).toHaveLength(1)
    expect(connections.get("window-a")).toHaveLength(1)
    expect(connections.get(created)).toHaveLength(1)
    await splitEvidence(page, "split-after-transient-listing-failure.png")
    expect(errors).toEqual([])
  })

  for (const renderer of ["dom", "webgl"] as const) {
    test(`detaches panes and drags windows into dynamic split zones with ${renderer}`, async ({
      page,
    }) => {
      test.setTimeout(process.env.JD_TERMINAL_EVIDENCE ? 180_000 : 90_000)
      await page.setViewportSize({ width: 1440, height: 900 })
      const { connections, creates, errors, strip } = await terminalFixture(
        page,
        renderer,
        undefined,
        true,
      )
      await strip.getByRole("button", { name: "window-b", exact: true }).click()
      await expect(terminalPane(page, "window-b").locator(".xterm-helper-textarea")).toBeFocused()
      await strip.getByRole("button", { name: "window-a", exact: true }).click()
      await splitTerminal(page, "right")
      await expect.poll(() => creates.length).toBe(1)
      const created = creates[0].id
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
      await expect(strip.locator("[data-window]")).toHaveCount(2)
      await expect(strip.locator(`[data-window="${created}"]`)).toHaveCount(0)
      for (const id of ["window-a", created]) {
        await expect(
          terminalPane(page, id).getByRole("button", { name: /as separate window$/ }),
        ).toBeVisible()
        await expect(
          terminalPane(page, id).getByRole("button", { name: /^Close pane/ }),
        ).toBeVisible()
      }
      await dragEvidence(page, `split-panes-hidden-from-windows-${renderer}.png`)
      await terminalPane(page, created)
        .getByRole("button", { name: /as separate window$/ })
        .click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
      await expect(strip.locator("[data-window]")).toHaveCount(3)
      await expect(strip.locator(`[data-window="${created}"]`)).toHaveAttribute(
        "data-active",
        "true",
      )
      await expect(terminalPane(page, created).locator(".xterm-helper-textarea")).toBeFocused()
      await dragEvidence(page, `detached-separate-window-${renderer}.png`)

      for (const direction of ["left", "right", "up", "down"] as const) {
        await strip.getByRole("button", { name: "window-a", exact: true }).click()
        await hoverWindowDrop(page, "window-b", "window-a", direction)
        const preview = terminalPane(page, "window-a").locator(
          `[data-terminal-drop="${direction}"]`,
        )
        await expect(preview).toBeVisible()
        await expect(preview).toHaveAttribute("data-drop-blocked", "false")
        const box = (await terminalPane(page, "window-a").boundingBox())!
        const overlay = (await preview.boundingBox())!
        expect(overlay.width).toBeCloseTo(
          box.width * (direction === "left" || direction === "right" ? 0.5 : 1),
          0,
        )
        expect(overlay.height).toBeCloseTo(
          box.height * (direction === "up" || direction === "down" ? 0.5 : 1),
          0,
        )
        await dragEvidence(page, `drop-overlay-${direction}-${renderer}.png`)
        await page.mouse.up()
        await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
        await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
        await expect(strip.locator('[data-window="window-b"]')).toHaveCount(0)
        await expect(strip.locator('[data-window="window-a"]')).toHaveAttribute(
          "data-active",
          "true",
        )
        await assertTerminalGrids(page, connections)
        const a = (await terminalPane(page, "window-a").boundingBox())!
        const b = (await terminalPane(page, "window-b").boundingBox())!
        if (direction === "left") expect(b.x + b.width).toBeLessThanOrEqual(a.x)
        if (direction === "right") expect(a.x + a.width).toBeLessThanOrEqual(b.x)
        if (direction === "up") expect(b.y + b.height).toBeLessThanOrEqual(a.y)
        if (direction === "down") expect(a.y + a.height).toBeLessThanOrEqual(b.y)
        await expect(terminalPane(page, "window-b").locator(".xterm-helper-textarea")).toBeFocused()
        await page.keyboard.type(`dropped-${direction}`)
        await expect
          .poll(() => connections.get("window-b")![0].input.join(""))
          .toContain(`dropped-${direction}`)
        expect(connections.get("window-a")![0].input.join("")).not.toContain(`dropped-${direction}`)
        await dragEvidence(page, `dropped-split-${direction}-${renderer}.png`)
        await terminalPane(page, "window-b")
          .getByRole("button", { name: /as separate window$/ })
          .click()
      }

      await strip.getByRole("button", { name: "window-a", exact: true }).click()
      await hoverWindowDrop(page, "window-b", "window-a", "left")
      for (const direction of ["up", "right", "down"] as const) {
        await hoverWindowDrop(page, "window-b", "window-a", direction, false)
        await expect(page.locator(`[data-terminal-drop="${direction}"]`)).toBeVisible()
        await dragEvidence(page, `dynamic-hover-${direction}-${renderer}.png`)
      }
      await page.keyboard.press("Escape")
      await page.mouse.up()
      await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)

      // Moving the pointer out of the canvas removes the preview; dropping on
      // the title strip must not accidentally reuse the last terminal zone.
      await hoverWindowDrop(page, "window-b", "window-a", "right")
      await expect(page.locator("[data-terminal-drop]")).toBeVisible()
      await page.mouse.move(20, 10, { steps: 10 })
      await expect(page.locator("[data-terminal-drop]")).toHaveCount(0)
      await page.mouse.up()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)

      // A menu provides the same placement without a dragging gesture.
      await page.getByRole("button", { name: "Split terminal", exact: true }).click()
      await page.getByRole("menuitem", { name: "window-b", exact: true }).hover()
      const submenu = page.locator('[data-slot="dropdown-menu-sub-content"][data-state="open"]')
      await expect(submenu).toBeVisible()
      await submenu.getByRole("menuitem", { name: "Split right", exact: true }).click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
      const before = (await terminalPane(page, "window-a").boundingBox())!
      await hoverWindowDrop(page, created, "window-b", "down")
      await expect(
        terminalPane(page, "window-b").locator('[data-terminal-drop="down"]'),
      ).toBeVisible()
      await dragEvidence(page, `nested-drop-overlay-${renderer}.png`)
      await page.mouse.up()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
      await expect(strip.locator("[data-window]")).toHaveCount(1)
      expect((await terminalPane(page, "window-a").boundingBox())!).toEqual(before)
      await assertTerminalGrids(page, connections)
      await dragEvidence(page, `nested-dropped-split-${renderer}.png`)

      // The group owner can also detach; a surviving pane becomes its tab.
      await terminalPane(page, "window-a")
        .getByRole("button", { name: /as separate window$/ })
        .click()
      await expect(strip.locator("[data-window]")).toHaveCount(2)
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
      await strip.getByRole("button", { name: "window-b", exact: true }).click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
      await assertTerminalGrids(page, connections)
      for (const id of ["window-a", "window-b", created]) {
        expect(connections.get(id)).toHaveLength(1)
        expect(connections.get(id)![0].closed).toBe(false)
      }
      expect(creates).toHaveLength(1)
      expect(errors).toEqual([])
    })

    test(`splits in all four directions without remounting the shell with ${renderer}`, async ({
      page,
    }) => {
      test.setTimeout(TERMINAL_TIMEOUT)
      await page.setViewportSize({ width: 1440, height: 900 })
      const { connections, creates, errors } = await terminalFixture(
        page,
        renderer,
        undefined,
        true,
      )
      const original = connections.get("window-a")![0]
      const originalSize = original.sizes.at(-1)!
      await expect(page.getByRole("button", { name: /^Search scrollback/ })).toHaveCount(0)
      await expect(page.getByRole("button", { name: /snippets|terminal behavior/i })).toHaveCount(0)

      for (const direction of ["left", "right", "up", "down"] as const) {
        await focusTerminal(page, "window-a")
        const previousCreates = creates.length
        await splitTerminal(page, direction)
        await expect.poll(() => creates.length).toBe(previousCreates + 1)
        const created = creates.at(-1)!
        expect(created.sourceWindowId).toBe("window-a")
        expect(created.agent).toBeUndefined()
        await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
        await expect(terminalPane(page, created.id)).toHaveAttribute("data-focused", "true")
        await assertTerminalGrids(page, connections)
        const firstBox = (await terminalPane(page, "window-a").boundingBox())!
        const addedBox = (await terminalPane(page, created.id).boundingBox())!
        if (direction === "left")
          expect(addedBox.x + addedBox.width).toBeLessThanOrEqual(firstBox.x)
        if (direction === "right")
          expect(firstBox.x + firstBox.width).toBeLessThanOrEqual(addedBox.x)
        if (direction === "up") expect(addedBox.y + addedBox.height).toBeLessThanOrEqual(firstBox.y)
        if (direction === "down")
          expect(firstBox.y + firstBox.height).toBeLessThanOrEqual(addedBox.y)
        await expect.poll(() => original.sizes.at(-1)).not.toEqual(originalSize)
        expect(connections.get("window-a")).toHaveLength(1)
        expect(original.closed).toBe(false)

        await focusTerminal(page, "window-a")
        const text = `original-${direction}`
        await page.keyboard.type(text)
        await expect.poll(() => original.input.join("")).toContain(text)
        expect(connections.get(created.id)![0].input.join("")).not.toContain(text)
        await splitEvidence(page, `split-${direction}-${renderer}.png`)
        await page
          .locator(`[data-terminal-window="${created.id}"]`)
          .getByRole("button", { name: /^Close pane/ })
          .click()
        await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
        await expect.poll(() => connections.get(created.id)![0].closed).toBe(true)
        await expect.poll(() => original.sizes.at(-1)).toEqual(originalSize)
      }
      expect(errors).toEqual([])
    })

    test(`resizes nested panes and routes keyboard focus with ${renderer}`, async ({ page }) => {
      test.setTimeout(process.env.JD_TERMINAL_EVIDENCE ? 300_000 : 120_000)
      await page.setViewportSize({ width: 1440, height: 900 })
      const { connections, creates, errors, strip } = await terminalFixture(
        page,
        renderer,
        undefined,
        true,
      )
      const original = connections.get("window-a")![0]
      await focusTerminal(page, "window-a")
      const inputBeforeSplit = original.input.join("")
      await page.keyboard.press("Control+Alt+Shift+ArrowRight")
      await expect.poll(() => creates.length).toBe(1)
      const top = creates[0].id
      await expect(terminalPane(page, top)).toHaveAttribute("data-focused", "true")
      await page.keyboard.press("Control+Alt+Shift+ArrowDown")
      await expect.poll(() => creates.length).toBe(2)
      const bottom = creates[1].id
      expect(creates[1].sourceWindowId).toBe(top)
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
      await expect(terminalPane(page, bottom).locator(".xterm-helper-textarea")).toBeFocused()
      expect(original.input.join("")).toBe(inputBeforeSplit)
      await assertTerminalGrids(page, connections)

      const width = page.getByRole("separator", { name: "Terminal split width", exact: true })
      const height = page.getByRole("separator", { name: "Terminal split height", exact: true })
      const originalWidth = (await terminalPane(page, "window-a").boundingBox())!.width
      const originalHeight = (await terminalPane(page, top).boundingBox())!.height
      await dragDivider(page, width, 80, 0)
      await expect
        .poll(async () => (await terminalPane(page, "window-a").boundingBox())!.width)
        .toBeGreaterThan(originalWidth + 50)
      await assertTerminalGrids(page, connections)
      await dragDivider(page, height, 0, 65)
      await expect
        .poll(async () => (await terminalPane(page, top).boundingBox())!.height)
        .toBeGreaterThan(originalHeight + 40)
      await assertTerminalGrids(page, connections)
      await splitEvidence(page, `nested-resized-${renderer}.png`)

      await width.focus()
      const beforeArrow = Number(await width.getAttribute("aria-valuenow"))
      await page.keyboard.press("ArrowLeft")
      await expect
        .poll(async () => Number(await width.getAttribute("aria-valuenow")))
        .toBeLessThan(beforeArrow)
      await page.keyboard.press("Home")
      await expect(width).toHaveAttribute("aria-valuenow", "50")
      await height.focus()
      const beforeHeightArrow = Number(await height.getAttribute("aria-valuenow"))
      await page.keyboard.press("ArrowUp")
      await expect
        .poll(async () => Number(await height.getAttribute("aria-valuenow")))
        .toBeLessThan(beforeHeightArrow)
      await height.dblclick()
      await expect(height).toHaveAttribute("aria-valuenow", "50")
      await assertTerminalGrids(page, connections)

      for (const id of ["window-a", top, bottom]) {
        await focusTerminal(page, id)
        const text = `typed-in-${id}`
        await page.keyboard.type(text)
        await expect.poll(() => connections.get(id)![0].input.join("")).toContain(text)
        for (const other of ["window-a", top, bottom].filter((other) => other !== id)) {
          expect(connections.get(other)![0].input.join("")).not.toContain(text)
        }
      }
      const inputBeforeFocus = ["window-a", top, bottom].map((id) =>
        connections.get(id)![0].input.join(""),
      )
      await page.keyboard.press("Control+Alt+KeyI")
      await expect(terminalPane(page, top).locator(".xterm-helper-textarea")).toBeFocused()
      await page.keyboard.press("Control+Alt+KeyH")
      await expect(terminalPane(page, "window-a").locator(".xterm-helper-textarea")).toBeFocused()
      await page.keyboard.press("Control+Alt+KeyL")
      await expect(terminalPane(page, top).locator(".xterm-helper-textarea")).toBeFocused()
      await page.keyboard.press("Control+Alt+KeyK")
      await expect(terminalPane(page, bottom).locator(".xterm-helper-textarea")).toBeFocused()
      await page.keyboard.press("Control+Alt+KeyP")
      await expect(terminalPane(page, "window-a").locator(".xterm-helper-textarea")).toBeFocused()
      expect(["window-a", top, bottom].map((id) => connections.get(id)![0].input.join(""))).toEqual(
        inputBeforeFocus,
      )

      await page.getByRole("button", { name: "Terminal actions", exact: true }).click()
      await page.getByRole("menuitem", { name: "Keyboard shortcuts", exact: true }).click()
      await expect(page.getByRole("dialog")).toBeVisible()
      // The dialog guard also applies while no control owns focus, which can
      // happen as a modal opens or closes.
      await page.evaluate(() => (document.activeElement as HTMLElement)?.blur())
      await page.keyboard.press("Control+Alt+Shift+ArrowRight")
      expect(creates).toHaveLength(2)
      await page.keyboard.press("Escape")
      await expect(page.getByRole("dialog")).toHaveCount(0)

      await strip.getByRole("button", { name: "window-b", exact: true }).click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
      await strip.getByRole("button", { name: "window-a", exact: true }).click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
      await page.locator('[data-session="session-b"]').getByRole("button").first().click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(1)
      await page.locator('[data-session="session-a"]').getByRole("button").first().click()
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(3)
      for (const id of ["window-a", top, bottom]) expect(connections.get(id)).toHaveLength(1)

      await focusTerminal(page, "window-a")
      connections.get(bottom)![0].socket.close()
      await expect.poll(() => connections.get(bottom)?.length).toBe(2)
      await expect(terminalPane(page, "window-a").locator(".xterm-helper-textarea")).toBeFocused()
      await assertTerminalGrids(page, connections)
      await page.getByRole("button", { name: "Fullscreen", exact: true }).click()
      await assertTerminalGrids(page, connections)
      await splitEvidence(page, `nested-fullscreen-${renderer}.png`)
      await page.getByRole("button", { name: /^Leave fullscreen/ }).click()
      await page.getByRole("button", { name: "Hide the sessions rail", exact: true }).click()
      await page.setViewportSize({ width: 390, height: 844 })
      await assertTerminalGrids(page, connections)
      await splitEvidence(page, `nested-mobile-${renderer}.png`)
      await page.setViewportSize({ width: 1440, height: 900 })
      await focusTerminal(page, bottom)
      await page.keyboard.press("Control+Alt+KeyW")
      await expect(page.locator("[data-terminal-window]:visible")).toHaveCount(2)
      await expect(height).toHaveCount(0)
      await assertTerminalGrids(page, connections)
      expect(errors).toEqual([])
    })
  }

  test("runs agents in the focused shell, and gives one its own window only when a program holds it", async ({
    page,
  }) => {
    test.setTimeout(TERMINAL_TIMEOUT)
    await page.setViewportSize({ width: 1440, height: 900 })
    const { connections, creates, errors } = await terminalFixture(page, "webgl", undefined, true)
    await splitTerminal(page, "right")
    await expect.poll(() => creates.length).toBe(1)
    const source = creates[0].id
    await expect(terminalPane(page, source).locator(".xterm-helper-textarea")).toBeFocused()
    const codex = page.getByRole("button", { name: "Codex", exact: true })
    await expect(codex.locator('img[src="/logos/openai.svg"]')).toBeVisible()
    await expect(
      page
        .getByRole("button", { name: "Claude", exact: true })
        .locator('img[src="/logos/claude.svg"]'),
    ).toBeVisible()
    await codex.click()
    const typed = (id: string) =>
      connections
        .get(id)!
        .flatMap((connection) => connection.input)
        .join("")
    await expect.poll(() => typed(source)).toContain("codex --yolo\r")
    expect(creates).toHaveLength(1)
    await expect(terminalPane(page, source).locator(".xterm-helper-textarea")).toBeFocused()
    await splitEvidence(page, "codex-launch.png")

    // A program holding the terminal would read the command as its own input.
    await page
      .locator('[data-window="window-a"]')
      .getByRole("button", { name: "window-a", exact: true })
      .click()
    await focusTerminal(page, "window-a")
    connections
      .get("window-a")!
      .at(-1)!
      .socket.send(JSON.stringify({ type: "state", data: { busy: true, process: "nvim" } }))
    await expect(page.locator('[data-window="window-a"]')).toHaveAttribute("data-busy", "true")
    await page.getByRole("button", { name: "Claude", exact: true }).click()
    await expect.poll(() => creates.length).toBe(2)
    expect(creates[1]).toMatchObject({ sourceWindowId: "window-a", agent: "claude" })
    expect(typed("window-a")).not.toContain("claude --dangerously-skip-permissions")
    const claude = creates[1].id
    await expect(terminalPane(page, claude).locator(".xterm-helper-textarea")).toBeFocused()
    await assertTerminalGrids(page, connections)
    await splitEvidence(page, "claude-launch.png")
    await page.getByRole("button", { name: "New window", exact: true }).click()
    await expect.poll(() => creates.length).toBe(3)
    expect(creates[2].sourceWindowId).toBe(claude)
    expect(creates[2].agent).toBeUndefined()
    await expect(terminalPane(page, creates[2].id).locator(".xterm-helper-textarea")).toBeFocused()
    expect(errors).toEqual([])
  })
})
