import { describe, expect, test } from "bun:test"
import { uploadDraft, uploadProblem, uploadProfile } from "./upload-profile"

describe("CAKE upload profile", () => {
  test("defaults to one class, source-host fairness and kept marks", () => {
    const draft = uploadDraft()
    expect(draft).toEqual({
      diffserv: "besteffort",
      flowMode: "dual-srchost",
      nat: false,
      wash: false,
      ackFilter: false,
      linkLayer: "noatm",
      overhead: "0",
      mpu: "0",
      rttMillis: "100",
    })
    expect(uploadProblem(draft)).toBeUndefined()
  })
  test("round-trips a saved profile and refuses values the server would", () => {
    const saved = {
      diffserv: "diffserv4",
      flowMode: "triple-isolate",
      nat: true,
      wash: true,
      ackFilter: true,
      linkLayer: "ptm",
      overhead: 34,
      mpu: 64,
      rttMillis: 50,
    }
    expect(uploadProfile(uploadDraft(saved))).toEqual(saved)
    expect(uploadProblem({ ...uploadDraft(), overhead: "300" })).toBe(
      "Overhead must be a whole number from -64 to 256.",
    )
    expect(uploadProblem({ ...uploadDraft(), rttMillis: "5" })).toBe(
      "RTT must be a whole number from 10 to 1000.",
    )
    expect(uploadProblem({ ...uploadDraft(), mpu: "1.5" })).toBe(
      "Minimum packet size must be a whole number from 0 to 256.",
    )
    expect(uploadProblem({ ...uploadDraft(), flowMode: "dual-dsthost" })).toBe(
      "Choose a supported CAKE upload profile.",
    )
  })
})
