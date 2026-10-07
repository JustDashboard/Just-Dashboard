import { expect, test } from "bun:test"
import { softwareGroups, softwareKey } from "./inventory"

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
