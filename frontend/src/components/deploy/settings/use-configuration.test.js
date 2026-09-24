import { describe, expect, test } from "bun:test"
import { draftChanges, settingDigest } from "./use-configuration"

describe("settingDigest", () => {
  test("a saved value keys the same draft whatever order its fields arrived in", () => {
    const one = { method: "recipe", recipe: "node", secrets: [{ name: "NPM_TOKEN" }] }
    const two = { secrets: [{ name: "NPM_TOKEN" }], recipe: "node", method: "recipe" }
    expect(settingDigest(one)).toBe(settingDigest(two))
  })

  test("a field set to undefined is the field left out, as it is after session storage", () => {
    expect(settingDigest({ method: "dockerfile", goVersion: undefined })).toBe(
      settingDigest({ method: "dockerfile" }),
    )
  })

  test("a different value is a different draft, and no digest is a prefix of another", () => {
    const a = settingDigest({ internalPort: 3000 })
    const b = settingDigest({ internalPort: 8080 })
    expect(a).not.toBe(b)
    expect(a).toMatch(/^[0-9a-f]{8}$/)
    expect(settingDigest("")).toMatch(/^[0-9a-f]{8}$/)
  })

  test("a sibling section's save leaves this section's key alone", () => {
    // The defect this replaces: drafts keyed on the configuration's revision,
    // which a Health checks save bumps, restarted the Runtime form beside it.
    const runtime = { internalPort: 3000, memoryMb: 512 }
    const before = { revision: 3, runtime, checks: [] }
    const after = { revision: 4, runtime, checks: [{ name: "ready" }] }
    expect(settingDigest(after.runtime)).toBe(settingDigest(before.runtime))
  })
})

describe("draftChanges", () => {
  test("counts the fields of an object that differ", () => {
    const saved = { buildCommand: "bun run build", startCommand: "bun start", noCache: false }
    expect(draftChanges(saved, saved)).toBe(0)
    expect(draftChanges({ ...saved, buildCommand: "npm run build" }, saved)).toBe(1)
    expect(draftChanges({ ...saved, noCache: true, rootDirectory: "apps/web" }, saved)).toBe(2)
  })

  test("counts the positions of a list that differ, so an edit and an addition are two", () => {
    const saved = [{ name: "ready", kind: "http" }]
    const draft = [
      { name: "ready", kind: "tcp" },
      { name: "smoke", kind: "http" },
    ]
    expect(draftChanges(draft, saved)).toBe(2)
    expect(draftChanges([], saved)).toBe(1)
  })

  test("a scalar is one change or none", () => {
    expect(draftChanges("api-production", "api-production")).toBe(0)
    expect(draftChanges("api", "api-production")).toBe(1)
  })

  test("key order and undefined fields are not edits", () => {
    expect(draftChanges({ b: 1, a: { y: 2, x: 1 } }, { a: { x: 1, y: 2 }, b: 1 })).toBe(0)
    expect(draftChanges({ a: 1, b: undefined }, { a: 1 })).toBe(0)
  })
})

describe("fields the server leaves out", () => {
  // The plan is Go with `omitempty` and no pointers: false, 0, "" and an empty
  // list come back as the field missing, so a draft holding one is not an edit.
  const saved = { method: "recipe", recipe: "node", secrets: [] }

  test("an empty value is the field left out, so a switch turned on and off is no change", () => {
    expect(draftChanges({ ...saved, noCache: false }, saved)).toBe(0)
    expect(draftChanges({ ...saved, rootDirectory: "" }, saved)).toBe(0)
    expect(draftChanges({ internalPort: 3000, hostPort: 0 }, { internalPort: 3000 })).toBe(0)
    expect(draftChanges({ privileged: false, capabilities: [] }, {})).toBe(0)
  })

  test("the key of a saved value does not move when the server drops an empty field", () => {
    expect(settingDigest({ ...saved, noCache: false, hostPort: 0 })).toBe(settingDigest(saved))
    expect(settingDigest([{ name: "ready", config: { port: 0 } }])).toBe(
      settingDigest([{ name: "ready", config: {} }]),
    )
  })

  test("a value that is not empty is still an edit, and a list keeps every position", () => {
    expect(draftChanges({ ...saved, noCache: true }, saved)).toBe(1)
    expect(draftChanges({ ...saved, rootDirectory: "apps/web" }, saved)).toBe(1)
    expect(draftChanges(["a", ""], ["a"])).toBe(1)
    expect(settingDigest({ env: ["A", ""] })).not.toBe(settingDigest({ env: ["A"] }))
  })
})
