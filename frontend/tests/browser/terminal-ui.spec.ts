import { expect, test, type Page, type WebSocketRoute } from "@playwright/test"

type Connection = {
  socket: WebSocketRoute
  closed: boolean
  input: string[]
  sizes: { rows: number; cols: number }[]
}

async function terminalFixture(page: Page, renderer: "dom" | "webgl") {
  const connections = new Map<string, Connection[]>()
  const errors: string[] = []
  const windows = new Map([
    ["session-a", ["window-a", "window-b"]],
    ["session-b", ["window-c"]],
  ])
  page.on("pageerror", (error) => errors.push(error.message))
  await page.addInitScript((renderer) => {
    localStorage.setItem("jd.terminal.renderer", renderer)
    localStorage.setItem(
      "jd.view.state",
      JSON.stringify({ "terminal.rail": true, "terminal.tools": false, "shell.sidebar": false }),
    )
  }, renderer)
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
          login: { user: "operator", home: "/home/operator", shell: "/bin/bash" },
          folders: [],
          sessions: Array.from(windows, ([id, items]) => ({
            id,
            title: id,
            live: true,
            windows: items.length,
            createdAt: "2026-09-01T00:00:00Z",
          })),
        },
      })
    }
    const match = path.match(/^\/terminal\/([^/]+)\/windows(?:\/([^/]+))?$/)
    if (match) {
      const [, session, id] = match
      if (route.request().method() === "DELETE") {
        windows.set(
          session,
          windows.get(session)!.filter((window) => window !== id),
        )
        return route.fulfill({ json: {} })
      }
      return route.fulfill({
        json: (windows.get(session) ?? []).map((id, index) => ({ id, name: id, index })),
      })
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
    const connection: Connection = { socket, closed: false, input: [], sizes: [] }
    connections.set(id, [...(connections.get(id) ?? []), connection])
    socket.onClose(() => {
      connection.closed = true
    })
    socket.onMessage((message) => {
      if (typeof message === "string") {
        const control = JSON.parse(message)
        if (control.type === "resize") connection.sizes.push(control)
      } else {
        connection.input.push(Buffer.from(message).toString("utf8"))
      }
    })
    // These cells are painted once, like a TUI's unchanged frame. Later output
    // contains only cursor updates and cannot reconstruct them after a remount.
    socket.send(
      Buffer.from(
        `shell history ${id}\r\n\x1b[?1049h\x1b[?25l\x1b[2J\x1b[HSession frame: ${id}\x1b[3;1HReady ▀█\x1b[6n`,
      ),
    )
  })
  await page.goto("/terminal")
  await expect(page.locator("[data-terminal-renderer]:visible")).toHaveAttribute(
    "data-terminal-renderer",
    renderer,
  )
  await expect.poll(() => connections.get("window-a")?.[0].input.length).toBeGreaterThan(0)
  return { connections, errors }
}

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
    const { connections, errors } = await terminalFixture(page, renderer)
    const first = connections.get("window-a")![0]
    const originalSize = first.sizes.at(-1)
    first.socket.send(Buffer.from("\x1b[5;1H\x1b[38;2;"))
    await page.getByRole("button", { name: "window-b", exact: true }).click()
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

    await page.getByRole("button", { name: "window-a", exact: true }).click()
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
    await page.getByRole("button", { name: "Close window window-b", exact: true }).click()
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
