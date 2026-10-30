import { createHash } from "node:crypto"
import type { Page, Route } from "@playwright/test"
import { jobs } from "./workbench-fixture"

/**
 * The API a fresh server's Backups page talks to while it imports from a
 * destination: a catalogue of another server's archives (two jobs, one sealed
 * to a key this server does not hold yet, and an archive from before
 * manifests were written beside archives), the import, the key import, and
 * the imported job with its runs and a restore plan that has to make a volume
 * and a directory. `mockImport` records what each route was sent.
 */
const now = Date.now()
const H = 3_600_000
const iso = (ms: number) => new Date(ms).toISOString()
const digest = (seed: string) => `sha256:${createHash("sha256").update(seed).digest("hex")}`
export const OLD_KEY = "age1qk7e2zt0v9g8mlxw4a6sdyhr5u3c8fj2np0wtq9ezlx3kv7m4ya6sl2hjd"
export const ORIGIN = "c4mzq2v7xk3rjw8n5tbh6pyd1f"

const archive = (key: string, ago: number, over: Record<string, unknown> = {}) => ({
  key: `db/${key}`,
  name: key,
  bytes: 412_000_000,
  startedAt: iso(now - ago),
  manifest: "sidecar",
  contents: { paths: 1, volumes: 1, databaseDumps: 0, sqliteSnapshots: 0 },
  kinds: ["paths", "volume"],
  digest: digest(key),
  encrypted: false,
  openable: true,
  importable: true,
  ...over,
})

export function catalogue({ keyHeld = false, imported = false } = {}) {
  const appArchives = [
    archive("App-files-job3-run41-20261029-030000-K7QXW2M4.tar.gz", 22 * H),
    archive("App-files-job3-run40-20261028-030000-P3MZ9T2D.tar.gz", 46 * H),
  ].map((item, index) =>
    imported ? { ...item, importable: false, importedRun: 902 - index, importedJob: 9 } : item,
  )
  const sealed = archive(
    "Postgres-nightly-job5-run77-20261029-023000-H8RWX4QZ.tar.gz.age",
    23 * H,
    {
      contents: { paths: 0, volumes: 0, databaseDumps: 2, sqliteSnapshots: 0 },
      kinds: ["dump"],
      encrypted: true,
      recipient: OLD_KEY,
      ...(keyHeld
        ? {}
        : {
            openable: false,
            importable: false,
            needsKey: true,
            problem:
              "Sealed to a backup key this server does not hold. Import that key to open it here.",
          }),
    },
  )
  return {
    destination: "wayy-backups/db",
    archives: 4,
    ours: 12,
    origins: [
      {
        id: ORIGIN,
        host: "old-vps",
        jobs: [
          {
            originJobId: 3,
            name: "App files",
            bytes: 824_000_000,
            newest: iso(now - 22 * H),
            archives: appArchives,
            ...(imported ? { importedJob: 9 } : {}),
          },
          {
            originJobId: 5,
            name: "Postgres nightly",
            bytes: 412_000_000,
            newest: iso(now - 23 * H),
            archives: [sealed],
          },
        ],
      },
      {
        id: "",
        jobs: [
          {
            originJobId: 4,
            name: "Legacy",
            bytes: 412_000_000,
            archives: [
              archive("Legacy-job4-run7-20250101-030000-ABCDEFGH.tar.gz", 300 * 24 * H, {
                manifest: "none",
                contents: { paths: 0, volumes: 0, databaseDumps: 0, sqliteSnapshots: 0 },
                kinds: [],
                importable: false,
                openable: false,
                problem:
                  "Written before archives carried their manifest beside them, so there is nothing to prove it against.",
              }),
            ],
          },
        ],
      },
    ],
  }
}

const importedJob = {
  id: 9,
  revision: 1,
  name: "App files (old-vps)",
  sources: ["/var/lib/docker/volumes/app_data/_data", "/srv/app"],
  excludes: [],
  targetKind: "b2",
  // Where it was imported from: the same place job 2 writes, read with its keys.
  target: { bucket: "wayy-backups", prefix: "db/" },
  schedule: "",
  retention: 0,
  retentionDays: 0,
  enabled: false,
  createdAt: iso(now - H),
  hasCredentials: true,
  imported: true,
  origin: { id: ORIGIN, host: "old-vps", jobId: 3, jobName: "App files" },
  lastRun: undefined as unknown,
  stored: { runs: 2, bytes: 824_000_000 },
}

const importedRun = (id: number, runId: number, ago: number) => ({
  id,
  jobId: 9,
  startedAt: iso(now - ago),
  status: "success",
  artifact: `db/App-files-job3-run${runId}-x.tar.gz`,
  sizeBytes: 412_000_000,
  log: `imported from wayy-backups/db\noriginally run ${runId} of job "App files" on old-vps\nproved the archive against its manifest`,
  trigger: "import",
  imported: true,
  manifest: {
    version: 1,
    artifactDigest: digest(`run-${runId}`),
    complete: true,
    sources: [
      {
        path: "/var/lib/docker/volumes/app_data/_data",
        archivePath: "source-0001",
        kind: "volume",
        volume: "app_data",
      },
      { path: "/srv/app", archivePath: "source-0002" },
    ],
    origin: {
      id: ORIGIN,
      host: "old-vps",
      jobId: 3,
      jobName: "App files",
      runId,
      startedAt: iso(now - ago),
    },
  },
})
const importedRuns = [importedRun(902, 41, 22 * H), importedRun(901, 40, 46 * H)]
importedJob.lastRun = importedRuns[0]

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

export async function mockImport(page: Page) {
  const state = {
    imported: false,
    keyHeld: false,
    catalogueBodies: [] as unknown[],
    importBodies: [] as unknown[],
    keyBodies: [] as unknown[],
    restoreBodies: [] as unknown[],
  }
  await page.route("**/api/v1/backups/", (route) =>
    json(route, state.imported ? [...jobs, importedJob] : jobs),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/catalogue",
    (route) => {
      state.catalogueBodies.push(route.request().postDataJSON())
      return json(route, catalogue({ keyHeld: state.keyHeld, imported: state.imported }))
    },
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/import",
    (route) => {
      state.importBodies.push(route.request().postDataJSON())
      state.imported = true
      return json(route, {
        destination: "wayy-backups/db",
        jobs: [importedJob],
        runs: importedRuns,
        skipped: [],
      })
    },
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/key",
    (route) =>
      json(route, {
        exists: false,
        encryptingJobs: 0,
        encryptedArchives: 0,
        ...(state.keyHeld ? { imported: [{ recipient: OLD_KEY, importedAt: iso(now) }] } : {}),
      }),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/key/import",
    (route) => {
      state.keyBodies.push(route.request().postDataJSON())
      state.keyHeld = true
      return json(route, { recipient: OLD_KEY, became: "opens" })
    },
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/9",
    (route) => json(route, importedJob),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/9/runs",
    (route) => json(route, { runs: importedRuns, running: false }),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/runs/902/restore-plan",
    (route) =>
      json(route, {
        targets: [
          {
            archivePath: "source-0001",
            path: "/var/lib/docker/volumes/app_data/_data",
            target: "/var/lib/docker/volumes/app_data/_data",
            kind: "volume",
            volume: "app_data",
            exists: false,
            creates: "volume",
          },
          {
            archivePath: "source-0002",
            path: "/srv/app",
            target: "/srv/app",
            kind: "path",
            exists: false,
            creates: "directory",
          },
        ],
      }),
  )
  await page.route(
    (url) => url.pathname === "/api/v1/backups/runs/902/restore",
    (route) => {
      state.restoreBodies.push(route.request().postDataJSON())
      return json(route, {
        runId: 902,
        destination: "",
        entries: 1204,
        bytes: 812_000_000,
        targets: ["/var/lib/docker/volumes/app_data/_data", "/srv/app"],
        created: ["volume app_data", "/srv/app"],
      })
    },
  )
  return state
}
