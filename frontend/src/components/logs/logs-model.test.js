import { describe, expect, test } from "bun:test"
import { EMPTY_FILTER } from "@/lib/log-filter"
import { lensFor } from "@/lib/log-lenses"
import {
  askOf,
  buildRows,
  columnWidthFor,
  emptiedFile,
  eventColumnFor,
  oneService,
  readingFor,
  readingShown,
  readingsWindowOf,
  sameQuestion,
  unaskedReadings,
  unitJournalLens,
  withAsk,
} from "./logs-model"

const tile = (reading, figure) => ({ reading, figure })

describe("a reading on the quick view that asks its question", () => {
  const postgres = lensFor("postgres")
  const tiles = postgres.readings.map((reading) => tile(reading, { value: 3 }))

  test("a view and a reading asking the same fields and levels are one question", () => {
    expect(sameQuestion({ levels: ["error", "critical"] }, { levels: ["critical", "error"] })).toBe(
      true,
    )
    expect(
      sameQuestion({ fields: { event: ["slow"] } }, { fields: { event: ["slow"] }, levels: [] }),
    ).toBe(true)
    expect(sameQuestion({ fields: { event: ["slow"] } }, { fields: { event: ["deadlock"] } })).toBe(
      false,
    )
    expect(sameQuestion({ levels: ["error"] }, { levels: ["error", "critical"] })).toBe(false)
  })

  test("Errors and Slow carry their readings; Restarts, which no view asks, is a chip of its own", () => {
    const view = (id) => postgres.views.find((v) => v.id === id)
    expect(readingFor(view("errors"), tiles)?.reading.id).toBe("errors")
    expect(readingFor(view("slow"), tiles)?.reading.id).toBe("slow")
    expect(readingFor(view("maintenance"), tiles)).toBeUndefined()
    const unasked = unaskedReadings(postgres.views, tiles).map((t) => t.reading.id)
    expect(unasked).toContain("restarts")
    expect(unasked).not.toContain("errors")
    expect(unasked).not.toContain("slow")
  })

  test("a view that also searches for words asks what no reading does", () => {
    const worded = { id: "w", label: "W", levels: ["error", "critical"], q: "timeout" }
    expect(readingFor(worded, tiles)).toBeUndefined()
  })
})

describe("a reading's figure as it is drawn", () => {
  test("a count, capped when the distinct count stopped being exact", () => {
    const reading = { id: "a", label: "Attackers", hint: "", distinct: "client" }
    expect(readingShown(reading, { value: 1200, capped: true }, { minutes: 1440 }).text).toBe(
      `${(1200).toLocaleString()}+`,
    )
    expect(readingShown(reading, undefined, { minutes: 1440 })).toEqual({ value: 0, text: "—" })
  })

  test("a per-minute reading is its rate over the window", () => {
    const reading = { id: "rate", label: "Requests/min", hint: "", figure: "per_minute" }
    const shown = readingShown(reading, { value: 90 }, { minutes: 60 })
    expect(shown.value).toBe(1.5)
    expect(shown.text).toBe((1.5).toLocaleString())
  })
})

describe("what a page's narrowing leaves of the filter", () => {
  const filter = { ...EMPTY_FILTER, q: "timeout", levels: ["warn"], fields: { user: ["bob"] } }

  test("each part replaces the filter's own only when given", () => {
    expect(withAsk(filter, { fields: { host: ["shop.test"] } })).toEqual({
      ...filter,
      fields: { host: ["shop.test"] },
    })
    expect(withAsk(filter, { fields: {}, levels: [], q: "" })).toEqual({
      ...filter,
      fields: {},
      levels: [],
      q: "",
    })
  })

  test("a window that names nothing is no narrowing at all", () => {
    expect(askOf({ since: "a", until: "b", label: "Around" })).toBeUndefined()
    expect(askOf(undefined)).toBeUndefined()
    expect(askOf({ since: "a", until: "b", levels: [] })).toEqual({
      fields: undefined,
      levels: [],
      q: undefined,
    })
  })
})

test("a file logrotate emptied is one whose history is all in its rotated set", () => {
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", archives: 2 })).toBe(true)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", size: 0, archives: 1 })).toBe(true)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log", size: 12, archives: 1 })).toBe(false)
  expect(emptiedFile({ path: "/var/log/postgresql/main.log" })).toBe(false)
  // A container's output has no file and no rotated set.
  expect(emptiedFile({ archives: 3 })).toBe(false)
})

describe("the event column's width", () => {
  const line = (event, extra = {}) => ({ text: event, event, ...extra })

  test("as wide as the longest event word on screen, so none is cut", () => {
    expect(eventColumnFor([line("upstream_refused"), line("ban")], "nginx-error")).toBe(
      "upstream refused".length,
    )
    expect(eventColumnFor([line("restart_scheduled")], "systemd")).toBe("restart scheduled".length)
  })

  test("never narrower than a level word needs, never wider than the bound", () => {
    expect(eventColumnFor([line("ban")], "fail2ban")).toBe(6)
    expect(eventColumnFor([line("too_many_connections")], "mysql")).toBe(17)
  })

  test("none while no line draws one: a continuation, an unmarked event, no event at all", () => {
    expect(eventColumnFor([], "postgres")).toBe(0)
    expect(eventColumnFor([{ text: "plain" }], "postgres")).toBe(0)
    expect(eventColumnFor([line("deadlock", { cont: true })], "postgres")).toBe(0)
    // A connection is too common to mark, and draws its level instead.
    expect(eventColumnFor([line("connection")], "postgres")).toBe(0)
  })
})

describe("the rows the lines are drawn as", () => {
  const rows = (lines, opts = {}) =>
    buildRows(lines, { dedupe: true, folds: new Set(), ...opts }).filter((r) => r.kind === "line")
  const health = (at) => ({ text: "GET /healthz 200", level: "info", timestamp: at })

  test("a run of repeats keeps its first line's key, so an opened row stays open", () => {
    const first = health("2026-09-27T10:00:01Z")
    const two = rows([first, health("2026-09-27T10:00:02Z")])
    const newest = health("2026-09-27T10:00:03Z")
    const three = rows([first, health("2026-09-27T10:00:02Z"), newest])
    expect(two).toHaveLength(1)
    expect(three).toHaveLength(1)
    expect(three[0].key).toBe(two[0].key)
    expect(three[0].repeat).toBe(3)
    // The row shows the newest of them, and when the first was.
    expect(three[0].line).toBe(newest)
    expect(three[0].since).toBe("2026-09-27T10:00:01Z")
  })

  test("a run starts under a rule only where the pane is one service's", () => {
    const started = { text: "Started postgresql.service", event: "started", lens: "systemd" }
    const failed = { text: "Failed with result 'exit-code'", event: "failed", lens: "systemd" }
    const drawn = (dividers) =>
      buildRows([started, failed], { dedupe: false, dividers, lens: "postgres", folds: new Set() })
        .filter((r) => r.kind === "divider")
        .map((r) => r.label)
    expect(drawn(true)).toEqual(["started"])
    expect(drawn(false)).toEqual([])
  })

  test("one service's stream, not a whole host's", () => {
    expect(oneService("docker", "docker:web")).toBe(true)
    expect(oneService("stack", "stack:shop")).toBe(true)
    expect(oneService("journal", "journal:postgresql.service")).toBe(true)
    expect(oneService("journal", "journal:")).toBe(false)
    expect(oneService("kernel", "kernel:")).toBe(false)
    expect(oneService("system", "file:/var/log/syslog")).toBe(false)
  })
})

test("a lens column is as wide as its longest value on screen, within bounds", () => {
  const line = (upstream, extra = {}) => ({ text: "x", attrs: { upstream }, ...extra })
  const long = "http://127.0.0.1:3000/"
  expect(columnWidthFor([line(long), line("unix:/run/a")], "upstream")).toBe(long.length)
  expect(columnWidthFor([line("a")], "upstream")).toBe(6)
  expect(columnWidthFor([line("x".repeat(80))], "upstream")).toBe(24)
  expect(columnWidthFor([line(long, { cont: true })], "upstream")).toBe(0)
})

test("a unit's journal offers the manager's Failures beside its own lens's views", () => {
  const postgres = lensFor("postgres")
  const unit = unitJournalLens(postgres, "journal", "journal:postgresql.service")
  expect(unit.views.map((v) => v.id)).toEqual([...postgres.views.map((v) => v.id), "failures"])
  expect(unit.facets).toBe(postgres.facets)
  // The whole journal, a container, and systemd's own lens are left as they are.
  expect(unitJournalLens(postgres, "journal", "journal:")).toBe(postgres)
  expect(unitJournalLens(postgres, "docker", "docker:db")).toBe(postgres)
  const systemd = lensFor("systemd")
  expect(unitJournalLens(systemd, "journal", "journal:cron.service")).toBe(systemd)
})

describe("the window a picked range reads readings over", () => {
  test("a preset is its own length and words", () => {
    expect(readingsWindowOf("24h", "", "")).toEqual({
      minutes: 1440,
      short: "in 24h",
      long: "the last 24 hours",
    })
    expect(readingsWindowOf("1h", "", "").long).toBe("the last hour")
  })

  test("a custom range is its length; everything on disk has none", () => {
    const now = Date.parse("2026-09-27T12:00:00Z")
    expect(readingsWindowOf("custom", "2026-09-27T10:00:00Z", "", now)?.minutes).toBe(120)
    expect(
      readingsWindowOf("custom", "2026-09-27T10:00:00Z", "2026-09-27T10:30:00Z", now)?.minutes,
    ).toBe(30)
    expect(readingsWindowOf("custom", "", "", now)).toBeUndefined()
    expect(readingsWindowOf("all", "", "", now)).toBeUndefined()
  })
})
