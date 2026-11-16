import { expect, test } from "bun:test"
import { ROLE_HUE, ROLE_MARK, ROLE_NAME } from "./role"
import { ROLE_LADDER } from "./capabilities"

test("every role has a name a person reads, a mark and a hue", () => {
  for (const role of ROLE_LADDER) {
    expect(ROLE_NAME[role]).toBeString()
    // The name is a word, never the key the API spells it with.
    expect(ROLE_NAME[role]).not.toBe(role)
    expect(ROLE_NAME[role][0]).toBe(ROLE_NAME[role][0].toUpperCase())
    expect(ROLE_MARK[role]).toBeDefined()
    expect(ROLE_HUE[role]).toMatch(/^var\(--tag-/)
  }
  expect(Object.keys(ROLE_NAME).sort()).toEqual([...ROLE_LADDER].sort())
})

test("no role's hue is a signal colour", () => {
  for (const hue of Object.values(ROLE_HUE)) expect(hue).not.toMatch(/red|amber/)
})
