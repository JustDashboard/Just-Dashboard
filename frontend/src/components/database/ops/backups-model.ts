import { bytes, duration, plural } from "@/lib/format"
import type { BackupJob, Job, JobLine } from "@/lib/types"
import type { Outcome } from "@/components/outcome-strip"
import type {
  DbBackupFile,
  DbDumpContents,
  DbDumpOrigin,
  DbTransferResult,
} from "@/components/database/ops/backups-types"

/**
 * What the Backups page says about a database's dumps, worked out from what
 * the server lists: which dump is the last backup, what a dump holds, how the
 * last operations ended, and what a restore will do. No React, so each
 * sentence can be held to its inputs.
 */

/** A week: past it the newest dump is called old, as the control center calls it. */
export const STALE_AFTER_MS = 7 * 24 * 60 * 60 * 1000

/** How many operations the strip of outcomes draws. */
export const STRIP = 14

export type TransferKind = "backup" | "restore" | "copy"

/** Which operation a transfer job is, read off the end of its kind. */
export function transferKind(job: Pick<Job, "kind">): TransferKind | undefined {
  const word = job.kind.slice(job.kind.lastIndexOf(".") + 1)
  return word === "backup" || word === "restore" || word === "copy" ? word : undefined
}

/** The kind prefix every dump, restore and copy of one connection runs under. */
export function transferPrefix(id: number): string {
  return `database.transfer.${id}.`
}

/**
 * What a transfer job produced: its last `result` line, which is JSON. A job
 * that has not got that far, or whose line is not an object, has none.
 */
export function transferResult(
  lines: readonly Pick<JobLine, "stream" | "text">[],
): DbTransferResult | undefined {
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    if (lines[index].stream !== "result") continue
    try {
      const parsed: unknown = JSON.parse(lines[index].text)
      if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
        return parsed as DbTransferResult
      }
    } catch {
      // Not JSON: an older server's plain sentence. There is nothing to read.
    }
    return undefined
  }
  return undefined
}

/**
 * What a dump holds, in a few words. A dump with no description beside it —
 * a file put in the directory by hand — says nothing, because nothing is
 * known; one taken with no options holds everything.
 */
export function contentsWords(
  contents: DbDumpContents | undefined,
  nouns: { object: string; objects: string },
): string {
  if (!contents) return ""
  const parts: string[] = []
  if (contents.databases && contents.databases.length > 0) {
    parts.push(
      `${contents.databases.length === 1 ? "Database" : "Databases"} ${contents.databases.join(", ")}`,
    )
  }
  const scope = contents.schemaOnly ? "Structure" : contents.dataOnly ? "Data" : ""
  if (contents.tables && contents.tables.length > 0) {
    const named = plural(contents.tables.length, nouns.object, nouns.objects)
    parts.push(scope ? `${scope} of ${named}` : named)
  } else if (contents.excludeTables && contents.excludeTables.length > 0) {
    const left = plural(contents.excludeTables.length, nouns.object, nouns.objects)
    parts.push(scope ? `${scope} of all but ${left}` : `All but ${left}`)
  } else if (scope) {
    parts.push(`${scope} only`)
  }
  if (parts.length === 0) parts.push("Everything")
  if (contents.compression === "none") parts.push("uncompressed")
  if (contents.compression === "gzip") parts.push("gzip")
  return parts.join(" · ")
}

/**
 * What one kept dump holds. A dump uploaded from elsewhere was not taken with
 * this page's options: its empty description is "not known", not "everything".
 */
export function holdsWords(
  file: Pick<DbBackupFile, "contents" | "origin" | "database">,
  nouns: { object: string; objects: string },
  /** The server numbers its databases: a dump is of some of those numbers. */
  numbered = false,
): string {
  if (file.origin === "upload") return ""
  if (numbered) {
    // Which numbers a dump holds is what a restore of it writes into: said
    // by number, never as "everything".
    const numbers = (file.database ?? "").split(",").filter(Boolean)
    if (numbers.length === 1) return `db ${numbers[0]}`
    if (numbers.length > 1) return `${numbers.length} numbered databases`
  }
  return contentsWords(file.contents, nouns)
}

/**
 * What the tool said it wrote, when that says something: "4 tables, 1054
 * rows". A summary that only names the tool again ("written by pg_dump") is
 * left out — the tool is already said beside it.
 */
export function summaryWords(
  file: Pick<DbBackupFile, "summary" | "tool" | "format">,
  /** The server numbers its databases: how many the dump holds is said as what it holds. */
  numbered = false,
): string {
  const summary = (file.summary ?? "").trim()
  if (!summary) return ""
  const tool = (file.tool ?? file.format).toLowerCase()
  if (summary.toLowerCase() === `written by ${tool}`) return ""
  return numbered ? summary.replace(/ in \d+ databases?$/, "") : summary
}

/**
 * A dump's name in two parts, so a narrow column can cut it in the middle:
 * the end — the time it was taken and its extension, which is what tells two
 * dumps apart — is always drawn, and the start gives way.
 */
export function nameParts(name: string, keep = 12): { head: string; tail: string } {
  if (name.length <= keep + 8) return { head: name, tail: "" }
  return { head: name.slice(0, -keep), tail: name.slice(-keep) }
}

/** Where a dump came from, when that is not the ordinary answer. */
export function originWord(origin: DbDumpOrigin | undefined): string {
  if (origin === "upload") return "uploaded"
  if (origin === "safety") return "safety dump"
  return ""
}

/**
 * The newest dump taken of this database. One uploaded from elsewhere is a
 * dump the page keeps, not a backup of what is here now.
 */
export function lastBackup(files: readonly DbBackupFile[]): DbBackupFile | undefined {
  return files.find((file) => file.origin !== "upload")
}

/** How old the last backup is, and whether that is an answer to worry about. */
export function backupAge(
  file: DbBackupFile | undefined,
  now: number,
): { stale: boolean; never: boolean } {
  if (!file) return { stale: true, never: true }
  const taken = Date.parse(file.takenAt)
  return { stale: Number.isFinite(taken) && now - taken > STALE_AFTER_MS, never: false }
}

/** What every kept dump adds up to. */
export function keptBytes(files: readonly DbBackupFile[]): number {
  return files.reduce((sum, file) => sum + (file.size || 0), 0)
}

/** How long a dump took, to the precision it deserves. */
export function tookWords(ms: number | undefined): string {
  if (ms === undefined || ms < 0) return ""
  if (ms < 1000) return "under a second"
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)} s`
  return duration(ms / 1000)
}

/**
 * The last operations as marks, oldest first: every dump that is kept, and
 * every dump, restore or copy the server still remembers that did not end
 * well — a failed dump leaves no file, so the list alone would say every
 * attempt succeeded. The one running now is last.
 */
export function transferOutcomes(
  files: readonly DbBackupFile[],
  jobs: readonly Job[],
  running: Job | undefined,
  when: (iso: string) => string,
  limit = STRIP,
): Outcome[] {
  type Mark = Outcome & { at: number }
  const marks: Mark[] = files.map((file) => ({
    key: `file:${file.file}`,
    tone: "success" as const,
    title: [
      originWord(file.origin) || "dump",
      when(file.takenAt),
      bytes(file.size),
      file.tool ?? file.format,
    ].join(" · "),
    at: Date.parse(file.takenAt) || 0,
  }))
  for (const job of jobs) {
    if (job.status === "running" || job.status === "succeeded") continue
    const kind = transferKind(job) ?? "operation"
    marks.push({
      key: `job:${job.id}`,
      tone: job.status === "failed" ? "danger" : "muted",
      title: `${kind} ${job.status} · ${when(job.endedAt ?? job.startedAt)}${job.error ? ` · ${job.error}` : ""}`,
      at: Date.parse(job.endedAt ?? job.startedAt) || 0,
    })
  }
  marks.sort((a, b) => a.at - b.at)
  const shown: Outcome[] = marks.slice(-limit).map(({ key, tone, title }) => ({ key, tone, title }))
  if (running) {
    shown.push({
      key: `job:${running.id}`,
      tone: "running",
      title: `${running.title}, running now`,
    })
  }
  return shown
}

/** What the strip is, said once for a screen reader. */
export function outcomesLabel(outcomes: readonly Outcome[]): string {
  const failed = outcomes.filter((one) => one.tone === "danger").length
  const running = outcomes.some((one) => one.tone === "running")
  const ended = outcomes.length - (running ? 1 : 0)
  return [
    `The last ${plural(ended, "operation")}`,
    failed > 0 ? `${failed} failed` : "none failed",
    ...(running ? ["one running now"] : []),
  ].join(", ")
}

/** A dump's name as the upload route takes it. */
const DUMP_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,180}$/

/** Why an uploaded dump's name would be refused, or nothing when the server takes it. */
export function uploadNameProblem(name: string, taken: readonly string[] = []): string | undefined {
  const typed = name.trim()
  if (!typed) return "A dump needs a name."
  if (!DUMP_NAME.test(typed)) {
    return "Letters, digits, dots, dashes and underscores, starting with a letter or digit; no spaces and no path."
  }
  if (typed.endsWith(".meta.json"))
    return "A name ending in .meta.json is kept for a dump's description."
  if (taken.includes(typed)) return "A dump of that name is already kept here. Delete it first."
  return undefined
}

/**
 * Why a new database's name would be refused. A key–value server numbers its
 * databases, so there the name is a number; everywhere else it is letters,
 * digits and underscores. The connection's own database is never a new one.
 */
export function newDatabaseProblem(
  name: string,
  { numbered, current }: { numbered: boolean; current: string },
): string | undefined {
  const typed = name.trim()
  if (!typed) return numbered ? "Which numbered database to load into." : "A database needs a name."
  if (numbered) {
    if (!/^\d+$/.test(typed)) return "A number: this server's databases are numbered."
    if (String(Number(typed)) === String(Number(current || "0"))) {
      return "That is the database this connection is on."
    }
    return undefined
  }
  if (!/^[A-Za-z_][A-Za-z0-9_]{0,62}$/.test(typed)) {
    return "Letters, digits and underscores, starting with a letter or an underscore."
  }
  if (typed.toLowerCase() === current.toLowerCase()) {
    return "That is the database this connection is on."
  }
  return undefined
}

/**
 * Where a dump of a server that numbers its databases goes back to. Every key
 * returns to the number it was dumped from, so a dump of several numbers is
 * a restore into several — and the ones that are not the connection's own are
 * what the reader has to be told about before anything runs.
 */
export function restoreReach(
  file: Pick<DbBackupFile, "database">,
  current: string,
): {
  /** The numbers the dump holds, as it lists them. Empty when it does not say. */
  numbers: string[]
  /** Those of them that are not the database this connection is on. */
  others: string[]
  /** Where it goes, in words: "db 9", "databases 1, 2, 9". */
  here: string
  /** More than one database, or an unknown number of them: the verb after `here` is plural. */
  several: boolean
} {
  const own = Number(current || "0")
  const numbers = (file.database ?? "")
    .split(",")
    .map((one) => one.trim())
    .filter(Boolean)
  const others = numbers.filter((one) => Number(one) !== own)
  return {
    numbers,
    others,
    here:
      numbers.length > 1
        ? `databases ${numbers.join(", ")}`
        : numbers.length === 1
          ? `db ${numbers[0]}`
          : "the numbered databases its keys came from",
    several: numbers.length !== 1,
  }
}

/**
 * What restoring this dump over a database does to what is there, in one
 * sentence — read off what the dump is, since that is what decides it.
 */
export function restoreEffect(file: Pick<DbBackupFile, "format" | "origin" | "tool">): string {
  const format = file.format.toLowerCase()
  if (format.includes("sqlite")) {
    return "The file's contents are replaced with the dump's. What was there is kept beside the file as a .bak copy."
  }
  if (format.includes("json lines")) {
    return "Every key or document the dump holds is written back over the one that is there. What the dump does not hold is left as it is."
  }
  if (format.includes("pg_dump")) {
    return "Everything the dump holds is dropped and created again from it. Objects the dump does not hold are left as they are."
  }
  if (format === "archive") {
    return "Every collection the dump holds is replaced with the dump's copy. Collections it does not hold are left as they are."
  }
  if (file.origin === "upload") {
    return "The script is run statement by statement, as written: what it drops or creates is whatever it says, and a statement that names another database reaches it with this connection's account."
  }
  return "Every table the dump holds is dropped and created again from it. Tables the dump does not hold are left as they are."
}

/** A scheduled backup job that dumps this connection: the one that runs next. */
export function scheduledDump(jobs: readonly BackupJob[], id: number): BackupJob | undefined {
  const mine = jobs.filter((job) => job.databaseDumps?.includes(id))
  const enabled = mine.filter((job) => job.enabled)
  const byNext = (a: BackupJob, b: BackupJob) =>
    (Date.parse(a.nextRun ?? "") || Infinity) - (Date.parse(b.nextRun ?? "") || Infinity)
  return [...enabled].sort(byNext)[0] ?? mine[0]
}

/** What a finished job did, in the line the page keeps beside it. */
export function resultWords(
  kind: TransferKind | undefined,
  result: DbTransferResult | undefined,
): string {
  if (!result) return ""
  const parts: string[] = []
  if (kind === "backup") {
    if (result.file) parts.push(result.file)
    if (result.size) parts.push(bytes(result.size))
    if (result.summary) parts.push(result.summary)
    // "written by pg_dump" has already said which tool.
    if (result.tool && !result.summary?.includes(result.tool)) parts.push(result.tool)
  } else {
    if (result.database) parts.push(`into ${result.database}`)
    if (result.summary) parts.push(result.summary)
    if (result.safetyDump) parts.push(`safety dump ${result.safetyDump}`)
  }
  return parts.join(" · ")
}

/** Which tables a dump request names, from what the form holds. */
export function tableChoice(
  mode: "all" | "only" | "except",
  picked: readonly string[],
): Pick<DbDumpContents, "tables" | "excludeTables"> {
  if (mode === "only" && picked.length > 0) return { tables: [...picked] }
  if (mode === "except" && picked.length > 0) return { excludeTables: [...picked] }
  return {}
}
