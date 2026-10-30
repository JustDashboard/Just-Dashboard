import { describe, expect, test } from "bun:test"
import {
  FACE_SIZE,
  containerRole,
  detectSystems,
  layoutAtlas,
  neighbours,
  searchNodes,
  searchSystems,
  settle,
  systemCensus,
  systemTitle,
} from "./server-systems"
import { DEFAULT_DOCUMENT, decodeDocument, isArranged } from "./server-map"

const item = (id, name, extra = {}) => ({
  id,
  name,
  tone: "ok",
  state: "running",
  href: `/x/${encodeURIComponent(id)}`,
  ...extra,
})

const reading = (key, items, extra = {}) => ({
  key,
  total: items.length,
  items,
  state: "read",
  ...extra,
})
const link = (from, to, via, also) => ({ from, to, via, ...(also ? { also } : {}) })

function serverMap(kinds, links = []) {
  return { host: { hostname: "box" }, kinds, links, checkedAt: "2026-10-29T00:00:00Z" }
}

/**
 * A deployment the way the server reports one: its release in a stack of two
 * containers, a bound Postgres in its own container with a volume and a nightly
 * dump, a domain with its certificate and the checkout it is built from.
 */
function shop() {
  return serverMap(
    [
      reading("projects", [item("project:1", "shop", { product: "nextjs" })]),
      reading("stacks", [item("stack:jd-shop", "jd-shop")]),
      reading("containers", [
        item("container:web", "shop-web-1", { product: "nodejs" }),
        item("container:worker", "shop-worker-1", { product: "nodejs" }),
        item("container:pg", "shop-postgres", { product: "postgres" }),
      ]),
      reading("databases", [item("db:1", "shop-db", { product: "postgres" })]),
      reading("volumes", [item("volume:pg", "shop-pg-data")]),
      reading("domains", [item("domain:shop.example.com", "shop.example.com")]),
      reading("certificates", [item("cert:/le/shop", "shop.example.com")]),
      reading("repositories", [item("repo:/srv/shop", "shop", { product: "git" })]),
      reading("backups", [item("backup:1", "shop nightly")]),
      reading("images", [item("image:sha", "node:22")]),
      reading("keys", [item("key:1", "ci")]),
    ],
    [
      link("project:1", "container:web", "release"),
      link("project:1", "container:worker", "release"),
      link("project:1", "stack:jd-shop", "release"),
      link("stack:jd-shop", "container:web", "member"),
      link("stack:jd-shop", "container:worker", "member"),
      link("project:1", "db:1", "binding", ["reference"]),
      link("container:pg", "db:1", "serves"),
      link("container:pg", "volume:pg", "mount"),
      link("db:1", "backup:1", "backup"),
      link("domain:shop.example.com", "project:1", "route"),
      link("domain:shop.example.com", "cert:/le/shop", "certificate"),
      link("project:1", "repo:/srv/shop", "source"),
      link("container:web", "image:sha", "image"),
    ],
  )
}

describe("containerRole", () => {
  test("an image that says what it is decides", () => {
    expect(containerRole({ name: "main", product: "postgres" })).toBe("database")
    expect(containerRole({ name: "x", product: "redis" })).toBe("cache")
    expect(containerRole({ name: "edge", product: "caddy" })).toBe("proxy")
    expect(containerRole({ name: "site", product: "nextjs" })).toBe("web")
  })

  test("a language's image is read by its name", () => {
    expect(containerRole({ name: "shop-worker-1", product: "nodejs" })).toBe("worker")
    expect(containerRole({ name: "just-dashboard-backend-1", product: "docker" })).toBe("api")
    expect(containerRole({ name: "just-dashboard-frontend-1", product: "docker" })).toBe("web")
    expect(containerRole({ name: "blog-1", product: "nginx" })).toBe("web")
    expect(containerRole({ name: "thing", product: "python" })).toBe("app")
  })
})

describe("detectSystems", () => {
  test("a deployment takes what it runs, keeps and answers to", () => {
    const [system, ...rest] = detectSystems(shop())
    expect(rest).toHaveLength(0)
    expect(system.kind).toBe("project")
    expect(systemTitle(system)).toBe("shop")
    const items = new Set(system.nodes.map((n) => n.itemId))
    for (const id of [
      "project:1",
      "container:web",
      "container:worker",
      "container:pg",
      "db:1",
      "volume:pg",
      "backup:1",
      "domain:shop.example.com",
      "cert:/le/shop",
      "repo:/srv/shop",
      "logs:project:1",
    ]) {
      expect(items.has(id)).toBe(true)
    }
    // What the map leaves to its own pages is not drawn.
    expect(items.has("image:sha")).toBe(false)
    expect(items.has("key:1")).toBe(false)
  })

  test("a release that runs as a stack is framed, with one line to the frame", () => {
    const [system] = detectSystems(shop())
    expect(system.frames.map((f) => f.itemId)).toEqual(["stack:jd-shop"])
    const web = system.nodes.find((n) => n.itemId === "container:web")
    expect(web.frame).toBe("stack:jd-shop")
    const fromProject = system.edges.filter(
      (e) => e.source.endsWith("/project:1") && e.shape === "flow",
    )
    expect(fromProject.map((e) => e.target.split("/").pop()).sort()).toEqual([
      "db:1",
      "frame:stack:jd-shop",
    ])
    // The stack's members are held by its frame, not wired to it.
    expect(system.edges.some((e) => e.via === "member")).toBe(false)
  })

  test("the checkout, the logs and the certificate hang under their boxes", () => {
    const [system] = detectSystems(shop())
    const project = system.nodes.find((n) => n.itemId === "project:1")
    const domain = system.nodes.find((n) => n.itemId === "domain:shop.example.com")
    const repo = system.nodes.find((n) => n.itemId === "repo:/srv/shop")
    const logs = system.nodes.find((n) => n.itemId === "logs:project:1")
    const cert = system.nodes.find((n) => n.itemId === "cert:/le/shop")
    expect(repo.face).toBe("satellite")
    expect(repo.host).toBe(project.id)
    expect(repo.port).toBe("Source")
    expect(logs.host).toBe(project.id)
    expect(logs.item.href).toBe(`${project.item.href}/logs`)
    expect(cert.host).toBe(domain.id)
    for (const sat of [repo, logs]) {
      expect(sat.y).toBeGreaterThanOrEqual(project.y + project.height)
    }
    const sub = system.edges.filter((e) => e.shape === "sub")
    expect(sub).toHaveLength(3)
  })

  test("lines run left to right: the domain, the deployment, its containers, its data, the backup", () => {
    const [system] = detectSystems(shop())
    const x = (id) => system.nodes.find((n) => n.itemId === id).x
    expect(x("domain:shop.example.com")).toBeLessThan(x("project:1"))
    expect(x("project:1")).toBeLessThan(x("container:web"))
    expect(x("container:pg")).toBeLessThan(x("db:1"))
    expect(x("db:1")).toBeLessThan(x("backup:1"))
    const at = new Map(system.nodes.map((n) => [n.id, n]))
    for (const f of system.frames) at.set(f.id, f)
    for (const e of system.edges.filter((e) => e.shape === "flow")) {
      expect(at.get(e.source).x).toBeLessThan(at.get(e.target).x)
    }
  })

  test("no two boxes in a system overlap, and the frame holds every one", () => {
    const [system] = detectSystems(shop())
    const boxes = system.nodes.filter((n) => !n.frame)
    for (const a of boxes) {
      for (const b of boxes) {
        if (a === b) continue
        const apart =
          a.x + a.width <= b.x ||
          b.x + b.width <= a.x ||
          a.y + a.height <= b.y ||
          b.y + b.height <= a.y
        expect(apart).toBe(true)
      }
      expect(a.x).toBeGreaterThanOrEqual(0)
      expect(a.y).toBeGreaterThanOrEqual(0)
      expect(a.x + a.width).toBeLessThanOrEqual(system.width)
      expect(a.y + a.height).toBeLessThanOrEqual(system.height)
    }
    const frame = system.frames[0]
    for (const n of system.nodes.filter((n) => n.frame)) {
      expect(n.x).toBeGreaterThanOrEqual(frame.x)
      expect(n.y).toBeGreaterThan(frame.y)
      expect(n.x + n.width).toBeLessThanOrEqual(frame.x + frame.width)
      expect(n.y + n.height).toBeLessThanOrEqual(frame.y + frame.height)
    }
  })

  test("the same reading lays out the same diagram", () => {
    expect(JSON.stringify(detectSystems(shop()))).toBe(JSON.stringify(detectSystems(shop())))
  })

  test("what two systems both use is drawn in each and marked shared", () => {
    const map = serverMap(
      [
        reading("projects", [item("project:1", "api"), item("project:2", "n8n")]),
        reading("containers", [item("container:pg", "postgres-main", { product: "postgres" })]),
        reading("databases", [item("db:9", "main-pg", { product: "postgres" })]),
      ],
      [
        link("project:1", "db:9", "binding"),
        link("project:2", "db:9", "reference"),
        link("container:pg", "db:9", "serves"),
      ],
    )
    const systems = detectSystems(map)
    expect(systems.map((s) => s.kind)).toEqual(["project", "project"])
    for (const s of systems) {
      const db = s.nodes.find((n) => n.itemId === "db:9")
      expect(db.shared).toEqual(systems.filter((o) => o !== s).map((o) => o.id))
      expect(s.nodes.some((n) => n.itemId === "container:pg")).toBe(true)
    }
  })

  test("a stack in nobody's release is its own system, its card wired to its members", () => {
    const map = serverMap(
      [
        reading("stacks", [item("stack:monitoring", "monitoring")]),
        reading("containers", [
          item("container:g", "grafana", { product: "grafana" }),
          item("container:p", "prometheus", { product: "prometheus" }),
        ]),
        reading("volumes", [item("volume:g", "grafana-storage")]),
        reading("domains", [item("domain:metrics.example.com", "metrics.example.com")]),
      ],
      [
        link("stack:monitoring", "container:g", "member"),
        link("stack:monitoring", "container:p", "member"),
        link("stack:monitoring", "volume:g", "member"),
        link("container:g", "volume:g", "mount"),
        link("domain:metrics.example.com", "container:g", "route"),
      ],
    )
    const [system] = detectSystems(map)
    expect(system.kind).toBe("stack")
    expect(system.frames).toHaveLength(0)
    const card = system.nodes.find((n) => n.itemId === "stack:monitoring")
    expect(card.face).toBe("stack")
    expect(system.edges.filter((e) => e.via === "member")).toHaveLength(2)
    // The volume a member mounts is joined by the mount, not again by the stack.
    expect(system.edges.some((e) => e.via === "member" && e.target.endsWith("volume:g"))).toBe(
      false,
    )
    expect(system.nodes.find((n) => n.itemId === "container:g").role).toBe("monitoring")
  })

  test("this dashboard is a system: its address, its tailnet, its stack and its store", () => {
    const map = serverMap(
      [
        reading("dashboard", [
          item("dashboard:self", "Just Dashboard", { product: "just-dashboard" }),
          item("dashboard:site", "box.tail.ts.net:8443"),
          item("dashboard:store", "vpsd.db", { product: "sqlite" }),
        ]),
        reading("stacks", [item("stack:just-dashboard", "just-dashboard")]),
        reading("containers", [
          item("container:caddy", "just-dashboard-caddy-1", { product: "caddy" }),
          item("container:fe", "just-dashboard-frontend-1"),
          item("container:be", "just-dashboard-backend-1"),
        ]),
        reading("tunnels", [item("vpn:tailscale", "Tailscale", { product: "tailscale" })]),
      ],
      [
        link("dashboard:self", "stack:just-dashboard", "self"),
        link("stack:just-dashboard", "container:caddy", "member"),
        link("stack:just-dashboard", "container:fe", "member"),
        link("stack:just-dashboard", "container:be", "member"),
        link("dashboard:site", "dashboard:self", "route"),
        link("dashboard:site", "vpn:tailscale", "tailnet"),
        link("container:be", "dashboard:store", "mount"),
      ],
    )
    const systems = detectSystems(map)
    expect(systems).toHaveLength(1)
    const [system] = systems
    expect(system.kind).toBe("dashboard")
    const face = (id) => system.nodes.find((n) => n.itemId === id)
    expect(face("dashboard:site").face).toBe("entry")
    expect(face("dashboard:store").face).toBe("datastore")
    expect(face("vpn:tailscale").face).toBe("entry")
    expect(face("container:caddy").role).toBe("proxy")
    expect(face("container:fe").role).toBe("web")
    expect(face("container:be").role).toBe("api")
    // The tailnet stands before the address it carries.
    expect(face("vpn:tailscale").x).toBeLessThan(face("dashboard:site").x)
    expect(system.frames.map((f) => f.itemId)).toEqual(["stack:just-dashboard"])
    expect(systemCensus(system)).toMatchObject({ containers: 3, databases: 1, domains: 1 })
  })

  test("a service is drawn only when something is wired to it", () => {
    const map = serverMap(
      [
        reading("services", [
          item("unit:gitea.service", "gitea"),
          item("unit:cron.service", "cron"),
          item("unit:postgresql.service", "postgresql"),
        ]),
        reading("domains", [item("domain:git.example.com", "git.example.com")]),
        reading("databases", [item("db:4", "legacy", { product: "postgres" })]),
      ],
      [
        link("domain:git.example.com", "unit:gitea.service", "route"),
        link("unit:postgresql.service", "db:4", "serves"),
      ],
    )
    const systems = detectSystems(map)
    // A native database is named by itself, and takes the service it runs under.
    expect(systems.map((s) => [s.kind, systemTitle(s)])).toEqual([
      ["database", "legacy"],
      ["service", "gitea"],
    ])
    expect(systems[0].nodes.some((n) => n.itemId === "unit:postgresql.service")).toBe(true)
    expect(systems.flatMap((s) => s.nodes).some((n) => n.itemId === "unit:cron.service")).toBe(
      false,
    )
  })

  test("what nothing claims is one system of its own, last", () => {
    const map = serverMap(
      [
        reading("projects", [item("project:1", "api")]),
        reading("domains", [item("domain:old.example.com", "old.example.com", { tone: "danger" })]),
        reading("certificates", [item("cert:/le/old", "old.example.com")]),
        reading("volumes", [item("volume:old", "old-uploads", { tone: "idle" })]),
        reading("backups", [item("backup:etc", "etc configs")]),
      ],
      [link("domain:old.example.com", "cert:/le/old", "certificate")],
    )
    const systems = detectSystems(map)
    expect(systems.map((s) => s.kind)).toEqual(["project", "loose"])
    const loose = systems[1]
    expect(systemTitle(loose)).toBe("Everything else")
    expect(loose.nodes.map((n) => n.itemId).sort()).toEqual([
      "backup:etc",
      "cert:/le/old",
      "domain:old.example.com",
      "volume:old",
    ])
    expect(loose.nodes.find((n) => n.itemId === "cert:/le/old").face).toBe("satellite")
    expect(loose.nodes.some((n) => n.itemId.startsWith("logs:"))).toBe(false)
  })

  test("a volume is left out only when it is unused and nothing is wired to it", () => {
    const map = serverMap(
      [
        reading("volumes", [
          item("volume:stray", "0030209f", { tone: "idle", state: "unused" }),
          item("volume:kept", "old-uploads", { tone: "idle", state: "unused" }),
          item("volume:busy", "scratch", { state: "in use" }),
        ]),
        reading("backups", [item("backup:uploads", "uploads nightly")]),
      ],
      [link("volume:kept", "backup:uploads", "backup")],
    )
    const [loose, ...others] = detectSystems(map)
    expect(others).toEqual([])
    expect(loose.nodes.map((n) => n.itemId).sort()).toEqual([
      "backup:uploads",
      "volume:busy",
      "volume:kept",
    ])
  })

  test("unused volumes alone draw no system", () => {
    const map = serverMap([
      reading("volumes", [item("volume:stray", "0030209f", { tone: "idle", state: "unused" })]),
    ])
    expect(detectSystems(map)).toEqual([])
  })

  test("an absent module and a reading that never arrived draw nothing", () => {
    const map = serverMap([
      reading("containers", [], { state: "absent" }),
      reading("projects", [], { state: "unread", reason: "Still being read" }),
    ])
    expect(detectSystems(map)).toEqual([])
    expect(detectSystems(undefined)).toEqual([])
  })
})

describe("layoutAtlas", () => {
  test("frames never overlap and keep the rail's order row by row", () => {
    const systems = detectSystems(
      serverMap(
        [
          reading(
            "containers",
            Array.from({ length: 9 }, (_, i) => item(`container:${i}`, `app-${i}`)),
          ),
        ],
        [],
      ),
    )
    const placed = layoutAtlas(systems)
    expect(placed.map((p) => p.system.id)).toEqual(systems.map((s) => s.id))
    for (const a of placed) {
      for (const b of placed) {
        if (a === b) continue
        const apart =
          a.x + a.width <= b.x ||
          b.x + b.width <= a.x ||
          a.y + a.height <= b.y ||
          b.y + b.height <= a.y
        expect(apart).toBe(true)
      }
    }
    // Nine small systems are rows, not one long line.
    expect(new Set(placed.map((p) => p.y)).size).toBeGreaterThan(1)
  })
})

describe("settle", () => {
  test("a dragged box goes where it was put and its satellites follow it", () => {
    const [system] = detectSystems(shop())
    const project = system.nodes.find((n) => n.itemId === "project:1")
    const repo = system.nodes.find((n) => n.itemId === "repo:/srv/shop")
    const out = settle(system, { [project.id]: { x: project.x + 40, y: project.y - 300 } })
    const moved = out.nodes.find((n) => n.id === project.id)
    const followed = out.nodes.find((n) => n.id === repo.id)
    expect(moved.y).toBe(project.y - 300)
    expect(followed.x - repo.x).toBe(40)
    expect(followed.y - repo.y).toBe(-300)
    expect(out.minY).toBeLessThan(0)
  })

  test("a stack's frame is drawn round its containers wherever they are", () => {
    const [system] = detectSystems(shop())
    const web = system.nodes.find((n) => n.itemId === "container:web")
    const out = settle(system, { [web.id]: { x: web.x + 500, y: web.y } })
    const frame = out.frames[0]
    expect(frame.x + frame.width).toBeGreaterThanOrEqual(web.x + 500 + web.width)
  })

  test("nothing moved is the layout itself", () => {
    const [system] = detectSystems(shop())
    const out = settle(system, {})
    expect(out.nodes).toBe(system.nodes)
    expect(out.minX).toBe(0)
    expect(out.maxX).toBe(system.width)
  })
})

describe("search and wiring", () => {
  test("a search keeps the systems that hold what it found", () => {
    const systems = detectSystems(shop())
    expect(searchSystems(systems, "")).toBeNull()
    expect([...searchSystems(systems, "pg-data")]).toEqual([systems[0].id])
    expect(searchSystems(systems, "nothing like it").size).toBe(0)
    const hits = searchNodes(systems[0], "worker")
    expect([...hits].map((id) => id.split("/").pop())).toEqual(["container:worker"])
  })

  test("a box's neighbours are what it is wired to inside its system", () => {
    const [system] = detectSystems(shop())
    const db = system.nodes.find((n) => n.itemId === "db:1")
    const near = [...neighbours(system, db.id)].map((id) => id.split("/").pop()).sort()
    expect(near).toEqual(["backup:1", "container:pg", "db:1", "project:1"])
  })

  test("the sizes a face is drawn at are the layout's", () => {
    const [system] = detectSystems(shop())
    const project = system.nodes.find((n) => n.itemId === "project:1")
    expect([project.width, project.height]).toEqual([FACE_SIZE.hero.width, FACE_SIZE.hero.height])
  })
})

describe("the document", () => {
  test("an older map's arrangement keeps only its minimap switch", () => {
    const doc = decodeDocument({
      version: 1,
      positions: { "container:a": { x: 1, y: 2 } },
      collapsed: ["branch:jobs"],
      minimap: false,
    })
    expect(doc).toEqual({ ...DEFAULT_DOCUMENT, minimap: false })
    expect(isArranged(doc)).toBe(false)
  })

  test("a version 2 document is read back defensively", () => {
    const doc = decodeDocument({
      version: 2,
      positions: { "system:project:1/db:1": { x: 10, y: 20 }, bad: { x: "no" } },
      focus: "system:project:1",
      viewport: { x: 1, y: 2, zoom: 0 },
      minimap: true,
    })
    expect(doc.positions).toEqual({ "system:project:1/db:1": { x: 10, y: 20 } })
    expect(doc.focus).toBe("system:project:1")
    expect(doc.viewport).toBeUndefined()
    expect(isArranged(doc)).toBe(true)
    expect(decodeDocument("nonsense")).toEqual(DEFAULT_DOCUMENT)
  })

  test("where whole systems were carried is kept, and alone makes the map arranged", () => {
    const doc = decodeDocument({
      version: 2,
      positions: {},
      frames: { "system:stack:data": { x: 1200, y: -40 }, "system:loose": { x: null, y: 0 } },
      minimap: true,
    })
    expect(doc.frames).toEqual({ "system:stack:data": { x: 1200, y: -40 } })
    expect(isArranged(doc)).toBe(true)
    // Saved before systems could be carried: none was.
    expect(decodeDocument({ version: 2, positions: {}, minimap: true }).frames).toEqual({})
  })
})
