import { expect, test } from "bun:test"
import { CAPABILITIES, ROLE_LADDER, ROLE_SUMMARY } from "./capabilities"

// What a role holds, by the ladder: each rung adds to the one before it.
const held = (role) =>
  CAPABILITIES.filter((cap) => ROLE_LADDER.indexOf(cap.from) <= ROLE_LADDER.indexOf(role)).map(
    (cap) => cap.key,
  )

// The Files page works as root, so writing files is root, as the shell is.
const asGoodAsRoot = (role) => held(role).some((key) => key === "file.write" || key === "terminal")

test("a role that can become root says so where it is handed out", () => {
  expect(ROLE_LADDER.filter(asGoodAsRoot)).toEqual(["limited", "admin"])
  for (const role of ROLE_LADDER.filter(asGoodAsRoot)) {
    expect(ROLE_SUMMARY[role]).toMatch(/\broot\b/)
  }
  // The sentence that made limited read as a lesser trust.
  expect(ROLE_SUMMARY.limited).not.toMatch(/no deleting|no accounts/)
})

test("the role that only reads says it reads secrets as root", () => {
  expect(asGoodAsRoot("readonly")).toBe(false)
  expect(ROLE_SUMMARY.readonly).toMatch(/\broot\b/)
  expect(ROLE_SUMMARY.readonly).toMatch(/secrets/)
  expect(ROLE_SUMMARY.readonly).not.toMatch(/^Look at everything, change nothing$/)
})

test("the powers that reach root or its secrets say so on the ladder", () => {
  const sentence = (key) => CAPABILITIES.find((cap) => cap.key === key).description
  expect(sentence("read")).toMatch(/as root/)
  expect(sentence("read")).toMatch(/secrets/)
  expect(sentence("file.write")).toMatch(/as root/)
  expect(sentence("file.write")).toMatch(/become root/)
  expect(sentence("terminal")).toMatch(/root/)
})
