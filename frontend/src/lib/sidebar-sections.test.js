import { describe, expect, test } from "bun:test"
import {
  BASE_SECTIONS,
  PURPOSES,
  SECTION_KEYS,
  STABLE_PURPOSES,
  hiddenFor,
  pressPurpose,
  purposeShown,
  sectionOf,
} from "./sidebar-sections"

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
  test("hides every section no chosen purpose brings, in the rail's order", () => {
    expect(hiddenFor(["databases"])).toEqual(
      SECTION_KEYS.filter((key) => ![...BASE_SECTIONS, "databases", "backups"].includes(key)),
    )
  })

  test("keeps packages, system users and the audit log in every answer", () => {
    for (const purposes of [[], ["databases"], ["ship", "workspace"]]) {
      for (const key of BASE_SECTIONS) expect(hiddenFor(purposes)).not.toContain(key)
    }
  })

  // The answer a new account gets if it only presses Continue.
  test("the stable five leave out exactly what the experimental one brings", () => {
    expect(hiddenFor(STABLE_PURPOSES)).toEqual(["network"])
  })

  test("hides all but the base when nothing is chosen, and nothing when everything is", () => {
    expect(hiddenFor([])).toEqual(SECTION_KEYS.filter((key) => !BASE_SECTIONS.includes(key)))
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

describe("categories", () => {
  // A section two cards shared made one of them a press that changed nothing.
  test("every section is one purpose's or the base's, and only one", () => {
    const owners = SECTION_KEYS.map((key) => [
      ...PURPOSES.filter((purpose) => purpose.sections.includes(key)).map((purpose) => purpose.key),
      ...(BASE_SECTIONS.includes(key) ? ["base"] : []),
    ])
    for (const [index, owner] of owners.entries()) {
      expect(owner, SECTION_KEYS[index]).toHaveLength(1)
    }
  })

  test("a new account starts with the five finished purposes, and they come first", () => {
    expect(STABLE_PURPOSES).toEqual(["ship", "databases", "workspace", "containers", "secure"])
    expect(PURPOSES.slice(0, 5).map((purpose) => purpose.key)).toEqual(STABLE_PURPOSES)
  })

  test("only routing the network is experimental, and it comes last", () => {
    expect(
      PURPOSES.filter((purpose) => purpose.stage === "experimental").map((p) => p.key),
    ).toEqual(["network"])
    expect(PURPOSES.at(-1)?.key).toBe("network")
  })
})

describe("pressPurpose", () => {
  const all = new Set(SECTION_KEYS)

  test("brings a purpose back whole, whether it was partly or wholly hidden", () => {
    const partly = new Set(["files", "remote-desktop", "network"])
    expect(purposeShown("workspace", partly, all)).toBe(false)
    const next = pressPurpose("workspace", partly, all)
    expect([...next]).toEqual(["network"])
    expect(purposeShown("workspace", next, all)).toBe(true)
  })

  test("takes a shown purpose out and leaves every other card as it was", () => {
    const shown = new Set(hiddenFor(STABLE_PURPOSES))
    const next = pressPurpose("databases", shown, all)
    expect([...next].sort()).toEqual(["backups", "databases", "network"])
    expect(purposeShown("ship", next, all)).toBe(true)
    expect(purposeShown("workspace", next, all)).toBe(true)
  })

  test("leaves the sections the account is not offered alone", () => {
    const offered = new Set(SECTION_KEYS.filter((key) => key !== "remote-desktop"))
    const hidden = new Set(["remote-desktop"])
    expect(purposeShown("workspace", hidden, offered)).toBe(true)
    expect(pressPurpose("workspace", hidden, offered).has("remote-desktop")).toBe(true)
  })
})
