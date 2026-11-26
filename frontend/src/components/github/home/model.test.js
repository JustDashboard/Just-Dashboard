import { describe, expect, test } from "bun:test"
import {
  CELL,
  CELL_GAP,
  WEEKDAY_GUTTER,
  blogHref,
  blogWords,
  busiestDay,
  dayTitle,
  eventWords,
  inboxFilterOf,
  inboxHref,
  inboxStateOf,
  inboxWhen,
  issueStateOf,
  monthLabels,
  recentRepositories,
  streaks,
  weeksThatFit,
} from "./model"

/** A calendar of `n` weeks from a Sunday, each day's count from `count(index)`. */
function calendar(n, start = "2025-11-30", count = () => 0) {
  const first = Date.parse(`${start}T00:00:00Z`)
  let index = 0
  const weeks = Array.from({ length: n }, () => ({
    days: Array.from({ length: 7 }, (_, weekday) => {
      const date = new Date(first + index * 86_400_000).toISOString().slice(0, 10)
      const c = count(index++)
      return { date, count: c, level: c ? 2 : 0, weekday }
    }),
  }))
  return { total: weeks.flatMap((w) => w.days).reduce((s, d) => s + d.count, 0), weeks }
}

describe("weeksThatFit", () => {
  test("the whole year on a desk, the latest weeks on a phone, never fewer than four", () => {
    const year = WEEKDAY_GUTTER + 53 * (CELL + CELL_GAP)
    expect(weeksThatFit(year, 53)).toBe(53)
    expect(weeksThatFit(year * 2, 53)).toBe(53)
    expect(weeksThatFit(358, 53)).toBe(Math.floor((358 - WEEKDAY_GUTTER + CELL_GAP) / 14))
    expect(weeksThatFit(40, 53)).toBe(4)
    expect(weeksThatFit(0, 53)).toBe(53)
  })
})

describe("monthLabels", () => {
  test("one name where each month begins, none crowding the one before", () => {
    const labels = monthLabels(calendar(20).weeks)
    // The first column is the last of November, which began before the calendar.
    expect(labels.map((l) => l.label)).toEqual(["Dec", "Jan", "Feb", "Mar", "Apr"])
    expect(labels[0].column).toBe(1)
    for (let i = 1; i < labels.length; i++) {
      expect(labels[i].column - labels[i - 1].column).toBeGreaterThanOrEqual(3)
    }
  })
  test("a first column deep into a month that began earlier is not named", () => {
    const labels = monthLabels(calendar(8, "2026-11-01").weeks)
    expect(labels[0].label).toBe("Nov")
    const late = monthLabels(calendar(8, "2025-12-14").weeks)
    expect(late[0].label).toBe("Jan")
  })
})

describe("streaks", () => {
  test("the longest run and the run that ends today", () => {
    // 3 days, a gap, 5 days, a gap, then the last 2 days.
    const busy = new Set([0, 1, 2, 4, 5, 6, 7, 8, 12, 13])
    expect(streaks(calendar(2, undefined, (i) => (busy.has(i) ? 1 : 0)))).toEqual({
      longest: 5,
      current: 2,
    })
  })
  test("today not yet contributed does not break yesterday's streak", () => {
    const busy = new Set([10, 11, 12])
    expect(streaks(calendar(2, undefined, (i) => (busy.has(i) ? 1 : 0))).current).toBe(3)
  })
  test("nothing at all is zero, and no calendar is zero", () => {
    expect(streaks(calendar(3))).toEqual({ longest: 0, current: 0 })
    expect(streaks(undefined)).toEqual({ longest: 0, current: 0 })
  })
})

test("busiestDay and dayTitle", () => {
  const cal = calendar(1, undefined, (i) => (i === 3 ? 9 : i))
  expect(busiestDay(cal)).toMatchObject({ date: "2025-12-03", count: 9 })
  expect(busiestDay(calendar(1))).toBeUndefined()
  expect(dayTitle({ date: "2025-12-03", count: 1, level: 1, weekday: 3 })).toBe(
    "1 contribution on 3 Dec",
  )
  expect(dayTitle({ date: "2026-01-09", count: 0, level: 0, weekday: 5 })).toBe(
    "No contributions on 9 Jan",
  )
})

test("recentRepositories leads with the latest pushed, archived left out", () => {
  const repo = (name, pushedAt, archived = false) => ({ fullName: name, pushedAt, archived })
  const repos = [
    repo("a/old", "2026-01-01T00:00:00Z"),
    repo("a/new", "2026-03-01T00:00:00Z"),
    repo("a/gone", "2026-04-01T00:00:00Z", true),
    repo("a/mid", "2026-02-01T00:00:00Z"),
  ]
  expect(recentRepositories(repos, 2).map((r) => r.fullName)).toEqual(["a/new", "a/mid"])
  expect(repos[0].fullName).toBe("a/old")
})

describe("a listing's address", () => {
  test("names a filter the listing has, or Created", () => {
    expect(inboxFilterOf("pull", "review")).toBe("review")
    expect(inboxFilterOf("pull", "mentioned")).toBe("mentioned")
    expect(inboxFilterOf("pull", null)).toBe("created")
    expect(inboxFilterOf("pull", "involves")).toBe("created")
    // Review requests are for pull requests only.
    expect(inboxFilterOf("issue", "review")).toBe("created")
    expect(inboxFilterOf("issue", "assigned")).toBe("assigned")
  })
  test("is open unless it says closed", () => {
    expect(inboxStateOf("closed")).toBe("closed")
    expect(inboxStateOf(null)).toBe("open")
    expect(inboxStateOf("merged")).toBe("open")
  })
})

describe("inboxWhen", () => {
  const times = {
    createdAt: "2026-11-01T10:00:00Z",
    updatedAt: "2026-11-03T10:00:00Z",
    closedAt: "2026-11-02T10:00:00Z",
  }
  test("an open one is when it was opened", () => {
    expect(inboxWhen({ ...times, state: "open", closedAt: undefined })).toEqual({
      open: true,
      verb: "opened",
      at: "2026-11-01T10:00:00Z",
    })
  })
  test("a merged or closed one is when it was, never who did it", () => {
    expect(inboxWhen({ ...times, state: "merged" })).toEqual({
      open: false,
      verb: "merged",
      at: "2026-11-02T10:00:00Z",
    })
    expect(inboxWhen({ ...times, state: "closed", closedAt: undefined })).toEqual({
      open: false,
      verb: "closed",
      at: "2026-11-03T10:00:00Z",
    })
  })
})

describe("inboxHref", () => {
  const listed = new Set(["northwind/api"])
  test("a listed repository opens in place with the request open", () => {
    expect(
      inboxHref(
        { repository: "Northwind/api", number: 7, htmlUrl: "https://github.com/x" },
        "pull",
        listed,
        1,
      ),
    ).toEqual({ href: "/github/Northwind/api?account=1&tab=pulls&pull=7", external: false })
    expect(
      inboxHref(
        { repository: "northwind/api", number: 3, htmlUrl: "https://github.com/x" },
        "issue",
        listed,
        2,
      ).href,
    ).toBe("/github/northwind/api?account=2&tab=issues&issue=3")
  })
  test("anywhere else opens on GitHub", () => {
    expect(
      inboxHref(
        {
          repository: "torvalds/linux",
          number: 1,
          htmlUrl: "https://github.com/torvalds/linux/pull/1",
        },
        "pull",
        listed,
        1,
      ),
    ).toEqual({ href: "https://github.com/torvalds/linux/pull/1", external: true })
  })
})

test("issueStateOf tells done from not planned", () => {
  expect(issueStateOf({ state: "open" })).toBe("open")
  expect(issueStateOf({ state: "closed", stateReason: "completed" })).toBe("completed")
  expect(issueStateOf({ state: "closed" })).toBe("completed")
  expect(issueStateOf({ state: "closed", stateReason: "not_planned" })).toBe("not_planned")
})

describe("eventWords", () => {
  const base = { id: "1", repository: "a/b", createdAt: "2026-01-01T00:00:00Z" }
  test("each kind says what happened, with its object apart", () => {
    expect(eventWords({ ...base, kind: "push", ref: "main", commits: 3 })).toEqual({
      verb: "Pushed 3 commits to",
      object: "main",
    })
    expect(eventWords({ ...base, kind: "push", ref: "dev", commits: 1 }).verb).toBe(
      "Pushed 1 commit to",
    )
    // GitHub's feed may not say how many.
    expect(eventWords({ ...base, kind: "push", ref: "dev" }).verb).toBe("Pushed to")
    expect(
      eventWords({ ...base, kind: "pull", action: "merged", number: 12, title: "Fix" }),
    ).toEqual({ verb: "Merged pull request", object: "#12 Fix" })
    expect(eventWords({ ...base, kind: "issue", action: "closed", number: 4 })).toEqual({
      verb: "Closed issue",
      object: "#4",
    })
    expect(eventWords({ ...base, kind: "review", action: "approved", number: 2 }).verb).toBe(
      "Approved",
    )
    expect(eventWords({ ...base, kind: "create", refType: "repository" })).toEqual({
      verb: "Created the repository",
    })
    expect(eventWords({ ...base, kind: "create", refType: "tag", ref: "v1.0" })).toEqual({
      verb: "Created tag",
      object: "v1.0",
    })
    expect(eventWords({ ...base, kind: "delete", ref: "old" })).toEqual({
      verb: "Deleted branch",
      object: "old",
    })
    expect(eventWords({ ...base, kind: "star" })).toEqual({ verb: "Starred" })
    expect(eventWords({ ...base, kind: "release", title: "Spring", ref: "v2" }).object).toBe(
      "Spring",
    )
    expect(eventWords({ ...base, kind: "release", ref: "v2" }).object).toBe("v2")
  })
})

test("blogHref adds a scheme and refuses what is not the web", () => {
  expect(blogHref("mira.dev")).toBe("https://mira.dev/")
  expect(blogHref("http://mira.dev/blog")).toBe("http://mira.dev/blog")
  expect(blogHref("javascript:alert(1)")).toBeUndefined()
  expect(blogHref("  ")).toBeUndefined()
  expect(blogHref(undefined)).toBeUndefined()
  expect(blogWords("https://mira.dev/")).toBe("mira.dev")
})
