import { expect, test } from "bun:test"
import {
  dependency,
  filterPackages,
  packageHref,
  packageName,
  packageScope,
  samePackage,
  softwareGroups,
  softwareKey,
} from "./inventory"

test("software shares sum the whole inventory and include a product's libraries", () => {
  const packages = [
    { name: "postgresql-16", size: 40, explicit: true },
    { name: "libpq5", size: 10, explicit: false },
    { name: "libc6", section: "libs", size: 20 },
    { name: "libtinfo6", section: "universe/libs", size: 5 },
    { name: "htop" },
  ]
  const before = structuredClone(packages)
  const groups = softwareGroups(packages)
  expect(groups.map((g) => [g.key, g.size, g.count])).toEqual([
    ["product:postgresql", 50, 2],
    ["section:libs", 25, 2],
    ["section:misc", 0, 1],
  ])
  expect(groups.reduce((n, g) => n + g.size, 0)).toBe(75)
  expect(packages).toEqual(before)
})

test("unknown software is grouped by its archive section without inventing a product", () => {
  expect(softwareKey({ name: "libdocker", section: "libs" })).toBe("section:libs")
  expect(softwareKey({ name: "nginx-common", section: "web" })).toBe("product:nginx")
  expect(softwareGroups([{ name: "unknown", size: -10 }])[0].size).toBe(0)
  expect(softwareGroups([])).toEqual([])
})

const LIST = [
  { name: "nginx", version: "1", explicit: true, section: "httpd", size: 10, upgradable: "2" },
  { name: "openssl", version: "1", explicit: false, size: 30, upgradable: "2", security: true },
  { name: "libc6", version: "1", explicit: false, section: "libs", summary: "GNU C Library" },
  { name: "containerd", version: "1", explicit: false, section: "admin", size: 20 },
]
const names = (filter) => filterPackages(LIST, filter).map((p) => p.name)
const base = { query: "", scope: "all", software: "", bySize: false }

test("the list's filter narrows by scope, software and words, and orders by size on request", () => {
  expect(names(base)).toEqual(["nginx", "openssl", "libc6", "containerd"])
  expect(names({ ...base, scope: "explicit" })).toEqual(["nginx"])
  expect(names({ ...base, scope: "dependencies" })).toEqual(["openssl", "libc6", "containerd"])
  expect(names({ ...base, scope: "upgradable" })).toEqual(["nginx", "openssl"])
  expect(names({ ...base, scope: "security" })).toEqual(["openssl"])
  expect(names({ ...base, software: "product:docker" })).toEqual(["containerd"])
  expect(names({ ...base, query: " gnu c " })).toEqual(["libc6"])
  expect(names({ ...base, query: "HTTPD" })).toEqual(["nginx"])
  expect(names({ ...base, bySize: true })).toEqual(["openssl", "containerd", "nginx", "libc6"])
})

test("a manager that records no by-hand installs opens on everything", () => {
  expect(packageScope("explicit", 0)).toBe("all")
  expect(packageScope("explicit", 3)).toBe("explicit")
  expect(packageScope("security", 0)).toBe("security")
  expect(packageScope("nonsense", 3)).toBe("explicit")
})

test("a package's address holds any name a manager accepts", () => {
  expect(packageHref("nginx")).toBe("/packages/nginx")
  expect(packageHref("nginx", "overview")).toBe("/packages/nginx")
  expect(packageHref("g++-13", "depends")).toBe("/packages/g%2B%2B-13?tab=depends")
  expect(packageHref("libc6:amd64")).toBe("/packages/libc6%3Aamd64")
  for (const name of ["g++-13", "libstdc++6", "libc6:amd64", "python3.12", "foo@bar_baz"]) {
    expect(packageName(packageHref(name).split("/").pop())).toBe(name)
    expect(new URL(packageHref(name, "log"), "http://x").pathname.split("/")).toHaveLength(3)
  }
  expect(packageName("100%")).toBe("100%")
  expect(packageName(undefined)).toBe("")
})

test("a dependency is the package to open and what is asked of it", () => {
  expect(dependency("libc6 (>= 2.34)")).toEqual({ name: "libc6", wants: "(>= 2.34)" })
  expect(dependency("debconf (>= 0.5) | debconf-2.0")).toEqual({
    name: "debconf",
    wants: "(>= 0.5) | debconf-2.0",
  })
  expect(dependency("libtinfo6")).toEqual({ name: "libtinfo6", wants: "" })
  expect(dependency("glibc>=2.38")).toEqual({ name: "glibc", wants: ">=2.38" })
  expect(samePackage("libc6:amd64", "libc6")).toBe(true)
  expect(samePackage("libc6", "libc6-dev")).toBe(false)
})
