import { describe, expect, test } from "bun:test"
import {
  ENGINE_ROOM,
  enginePicture,
  engineRelated,
  engineWords,
  volumeName,
} from "./engine-picture-model"

const container = (name, stack, over = {}) => ({
  id: `${name}-id`,
  name,
  image: `${name}:1`,
  state: "running",
  status: "Up 2 hours",
  composeStack: stack,
  ...over,
})

const stack = (name, over = {}) => ({
  name,
  services: [],
  running: 1,
  total: 1,
  deployed: true,
  state: "running",
  ...over,
})

const network = (name, usedBy, over = {}) => ({
  id: `${name}-id`,
  name,
  internal: false,
  labels: {},
  usedBy,
  ...over,
})

const volume = (name, users, over = {}) => ({
  name,
  labels: {},
  size: 1024 * 1024,
  usedBy: users.map((user) => ({ id: `${user}-id`, name: user })),
  ...over,
})

describe("enginePicture, the left side", () => {
  test("a project with a failing container is lit, and leads", () => {
    const picture = enginePicture({
      containers: [
        container("shop-web", "shop"),
        container("shop-worker", "shop", {
          state: "exited",
          status: "Exited (1) 12 minutes ago",
        }),
        container("blog-web", "blog"),
        container("portainer"),
      ],
      stacks: [stack("blog"), stack("shop", { running: 1, total: 2, state: "partial" })],
    })
    expect(picture.start.map((node) => [node.id, node.state])).toEqual([
      ["stack:shop", "danger"],
      ["stack:blog", "up"],
      ["container:portainer-id", "up"],
    ])
    expect(picture.start[0].words).toBe("1 of 2 running · 1 failing")
    expect(picture.start[0].href).toBe("/docker/stacks/shop")
    expect(picture.totals).toMatchObject({ stacks: 2, standalone: 1 })
  })

  test("partly up is amber, stopped is down, never deployed is idle", () => {
    const picture = enginePicture({
      containers: [container("a-web", "a", { health: "starting" })],
      stacks: [
        stack("a"),
        stack("b", { running: 0, state: "stopped" }),
        stack("c", { running: 0, total: 3, deployed: false, state: "not-deployed" }),
      ],
    })
    const state = Object.fromEntries(picture.start.map((node) => [node.name, node.state]))
    expect(state).toEqual({ a: "warning", b: "down", c: "idle" })
    const words = Object.fromEntries(picture.start.map((node) => [node.name, node.words]))
    expect(words.b).toBe("stopped")
    expect(words.c).toBe("not deployed · 3 services defined")
  })

  test("a container started by hand says how it stopped; a stop is not a failure", () => {
    const picture = enginePicture({
      containers: [
        container("mailhog", undefined, { state: "exited", status: "Exited (0) 2 days ago" }),
        container("kuma", undefined, { health: "unhealthy" }),
        container("job", undefined, { state: "exited", status: "Exited (2) 3 minutes ago" }),
        // 137 is what `docker stop` leaves when the grace period runs out…
        container("slow", undefined, { state: "exited", status: "Exited (137) 1 hour ago" }),
        // …and what the kernel leaves, which only the event log tells apart.
        container("hungry", undefined, { state: "exited", status: "Exited (137) 5 minutes ago" }),
        container("fresh", undefined, { state: "created", status: "Created" }),
      ],
      stacks: [],
      oomKilled: new Set(["hungry-id"]),
    })
    expect(picture.start.map((node) => [node.name, node.state, node.words])).toEqual([
      ["hungry", "danger", "killed for memory · 5 minutes ago"],
      ["job", "danger", "exit 2 · 3 minutes ago"],
      ["kuma", "danger", "failing its health check"],
      ["fresh", "down", "never started"],
      ["mailhog", "down", "stopped · 2 days ago"],
      ["slow", "down", "stopped · 1 hour ago"],
    ])
  })

  test("a project is lit by a kill for memory the same way", () => {
    const worker = container("shop-worker", "shop", {
      state: "exited",
      status: "Exited (137) 12 minutes ago",
    })
    const shop = stack("shop", { running: 1, total: 2, state: "partial" })
    const web = container("shop-web", "shop")
    expect(enginePicture({ containers: [web, worker], stacks: [shop] }).start[0]).toMatchObject({
      state: "warning",
      words: "1 of 2 running",
    })
    expect(
      enginePicture({
        containers: [web, worker],
        stacks: [shop],
        oomKilled: new Set(["shop-worker-id"]),
      }).start[0],
    ).toMatchObject({ state: "danger", words: "1 of 2 running · 1 failing" })
  })

  test("a project the stack list does not know keeps its containers on the picture", () => {
    const picture = enginePicture({ containers: [container("lost-web", "lost")], stacks: [] })
    expect(picture.start.map((node) => node.id)).toEqual(["container:lost-web-id"])
  })

  test("what a side has no room for is counted, and one over the room is drawn", () => {
    const names = (n) => Array.from({ length: n }, (_, i) => `s${String(i).padStart(2, "0")}`)
    const some = enginePicture({
      containers: [],
      stacks: names(ENGINE_ROOM.start + 1).map((name) => stack(name)),
    })
    expect(some.start).toHaveLength(ENGINE_ROOM.start + 1)
    expect(some.more.start).toEqual([])
    const many = enginePicture({
      containers: [],
      stacks: names(ENGINE_ROOM.start + 4).map((name) => stack(name)),
    })
    expect(many.start).toHaveLength(ENGINE_ROOM.start)
    expect(many.more.start).toEqual(["s06", "s07", "s08", "s09"])
  })
})

describe("enginePicture, the right side", () => {
  const containers = [
    container("shop-web", "shop"),
    container("shop-db", "shop"),
    container("portainer"),
    container("old", undefined, { state: "exited", status: "Exited (0) 2 days ago" }),
  ]
  const stacks = [stack("shop", { running: 2, total: 2 })]

  test("a network is carried while a container on it runs, and Docker's empty three are left out", () => {
    const picture = enginePicture({
      containers,
      stacks,
      networks: [
        network("shop_default", ["shop-web", "shop-db"]),
        network("bridge", ["portainer"], { owner: { kind: "system" } }),
        network("host", [], { owner: { kind: "system" } }),
        network("legacy", ["old"]),
        network("spare", []),
        network("unread", [], { membersKnown: false }),
      ],
      volumes: [],
    })
    expect(picture.totals.networks).toBe(5)
    const byName = Object.fromEntries(picture.end.map((node) => [node.name, node]))
    expect(byName.shop_default.state).toBe("up")
    expect(byName.shop_default.words).toBe("2 containers")
    expect(byName.shop_default.peers).toEqual(["stack:shop"])
    expect(byName.bridge.peers).toEqual(["container:portainer-id"])
    expect(byName.host).toBeUndefined()
    // Three have room when there are more than four; the rest are counted,
    // and the ones with the most containers on them are the ones drawn.
    expect(picture.end.filter((node) => node.kind === "network").map((node) => node.name)).toEqual([
      "shop_default",
      "bridge",
      "legacy",
    ])
    expect(picture.more.networks).toEqual(["spare", "unread"])
  })

  test("a volume nothing mounts is amber and first, and says so", () => {
    const picture = enginePicture({
      containers,
      stacks,
      networks: [],
      volumes: [
        volume("shop_pgdata", ["shop-db"], {
          labels: { "com.docker.compose.project": "shop" },
          size: 3 * 1024 * 1024 * 1024,
        }),
        volume("leftover", [], { size: 10 }),
        volume("nas", [], { mountType: "nfs" }),
        volume("old-data", ["old"]),
      ],
    })
    expect(picture.end.map((node) => [node.name, node.state])).toEqual([
      ["leftover", "warning"],
      ["pgdata", "up"],
      ["old-data", "down"],
      ["nas", "idle"],
    ])
    const pgdata = picture.end.find((node) => node.name === "pgdata")
    expect(pgdata.prefix).toBe("shop_")
    expect(pgdata.words).toBe("3.0 GB · shop-db")
    expect(pgdata.href).toBe("/docker/volumes/shop_pgdata")
    expect(picture.end[0].words).toContain("nothing mounts it")
  })

  test("unread networks and volumes leave the side empty rather than wrong", () => {
    const picture = enginePicture({ containers, stacks })
    expect(picture.end).toEqual([])
    expect(picture.totals).toMatchObject({ networks: 0, volumes: 0 })
  })
})

describe("engineRelated", () => {
  test("a project lights its own networks and volumes, both ways", () => {
    const picture = enginePicture({
      containers: [container("shop-db", "shop"), container("blog-web", "blog")],
      stacks: [stack("shop"), stack("blog")],
      networks: [network("shop_default", ["shop-db"]), network("blog_default", ["blog-web"])],
      volumes: [
        volume("shop_pgdata", ["shop-db"]),
        // Nothing mounts it now; its label still says whose it was.
        volume("blog_uploads", [], { labels: { "com.docker.compose.project": "blog" } }),
      ],
    })
    const related = engineRelated([...picture.start, ...picture.end])
    expect(related("stack:shop", "network:shop_default-id")).toBe(true)
    expect(related("stack:shop", "volume:shop_pgdata")).toBe(true)
    expect(related("volume:shop_pgdata", "stack:shop")).toBe(true)
    expect(related("stack:shop", "network:blog_default-id")).toBe(false)
    expect(related("stack:blog", "volume:blog_uploads")).toBe(true)
  })
})

test("engineWords counts what runs", () => {
  expect(
    engineWords([container("a"), container("b", undefined, { state: "exited", status: "" })]),
  ).toBe("1 of 2 containers running")
})

test("volumeName sets a project's prefix apart and shortens an anonymous name", () => {
  expect(
    volumeName({ name: "shop_pgdata", labels: { "com.docker.compose.project": "shop" } }),
  ).toEqual({ prefix: "shop_", name: "pgdata" })
  expect(volumeName({ name: "plain", labels: {} })).toEqual({ name: "plain" })
  expect(volumeName({ name: "ab12".repeat(16), labels: {} })).toEqual({ name: "ab12ab12ab12" })
})
