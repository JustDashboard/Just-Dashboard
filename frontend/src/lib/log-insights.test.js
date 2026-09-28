import { describe, expect, test } from "bun:test"
import {
  andFields,
  andLevels,
  groupScope,
  overviewFacets,
  overviewSplit,
  patternRegex,
  readingFigure,
  readingSearches,
} from "./log-insights"
import { lensFor } from "./log-lenses"
import { EMPTY_FILTER, matchFields } from "./log-filter"

describe("a group's question on top of the reader's", () => {
  test("keys the reader did not narrow are the group's", () => {
    expect(andFields({ user: ["postgres"] }, { event: ["slow"] })).toEqual({
      user: ["postgres"],
      event: ["slow"],
    })
  })

  test("two sets of values on one key meet rather than widen", () => {
    expect(andFields({ event: ["auth_failed", "slow"] }, { event: ["slow", "deadlock"] })).toEqual({
      event: ["slow"],
    })
  })

  test("values that cannot both hold leave nothing to ask", () => {
    expect(andFields({ event: ["auth_failed"] }, { event: ["slow"] })).toBeNull()
  })

  test("a negation holds beside the group's choice", () => {
    const fields = andFields({ event: ["!cron_session", "!ssh_scan"] }, { event: ["ssh_failed"] })
    expect(fields).toEqual({ event: ["ssh_failed", "!cron_session", "!ssh_scan"] })
    expect(matchFields({ event: "ssh_failed" }, fields)).toBe(true)
    expect(matchFields({ event: "ssh_scan" }, fields)).toBe(false)
  })

  test("an exact value is kept when the other side's substring allows it", () => {
    expect(andFields({ path: ["~/api"] }, { path: ["/api/users", "/health"] })).toEqual({
      path: ["/api/users"],
    })
  })

  test("case does not part two spellings of one value", () => {
    expect(andFields({ method: ["GET"] }, { method: ["get", "post"] })).toEqual({
      method: ["GET"],
    })
  })

  test("levels meet the same way, and none means every level", () => {
    expect(andLevels([], ["error"])).toEqual(["error"])
    expect(andLevels(["warn", "error"], undefined)).toEqual(["warn", "error"])
    expect(andLevels(["warn", "error"], ["error", "critical"])).toEqual(["error"])
    expect(andLevels(["info"], ["error"])).toBeNull()
  })

  test("a group whose question the reader already ruled out is not asked", () => {
    const filter = { ...EMPTY_FILTER, levels: ["info"] }
    expect(groupScope({ levels: ["error", "critical"] }, filter)).toBeNull()
    expect(groupScope({ fields: { event: ["slow"] } }, filter)).toEqual({
      ...filter,
      fields: { event: ["slow"] },
    })
  })
})

describe("the overview", () => {
  test("ranks the lens's keys, the level and the patterns, within the server's twelve", () => {
    const facets = overviewFacets(lensFor("postgres"))
    expect(facets.slice(0, 3)).toEqual(["event", "level", "user"])
    expect(facets).toContain("pattern")
    expect(facets.length).toBeLessThanOrEqual(12)
    expect(overviewFacets(undefined)).toEqual(["level", "pattern"])
  })

  test("splits its chart by event, a stack's by service, and a plain log by level", () => {
    expect(overviewSplit(lensFor("postgres"))).toBe("event")
    expect(overviewSplit(lensFor("stack"))).toBe("service")
    expect(overviewSplit(undefined)).toBeUndefined()
  })

  test("a pattern becomes an expression that finds the lines it came from", () => {
    const re = new RegExp(patternRegex("connection received: host=<*> port=<*> (x) [y] $1.5"))
    expect(re.test("connection received: host=10.0.0.4 port=51202 (x) [y] $1.5")).toBe(true)
    expect(re.test("connection received: host=10.0.0.4")).toBe(false)
  })
})

describe("readings", () => {
  test("Postgres's five readings are two searches", () => {
    const searches = readingSearches(lensFor("postgres").readings)
    expect(searches).toHaveLength(2)
    const [events, levels] = searches
    expect(events.params.facets).toBe("event")
    expect(events.params.histogramBy).toBe("event")
    expect(events.params.f).toEqual([
      "event:slow",
      "event:auth_failed",
      "event:deadlock",
      "event:lock_wait",
      "event:startup",
    ])
    expect(events.reads.map((r) => r.id)).toEqual(["slow", "auth", "locks", "restarts"])
    expect(levels.params.levels).toBe("critical,error")
    expect(levels.reads).toEqual([{ id: "errors", kind: "matched" }])
  })

  test("a distinct count asks on its own, over its own predicates", () => {
    const searches = readingSearches(lensFor("auth").readings)
    const distinct = searches.find((s) => s.reads[0].kind === "distinct")
    expect(distinct.params.facets).toBe("client")
    expect(distinct.params.f).toEqual([
      "event:ssh_failed",
      "event:ssh_invalid_user",
      "event:ssh_max_attempts",
    ])
    expect(searches).toHaveLength(2)
  })

  test("a reading of any value takes every line with the key, and its siblings count within it", () => {
    const [search] = readingSearches(lensFor("http-access").readings)
    expect(search.params.f).toEqual(["class:*"])
    expect(search.params.histogramValues).toBe("4xx,5xx")
    const result = {
      matched: 120,
      histogram: [
        { start: "a", total: 70, counts: { "4xx": 5, "5xx": 1, "*": 64 } },
        { start: "b", total: 50, counts: { "4xx": 2, "*": 48 } },
      ],
      facets: {
        class: {
          values: [
            { value: "2xx", count: 100, errors: 0 },
            { value: "4xx", count: 7, errors: 0 },
            { value: "5xx", count: 1, errors: 1 },
          ],
          distinct: 3,
          other: 0,
          missing: 0,
        },
      },
    }
    const byId = Object.fromEntries(search.reads.map((read) => [read.id, read]))
    expect(readingFigure(byId.rate, result)).toEqual({ value: 120, series: [70, 50] })
    expect(readingFigure(byId["4xx"], result)).toEqual({ value: 7, series: [5, 2] })
    expect(readingFigure(byId["5xx"], result)).toEqual({ value: 1, series: [1, 0] })
  })

  test("a distinct figure says when it stopped being exact", () => {
    const read = { id: "attackers", kind: "distinct", key: "client" }
    const result = {
      matched: 40000,
      histogram: [],
      facets: {
        client: { values: [], distinct: 20000, distinctCapped: true, other: 0, missing: 0 },
      },
    }
    expect(readingFigure(read, result)).toEqual({ value: 20000, capped: true })
  })
})
