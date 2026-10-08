import { describe, expect, test } from "bun:test"
import { sqmDraft, sqmProblem, sqmProfile } from "./sqm-profile"

describe("explicit download SQM intent", () => {
  test("defaults isolate destination hosts, wash DSCP, and do not enable NAT", () => {
    expect(sqmProfile(sqmDraft())).toEqual({
      diffserv: "besteffort",
      flowMode: "dual-dsthost",
      nat: false,
      preserveDscp: false,
      linkLayer: "noatm",
      overhead: 0,
      mpu: 0,
      rttMillis: 100,
    })
  })
  test("rejects blank/zero download, decimal/blank/overflow profile parameters", () => {
    for (const rate of [0, Number.NaN, -1, 100_000_001])
      expect(sqmProblem(sqmDraft(), rate)).toBeDefined()
    for (const [key, text] of [
      ["overhead", ""],
      ["overhead", "22.5"],
      ["overhead", "-65"],
      ["mpu", "257"],
      ["rttMillis", "9"],
      ["rttMillis", "1e2"],
      ["rttMillis", "1001"],
    ])
      expect(sqmProblem({ ...sqmDraft(), [key]: text }, 9000)).toBeDefined()
  })
  test("round-trips saved intent and excludes all saved ownership/evidence fields", () => {
    const saved = {
      ...sqmProfile(sqmDraft()),
      diffserv: "diffserv4",
      overhead: 22,
      mpu: 64,
      preserveDscp: true,
      ifb: "jds0123456789ab",
      token: "secret owner",
      queue: { drops: 30 },
    }
    const profile = sqmProfile(sqmDraft(saved))
    expect(profile).toEqual({
      ...sqmProfile(sqmDraft()),
      diffserv: "diffserv4",
      overhead: 22,
      mpu: 64,
      preserveDscp: true,
    })
    expect(sqmProblem(sqmDraft(saved), 9000)).toBeUndefined()
    expect(profile).not.toHaveProperty("ifb")
    expect(profile).not.toHaveProperty("token")
    expect(profile).not.toHaveProperty("queue")
  })
})
