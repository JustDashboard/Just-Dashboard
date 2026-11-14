import { expect, test } from "bun:test"
import { engineHref, engineIdOf, engineSibling, engineTabOf } from "./engine-views"

test("an engine's address opens on the overview, and names any other view", () => {
  expect(engineHref("dns-1")).toBe("/network/dns/services/dns-1")
  expect(engineHref("dns-1", "filters")).toBe("/network/dns/services/dns-1?tab=filters")
  expect(engineTabOf(null)).toBe("overview")
  expect(engineTabOf("dhcp")).toBe("dhcp")
  expect(engineTabOf("inventory")).toBe("overview")
})

test("a retained change's review is in Changes, whatever view the address names", () => {
  expect(engineHref("dns-1", "overview", "c9")).toBe(
    "/network/dns/services/dns-1?tab=changes&change=c9",
  )
  expect(engineTabOf("queries", "c9")).toBe("changes")
  expect(engineTabOf(null, "c9")).toBe("changes")
})

test("an id that needs escaping goes into the address escaped and comes back whole", () => {
  expect(engineHref("a/b c")).toBe("/network/dns/services/a%2Fb%20c")
  expect(engineIdOf("a%2Fb%20c")).toBe("a/b c")
  expect(engineIdOf("%E0%A4%A")).toBe("%E0%A4%A")
  expect(engineIdOf(undefined)).toBe("")
})

test("the rail is walked one engine at a time and stops at its ends", () => {
  const engines = [{ id: "a" }, { id: "b" }, { id: "c" }]
  expect(engineSibling(engines, "b", 1)?.id).toBe("c")
  expect(engineSibling(engines, "b", -1)?.id).toBe("a")
  expect(engineSibling(engines, "c", 1)).toBeUndefined()
  expect(engineSibling(engines, "a", -1)).toBeUndefined()
  // An engine that is not in the list steps onto its nearest end.
  expect(engineSibling(engines, "gone", 1)?.id).toBe("a")
  expect(engineSibling(engines, "gone", -1)?.id).toBe("c")
})
