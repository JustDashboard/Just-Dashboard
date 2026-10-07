import { expect, test } from "bun:test"
import { COUNTRIES, countryName, flag, searchCountries } from "./countries"

test("every ISO 3166-1 alpha-2 code is here once, named", () => {
  expect(COUNTRIES).toHaveLength(249)
  expect(new Set(COUNTRIES.map((c) => c.code)).size).toBe(249)
  for (const { code, name } of COUNTRIES) {
    expect(code).toMatch(/^[A-Z]{2}$/)
    expect(name.trim()).not.toBe("")
  }
})

test("the picker reads in alphabetical order of the name", () => {
  const names = COUNTRIES.map((c) => c.name)
  expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b, "en")))
})

test("a flag is the two regional-indicator letters, whatever the case", () => {
  expect(flag("US")).toBe("\u{1F1FA}\u{1F1F8}")
  expect(flag("ro")).toBe("\u{1F1F7}\u{1F1F4}")
  expect(flag("cn")).toBe(flag("CN"))
})

test("a flag is empty for anything that is not a two-letter code", () => {
  for (const bad of ["", "u", "usa", "1a", "ü1", "  "]) expect(flag(bad)).toBe("")
})

test("a name is found by either case and a stranger comes back as its code", () => {
  expect(countryName("ru")).toBe("Russia")
  expect(countryName("RU")).toBe("Russia")
  expect(countryName("zz")).toBe("ZZ")
})

test("search matches part of a name, or a whole code", () => {
  expect(searchCountries("korea").map((c) => c.code)).toEqual(["KP", "KR"])
  expect(searchCountries("  RO ").map((c) => c.code)).toContain("RO")
  expect(searchCountries("")).toHaveLength(249)
  expect(searchCountries("nowhere at all")).toEqual([])
})
