import { describe, expect, test } from "bun:test"
import {
  connectsItself,
  foundAction,
  foundShelves,
  instanceAddress,
  instanceHref,
  instanceState,
  instanceWhere,
  needsPassword,
  privateState,
  scanNotes,
  scanSilences,
  scanningFiles,
} from "./inventory"

function instance(over = {}) {
  return {
    key: "docker:shop-db",
    kind: "server",
    name: "shop-db",
    engine: "postgres",
    driver: "postgres",
    flavor: "postgres",
    label: "PostgreSQL",
    source: "docker",
    state: "running",
    endpoints: [
      { kind: "tcp", host: "127.0.0.1", port: 5433, scope: "loopback", primary: true },
      { kind: "container", host: "172.17.0.4", port: 5432 },
    ],
    container: { id: "abc123", name: "shop-db", image: "postgres:16", dataVolumes: [] },
    credentials: "env",
    confidence: "image",
    evidence: [],
    connectable: true,
    connections: [],
    ...over,
  }
}

const stopped = instance({
  key: "docker:old-db",
  name: "old-db",
  state: "exited",
  connectable: false,
  reason: "the container is exited — start it to connect",
})
const declared = instance({
  key: "compose:shop/db",
  name: "db",
  source: "compose",
  state: "declared",
  connectable: false,
  reason: "no container exists for it — bring the stack up to connect",
  container: {
    name: "db",
    image: "postgres:16",
    composeProject: "shop",
    composeService: "db",
    dataVolumes: [],
  },
  endpoints: [],
})
const native = instance({
  key: "host:postgresql@17-main.service",
  name: "postgresql@17-main",
  source: "host",
  container: undefined,
  host: { unit: "postgresql@17-main.service", unitState: "active", user: "postgres" },
  endpoints: [
    { kind: "tcp", host: "127.0.0.1", port: 5438, scope: "loopback", primary: true },
    { kind: "unix", path: "/var/run/postgresql/.s.PGSQL.5438" },
  ],
  credentials: "peer",
  confidence: "socket",
})
const inactive = instance({
  key: "host:redis-server.service",
  name: "redis-server",
  engine: "redis",
  driver: "redis",
  source: "host",
  state: "inactive",
  container: undefined,
  host: { unit: "redis-server.service", unitState: "inactive" },
  endpoints: [],
  connectable: false,
  reason: "redis-server.service is not running — start it to connect",
})
const memcached = instance({
  key: "docker:mc",
  name: "mc",
  engine: "memcached",
  driver: "",
  flavor: undefined,
  label: "Memcached",
  credentials: "unknown",
  connectable: false,
  reason: "this dashboard has no driver for Memcached yet",
  container: { id: "mc1", name: "mc", image: "memcached:1.6", dataVolumes: [] },
})
const file = instance({
  key: "file:/srv/app/data.db",
  kind: "file",
  name: "data.db",
  engine: "sqlite",
  driver: "sqlite",
  source: "file",
  state: "file",
  container: undefined,
  endpoints: [{ kind: "file", path: "/srv/app/data.db", primary: true }],
  file: {
    path: "/srv/app/data.db",
    size: 4096,
    modified: "2026-09-25T00:00:00Z",
    holder: "application",
  },
  credentials: "open",
  confidence: "magic",
})
const own = instance({
  ...file,
  key: "file:/var/lib/jd/vpsd.db",
  name: "vpsd.db",
  file: { ...file.file, path: "/var/lib/jd/vpsd.db", holder: "self", wal: true },
  connectable: false,
  self: true,
})
const embedded = instance({
  key: "embedded:app:/data/app.db",
  kind: "embedded",
  name: "app.db",
  engine: "sqlite",
  driver: "sqlite",
  connectable: false,
  reason: "it is in the container's writable layer — removing the container deletes it",
  container: { id: "app1", name: "app", image: "app:1", dataVolumes: [] },
  file: { path: "/data/app.db", size: 1, modified: "", holder: "container" },
  endpoints: [],
})

describe("the one press a found instance offers", () => {
  test("a server that states its credentials connects; one that does not asks for them", () => {
    expect(foundAction(instance())).toEqual({ kind: "connect" })
    expect(foundAction(instance({ credentials: "secret-file" }))).toEqual({ kind: "connect" })
    expect(foundAction(instance({ credentials: "needed" }))).toEqual({
      kind: "credentials",
      choice: false,
    })
    expect(foundAction(instance({ credentials: "unknown", confidence: "port" })).kind).toBe(
      "credentials",
    )
  })

  test("a native server offers the second way in only where the engine has one", () => {
    expect(foundAction(native, true)).toEqual({ kind: "credentials", choice: true })
    expect(foundAction(native, false)).toEqual({ kind: "credentials", choice: false })
  })

  test("a file is opened, the dashboard's own store is nothing to press", () => {
    expect(foundAction(file)).toEqual({ kind: "open" })
    expect(foundAction(own)).toEqual({ kind: "none" })
  })

  test("what is down is started; what was never created is looked at where it is declared", () => {
    expect(foundAction(stopped)).toEqual({ kind: "start-container", id: "abc123" })
    expect(foundAction(inactive)).toEqual({ kind: "start-unit", unit: "redis-server.service" })
    expect(foundAction(declared)).toEqual({
      kind: "look",
      href: "/docker/stacks/shop",
      label: "Open stack",
    })
  })

  test("an engine with no driver links to the container or unit that runs it", () => {
    expect(foundAction(memcached)).toEqual({
      kind: "look",
      href: "/docker/containers/mc1",
      label: "Open container",
    })
    expect(foundAction(embedded).kind).toBe("look")
  })

  test("a thing with nowhere to go is nothing to press", () => {
    expect(instanceHref(instance({ container: undefined, host: {} }))).toBeNull()
    expect(instanceHref(file)).toEqual({ href: "/files?path=%2Fsrv%2Fapp", label: "Open folder" })
  })
})

describe("where a found instance is", () => {
  test("a container by its name and address, a compose service by its project", () => {
    expect(instanceAddress(instance())).toBe("127.0.0.1:5433")
    expect(instanceWhere(instance())).toEqual({
      kind: "container",
      text: "shop-db · 127.0.0.1:5433",
    })
    expect(instanceWhere(declared)).toEqual({ kind: "compose service", text: "shop/db" })
  })

  test("a native server by its unit, a file by its path, an embedded one by its container", () => {
    expect(instanceWhere(native)).toEqual({
      kind: "unit",
      text: "postgresql@17-main.service · 127.0.0.1:5438",
    })
    expect(instanceWhere(file)).toEqual({ kind: "file", text: "/srv/app/data.db" })
    expect(instanceWhere(embedded).text).toBe("app:/data/app.db")
    expect(
      instanceWhere(
        instance({ container: undefined, source: "host", host: { process: "mysqld" } }),
      ),
    ).toEqual({ kind: "process", text: "mysqld · 127.0.0.1:5433" })
  })

  test("the state word is the machine's own, except where ours says more", () => {
    expect(instanceState(stopped)).toBe("exited")
    expect(instanceState(declared)).toBe("not created")
    expect(instanceState(file)).toBe("file")
    expect(instanceState(own)).toBe("in use")
  })
})

describe("the shelves of what was found", () => {
  const inventory = {
    instances: [
      instance(),
      instance({ key: "docker:blog-db", name: "blog-db", connections: [3] }),
      stopped,
      memcached,
      file,
      own,
      instance({
        ...file,
        key: "file:/home/u/.cache/x.db",
        file: { ...file.file, holder: "tool" },
      }),
      instance({ key: "docker:skip", name: "skip", ignored: true }),
    ],
    scans: [],
    ignored: ["docker:skip", "docker:long-gone"],
    detail: "full",
    checkedAt: "",
  }

  test("connected ones are left out; the rest are servers, files, kept and ignored", () => {
    const shelves = foundShelves(inventory)
    expect(shelves.servers.map((one) => one.name)).toEqual(["shop-db", "old-db", "mc"])
    expect(shelves.files.map((one) => one.name)).toEqual(["data.db"])
    expect(shelves.kept).toHaveLength(2)
    expect(shelves.ignored.map((one) => one.name)).toEqual(["skip"])
    expect(shelves.gone).toEqual(["docker:long-gone"])
    expect(foundShelves(undefined).servers).toEqual([])
  })

  test("private state is a tool's, the system's or the dashboard's own", () => {
    expect(privateState(own)).toBe(true)
    expect(privateState(file)).toBe(false)
  })

  test("only a running, recognised server that states its credentials connects itself", () => {
    expect(connectsItself(instance())).toBe(true)
    expect(connectsItself(instance({ confidence: "port" }))).toBe(false)
    expect(connectsItself(native)).toBe(false)
    expect(connectsItself(stopped)).toBe(false)
    expect(connectsItself(file)).toBe(false)
  })

  test("a server waiting for a password is one that is up, unconnected and not ignored", () => {
    expect(needsPassword(native)).toBe(true)
    expect(needsPassword(instance({ credentials: "needed" }))).toBe(true)
    expect(needsPassword({ ...native, ignored: true })).toBe(false)
    expect(needsPassword({ ...native, connections: [4] })).toBe(false)
    expect(needsPassword(instance())).toBe(false)
  })
})

describe("what the collectors left unsaid", () => {
  const scans = [
    { source: "docker", ok: false, reason: "Docker did not answer: no socket", count: 0 },
    { source: "compose", ok: false, reason: "Docker did not answer: no socket", count: 0 },
    { source: "units", ok: true, count: 200 },
    {
      source: "files",
      ok: true,
      reason: "6 directories were not scanned",
      running: true,
      count: 3,
    },
  ]

  test("collectors that failed for one reason are one silence", () => {
    expect(scanSilences(scans)).toEqual([
      { what: "containers and compose services", reason: "Docker did not answer: no socket" },
    ])
    expect(scanSilences([{ source: "units", ok: true, count: 1 }])).toEqual([])
  })

  test("a collector that read says how far it got, and whether it is still reading", () => {
    expect(scanNotes(scans)).toEqual(["6 directories were not scanned"])
    expect(scanningFiles(scans)).toBe(true)
    expect(scanningFiles([{ source: "files", ok: true, count: 1 }])).toBe(false)
  })
})
