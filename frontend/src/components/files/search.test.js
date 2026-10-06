import { expect, test } from "bun:test"
import { editorHref, hitLocation, localFileMatches, mergeFileMatches, searchScopes } from "./search"

const entries = ["nginx.conf", "nginx.conf.bak", "config.nginx", "notes.md", "📦package.json"].map(
  (name) => ({
    name,
    path: "/work/" + name,
    isDir: false,
    size: 10,
  }),
)

test("an exact name ranks first and scattered letters still match", () => {
  expect(localFileMatches(entries, "nginx.conf")[0].name).toBe("nginx.conf")
  expect(localFileMatches(entries, "ngxconf").map((hit) => hit.name)).toContain("nginx.conf")
  expect(localFileMatches(entries, "missing")).toHaveLength(0)
})

test("local and disk results merge without repeating an entry", () => {
  const local = localFileMatches(entries, "ngx")
  const remote = [
    { path: "/work/nginx.conf", name: "nginx.conf", isDir: false },
    {
      path: "/work/etc/nginx.conf",
      name: "nginx.conf",
      isDir: false,
    },
  ]
  const combined = mergeFileMatches(local, remote)
  expect(new Set(combined.map((hit) => hit.path)).size).toBe(combined.length)
  expect(combined[0]).toEqual(remote[0])
  expect(combined).toContainEqual(remote[1])
})

test("fuzzy positions and links preserve Unicode and encoded path characters", () => {
  expect(localFileMatches(entries, "package")[0].matches[0]).toBe(2)
  const url = new URL(editorHref("/work/a & b#?.txt", "/work", 42), "https://example.test")
  expect(url.pathname).toBe("/files/editor")
  expect(url.searchParams.get("path")).toBe("/work/a & b#?.txt")
  expect(url.searchParams.get("line")).toBe("42")
})

const places = {
  home: "/root",
  roots: ["/"],
  places: [
    { name: "root", path: "/root", kind: "home" },
    { name: "/", path: "/", kind: "root" },
    { name: "ubuntu", path: "/home/ubuntu", kind: "user" },
    { name: "Configuration", path: "/etc", kind: "notable" },
  ],
}

test("home is the account home around the folder, not the dashboard's own", () => {
  const inside = searchScopes("/home/ubuntu/Downloads", places)
  expect(inside.map((s) => [s.key, s.path])).toEqual([
    ["folder", "/home/ubuntu/Downloads"],
    ["home", "/home/ubuntu"],
    ["everywhere", "/"],
  ])
  // Outside every home, a one-account server's home is that account's.
  expect(searchScopes("/etc", places).find((s) => s.key === "home").path).toBe("/home/ubuntu")
  // The folder that is the home is one choice, not two.
  expect(searchScopes("/home/ubuntu", places).map((s) => s.key)).toEqual(["folder", "everywhere"])
  expect(searchScopes("/", places).map((s) => s.key)).toEqual(["folder", "home"])
})

test("a hit's location is its folder, said from the scope", () => {
  expect(hitLocation("/home/ubuntu/Downloads/a.mp4", "/home/ubuntu")).toBe("ubuntu/Downloads")
  expect(hitLocation("/home/ubuntu/a.mp4", "/home/ubuntu")).toBe("ubuntu")
  expect(hitLocation("/etc/nginx/nginx.conf", "/")).toBe("/etc/nginx")
})
