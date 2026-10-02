import { expect, test } from "bun:test"
import { editorHref, localFileMatches, mergeFileMatches } from "./search"

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
