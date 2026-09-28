import { describe, expect, test } from "bun:test"
import { existsSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { hasProductLogo } from "@/components/product-logo"
import { LOG_FIELDS } from "./log-fields"
import {
  LENS_CHOICES,
  STACK_LENS,
  eventLabel,
  eventMeta,
  lensFor,
  withLensDefaults,
} from "./log-lenses"
import { EMPTY_FILTER, fieldsOf, validFieldKey, validFieldValue } from "./log-filter"

const lenses = [...LENS_CHOICES.map((c) => lensFor(c.id)), lensFor(STACK_LENS)]

// The events and attributes each parser can emit, written by the Go lens
// tests (`go test ./internal/logsx -run TestLensGolden -update`). Every one
// needs a word here, or a line the server names reaches the page unnamed.
const goldenPath = join(
  import.meta.dir,
  "../../../backend/internal/logsx/testdata/lenses.golden.json",
)
const golden = existsSync(goldenPath) ? JSON.parse(readFileSync(goldenPath, "utf8")) : null

describe("the server's vocabulary", () => {
  if (!golden) {
    test.skip("lenses.golden.json is not written yet — run `go test ./internal/logsx -run TestLensGolden -update` once the Go lenses land", () => {})
    return
  }

  test("every lens the server registers is known here", () => {
    for (const id of Object.keys(golden)) expect(lensFor(id)?.id).toBe(id)
  })

  test("every lens here is one the server registers", () => {
    for (const { id } of LENS_CHOICES) expect(`${id}:${Boolean(golden[id])}`).toBe(`${id}:true`)
  })

  // What a lens's lines can carry: its own vocabulary and that of every lens
  // it hands lines on to, plus the keys the engine answers for every line.
  const reach = (id, seen = new Set()) => {
    if (seen.has(id)) return { events: new Set(), attrs: new Set() }
    seen.add(id)
    const events = new Set(golden[id]?.events ?? [])
    const attrs = new Set(golden[id]?.attrs ?? [])
    for (const included of lensFor(id)?.includes ?? []) {
      const more = reach(included, seen)
      for (const e of more.events) events.add(e)
      for (const a of more.attrs) attrs.add(a)
    }
    return { events, attrs }
  }
  const ENGINE_KEYS = new Set(["event", "level", "stream", "source", "pattern"])

  test("every event a view, reading, group or default names is one the parser emits", () => {
    for (const lens of lenses.filter((l) => l.id !== STACK_LENS)) {
      const { events } = reach(lens.id)
      const named = [
        ...lens.views.map((v) => v.fields),
        ...(lens.readings ?? []).map((r) => r.fields),
        ...(lens.groups ?? []).map((g) => g.fields),
        lens.defaults?.fields,
      ].flatMap((fields) => fields?.event ?? [])
      const missing = named.map((raw) => raw.replace(/^!/, "")).filter((id) => !events.has(id))
      expect({ lens: lens.id, missing }).toEqual({ lens: lens.id, missing: [] })
    }
  })

  test("every event word here is for an event the parser emits", () => {
    for (const lens of lenses.filter((l) => l.id !== STACK_LENS)) {
      const stale = Object.keys(lens.events).filter((id) => !golden[lens.id]?.events.includes(id))
      expect({ lens: lens.id, stale }).toEqual({ lens: lens.id, stale: [] })
    }
  })

  test("every key a lens ranks, samples, shows or filters on is one its lines carry", () => {
    for (const lens of lenses.filter((l) => l.id !== STACK_LENS)) {
      const { attrs } = reach(lens.id)
      const keys = [
        ...lens.facets,
        ...(lens.columns ?? []),
        ...(lens.groups ?? []).flatMap((g) =>
          [g.by, ...(g.sample ?? []), g.measure].filter(Boolean),
        ),
        ...(lens.readings ?? []).map((r) => r.distinct).filter(Boolean),
        ...(lens.measure ? [lens.measure.key] : []),
        ...[
          ...lens.views.map((v) => v.fields),
          ...(lens.readings ?? []).map((r) => r.fields),
          ...(lens.groups ?? []).map((g) => g.fields),
          lens.defaults?.fields,
        ].flatMap((fields) => Object.keys(fields ?? {})),
      ]
      const missing = [...new Set(keys)].filter((key) => !ENGINE_KEYS.has(key) && !attrs.has(key))
      expect({ lens: lens.id, missing }).toEqual({ lens: lens.id, missing: [] })
    }
  })

  for (const [id, { events = [], attrs = [] }] of Object.entries(golden)) {
    test(`${id}: every event has a word`, () => {
      const unnamed = (events ?? []).filter((event) => !eventMeta(id, event))
      expect(unnamed).toEqual([])
    })
    test(`${id}: every attribute has a label and a kind`, () => {
      const unknown = (attrs ?? []).filter((key) => !LOG_FIELDS[key])
      expect(unknown).toEqual([])
    })
  }
})

describe("the registry", () => {
  test("a lens is drawn as a product only when the product has a logo", () => {
    for (const lens of lenses) {
      if (lens.product) expect(hasProductLogo(lens.product)).toBe(true)
    }
  })

  test("the logs of no product carry no product", () => {
    // The rail keeps the host's distribution on auth.log and syslog; a lens
    // product there would replace it.
    for (const id of [
      "auth",
      "syslog",
      "kernel",
      "firewall",
      "cron",
      "packages",
      "systemd",
      "app",
    ]) {
      expect(lensFor(id).product).toBeUndefined()
    }
    expect(lensFor("mssql").product).toBe("sqlserver")
    expect(lensFor("certbot").product).toBe("lets-encrypt")
  })

  test("every view, reading, group and default names only events the lens can see", () => {
    for (const lens of lenses) {
      const named = [
        ...lens.views.map((v) => v.fields),
        ...(lens.readings ?? []).map((r) => r.fields),
        ...(lens.groups ?? []).map((g) => g.fields),
        lens.defaults?.fields,
      ].flatMap((fields) => fields?.event ?? [])
      for (const raw of named) {
        const id = raw.replace(/^!/, "")
        expect({ lens: lens.id, event: id, meta: Boolean(eventMeta(lens.id, id)) }).toEqual({
          lens: lens.id,
          event: id,
          meta: true,
        })
      }
    }
  })

  test("every predicate in the registry is one the server accepts", () => {
    for (const lens of lenses) {
      const all = [
        ...lens.views.map((v) => v.fields),
        ...(lens.readings ?? []).map((r) => r.fields),
        ...(lens.groups ?? []).map((g) => g.fields),
        lens.defaults?.fields,
      ].filter(Boolean)
      for (const fields of all) {
        for (const [key, values] of Object.entries(fields)) {
          expect(validFieldKey(key)).toBe(true)
          for (const value of values)
            expect(`${key}:${value}:${validFieldValue(value)}`).toEndWith(":true")
        }
        expect(fieldsOf({ fields })).toBe(fields)
      }
    }
  })

  test("ids are unique within a lens, and the limits hold", () => {
    for (const lens of lenses) {
      for (const list of [lens.views, lens.readings ?? [], lens.groups ?? []]) {
        expect(new Set(list.map((item) => item.id)).size).toBe(list.length)
      }
      expect((lens.groups ?? []).length).toBeLessThanOrEqual(4)
      expect((lens.columns ?? []).length).toBeLessThanOrEqual(3)
      expect(lens.facets.length).toBeLessThanOrEqual(12)
    }
  })

  test("every key a lens ranks, samples or shows has a label", () => {
    for (const lens of lenses) {
      const keys = [
        ...lens.facets,
        ...(lens.columns ?? []),
        ...(lens.groups ?? []).flatMap((g) =>
          [g.by, ...(g.sample ?? []), g.measure].filter(Boolean),
        ),
        ...(lens.readings ?? []).map((r) => r.distinct).filter(Boolean),
        ...(lens.measure ? [lens.measure.key] : []),
      ]
      for (const key of keys)
        expect(`${lens.id}:${key}:${Boolean(LOG_FIELDS[key])}`).toEndWith(":true")
    }
  })

  test("event words are short and lower case", () => {
    for (const lens of lenses) {
      for (const [id, meta] of Object.entries(lens.events)) {
        expect(`${lens.id}.${id}: ${meta.label}`).toMatch(/: [a-z0-9/ -]+$/)
        expect(meta.label.split(" ").length).toBeLessThanOrEqual(3)
      }
    }
  })
})

describe("naming an event", () => {
  test("the line's own lens wins over the stream's", () => {
    expect(eventMeta("syslog", "ssh_failed", "auth")?.label).toBe("failed")
    expect(eventMeta("postgres", "oom")?.label).toBe("out of memory")
    expect(eventMeta("syslog", "oom", "systemd")?.label).toBe("oom kill")
  })

  test("a lens reaches what it includes, and a journal unit reaches systemd's lines", () => {
    expect(eventMeta("nginx", "upstream_timeout")?.tone).toBe("danger")
    expect(eventMeta("nginx", "request")?.mark).toBe(false)
    expect(eventMeta("syslog", "block")?.label).toBe("blocked")
    expect(eventMeta("pm2", "startup")?.divider).toBe(true)
    expect(eventMeta("app", "restart_scheduled")?.label).toBe("restart scheduled")
  })

  test("nothing for an id no lens names, and the id read as words for a label", () => {
    expect(eventMeta("postgres", "no_such_event")).toBeUndefined()
    expect(eventMeta(undefined, undefined)).toBeUndefined()
    expect(eventLabel("postgres", "no_such_event")).toBe("no such event")
  })

  test("none and unknown ids have no lens", () => {
    expect(lensFor("none")).toBeUndefined()
    expect(lensFor("")).toBeUndefined()
    expect(lensFor(undefined)).toBeUndefined()
    expect(lensFor("gopher")).toBeUndefined()
    expect(lensFor(STACK_LENS).facets).toEqual(["service", "event", "level"])
    expect(LENS_CHOICES.some((c) => c.id === STACK_LENS)).toBe(false)
  })
})

describe("a lens's defaults", () => {
  test("replace the last source's predicates, as chips", () => {
    const f = withLensDefaults({ ...EMPTY_FILTER, fields: { user: ["x"] } }, lensFor("auth"))
    expect(f.fields).toEqual({ event: ["!cron_session", "!ssh_scan"] })
  })

  test("a lens without any leaves the filter alone", () => {
    const f = { ...EMPTY_FILTER, q: "x" }
    expect(withLensDefaults(f, lensFor("postgres"))).toBe(f)
    expect(withLensDefaults(f, undefined)).toBe(f)
  })

  test("the levels a lens left imposed go with it; levels the reader chose stay", () => {
    const clickhouse = lensFor("clickhouse")
    const opened = withLensDefaults(EMPTY_FILTER, clickhouse)
    expect(opened.levels).toEqual(clickhouse.defaults.levels)
    expect(withLensDefaults(opened, lensFor("app"), clickhouse).levels).toEqual([])
    const chosen = { ...EMPTY_FILTER, levels: ["error"] }
    expect(withLensDefaults(chosen, lensFor("app"), clickhouse).levels).toEqual(["error"])
  })
})

test("a reading's hint fits one line of its tile", () => {
  for (const lens of lenses) {
    for (const reading of lens.readings) {
      expect(`${lens.id}: ${reading.hint}`).toBe(`${lens.id}: ${reading.hint.slice(0, 24)}`)
    }
  }
})

test("only a run's start is drawn as a rule", () => {
  for (const lens of lenses) {
    for (const [id, meta] of Object.entries(lens.events)) {
      if (meta.divider)
        expect(`${lens.id}.${id}`).toMatch(/^(systemd\.started|(app|pm2)\.startup)$/)
    }
  }
})
