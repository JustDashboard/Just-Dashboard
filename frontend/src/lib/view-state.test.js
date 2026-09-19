import { describe, expect, test } from "bun:test"
import { createStore, usable } from "./view-state"

function fakeStorage() {
  const map = new Map()
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    map,
  }
}

describe("the remembered-state store", () => {
  test("a written value comes back, is persisted as one document, and is announced", () => {
    const storage = fakeStorage()
    const store = createStore(() => storage, "jd.test")
    let announced = 0
    store.subscribe(() => announced++)
    store.write("docker.containers.query", "nginx")
    expect(store.read("docker.containers.query")).toBe("nginx")
    expect(JSON.parse(storage.map.get("jd.test"))).toEqual({ "docker.containers.query": "nginx" })
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
    expect(JSON.parse(storage.map.get("jd.test"))).toEqual({ "deploy.fleet.query": "sh" })
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
