import { expect, test } from "bun:test"
import { TOOL_GROUPS } from "./tool-defs"
import { portProblem, toolReady } from "./tool-input"

const tools = TOOL_GROUPS.flatMap((group) => group.tools)
const tool = (key) => tools.find((entry) => entry.key === key)

test("invalid ports never become port zero or the backend's default", () => {
  for (const port of ["", "0", "-1", "65536", "1.5", "443e0", "0x1bb", "https"]) {
    expect(portProblem(port)).toBeDefined()
    expect(toolReady(tool("port"), "example.com", port, "")).toBe(false)
  }
  for (const port of ["1", "443", "65535", " 443 "]) expect(portProblem(port)).toBeUndefined()
})

test("host tools work without a target and Wake-on-LAN requires its LAN interface", () => {
  expect(toolReady(tool("capabilities"), "", "", "")).toBe(true)
  expect(toolReady(tool("route"), "", "", "")).toBe(false)
  expect(toolReady(tool("wol"), "00:11:22:33:44:55", "", " ")).toBe(false)
  expect(toolReady(tool("wol"), "00:11:22:33:44:55", "", "eno1")).toBe(true)
})
