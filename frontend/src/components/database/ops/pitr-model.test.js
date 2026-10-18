import { describe, expect, test } from "bun:test"
import {
  BEHIND_AFTER,
  archiveHealth,
  commandLine,
  fromLocalInput,
  keepsWords,
  locationWords,
  pastTheEnd,
  pitrJobPrefix,
  settingsText,
  setupProblem,
  targetProblem,
  targetWords,
  toLocalInput,
  windowWords,
} from "./pitr-model"

const archive = (over = {}) => ({
  id: 3,
  container: "shop-db",
  connectionId: 1,
  stream: "shop-db-20261010-030000",
  targetKind: "s3",
  target: { bucket: "backups", prefix: "servers" },
  hasCredentials: true,
  keyFingerprint: "ABCD",
  schedule: "0 3 * * *",
  retentionDays: 7,
  image: "postgres:17-alpine",
  createdAt: "2026-10-10T03:00:00Z",
  ...over,
})

const pitr = (over = {}) => ({
  available: true,
  container: "shop-db",
  running: true,
  archive: archive(),
  earlier: [],
  window: { from: "2026-10-10T03:05:00Z", to: "2026-10-17T09:00:00Z" },
  restorePoints: [],
  restores: [],
  defaults: {
    schedule: "0 3 * * *",
    retentionDays: 7,
    directory: "/var/backups/just-dashboard/wal",
  },
  archiver: {
    archiveMode: "on",
    walLevel: "replica",
    archivedCount: 120,
    lastArchived: "000000010000000000000078",
    lastArchivedAt: "2026-10-17T09:00:00Z",
    failedCount: 0,
    waiting: 0,
    version: 170000,
    inRecovery: false,
  },
  ...over,
})

describe("archiving's health", () => {
  test("a server that hands every segment over is archiving", () => {
    expect(archiveHealth(pitr())).toEqual({ tone: "running", word: "Archiving" })
  })
  test("no archive is off", () => {
    expect(archiveHealth(pitr({ archive: undefined })).word).toBe("Off")
  })
  test("a failure after the last success is failing, with WAL-G's own words", () => {
    const failing = pitr({
      archiveError: "AccessDenied: Access Denied",
      archiver: {
        ...pitr().archiver,
        failedCount: 2,
        lastFailed: "0000000100000000000000079",
        lastFailedAt: "2026-10-17T09:01:00Z",
      },
    })
    expect(archiveHealth(failing)).toMatchObject({
      tone: "danger",
      word: "Failing",
      detail: "AccessDenied: Access Denied",
    })
    // A failure that has been followed by a success is history, not the state.
    const recovered = pitr({
      archiver: { ...pitr().archiver, failedCount: 2, lastFailedAt: "2026-10-17T08:00:00Z" },
    })
    expect(archiveHealth(recovered).word).toBe("Archiving")
  })
  test("segments piling up are behind", () => {
    const behind = pitr({ archiver: { ...pitr().archiver, waiting: BEHIND_AFTER + 1 } })
    expect(archiveHealth(behind)).toMatchObject({ tone: "notice", word: "Behind" })
    expect(archiveHealth(behind).detail).toContain("4 finished segments are waiting")
  })
  test("a container made again without the settings is not archiving, whatever else is true", () => {
    expect(archiveHealth(pitr({ drift: true }))).toMatchObject({
      tone: "danger",
      word: "Not archiving",
    })
  })
  test("a stopped server writes nothing to archive", () => {
    expect(archiveHealth(pitr({ running: false, archiver: undefined })).word).toBe("Server stopped")
  })
})

describe("what an archive is and covers", () => {
  test("the window is a sentence, and an archive with no backup yet says so", () => {
    expect(windowWords(pitr().window)).toStartWith("Any moment from ")
    expect(windowWords({})).toBe("No moment yet: the first base backup has not finished.")
  })
  test("a location is where the operator would look", () => {
    expect(locationWords(archive())).toBe("s3://backups/servers/shop-db-20261010-030000")
    expect(locationWords(archive({ target: { bucket: "/b/" } }))).toBe(
      "s3://b/shop-db-20261010-030000",
    )
    expect(
      locationWords(archive({ targetKind: "local", target: { path: "/var/backups/wal/" } })),
    ).toBe("/var/backups/wal/shop-db-20261010-030000")
  })
  test("retention reads as what is kept", () => {
    expect(keepsWords(7)).toBe("the last 7 days")
    expect(keepsWords(1)).toBe("the last 1 day")
    expect(keepsWords(0)).toBe("everything")
  })
  test("a job belongs to its server by the prefix of its kind", () => {
    expect(pitrJobPrefix("shop-db")).toBe("database.pitr.shop-db.")
  })
})

describe("what a form will run", () => {
  test("the command line quotes what a shell would split", () => {
    expect(
      commandLine([
        "postgres",
        "-c",
        "archive_command=/jd-pitr/wal-g wal-push %p",
        "-c",
        "archive_mode=on",
      ]),
    ).toBe("postgres -c 'archive_command=/jd-pitr/wal-g wal-push %p' -c archive_mode=on")
    expect(commandLine(["-c", "x=it's"])).toBe("-c 'x=it'\\''s'")
  })
  test("settings read as the server's configuration file holds them", () => {
    expect(
      settingsText({ restore_command: "/jd-pitr/wal-g wal-fetch %f %p", archive_mode: "off" }),
    ).toBe("archive_mode = 'off'\nrestore_command = '/jd-pitr/wal-g wal-fetch %f %p'")
  })
  test("a moment survives the round trip through a datetime field", () => {
    const iso = "2026-10-17T09:05:07.000Z"
    expect(fromLocalInput(toLocalInput(iso))).toBe(iso)
    expect(toLocalInput("not a date")).toBe("")
    expect(fromLocalInput("")).toBeUndefined()
  })
})

describe("what a recovery may be asked for", () => {
  const window = { from: "2026-10-10T03:05:00Z", to: "2026-10-17T09:00:00Z" }
  const now = Date.parse("2026-10-17T09:00:30Z")
  test("a moment inside the window, the latest, and a named point are fine", () => {
    expect(
      targetProblem({ kind: "time", time: "2026-10-12T00:00:00Z" }, window, now),
    ).toBeUndefined()
    expect(targetProblem({ kind: "latest" }, window, now)).toBeUndefined()
    expect(targetProblem({ kind: "name", name: "before-migration" }, window, now)).toBeUndefined()
  })
  test("before the archive, in the future, or no answer at all is refused", () => {
    expect(targetProblem({ kind: "time", time: "2026-10-01T00:00:00Z" }, window, now)).toContain(
      "no earlier",
    )
    expect(targetProblem({ kind: "time", time: "2026-10-18T00:00:00Z" }, window, now)).toBe(
      "That moment has not come yet.",
    )
    expect(targetProblem({ kind: "time" }, window, now)).toBe("Choose the moment to recover to.")
    expect(targetProblem({ kind: "name", name: "two words" }, window, now)).toContain("1 to 63")
    expect(targetProblem({ kind: "name", name: "" }, window, now)).toContain("Choose")
  })
  test("a moment after the newest archived segment is the end of the archive", () => {
    expect(pastTheEnd({ kind: "time", time: "2026-10-17T09:00:10Z" }, window)).toBe(true)
    expect(pastTheEnd({ kind: "time", time: "2026-10-17T08:59:00Z" }, window)).toBe(false)
    expect(pastTheEnd({ kind: "latest" }, window)).toBe(false)
  })
  test("a target in words prefers where the copy really stopped", () => {
    expect(targetWords({ kind: "latest" })).toBe("the latest moment")
    expect(targetWords({ kind: "name", name: "before" })).toBe("restore point before")
    expect(targetWords({ kind: "latest" }, "2026-10-17T09:00:00Z")).not.toBe("the latest moment")
  })
})

describe("the setup form", () => {
  const form = (over = {}) => ({
    source: "local",
    path: "/var/backups/just-dashboard/wal",
    bucket: "",
    endpoint: "",
    accessKey: "",
    secretKey: "",
    retentionDays: "7",
    ...over,
  })
  test("each destination asks for what it needs", () => {
    expect(setupProblem(form())).toBeUndefined()
    expect(setupProblem(form({ path: "backups" }))).toContain("full path")
    expect(setupProblem(form({ source: "s3" }))).toBe("Name the bucket.")
    expect(setupProblem(form({ source: "s3", bucket: "b" }))).toBeUndefined()
    expect(setupProblem(form({ source: "b2", bucket: "b" }))).toContain("endpoint")
    expect(setupProblem(form({ source: "s3", bucket: "b", accessKey: "AKIA" }))).toContain(
      "both keys",
    )
    expect(setupProblem(form({ source: "job" }))).toBe("Choose the backup job.")
    expect(setupProblem(form({ source: "job", fromJob: 9 }))).toBeUndefined()
  })
  test("retention is a whole number of days", () => {
    expect(setupProblem(form({ retentionDays: "0" }))).toBeUndefined()
    for (const days of ["-1", "1.5", "4000", "a week"]) {
      expect(setupProblem(form({ retentionDays: days }))).toContain("Keep between")
    }
  })
})
