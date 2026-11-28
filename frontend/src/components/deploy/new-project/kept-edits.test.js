import { describe, expect, test } from "bun:test"
import {
  NO_KEPT_EDITS,
  editedPaths,
  hasKeptEdits,
  keepEdits,
  withKeptEdits,
  withPaths,
} from "./kept-edits"

const flow = (over = {}) => ({
  name: "shop",
  profile: "web",
  source: { kind: "git", ref: "main" },
  draft: { id: "d", revision: 2 },
  sourceLabel: "acme/shop",
  configuration: {
    build: { method: "recipe", buildCommand: "bun run build", startCommand: "bun start" },
    runtime: { internalPort: 3000, mounts: [] },
    variables: [{ name: "A", value: "1" }],
    domains: [],
  },
  ...over,
})

const withConfig = (base, change) => ({
  ...base,
  configuration: change(structuredClone(base.configuration)),
})

describe("editedPaths", () => {
  test("names each field an edit changed, down to the leaf", () => {
    const before = flow()
    const after = withConfig({ ...before, name: "shop-two" }, (c) => {
      c.build.startCommand = "node server.js"
      return c
    })
    expect(editedPaths(before, after)).toEqual([
      ["name"],
      ["configuration", "build", "startCommand"],
    ])
  })

  test("a list is one field, and the server's parts of the flow are not the person's", () => {
    const before = flow()
    const after = withConfig({ ...before, draft: { id: "d", revision: 9 } }, (c) => {
      c.variables[0].value = "2"
      return c
    })
    expect(editedPaths(before, after)).toEqual([["configuration", "variables"]])
  })

  test("a rebuilt plan that says the same is no edit", () => {
    const before = flow()
    expect(editedPaths(before, structuredClone(before))).toEqual([])
  })

  test("a field taken out is an edit", () => {
    const before = flow()
    const after = withConfig(before, (c) => {
      delete c.build.buildCommand
      return c
    })
    expect(editedPaths(before, after)).toEqual([["configuration", "build", "buildCommand"]])
  })
})

describe("kept edits over a read-back", () => {
  test("a field the person touched keeps their value; every other takes the read-back's", () => {
    const seen = flow()
    const typed = withConfig(seen, (c) => {
      c.build.startCommand = "node server.js"
      return c
    })
    const kept = keepEdits(NO_KEPT_EDITS, editedPaths(seen, typed), typed)
    // The assistant saved a port meanwhile, read back at a new revision.
    const readBack = withConfig(flow({ draft: { id: "d", revision: 5 } }), (c) => {
      c.runtime.internalPort = 8080
      return c
    })
    const merged = withKeptEdits(readBack, kept)
    expect(merged.configuration.build.startCommand).toBe("node server.js")
    expect(merged.configuration.runtime.internalPort).toBe(8080)
    expect(merged.draft.revision).toBe(5)
    // The read-back itself is left as it was.
    expect(readBack.configuration.build.startCommand).toBe("bun start")
  })

  test("the person's later edit of a field replaces their earlier one", () => {
    const seen = flow()
    const first = { ...seen, name: "shop-a" }
    let kept = keepEdits(NO_KEPT_EDITS, editedPaths(seen, first), first)
    const second = { ...first, name: "shop-b" }
    kept = keepEdits(kept, editedPaths(first, second), second)
    expect(Object.keys(kept)).toHaveLength(1)
    expect(withKeptEdits(flow(), kept).name).toBe("shop-b")
  })

  test("a field the person took out stays out", () => {
    const seen = flow()
    const typed = withConfig(seen, (c) => {
      delete c.build.buildCommand
      return c
    })
    const kept = keepEdits(NO_KEPT_EDITS, editedPaths(seen, typed), typed)
    expect("buildCommand" in withKeptEdits(flow(), kept).configuration.build).toBe(false)
  })

  test("nothing kept is nothing to save", () => {
    expect(hasKeptEdits(NO_KEPT_EDITS)).toBe(false)
    const seen = flow()
    expect(hasKeptEdits(keepEdits(NO_KEPT_EDITS, [["name"]], { ...seen, name: "x" }))).toBe(true)
  })
})

test("an edit made on the page as it was drawn writes only its own fields over the page now", () => {
  const drawn = flow()
  // A read-back landed after the field was drawn: the port is the assistant's now.
  const now = withConfig(flow({ draft: { id: "d", revision: 5 } }), (c) => {
    c.runtime.internalPort = 8080
    return c
  })
  const typed = { ...drawn, name: "shop-two" }
  const next = withPaths(now, editedPaths(drawn, typed), typed)
  expect(next.name).toBe("shop-two")
  expect(next.configuration.runtime.internalPort).toBe(8080)
  expect(next.draft.revision).toBe(5)
})
