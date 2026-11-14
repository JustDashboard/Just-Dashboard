import { expect, test } from "bun:test"
import { isHubSession } from "./hub-session"

test("the session in use, made by Just Hub, is a hub session", () => {
  expect(
    isHubSession([
      { id: "a", current: false, authMethod: "totp" },
      { id: "b", current: true, authMethod: "hub" },
    ]),
  ).toBe(true)
})

test("another session of the account made by the hub does not make this one a hub session", () => {
  expect(
    isHubSession([
      { id: "a", current: true, authMethod: "passkey" },
      { id: "b", current: false, authMethod: "hub" },
    ]),
  ).toBe(false)
})

test("an answer that is not a list of sessions is not a hub session", () => {
  expect(isHubSession(undefined)).toBe(false)
  expect(isHubSession({})).toBe(false)
  expect(isHubSession([null])).toBe(false)
  expect(isHubSession([])).toBe(false)
})
