import { describe, expect, test } from "bun:test"
import { createStore, usable } from "./view-state"

function fakeStorage() {
  const map = new Map()
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    removeItem: (key) => map.delete(key),
    key: (i) => [...map.keys()][i] ?? null,
    get length() {
      return map.size
    },
    map,
  }
}

describe("the remembered-state store", () => {
  test("a written value comes back, is persisted independently, and is announced", () => {
    const storage = fakeStorage()
    const store = createStore(() => storage, "jd.test")
    let announced = 0
    store.subscribe(() => announced++)
    store.write("docker.containers.query", "nginx")
    expect(store.read("docker.containers.query")).toBe("nginx")
    expect(JSON.parse(storage.map.get("jd.test.entry.docker.containers.query"))).toBe("nginx")
    expect(announced).toBe(1)
  })

  test("a second store over the same storage reads what the first wrote", () => {
    const storage = fakeStorage()
    createStore(() => storage, "jd.test").write("logs.source", "journal:nginx")
    expect(createStore(() => storage, "jd.test").read("logs.source")).toBe("journal:nginx")
  })

  test("forgetting a prefix drops exactly the keys under it", () => {
    const storage = fakeStorage()
    const store = createStore(() => storage, "jd.test")
    store.write("deploy.new.flow", { name: "shop" })
    store.write("deploy.new.git.url", "https://example.com/shop.git")
    store.write("deploy.fleet.query", "sh")
    store.forget("deploy.new.")
    expect(store.read("deploy.new.flow")).toBeUndefined()
    expect(store.read("deploy.new.git.url")).toBeUndefined()
    expect(store.read("deploy.fleet.query")).toBe("sh")
    const reloaded = createStore(() => storage, "jd.test")
    expect(reloaded.read("deploy.new.flow")).toBeUndefined()
    expect(reloaded.read("deploy.fleet.query")).toBe("sh")
  })

  test("without a storage area the store still works for the life of the page", () => {
    const store = createStore(() => null, "jd.test")
    store.write("secret", "hunter2")
    expect(store.read("secret")).toBe("hunter2")
    store.forget("")
    expect(store.read("secret")).toBeUndefined()
  })

  test("a corrupt document means the defaults rather than a crash", () => {
    const storage = fakeStorage()
    storage.setItem("jd.test", "{not json")
    const store = createStore(() => storage, "jd.test")
    expect(store.read("anything")).toBeUndefined()
    store.write("anything", 1)
    expect(store.read("anything")).toBe(1)
  })

  test("existing documents migrate and small writes never rewrite unrelated drafts", () => {
    const storage = fakeStorage()
    const draft = "select ".repeat(150_000)
    storage.setItem("jd.test", JSON.stringify({ draft, query: "old" }))
    const store = createStore(() => storage, "jd.test")
    expect(store.read("draft")).toBe(draft)
    const writes = []
    const set = storage.setItem
    storage.setItem = (key, value) => {
      writes.push([key, value])
      set(key, value)
    }
    store.write("query", "new")
    expect(writes).toEqual([["jd.test.entry.query", '"new"']])
    expect(storage.getItem("jd.test")).toBeNull()
    const reloaded = createStore(() => storage, "jd.test")
    expect(reloaded.read("draft")).toBe(draft)
    expect(reloaded.read("query")).toBe("new")
    store.forget("")
    expect(storage.length).toBe(0)
  })

  test("failed migration restores the old document and continues persisting", () => {
    const storage = fakeStorage()
    storage.setItem("jd.test", JSON.stringify({ a: "draft", b: "another" }))
    const set = storage.setItem
    storage.setItem = (key, value) => {
      if (key.endsWith(".b")) throw new Error("quota")
      set(key, value)
    }
    const store = createStore(() => storage, "jd.test")
    expect(store.read("a")).toBe("draft")
    expect(JSON.parse(storage.getItem("jd.test"))).toEqual({ a: "draft", b: "another" })
    store.write("a", "edited")
    expect(createStore(() => storage, "jd.test").read("a")).toBe("edited")
    store.forget("")
    expect(createStore(() => storage, "jd.test").read("a")).toBeUndefined()
  })
})

describe("whether a stored value still fits its slot", () => {
  test("the kind has to match, shallowly", () => {
    expect(usable("grid", "list")).toBe(true)
    expect(usable(3, 0)).toBe(true)
    expect(usable("3", 0)).toBe(false)
    expect(usable({ key: "name" }, { key: "size" })).toBe(true)
    expect(usable([], { key: "size" })).toBe(false)
    expect(usable({}, [])).toBe(false)
  })

  test("a nullable slot takes anything, and null fits any slot", () => {
    expect(usable("journal:nginx", null)).toBe(true)
    expect(usable({ conn: 3 }, null)).toBe(true)
    expect(usable(null, "main")).toBe(true)
    expect(usable("x", undefined)).toBe(true)
  })
})
