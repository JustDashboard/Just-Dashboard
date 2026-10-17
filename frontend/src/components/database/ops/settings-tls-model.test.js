import { describe, expect, test } from "bun:test"
import {
  certificateLife,
  changeEffects,
  changedWords,
  daysUntil,
  shortFingerprint,
  tlsState,
} from "./settings-tls-model"

const NOW = Date.parse("2026-10-17T10:00:00Z")
const days = (n) => new Date(NOW + n * 24 * 60 * 60 * 1000).toISOString()

describe("how long a certificate has", () => {
  test("days are whole and negative once it ended", () => {
    expect(daysUntil(days(30), NOW)).toBe(30)
    expect(daysUntil(days(0.5), NOW)).toBe(0)
    expect(daysUntil(days(-2), NOW)).toBe(-2)
    expect(daysUntil(undefined, NOW)).toBeUndefined()
    expect(daysUntil("not a date", NOW)).toBeUndefined()
  })

  test("it says when the renewal is already due", () => {
    expect(certificateLife({ notAfter: days(200), renewAt: days(170) }, NOW)).toBe("200 days left")
    expect(certificateLife({ notAfter: days(20), renewAt: days(-10) }, NOW)).toBe(
      "20 days left, renews on its own",
    )
    expect(certificateLife({ notAfter: days(1), renewAt: days(-29) }, NOW)).toBe(
      "1 day left, renews on its own",
    )
    expect(certificateLife({ notAfter: days(0.2) }, NOW)).toBe("Ends today")
    expect(certificateLife({ notAfter: days(-3) }, NOW)).toBe("Expired")
    expect(certificateLife({}, NOW)).toBe("—")
  })
})

describe("the state of a server's TLS", () => {
  const base = { supported: true, enabled: false, require: false, canOptional: true }

  test("each state has its own word", () => {
    expect(tlsState({ ...base, supported: false }).word).toBe("Not managed here")
    expect(tlsState(base).word).toBe("Off")
    expect(tlsState({ ...base, enabled: true }).word).toBe("TLS allowed")
    expect(tlsState({ ...base, enabled: true, require: true }).word).toBe("TLS required")
    expect(tlsState({ ...base, enabled: true, require: true, canOptional: false }).word).toBe(
      "TLS only",
    )
  })

  test("a server that has TLS on keeps saying so while it cannot be reached to change", () => {
    expect(tlsState({ ...base, supported: false, enabled: true }).word).toBe("TLS allowed")
  })

  test("only a server with TLS on counts as on", () => {
    expect(tlsState(base).on).toBe(false)
    expect(tlsState({ ...base, enabled: true }).on).toBe(true)
  })
})

describe("what a change tells the person asking", () => {
  const tls = { linked: 2, canOptional: true, container: "jd-postgres" }

  test("requiring TLS warns the deployments that connect by name", () => {
    const effects = changeEffects("require", tls, "PostgreSQL")
    expect(effects.title).toBe("Require TLS for PostgreSQL")
    expect(effects.lead).toContain("jd-postgres")
    expect(effects.cautions.join(" ")).toContain("2 deployment environments reach")
  })

  test("a server with nothing linked has nothing to warn about", () => {
    const effects = changeEffects("require", { ...tls, linked: 0 }, "PostgreSQL")
    expect(effects.cautions).toHaveLength(1)
    expect(changeEffects("require", { ...tls, linked: 1 }, "x").cautions.join(" ")).toContain(
      "1 deployment environment reaches",
    )
  })

  test("turning TLS on for an engine with one protocol per port says plain text stops", () => {
    const redis = changeEffects("on", { ...tls, canOptional: false }, "Redis")
    expect(redis.cautions.join(" ")).toContain("plain text stops being accepted")
    expect(changeEffects("on", tls, "PostgreSQL").cautions.join(" ")).toContain("still accepted")
  })

  test("every change has a word for the toast", () => {
    for (const change of ["on", "allow", "require", "off"]) {
      expect(changedWords(change, "shop")).toContain("shop")
      expect(changeEffects(change, tls, "x").confirm.length).toBeGreaterThan(0)
    }
  })
})

describe("an authority's fingerprint", () => {
  test("is shortened and grouped", () => {
    expect(shortFingerprint("a1b2c3d4e5f60718293a")).toBe("A1B2 C3D4 E5F6 0718")
    expect(shortFingerprint("")).toBe("")
  })
})
