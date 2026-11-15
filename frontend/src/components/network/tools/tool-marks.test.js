import { expect, test } from "bun:test"
import { TOOL_GROUPS } from "./tool-defs"
import { PATH_TOOL } from "./tool-links"
import { GROUP_HUE, TOOL_GLYPH } from "./tool-marks"

test("every diagnostic in the rail has a mark of its own choosing", () => {
  const keys = [...TOOL_GROUPS.flatMap((group) => group.tools.map((tool) => tool.key)), PATH_TOOL]
  expect(keys).toHaveLength(27)
  for (const key of [...keys, "subnet"]) expect(TOOL_GLYPH[key], key).toBeDefined()
  // Nothing is drawn for a tool that no longer exists.
  expect(Object.keys(TOOL_GLYPH).sort()).toEqual([...keys, "subnet"].sort())
})

test("each group has a hue and no two share one", () => {
  expect(GROUP_HUE).toHaveLength(TOOL_GROUPS.length)
  expect(new Set(GROUP_HUE).size).toBe(GROUP_HUE.length)
})
