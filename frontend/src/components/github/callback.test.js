import { expect, test } from "bun:test"
import { deniedWords, readReturn } from "./callback"

test("readReturn tells a code, a refusal and nothing apart", () => {
  expect(readReturn("?code=abc&state=xyz")).toEqual({ kind: "code", code: "abc", state: "xyz" })
  expect(readReturn("?error=access_denied&error_description=The+user+denied")).toEqual({
    kind: "denied",
    error: "access_denied",
    description: "The user denied",
  })
  expect(readReturn("?code=abc")).toEqual({ kind: "empty" })
  expect(readReturn("")).toEqual({ kind: "empty" })
})

test("deniedWords says what a press of Cancel did", () => {
  expect(deniedWords("access_denied")).toMatch(/cancelled on GitHub/)
  expect(deniedWords("redirect_uri_mismatch")).toMatch(/callback URLs/)
  expect(deniedWords("other", "Something else")).toBe("Something else")
  expect(deniedWords("other")).toMatch(/\(other\)/)
})
