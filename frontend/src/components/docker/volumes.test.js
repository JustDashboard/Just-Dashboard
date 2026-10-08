import { describe, expect, test } from "bun:test"
import {
  backupOf,
  holderOf,
  holders,
  isAnonymous,
  prunable,
  pruneShare,
  sizeReading,
  splitName,
  standing,
  visibleVolumes,
  volumeProduct,
  withLiveStates,
} from "./volumes"

const MB = 1024 * 1024

function volume(name, options = {}) {
  return {
    name,
    driver: "local",
    mountpoint: `/var/lib/docker/volumes/${name}/_data`,
    createdAt: options.createdAt ?? "2026-09-01T00:00:00Z",
    scope: "local",
    labels: options.labels ?? {},
    size: options.size ?? 10 * MB,
    refCount: options.refCount ?? (options.usedBy ?? []).length,
    inUse: (options.usedBy ?? []).length > 0,
    usedBy: options.usedBy ?? [],
    ...options.extra,
  }
}

const user = (name, state, stack) => ({
  id: `${name}-id`,
  name,
  state,
  destination: "/data",
  readOnly: false,
  stack,
})

const compose = (project, own) => ({
  "com.docker.compose.project": project,
  "com.docker.compose.volume": own,
})

describe("standing", () => {
  test("a volume a running container mounts is in use", () => {
    expect(standing(volume("a", { usedBy: [user("db", "exited"), user("web", "running")] }))).toBe(
      "running",
    )
  })

  test("a stopped container still holds its volume, as the daemon counts it", () => {
    const held = volume("n8n_data", { usedBy: [user("n8n", "exited")] })
    expect(standing(held)).toBe("stopped")
    expect(prunable(held)).toBe(false)
  })

  test("a reference the container listing could not name is still a hold", () => {
    const held = volume("x", { refCount: 1 })
    expect(standing(held)).toBe("stopped")
    expect(prunable(held)).toBe(false)
  })

  test("a stack taken down leaves its volumes behind, and a prune takes them", () => {
    const left = volume("nextcloud_db", { labels: compose("nextcloud", "db") })
    expect(standing(left)).toBe("down")
    expect(prunable(left)).toBe(true)
    expect(holderOf(left)).toEqual({ key: "stack:nextcloud", name: "nextcloud", kind: "stack" })
  })

  test("anonymous by Docker's label, or by the hash before it labelled them", () => {
    expect(isAnonymous(volume("x", { labels: { "com.docker.volume.anonymous": "" } }))).toBe(true)
    expect(isAnonymous(volume("a".repeat(64)))).toBe(true)
    expect(isAnonymous(volume("my-data"))).toBe(false)
    expect(standing(volume("b".repeat(64)))).toBe("anonymous")
    expect(standing(volume("my-data"))).toBe("loose")
  })
})

describe("prunable", () => {
  test("the daemon skips other drivers and local volumes with options", () => {
    expect(prunable(volume("plugin", { extra: { driver: "rclone" } }))).toBe(false)
    expect(
      prunable(volume("nfs", { extra: { options: { type: "nfs", o: "addr=10.0.0.2" } } })),
    ).toBe(false)
    expect(prunable(volume("plain", { extra: { options: {} } }))).toBe(true)
  })

  test("the share counts measured bytes only", () => {
    const share = pruneShare([
      volume("a", { size: 5 * MB }),
      volume("b", { size: -1, refCount: -1 }),
      volume("c", { usedBy: [user("x", "running")] }),
    ])
    expect(share.volumes.map((v) => v.name)).toEqual(["a", "b"])
    expect(share.size).toBe(5 * MB)
  })
})

describe("sizeReading", () => {
  test("names the three things a dash used to mean", () => {
    expect(sizeReading(volume("a", { size: 3 }))).toEqual({ bytes: 3 })
    expect(sizeReading(volume("a", { size: 0, refCount: 0 }))).toEqual({ word: "empty" })
    expect(sizeReading(volume("a", { size: 0, refCount: -1 }))).toEqual({ word: "not measured" })
    expect(
      sizeReading(volume("a", { size: -1, refCount: -1, extra: { driver: "rclone" } })),
    ).toEqual({ word: "not measurable" })
  })
})

test("splitName steps back the project only when Compose wrote the name", () => {
  expect(splitName(volume("shop_pgdata", { labels: compose("shop", "pgdata") }))).toEqual({
    prefix: "shop_",
    rest: "pgdata",
  })
  expect(splitName(volume("custom", { labels: compose("shop", "pgdata") }))).toEqual({
    prefix: "",
    rest: "custom",
  })
})

test("holders sum each stack, each lone container and what nothing mounts", () => {
  const list = holders([
    volume("shop_db", { size: 20 * MB, usedBy: [user("shop-db-1", "running", "shop")] }),
    volume("shop_files", { size: 30 * MB, usedBy: [user("shop-web-1", "running", "shop")] }),
    volume("minio", { size: 40 * MB, usedBy: [user("minio", "running")] }),
    volume("old_db", { size: 5 * MB, labels: compose("old", "db") }),
    volume("loose", { size: 1 * MB }),
  ])
  expect(list.map((h) => [h.name, h.size / MB, h.volumes.length, h.down])).toEqual([
    ["shop", 50, 2, false],
    ["minio", 40, 1, false],
    ["old", 5, 1, true],
    ["Nothing", 1, 1, false],
  ])
})

test("backupOf tells a paused job from no job", () => {
  const base = { kind: "volume", id: "v", name: "v", suggest: { name: "v", sources: [] } }
  expect(
    backupOf({
      ...base,
      coveredBy: [{ jobId: 1, jobName: "Nightly", enabled: true }],
      protected: true,
      lastBackupAt: "2026-10-08T00:00:00Z",
    }),
  ).toEqual({
    state: "protected",
    jobs: [{ id: 1, name: "Nightly" }],
    lastAt: "2026-10-08T00:00:00Z",
  })
  expect(
    backupOf({
      ...base,
      coveredBy: [{ jobId: 2, jobName: "Weekly", enabled: false }],
      protected: false,
    }).state,
  ).toBe("paused")
  expect(backupOf({ ...base, coveredBy: [], protected: false }).state).toBe("none")
  expect(backupOf(undefined)).toBeUndefined()
})

test("live states replace a user's state without touching unchanged volumes", () => {
  const still = volume("a", { usedBy: [user("a", "running")] })
  const moved = volume("b", { usedBy: [user("b", "running")] })
  const next = withLiveStates([still, moved], new Map([["b-id", "exited"]]))
  expect(next[0]).toBe(still)
  expect(next[1].usedBy[0].state).toBe("exited")
  expect(standing(next[1])).toBe("stopped")
})

describe("visibleVolumes", () => {
  const list = [
    volume("big", {
      size: 90 * MB,
      usedBy: [user("minio", "running")],
      createdAt: "2026-01-01T00:00:00Z",
    }),
    volume("mid", {
      size: 50 * MB,
      usedBy: [user("n8n", "exited")],
      createdAt: "2026-03-01T00:00:00Z",
    }),
    volume("gone_db", {
      size: 20 * MB,
      labels: compose("gone", "db"),
      createdAt: "2026-05-01T00:00:00Z",
    }),
    volume("c".repeat(64), { size: 1 * MB, createdAt: "2026-07-01T00:00:00Z" }),
  ]
  const base = {
    query: "",
    show: "all",
    standing: "",
    holder: "",
    sort: { key: "size", desc: true },
    backups: new Map([
      ["big", { state: "protected", jobs: [] }],
      ["mid", { state: "none", jobs: [] }],
    ]),
  }
  const names = (options) =>
    visibleVolumes(list, { ...base, ...options }).map((v) => v.name.slice(0, 8))

  test("largest first by default, and every column orders both ways", () => {
    expect(names({})).toEqual(["big", "mid", "gone_db", "cccccccc"])
    expect(names({ sort: { key: "created", desc: true } })).toEqual([
      "cccccccc",
      "gone_db",
      "mid",
      "big",
    ])
    expect(names({ sort: { key: "standing", desc: false } })).toEqual([
      "big",
      "mid",
      "gone_db",
      "cccccccc",
    ])
    expect(names({ sort: { key: "name", desc: false } })).toEqual([
      "big",
      "cccccccc",
      "gone_db",
      "mid",
    ])
  })

  test("the chips narrow by standing and by backup", () => {
    expect(names({ show: "unmounted" })).toEqual(["gone_db", "cccccccc"])
    expect(names({ show: "stopped" })).toEqual(["mid"])
    // Only volumes the Backups page answered for can be called unprotected.
    expect(names({ show: "unprotected" })).toEqual(["mid"])
  })

  test("search reads the containers, their paths and the holder", () => {
    expect(names({ query: "n8n" })).toEqual(["mid"])
    expect(names({ query: "gone" })).toEqual(["gone_db"])
    expect(names({ query: "/data" })).toEqual(["big", "mid"])
  })

  test("the band narrows to a holder or a standing", () => {
    expect(names({ holder: "stack:gone" })).toEqual(["gone_db"])
    expect(names({ standing: "anonymous" })).toEqual(["cccccccc"])
  })
})

test("a volume is drawn as what mounts it, or as what its stack and name say", () => {
  const products = new Map([
    ["db-id", "postgresql"],
    ["app-id", "docker"],
  ])
  expect(
    volumeProduct(
      volume("x", { usedBy: [user("app", "running"), user("db", "running")] }),
      products,
    ),
  ).toBe("postgresql")
  expect(
    volumeProduct(volume("nextcloud_db", { labels: compose("nextcloud", "db") }), products),
  ).toBe("nextcloud")
  expect(volumeProduct(volume("redis-cache"), products)).toBe("redis")
  // Two letters of a suffix are not a language.
  expect(volumeProduct(volume("app_r"), products)).toBeUndefined()
  expect(volumeProduct(volume("d".repeat(64)), products)).toBeUndefined()
})
