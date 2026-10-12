import { describe, expect, test } from "bun:test"
import { PURPOSES, SECTION_KEYS, hiddenFor, sectionOf } from "./sidebar-sections"

describe("sectionOf", () => {
  test("names the section a page is inside, at any depth and with a query", () => {
    expect(sectionOf("/proxy")).toBe("proxy")
    expect(sectionOf("/proxy/sites/shop")).toBe("proxy")
    expect(sectionOf("/proxy/sites?site=shop.example.com")).toBe("proxy")
    expect(sectionOf("/deploy/12/settings/domains")).toBe("deploy")
    expect(sectionOf("/system-users")).toBe("system-users")
  })

  // A rail that could hide these would have no way to say how the machine is,
  // and no way back to the switch that hid them.
  test("keeps Overview, Monitoring, Settings and the account out of reach", () => {
    for (const path of ["/", "/metrics", "/processes/pm2", "/logs", "/dashboard", "/account"]) {
      expect(sectionOf(path)).toBeNull()
    }
  })

  test("does not mistake a prefix for a section", () => {
    expect(sectionOf("/dockerfile")).toBeNull()
    expect(sectionOf("/network-tools")).toBeNull()
  })
})

describe("hiddenFor", () => {
  test("hides every section no chosen purpose uses, in the rail's order", () => {
    expect(hiddenFor(["databases"])).toEqual(
      SECTION_KEYS.filter((key) => !["databases", "backups", "docker"].includes(key)),
    )
  })

  test("shows a section two purposes share once", () => {
    const hidden = hiddenFor(["ship", "databases"])
    expect(hidden).not.toContain("backups")
    expect(hidden).not.toContain("docker")
    expect(new Set(hidden).size).toBe(hidden.length)
  })

  test("hides everything when nothing is chosen, and nothing when everything is", () => {
    expect(hiddenFor([])).toEqual([...SECTION_KEYS])
    expect(hiddenFor(PURPOSES.map((purpose) => purpose.key))).toEqual([])
  })

  // The server stores what this sends and refuses a name that is not one.
  test("only ever names sections the server will accept", () => {
    for (const key of SECTION_KEYS) expect(key).toMatch(/^[a-z][a-z0-9-]{0,31}$/)
    for (const purpose of PURPOSES) {
      for (const key of purpose.sections) expect(SECTION_KEYS).toContain(key)
    }
  })
})
