import { expect, test } from "bun:test"
import { withPasswords, withTypedPassword, withoutPassword } from "./domain-passwords"

const saved = { hostname: "a.example.test", https: true, ownership: "managed" }
const guarded = { ...saved, protection: { username: "team", hash: "sealed" } }

test("a typed password is set by name and gone when emptied", () => {
  const typed = withTypedPassword({}, "a.example.test", "long enough")
  expect(typed).toEqual({ "a.example.test": "long enough" })
  expect(withTypedPassword(typed, "a.example.test", "")).toEqual({})
})

test("the password joins the draft on the way to the server and keeps the hash", () => {
  expect(withPasswords([guarded], { "a.example.test": "long enough" })).toEqual([
    { ...saved, protection: { username: "team", hash: "sealed", password: "long enough" } },
  ])
})

test("a name with nothing typed is sent as it is, so its saved hash stands", () => {
  expect(withPasswords([guarded], {})).toEqual([guarded])
})

test("a name that no longer asks for a password does not send one", () => {
  expect(withPasswords([saved], { "a.example.test": "long enough" })).toEqual([saved])
})

test("a draft written before the password moved out loses only the password", () => {
  const old = { ...saved, protection: { username: "team", hash: "sealed", password: "typed" } }
  expect(withoutPassword(old)).toEqual(guarded)
  expect(withoutPassword(saved)).toBe(saved)
})
