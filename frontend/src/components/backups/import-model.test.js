import { describe, expect, test } from "bun:test"
import {
  EMPTY_DESTINATION,
  archiveState,
  contentsWords,
  destinationBody,
  destinationReady,
  importSummary,
  importableArchives,
  knownDestinations,
  missingKeys,
  originTitle,
  shortKey,
} from "./import-model"

const job = (id, name, targetKind, target) => ({ id, name, targetKind, target })

describe("knownDestinations", () => {
  test("a bucket's prefixes are places of their own, and one place is listed once", () => {
    const list = knownDestinations([
      job(1, "Files", "s3", { bucket: "b", prefix: "nightly" }),
      job(2, "Database", "s3", { bucket: "b", prefix: "/nightly/" }),
      job(3, "Media", "s3", { bucket: "b", prefix: "media" }),
      job(4, "Here", "local", { path: "/var/backups" }),
    ])
    expect(list.map((d) => d.place)).toEqual(["b/nightly", "b/media", "/var/backups"])
    expect(list[0].jobs).toEqual(["Files", "Database"])
    expect(list[0].jobId).toBe(1)
    expect(list[0].service).toBe("Amazon S3")
    expect(list[2].service).toBe("A directory on this server")
  })

  test("names SFTP and rclone destinations the way their jobs do", () => {
    const [sftp, rclone] = knownDestinations([
      job(5, "Offsite", "sftp", {
        host: "backup.example.com",
        port: 2222,
        username: "ops",
        path: "srv",
      }),
      job(6, "Drive", "rclone", { remoteType: "drive", path: "backups" }),
    ])
    expect(sftp.service).toBe("SFTP · backup.example.com")
    expect(sftp.place).toBe("ops@backup.example.com:2222:srv")
    expect(rclone.service).toBe("Google Drive")
  })
})

describe("an entered destination", () => {
  test("is ready once it has what reading it needs", () => {
    expect(destinationReady({ ...EMPTY_DESTINATION, kind: "s3" })).toBe(false)
    expect(destinationReady({ ...EMPTY_DESTINATION, kind: "s3", bucket: "b" })).toBe(true)
    expect(destinationReady({ ...EMPTY_DESTINATION, kind: "b2", bucket: "b" })).toBe(false)
    expect(
      destinationReady({ ...EMPTY_DESTINATION, kind: "sftp", host: "h", username: "u", path: "p" }),
    ).toBe(false)
    expect(
      destinationReady({
        ...EMPTY_DESTINATION,
        kind: "sftp",
        host: "h",
        username: "u",
        path: "p",
        password: "x",
      }),
    ).toBe(true)
    expect(destinationReady({ ...EMPTY_DESTINATION, kind: "rclone" })).toBe(false)
  })

  test("sends only the keys its kind takes, and an SFTP key a reading handed back", () => {
    expect(
      destinationBody({
        ...EMPTY_DESTINATION,
        kind: "s3",
        bucket: " b ",
        accessKey: "a",
        secretKey: "s",
      }),
    ).toEqual({
      targetKind: "s3",
      target: { bucket: "b", region: "", endpoint: "", prefix: "" },
      secrets: { accessKeyId: "a", secretAccessKey: "s" },
    })
    expect(
      destinationBody({ ...EMPTY_DESTINATION, kind: "s3", bucket: "b" }).secrets,
    ).toBeUndefined()
    const sftp = destinationBody({
      ...EMPTY_DESTINATION,
      kind: "sftp",
      host: "h",
      port: "",
      username: "u",
      path: "p",
      password: "pw",
      hostKey: "ssh-ed25519 AAAA",
    })
    expect(sftp).toEqual({
      targetKind: "sftp",
      target: { host: "h", port: 22, username: "u", path: "p", hostKey: "ssh-ed25519 AAAA" },
      secrets: { password: "pw" },
    })
    expect(destinationBody({ ...EMPTY_DESTINATION, kind: "local", path: " /mnt/old " })).toEqual({
      targetKind: "local",
      target: { path: "/mnt/old" },
    })
  })
})

const archive = (over = {}) => ({
  key: "k",
  name: "n",
  bytes: 1,
  manifest: "sidecar",
  contents: { paths: 0, volumes: 0, databaseDumps: 0, sqliteSnapshots: 0 },
  kinds: [],
  encrypted: false,
  openable: true,
  importable: true,
  ...over,
})

describe("archiveState", () => {
  test("says where each archive stands for this server", () => {
    expect(archiveState(archive()).state).toBe("importable")
    expect(archiveState(archive({ importable: false, importedRun: 4 })).state).toBe("imported")
    expect(
      archiveState(
        archive({ importable: false, openable: false, needsKey: true, recipient: "age1x" }),
      ),
    ).toEqual({ state: "needs-key", label: "needs its key", tone: "warning" })
    expect(archiveState(archive({ importable: false, manifest: "none" })).state).toBe("no-manifest")
    expect(archiveState(archive({ importable: false, problem: "damaged" })).tone).toBe("danger")
  })
})

describe("catalogue readings", () => {
  const catalogue = {
    destination: "b",
    archives: 4,
    ours: 0,
    origins: [
      {
        id: "x",
        host: "old",
        jobs: [
          {
            originJobId: 1,
            name: "Files",
            bytes: 4,
            archives: [
              archive(),
              archive({ importable: false, needsKey: true, recipient: "age1one" }),
              archive({ importable: false, needsKey: true, recipient: "age1one" }),
              archive({ importable: false, needsKey: true, recipient: "age1two" }),
            ],
          },
        ],
      },
    ],
  }

  test("lists each missing key once", () => {
    expect(missingKeys(catalogue)).toEqual(["age1one", "age1two"])
  })

  test("a job's import takes only what can be imported now", () => {
    expect(importableArchives(catalogue.origins[0].jobs[0])).toHaveLength(1)
  })

  test("names what covers what and who took it", () => {
    expect(contentsWords({ paths: 2, volumes: 1, databaseDumps: 1, sqliteSnapshots: 0 })).toBe(
      "2 paths · 1 volume · 1 database dump",
    )
    expect(originTitle({ id: "x", host: "old", jobs: [] })).toBe("old")
    expect(originTitle({ id: "x", thisServer: true, jobs: [] })).toBe(
      "This server, records no longer here",
    )
    expect(originTitle({ id: "", jobs: [] })).toBe("Written before archives carried their manifest")
  })

  test("reports an import in a sentence", () => {
    const into = { name: "Files (old)" }
    expect(importSummary({ jobs: [into], runs: [{}, {}], skipped: [] })).toBe(
      "Imported 2 archives into Files (old)",
    )
    expect(importSummary({ jobs: [], runs: [], skipped: [{ key: "k", reason: "r" }] })).toBe(
      "Nothing imported; 1 left",
    )
  })

  test("shortens a key keeping both ends", () => {
    expect(shortKey("age1qk7e2zt0v9g8mlxw4a6sdyhr5u3c8fj2np0wtq9ezlx3kv7m4ya6sl2hjd")).toBe(
      "age1qk7e2zt0…a6sl2hjd",
    )
  })
})
