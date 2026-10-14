import { expect, test, type Locator, type Page } from "@playwright/test"

const now = new Date().toISOString()

type Note = {
  id: string
  kind: string
  x: number
  y: number
  w: number
  h: number
  text: string
  color: string
  style: string
  size: string
  align: string
  font: string
  locked: boolean
}
type Link = { id: string; from: string; to: string; label: string; color: string }
type Doc = { version: number; background: string; notes: Note[]; links: Link[] }
type Refusal = { status: number; code: string; message: string }

const note = (id: string, text: string, x: number, y: number, extra: Partial<Note> = {}): Note => ({
  id,
  kind: "sticky",
  x,
  y,
  w: 220,
  h: 220,
  text,
  color: "yellow",
  style: "paper",
  size: "m",
  align: "left",
  font: "sans",
  locked: false,
  ...extra,
})

const COLORS = ["yellow", "orange", "red", "pink", "violet", "blue", "teal", "green", "graphite"]

/**
 * The stickyboard API as the Go handler behaves: a document is refused when a
 * link names a note that is not on the board or a colour the editor cannot
 * draw, a stale revision is a conflict, and a rename leaves the revision
 * alone. A mock that took anything is how a save that could never succeed
 * would pass this spec.
 */
async function mockStickyboards(
  page: Page,
  doc: Doc = { version: 1, background: "dots", notes: [], links: [] },
  capabilities = ["read", "service.control", "destructive", "system.admin"],
) {
  const board = {
    name: "Ops wall",
    revision: 1,
    doc,
    puts: [] as Doc[],
    creates: [] as { name: string; document?: Doc }[],
    refuse: null as Refusal | null,
    deleted: false,
  }
  const summary = () => ({
    id: 1,
    name: board.name,
    revision: board.revision,
    notes: board.doc.notes.filter((n) => n.kind === "sticky").length,
    links: board.doc.links.length,
    preview: {
      notes: board.doc.notes.map(({ x, y, w, h, kind, color, style, text }) => ({
        x,
        y,
        w,
        h,
        kind,
        color,
        style,
        text,
      })),
      links: [],
    },
    createdAt: now,
    updatedAt: now,
  })
  const invalid = (d: Doc) => {
    if (d.version !== 1) return "document version must be 1"
    const ids = new Set(d.notes.map((n) => n.id))
    if (ids.size !== d.notes.length) return "a note id is used twice"
    if (d.notes.some((n) => !COLORS.includes(n.color))) return "a colour this board cannot draw"
    if (d.links.some((l) => !ids.has(l.from) || !ids.has(l.to) || l.from === l.to)) {
      return "a link joins a note that is not on the board"
    }
    return null
  }
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    const refuse = ({ status, code, message }: Refusal) =>
      json({ error: { code, message } }, status)
    if (path === "/auth/session") {
      return json({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities,
        user: { id: 1, username: "operator", role: "admin", totpEnabled: true },
      })
    }
    if (path === "/stickyboards/" && request.method() === "GET") {
      return json(board.deleted ? [] : [summary()])
    }
    if (path === "/stickyboards/" && request.method() === "POST") {
      const body = request.postDataJSON() as { name: string; document?: Doc }
      board.creates.push(body)
      if (body.document) {
        const why = invalid(body.document)
        if (why) return refuse({ status: 400, code: "bad_request", message: why })
        board.doc = body.document
      }
      board.name = body.name.trim()
      board.deleted = false
      return json({ ...summary(), document: board.doc }, 201)
    }
    if (path === "/stickyboards/1" && request.method() === "GET") {
      return json({ ...summary(), document: board.doc })
    }
    if (path === "/stickyboards/1" && request.method() === "PUT") {
      const body = request.postDataJSON() as { document: Doc; revision: number }
      board.puts.push(body.document)
      if (board.refuse) return refuse(board.refuse)
      const why = invalid(body.document)
      if (why) return refuse({ status: 400, code: "bad_request", message: why })
      if (body.revision !== board.revision) {
        return refuse({ status: 409, code: "stickyboard_conflict", message: "board changed" })
      }
      board.doc = body.document
      board.revision++
      return json(summary())
    }
    if (path === "/stickyboards/1" && request.method() === "PATCH") {
      board.name = (request.postDataJSON() as { name: string }).name
      return json(summary())
    }
    if (path === "/stickyboards/1" && request.method() === "DELETE") {
      board.deleted = true
      return route.fulfill({ status: 204 })
    }
    return json([])
  })
  return board
}

const canvasNote = (page: Page, text: string) =>
  page.locator(".react-flow__node", { hasText: text })

async function openBoard(page: Page) {
  await page.goto("/stickyboard/1")
  await expect(page.getByLabel("Board name")).toHaveValue("Ops wall")
  await expect(page.locator(".react-flow__pane")).toBeVisible()
}

/** Drags a link out of one note's ring, by the side named, to a point on the screen. */
async function dragLink(
  page: Page,
  from: Locator,
  side: "left" | "right" | "top" | "bottom",
  to: { x: number; y: number },
) {
  await from.hover()
  const ring = (await from.locator(`.react-flow__handle-${side}`).boundingBox())!
  await page.mouse.move(ring.x + ring.width / 2, ring.y + ring.height / 2)
  await page.mouse.down()
  await page.mouse.move(to.x, to.y, { steps: 12 })
  await page.mouse.up()
}

test("a board starts from a template, and the list draws it and counts what it holds", async ({
  page,
}) => {
  const board = await mockStickyboards(page)
  board.deleted = true
  await page.goto("/stickyboard")
  await expect(page.getByText("Put up your first board")).toBeVisible()

  // A template on the page is the way in to New board on that template.
  await page.getByRole("button", { name: "New kanban board" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: /Kanban/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await dialog.getByLabel("Name").fill("Sprint 12")
  await dialog.getByRole("button", { name: "Create board" }).click()
  await expect(page).toHaveURL(/\/stickyboard\/1$/)
  expect(board.creates).toHaveLength(1)
  expect(board.creates[0].name).toBe("Sprint 12")
  expect(board.creates[0].document?.notes.map((n) => n.text)).toContain("To do")
  await expect(canvasNote(page, "Upgrade Postgres to 17")).toBeVisible()
  // Opening is not an edit: a save here would bump the revision on every
  // visit and put a second tab looking at the board into a conflict.
  await page.mouse.move(700, 500)
  await page.mouse.wheel(0, 300)
  await page.waitForTimeout(1200)
  expect(board.puts).toHaveLength(0)
  await expect(page.getByRole("status")).toContainText("Saved on this server")

  await page.goto("/stickyboard")
  // What the boards hold is a line of facts under the product's name, not a
  // row of stat tiles.
  await expect(page.locator("[data-slot=host-identity]")).toContainText("4 sticky notes")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Sprint 12", exact: true })).toBeVisible()
  // The card's picture is the board: every note in its colour, and on a board
  // whose notes are large enough to read, what they say.
  const cards = page.getByRole("list", { name: "Stickyboards" })
  await expect(cards.locator("svg [data-note-color]")).toHaveCount(4)
  board.doc = { ...board.doc, notes: [note("a", "Renew the wildcard cert", 0, 0)], links: [] }
  await page.reload()
  await expect(cards.locator("svg text", { hasText: "Renew the wildcard" })).toHaveCount(1)
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
})

test("double-clicking bare board writes a note there, and double-clicking a note edits that note", async ({
  page,
}) => {
  const board = await mockStickyboards(page, {
    version: 1,
    background: "dots",
    notes: [note("a", "Rotate the backup key", 0, 0)],
    links: [],
  })
  await openBoard(page)
  const existing = canvasNote(page, "Rotate the backup key")
  const box = (await existing.boundingBox())!

  // A double press on a note used to land on the pane underneath it too, so
  // it put a fresh note on top of the one being opened.
  await existing.dblclick()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.type("\n- [ ] new key")
  await page.keyboard.press("Escape")
  await expect.poll(() => board.doc.notes.length).toBe(1)
  await expect.poll(() => board.doc.notes[0].text).toBe("Rotate the backup key\n- [ ] new key")
  await expect(existing.getByRole("checkbox", { name: "Mark done" })).toBeVisible()

  await existing.getByRole("checkbox", { name: "Mark done" }).click()
  await expect.poll(() => board.doc.notes[0].text).toContain("- [x] new key")
  await expect(existing.getByLabel("1 of 1 done")).toBeVisible()

  await page.mouse.dblclick(box.x + box.width + 300, box.y + box.height / 2)
  await page.keyboard.type("Fresh idea")
  await page.keyboard.press("Escape")
  await expect.poll(() => board.doc.notes.map((n) => n.text)).toContain("Fresh idea")
  await expect(page.getByRole("status")).toContainText("Saved on this server")

  // The key that makes a note is not typed into it.
  await page.mouse.move(box.x + box.width / 2, box.y + box.height + 300)
  await page.keyboard.press("t")
  await page.keyboard.type("Heading")
  await page.keyboard.press("Escape")
  await expect.poll(() => board.doc.notes.find((n) => n.kind === "text")?.text).toBe("Heading")
})

test("dragging a note's ring links it to another note, or to a new note on bare board", async ({
  page,
}) => {
  const board = await mockStickyboards(page, {
    version: 1,
    background: "dots",
    notes: [note("a", "Plan the release", 0, 0), note("b", "Write release notes", 500, 0)],
    links: [],
  })
  await openBoard(page)
  const plan = canvasNote(page, "Plan the release")
  const notes = canvasNote(page, "Write release notes")
  const target = (await notes.boundingBox())!

  await dragLink(page, plan, "right", { x: target.x + 80, y: target.y + 100 })
  await expect.poll(() => board.doc.links.map((l) => `${l.from}>${l.to}`)).toEqual(["a>b"])
  await expect(page.locator(".react-flow__edge")).toHaveCount(1)

  // The same pair again selects the link it already has.
  await dragLink(page, notes, "left", { x: target.x - 300, y: target.y + 100 })
  await page.waitForTimeout(500)
  expect(board.doc.links).toHaveLength(1)

  await dragLink(page, notes, "bottom", { x: target.x + 110, y: target.y + 520 })
  await page.keyboard.type("Post on the blog")
  await page.keyboard.press("Escape")
  await expect.poll(() => board.doc.notes.map((n) => n.text)).toContain("Post on the blog")
  await expect.poll(() => board.doc.links).toHaveLength(2)
  const made = board.doc.notes.find((n) => n.text === "Post on the blog")!
  expect(board.doc.links.some((l) => l.from === "b" && l.to === made.id)).toBe(true)

  // Deleting a note takes its links with it, and undo brings both back.
  await notes.click()
  await page.keyboard.press("Delete")
  await expect.poll(() => board.doc.links).toHaveLength(0)
  await expect.poll(() => board.doc.notes.map((n) => n.id)).not.toContain("b")
  await page.keyboard.press("ControlOrMeta+z")
  await expect.poll(() => board.doc.links).toHaveLength(2)
})

test("the format bar colours the selection, and a stale tab stops saving and says so", async ({
  page,
}) => {
  const board = await mockStickyboards(page, {
    version: 1,
    background: "dots",
    notes: [note("a", "Plan the release", 0, 0)],
    links: [],
  })
  await openBoard(page)
  const plan = canvasNote(page, "Plan the release")
  await plan.click()
  const bar = page.getByRole("toolbar", { name: "Format" })
  await expect(bar).toBeVisible()
  await bar.getByRole("radio", { name: "Violet" }).click()
  await expect.poll(() => board.doc.notes[0].color).toBe("violet")
  await bar.getByRole("radio", { name: "Tinted on the board" }).click()
  await expect.poll(() => board.doc.notes[0].style).toBe("tint")
  // The bar is held inside the board, clear of the tool rail.
  const rail = (await page.getByRole("toolbar", { name: "Tools" }).boundingBox())!
  const placed = (await bar.boundingBox())!
  expect(placed.x).toBeGreaterThanOrEqual(rail.x + rail.width)

  await page.keyboard.press("Digit8")
  await expect.poll(() => board.doc.notes[0].color).toBe("green")

  board.revision = 9
  await page.keyboard.press("Digit1")
  await expect(page.getByRole("status")).toContainText("Changed in another tab")
  await expect(page.getByRole("button", { name: "Reload" })).toBeVisible()
  const attempts = board.puts.length
  await page.keyboard.press("Digit2")
  await page.waitForTimeout(1200)
  expect(board.puts).toHaveLength(attempts)
  expect(
    await page.evaluate(() => {
      const event = new Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    }),
  ).toBe(true)
})

test("a reader sees the board and cannot change it", async ({ page }) => {
  const board = await mockStickyboards(
    page,
    { version: 1, background: "dots", notes: [note("a", "Plan the release", 0, 0)], links: [] },
    ["read"],
  )
  await openBoard(page)
  await expect(page.getByRole("status")).toContainText("Read only")
  await expect(page.getByRole("button", { name: "Sticky note" })).toHaveCount(0)
  await expect(page.getByLabel("Board name")).toHaveAttribute("readonly", "")
  const box = (await canvasNote(page, "Plan the release").boundingBox())!
  await page.mouse.dblclick(box.x + box.width + 200, box.y + 50)
  await page.keyboard.press("n")
  await page.waitForTimeout(800)
  expect(board.puts).toHaveLength(0)
  await expect(page.locator(".react-flow__node")).toHaveCount(1)
})

test("the first note on an empty board stays where it was put", async ({ page }) => {
  const board = await mockStickyboards(page)
  await openBoard(page)
  await expect(page.getByText("This board is empty")).toBeVisible()
  // React Flow fits the board again whenever its fit flag turns on, and the
  // flag followed the notes, so the first note jumped to the middle and the
  // next one, put at the pointer, landed on top of it.
  await page.mouse.dblclick(500, 400)
  await page.keyboard.type("First")
  await page.keyboard.press("Escape")
  const first = (await canvasNote(page, "First").boundingBox())!
  expect(Math.abs(first.x + first.width / 2 - 500)).toBeLessThan(4)
  expect(Math.abs(first.y + first.height / 2 - 400)).toBeLessThan(4)
  await page.mouse.click(900, 700)
  await page.mouse.move(900, 400)
  await page.keyboard.press("n")
  await page.keyboard.type("Second")
  await page.keyboard.press("Escape")
  const second = (await canvasNote(page, "Second").boundingBox())!
  expect(second.x).toBeGreaterThan(first.x + first.width)
  expect((await canvasNote(page, "First").boundingBox())!.x).toBe(first.x)
  await expect.poll(() => board.doc.notes.map((n) => n.text)).toEqual(["First", "Second"])
})

test("Escape cancels a board rename without persisting its draft", async ({ page }) => {
  const board = await mockStickyboards(page)
  await openBoard(page)
  const name = page.getByLabel("Board name")
  await name.fill("Canceled name")
  await name.press("Escape")
  await expect(name).toHaveValue("Ops wall")
  expect(board.name).toBe("Ops wall")
  await name.fill("Saved name")
  await name.press("Enter")
  await expect.poll(() => board.name).toBe("Saved name")
})

test("duplication stays with a dirty board when the latest save fails", async ({ page }) => {
  const board = await mockStickyboards(page, {
    version: 1,
    background: "dots",
    notes: [note("a", "Keep my edit", 0, 0)],
    links: [],
  })
  board.refuse = { status: 503, code: "unavailable", message: "Save temporarily unavailable" }
  let copies = 0
  await page.route("**/api/v1/stickyboards/1/duplicate", async (route) => {
    copies += 1
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({ id: 2 }),
    })
  })
  await openBoard(page)
  await canvasNote(page, "Keep my edit").click()
  await page.getByRole("toolbar", { name: "Format" }).getByRole("radio", { name: "Violet" }).click()
  await page.getByRole("button", { name: "Board options" }).click()
  await page.getByRole("menuitem", { name: "Duplicate board" }).click()
  await expect(page.getByText("Save the board before duplicating it")).toBeVisible()
  await expect(page).toHaveURL(/\/stickyboard\/1$/)
  expect(copies).toBe(0)
  await expect(canvasNote(page, "Keep my edit")).toBeVisible()
})

test("leaving drains edits made during the first save and stays when the next save fails", async ({
  page,
}) => {
  await mockStickyboards(page, {
    version: 1,
    background: "dots",
    notes: [note("a", "Keep this", 0, 0)],
    links: [],
  })
  const writes: Doc[] = []
  let release = () => {}
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/stickyboards/1", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    writes.push(route.request().postDataJSON().document as Doc)
    if (writes.length === 1) {
      await pending
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ revision: 2 }),
      })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "unavailable", message: "Second save failed" } }),
    })
  })
  await openBoard(page)
  await canvasNote(page, "Keep this").click()
  const format = page.getByRole("toolbar", { name: "Format" })
  await format.getByRole("radio", { name: "Violet" }).click()
  await expect.poll(() => writes.length).toBe(1)
  await page.getByRole("button", { name: "Back to Stickyboard" }).click()
  await format.getByRole("radio", { name: "Teal" }).click()
  release()
  await expect(page.getByRole("dialog", { name: "Leave without saving?" })).toBeVisible()
  expect(writes[1].notes[0].color).toBe("teal")
  await expect(page).toHaveURL(/\/stickyboard\/1$/)
  await page.getByRole("button", { name: "Stay", exact: true }).click()
  await expect(canvasNote(page, "Keep this")).toBeVisible()
})
