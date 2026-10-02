import { describe, expect, test } from "bun:test"
import {
  STALE_AFTER_MS,
  backupAge,
  contentsWords,
  holdsWords,
  keptBytes,
  lastBackup,
  newDatabaseProblem,
  originWord,
  outcomesLabel,
  restoreEffect,
  resultWords,
  scheduledDump,
  tableChoice,
  tookWords,
  transferKind,
  transferOutcomes,
  transferResult,
  uploadNameProblem,
} from "./backups-model"

const NOW = Date.parse("2026-10-02T09:00:00Z")
const TABLES = { object: "table", objects: "tables" }
const file = (name, over = {}) => ({
  file: name,
  size: 1000,
  takenAt: "2026-10-02T08:00:00Z",
  format: "pg_dump archive",
  ...over,
})
const job = (id, kind, status, over = {}) => ({
  id,
  kind: `database.transfer.1.${kind}`,
  title: `${kind} shop`,
  status,
  exitCode: 0,
  startedAt: "2026-10-02T08:30:00Z",
  lines: 0,
  ...over,
})

describe("what a transfer job is and what it produced", () => {
  test("the kind is the last word of the job's kind", () => {
    expect(transferKind(job("a", "backup", "running"))).toBe("backup")
    expect(transferKind(job("a", "restore", "running"))).toBe("restore")
    expect(transferKind(job("a", "copy", "running"))).toBe("copy")
    expect(transferKind({ kind: "packages.upgrade" })).toBeUndefined()
  })

  test("the result is the last result line, parsed", () => {
    const lines = [
      { stream: "status", text: "Dumping shop" },
      { stream: "stdout", text: '{"not":"the result"}' },
      { stream: "result", text: '{"file":"a.dump","size":12,"tool":"pg_dump"}' },
    ]
    expect(transferResult(lines)).toEqual({ file: "a.dump", size: 12, tool: "pg_dump" })
  })

  test("a job that has not said, or said something that is not an object, has none", () => {
    expect(transferResult([{ stream: "stdout", text: "working" }])).toBeUndefined()
    expect(transferResult([{ stream: "result", text: "done" }])).toBeUndefined()
    expect(transferResult([{ stream: "result", text: "[1,2]" }])).toBeUndefined()
    expect(transferResult([])).toBeUndefined()
  })

  test("a dump's result names the file once and the tool once", () => {
    expect(
      resultWords("backup", {
        file: "a.dump",
        size: 1536,
        summary: "written by pg_dump",
        tool: "pg_dump",
      }),
    ).toBe("a.dump · 1.5 KB · written by pg_dump")
    expect(resultWords("backup", { file: "a.sql", summary: "4 tables", tool: "built-in" })).toBe(
      "a.sql · 4 tables · built-in",
    )
  })

  test("a restore's result says where it went and the safety dump it took", () => {
    expect(resultWords("restore", { database: "shop", safetyDump: "shop-1.dump" })).toBe(
      "into shop · safety dump shop-1.dump",
    )
    expect(resultWords("copy", undefined)).toBe("")
  })
})

describe("what a dump holds", () => {
  test("a dump taken with no options holds everything; one with no description says nothing", () => {
    expect(contentsWords({}, TABLES)).toBe("Everything")
    expect(contentsWords(undefined, TABLES)).toBe("")
  })

  test("the scope and the tables are said together", () => {
    expect(contentsWords({ schemaOnly: true }, TABLES)).toBe("Structure only")
    expect(contentsWords({ dataOnly: true }, TABLES)).toBe("Data only")
    expect(contentsWords({ tables: ["a", "b"] }, TABLES)).toBe("2 tables")
    expect(contentsWords({ tables: ["a"], schemaOnly: true }, TABLES)).toBe("Structure of 1 table")
    expect(contentsWords({ excludeTables: ["a", "b", "c"] }, TABLES)).toBe("All but 3 tables")
    expect(contentsWords({ excludeTables: ["a"], dataOnly: true }, TABLES)).toBe(
      "Data of all but 1 table",
    )
  })

  test("a key–value dump names its numbered databases, and compression is said", () => {
    expect(contentsWords({ databases: [9] }, { object: "key", objects: "keys" })).toBe("Database 9")
    expect(contentsWords({ databases: [0, 3] }, { object: "key", objects: "keys" })).toBe(
      "Databases 0, 3",
    )
    expect(contentsWords({ compression: "gzip" }, TABLES)).toBe("Everything · gzip")
    expect(contentsWords({ compression: "none" }, TABLES)).toBe("Everything · uncompressed")
  })

  test("the engine's own noun is used", () => {
    expect(
      contentsWords({ tables: ["orders"] }, { object: "collection", objects: "collections" }),
    ).toBe("1 collection")
  })

  test("an uploaded dump was not taken with these options: what it holds is not known", () => {
    expect(holdsWords({ contents: {}, origin: "upload" }, TABLES)).toBe("")
    expect(holdsWords({ contents: {}, origin: "dump" }, TABLES)).toBe("Everything")
    expect(holdsWords({ contents: { tables: ["a"] }, origin: "safety" }, TABLES)).toBe("1 table")
  })

  test("an origin is said only when it is not the ordinary one", () => {
    expect(originWord("dump")).toBe("")
    expect(originWord(undefined)).toBe("")
    expect(originWord("upload")).toBe("uploaded")
    expect(originWord("safety")).toBe("safety dump")
  })
})

describe("when the database was last backed up", () => {
  test("an uploaded dump is not a backup of what is here", () => {
    const files = [file("up.sql", { origin: "upload" }), file("a.dump"), file("b.dump")]
    expect(lastBackup(files)?.file).toBe("a.dump")
    expect(lastBackup([file("up.sql", { origin: "upload" })])).toBeUndefined()
  })

  test("a safety dump is one", () => {
    expect(lastBackup([file("s.dump", { origin: "safety" })])?.file).toBe("s.dump")
  })

  test("never, and older than a week, are the answers to worry about", () => {
    expect(backupAge(undefined, NOW)).toEqual({ stale: true, never: true })
    expect(backupAge(file("a"), NOW)).toEqual({ stale: false, never: false })
    const old = new Date(NOW - STALE_AFTER_MS - 1000).toISOString()
    expect(backupAge(file("a", { takenAt: old }), NOW).stale).toBe(true)
  })

  test("what is kept adds up", () => {
    expect(keptBytes([file("a"), file("b", { size: 24 })])).toBe(1024)
    expect(keptBytes([])).toBe(0)
  })

  test("a duration is said to the precision it deserves", () => {
    expect(tookWords(undefined)).toBe("")
    expect(tookWords(388)).toBe("under a second")
    expect(tookWords(2400)).toBe("2.4 s")
    expect(tookWords(95_000)).toBe("1m 35s")
  })
})

describe("how the last operations ended", () => {
  const when = (iso) => iso

  test("kept dumps are successes, oldest first, and the running job is last", () => {
    const files = [
      file("new.dump", { takenAt: "2026-10-02T08:00:00Z" }),
      file("old.dump", { takenAt: "2026-10-01T08:00:00Z" }),
    ]
    const marks = transferOutcomes(files, [], job("r", "backup", "running"), when)
    expect(marks.map((mark) => [mark.key, mark.tone])).toEqual([
      ["file:old.dump", "success"],
      ["file:new.dump", "success"],
      ["job:r", "running"],
    ])
  })

  test("a failed job is a mark of its own: it left no file to speak for it", () => {
    const marks = transferOutcomes(
      [file("a.dump", { takenAt: "2026-10-02T08:00:00Z" })],
      [
        job("f", "backup", "failed", { endedAt: "2026-10-02T08:40:00Z", error: "no such table" }),
        job("c", "restore", "cancelled", { endedAt: "2026-10-02T08:50:00Z" }),
        // Already said by its file.
        job("s", "backup", "succeeded", { endedAt: "2026-10-02T08:00:01Z" }),
      ],
      undefined,
      when,
    )
    expect(marks.map((mark) => mark.tone)).toEqual(["success", "danger", "muted"])
    expect(marks[1].title).toContain("no such table")
    expect(outcomesLabel(marks)).toBe("The last 3 operations, 1 failed")
  })

  test("only the last few are drawn", () => {
    const files = Array.from({ length: 20 }, (_, index) =>
      file(`f${index}`, { takenAt: new Date(NOW - index * 3600_000).toISOString() }),
    )
    const marks = transferOutcomes(files, [], undefined, when, 14)
    expect(marks).toHaveLength(14)
    expect(marks.at(-1)?.key).toBe("file:f0")
    expect(outcomesLabel(marks)).toBe("The last 14 operations, none failed")
  })
})

describe("names the server would refuse", () => {
  test("an uploaded dump's name", () => {
    expect(uploadNameProblem("shop-2026.sql.gz")).toBeUndefined()
    expect(uploadNameProblem("")).toBeDefined()
    expect(uploadNameProblem("my dump.sql")).toBeDefined()
    expect(uploadNameProblem("../etc/passwd")).toBeDefined()
    expect(uploadNameProblem(".hidden")).toBeDefined()
    expect(uploadNameProblem("a.dump.meta.json")).toContain(".meta.json")
    expect(uploadNameProblem("a.dump", ["a.dump"])).toContain("already kept")
  })

  test("a new database's name is letters, digits and underscores, and not the connection's own", () => {
    const sql = { numbered: false, current: "shop" }
    expect(newDatabaseProblem("shop_copy", sql)).toBeUndefined()
    expect(newDatabaseProblem("", sql)).toBeDefined()
    expect(newDatabaseProblem("shop-copy", sql)).toBeDefined()
    expect(newDatabaseProblem("9lives", sql)).toBeDefined()
    expect(newDatabaseProblem("SHOP", sql)).toContain("this connection")
  })

  test("on a server that numbers its databases the name is a number", () => {
    const numbered = { numbered: true, current: "9" }
    expect(newDatabaseProblem("12", numbered)).toBeUndefined()
    expect(newDatabaseProblem("twelve", numbered)).toBeDefined()
    expect(newDatabaseProblem("9", numbered)).toContain("this connection")
    expect(newDatabaseProblem("09", numbered)).toContain("this connection")
    expect(newDatabaseProblem("0", { numbered: true, current: "" })).toContain("this connection")
  })
})

describe("what a restore does, by what the dump is", () => {
  test("each format has its own sentence", () => {
    expect(restoreEffect(file("a", { format: "pg_dump archive" }))).toContain("dropped and created")
    expect(restoreEffect(file("a", { format: "SQLite file" }))).toContain(".bak")
    expect(restoreEffect(file("a", { format: "JSON Lines" }))).toContain("left as it is")
    expect(restoreEffect(file("a", { format: "archive" }))).toContain("collection")
    expect(restoreEffect(file("a", { format: "SQL" }))).toContain("Every table")
  })

  test("a script somebody uploaded runs as written", () => {
    expect(restoreEffect(file("a", { format: "compressed SQL", origin: "upload" }))).toContain(
      "as written",
    )
  })
})

describe("the form and the schedule", () => {
  test("a table choice with nothing ticked asks for every table", () => {
    expect(tableChoice("all", ["a"])).toEqual({})
    expect(tableChoice("only", [])).toEqual({})
    expect(tableChoice("only", ["a", "b"])).toEqual({ tables: ["a", "b"] })
    expect(tableChoice("except", ["a"])).toEqual({ excludeTables: ["a"] })
  })

  test("the scheduled job is the enabled one that runs next", () => {
    const jobs = [
      { id: 1, name: "weekly", enabled: true, databaseDumps: [7], nextRun: "2026-10-09T03:00:00Z" },
      {
        id: 2,
        name: "nightly",
        enabled: true,
        databaseDumps: [7],
        nextRun: "2026-10-03T03:00:00Z",
      },
      { id: 3, name: "other", enabled: true, databaseDumps: [8], nextRun: "2026-10-02T10:00:00Z" },
      { id: 4, name: "paused", enabled: false, databaseDumps: [9] },
    ]
    expect(scheduledDump(jobs, 7)?.name).toBe("nightly")
    expect(scheduledDump(jobs, 9)?.name).toBe("paused")
    expect(scheduledDump(jobs, 1)).toBeUndefined()
  })
})
