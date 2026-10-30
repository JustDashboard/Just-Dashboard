import { describe, expect, test } from "bun:test"
import {
  diagnosisDestination,
  diagnosisGlyph,
  diagnosisProvenance,
  diagnosisSends,
} from "./run-diagnosis-model"

describe("run diagnosis", () => {
  test("names where the question goes", () => {
    expect(diagnosisDestination({ provider: "anthropic" })).toBe("Anthropic")
    expect(diagnosisDestination({ provider: "openai", baseUrl: "http://127.0.0.1:11434/v1" })).toBe(
      "127.0.0.1:11434",
    )
    expect(diagnosisDestination({ provider: "openai", baseUrl: "not a url" })).toBe(
      "the model's server",
    )
    expect(diagnosisDestination({ provider: "openai" })).toBe("the model's server")
  })

  test("says what is sent and that nothing changes", () => {
    const line = diagnosisSends({ provider: "anthropic" })
    expect(line).toContain("to Anthropic")
    expect(line).toContain("secret values masked")
    expect(line).toContain("Nothing is changed")
  })

  test("counts what was sent", () => {
    expect(diagnosisProvenance({ sentLines: 1, masked: 0 })).toBe("1 line sent")
    expect(diagnosisProvenance({ sentLines: 86, masked: 1 })).toBe("86 lines sent · 1 value masked")
    expect(diagnosisProvenance({ sentLines: 86, masked: 3 })).toBe(
      "86 lines sent · 3 values masked",
    )
  })

  test("draws the provider", () => {
    expect(diagnosisGlyph("anthropic")).toBe("claude")
    expect(diagnosisGlyph("openai")).toBe("openai")
  })
})
