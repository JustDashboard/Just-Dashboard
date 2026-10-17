import { describe, expect, test } from "bun:test"
import {
  extensionCategories,
  extensionCounts,
  extensionLogo,
  groupExtensions,
  hasUpdate,
  listWords,
  memberWords,
  needsLoading,
  preloadStage,
  shownExtensions,
  waitingLibraries,
} from "./extensions-model"

const ext = (name, over = {}) => ({ name, installed: false, ...over })

const LIST = [
  ext("plpgsql", {
    installed: true,
    version: "1.0",
    availableVersion: "1.0",
    category: "Languages",
  }),
  ext("hstore", {
    installed: true,
    version: "1.4",
    availableVersion: "1.8",
    category: "Data types",
  }),
  ext("pg_trgm", { availableVersion: "1.6", category: "Search", about: "Trigram similarity." }),
  ext("pg_cron", { availableVersion: "1.6", category: "Scheduling", library: "pg_cron" }),
  ext("auto_explain", { module: true, library: "auto_explain", category: "Monitoring" }),
  ext("vector", { unavailable: true, category: "Vectors" }),
  ext("local_thing", { availableVersion: "0.1", comment: "made here" }),
]

const preload = (over = {}) => ({
  loaded: [],
  configured: [],
  pendingRestart: false,
  settable: true,
  ...over,
})

describe("the list a view draws", () => {
  test("each chip counts its own rows, and an update is a newer default on an installed one", () => {
    expect(extensionCounts(LIST)).toEqual({ enabled: 2, available: 4, missing: 1, updates: 1 })
    expect(hasUpdate(LIST[1])).toBe(true)
    expect(hasUpdate(LIST[0])).toBe(false)
    // A module has no version to update, and one not installed has nothing to update.
    expect(hasUpdate(ext("auto_explain", { module: true, installed: true }))).toBe(false)
    expect(hasUpdate(ext("pg_trgm", { version: "1.5", availableVersion: "1.6" }))).toBe(false)
  })

  test("words match the name, the sentence, the engine's comment and the category", () => {
    const names = (list) => list.map((one) => one.name)
    expect(names(shownExtensions(LIST, "available", "trigram", ""))).toEqual(["pg_trgm"])
    expect(names(shownExtensions(LIST, "available", "MADE", ""))).toEqual(["local_thing"])
    expect(names(shownExtensions(LIST, "available", "schedul", ""))).toEqual(["pg_cron"])
    expect(names(shownExtensions(LIST, "available", "", "Monitoring"))).toEqual(["auto_explain"])
    expect(names(shownExtensions(LIST, "available", "", "Other"))).toEqual(["local_thing"])
    expect(names(shownExtensions(LIST, "missing", "", ""))).toEqual(["vector"])
    expect(names(shownExtensions(LIST, "updates", "", ""))).toEqual(["hstore"])
  })

  test("categories are by name with the unplaced last, and a group keeps the list's order", () => {
    const available = shownExtensions(LIST, "available", "", "")
    expect(extensionCategories(available)).toEqual(["Monitoring", "Scheduling", "Search", "Other"])
    expect(
      groupExtensions(available).map((group) => [group.category, group.extensions.length]),
    ).toEqual([
      ["Monitoring", 1],
      ["Scheduling", 1],
      ["Search", 1],
      ["Other", 1],
    ])
  })
})

describe("enabling one that needs its library loaded at start", () => {
  const cron = LIST[3]
  const explain = LIST[4]

  test("the step is read off what the server loads, so a dialog opens where it was left", () => {
    expect(preloadStage(cron, preload())).toBe("load")
    expect(preloadStage(cron, preload({ configured: ["pg_cron"], pendingRestart: true }))).toBe(
      "restart",
    )
    expect(preloadStage(cron, preload({ configured: ["pg_cron"], loaded: ["pg_cron"] }))).toBe(
      "create",
    )
    expect(
      preloadStage(
        { ...cron, installed: true },
        preload({ configured: ["pg_cron"], loaded: ["pg_cron"] }),
      ),
    ).toBe("done")
    // A module is done once it is loaded: there is nothing to create.
    expect(preloadStage(explain, preload({ loaded: ["auto_explain"] }))).toBe("done")
  })

  test("a list the dashboard cannot change stops it before the first step, and not after", () => {
    const fixed = preload({ settable: false, reason: "given on the command line" })
    expect(preloadStage(cron, fixed)).toBe("blocked")
    expect(preloadStage(cron, { ...fixed, configured: ["pg_cron"] })).toBe("restart")
    expect(preloadStage(cron, { ...fixed, loaded: ["pg_cron"], configured: ["pg_cron"] })).toBe(
      "create",
    )
  })

  test("only an extension whose library is not loaded yet takes the long way", () => {
    expect(needsLoading(cron, preload())).toBe(true)
    expect(needsLoading(cron, preload({ loaded: ["pg_cron"] }))).toBe(false)
    expect(needsLoading(LIST[2], preload())).toBe(false)
    expect(needsLoading(explain, preload({ loaded: ["auto_explain"] }))).toBe(true)
  })

  test("a restart's effect is what the files name and the server has not loaded, and the reverse", () => {
    expect(
      waitingLibraries(
        preload({
          loaded: ["pg_stat_statements", "pgaudit"],
          configured: ["pg_stat_statements", "pg_cron"],
        }),
      ),
    ).toEqual({ adding: ["pg_cron"], removing: ["pgaudit"] })
  })
})

describe("words", () => {
  test("what an extension added is counted the way a reader counts it", () => {
    expect(
      memberWords([
        { kind: "function", count: 57 },
        { kind: "operator class", count: 4 },
        { kind: "operator family", count: 4 },
        { kind: "type", count: 1 },
      ]),
    ).toBe("57 functions, 4 operator classes, 4 operator families and 1 type")
    expect(memberWords([{ kind: "index", count: 2 }])).toBe("2 indexes")
    expect(memberWords([])).toBe("nothing")
    expect(listWords(["cube"])).toBe("cube")
    expect(listWords(["a", "b", "c"])).toBe("a, b and c")
  })

  test("an extension is drawn as PostgreSQL's only when it is PostgreSQL's", () => {
    expect(extensionLogo("pg_stat_statements", "postgresql", false)).toBe("postgresql")
    expect(extensionLogo("timescaledb_toolkit", "postgresql", false)).toBe("timescaledb")
    expect(extensionLogo("vector", "postgresql", false)).toBeUndefined()
    expect(extensionLogo("InnoDB", "mysql", true)).toBe("mysql")
  })
})
