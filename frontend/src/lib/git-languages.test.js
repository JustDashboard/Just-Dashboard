import { describe, expect, test } from "bun:test"
import { languageColour, languagePercent, languageProduct, languageSegments } from "./git-languages"

describe("a checkout's languages", () => {
  test("each named language has its token and its logo", () => {
    expect(languageColour("TypeScript")).toBe("var(--language-typescript)")
    expect(languageColour("C++")).toBe("var(--language-cpp)")
    expect(languageProduct("Shell")).toBe("shellscript")
    expect(languageProduct("Dockerfile")).toBe("docker")
  })

  test("a language outside the set keeps the slate hue and no logo", () => {
    expect(languageColour("Fortran")).toBe("var(--tag-slate)")
    expect(languageProduct("Fortran")).toBeUndefined()
  })

  test("shares print as the forge prints them", () => {
    expect(languagePercent(0.6234)).toBe("62%")
    expect(languagePercent(0.0412)).toBe("4.1%")
    expect(languagePercent(0.0004)).toBe("<0.1%")
  })

  test("the bar spans its track, with what the server dropped as one segment", () => {
    const segments = languageSegments([
      { name: "Go", share: 0.6 },
      { name: "TypeScript", share: 0.35 },
    ])
    expect(segments.map((s) => s.name)).toEqual(["Go", "TypeScript", "Other"])
    expect(segments[2].share).toBeCloseTo(0.05)
    expect(languageSegments(undefined)).toEqual([])
    expect(languageSegments([{ name: "Go", share: 1 }]).map((s) => s.name)).toEqual(["Go"])
  })
})
