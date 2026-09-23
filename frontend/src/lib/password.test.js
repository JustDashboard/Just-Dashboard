import { expect, test } from "bun:test"
import { passwordChecks } from "./password"

test("twelve bytes and three classes, as the server counts them", () => {
  expect(passwordChecks("")).toEqual({ long: false, classes: 0, mixed: false, ok: false })
  expect(passwordChecks("correcthorsebattery")).toMatchObject({ long: true, classes: 1, ok: false })
  expect(passwordChecks("Correcthorse1")).toMatchObject({ long: true, classes: 3, ok: true })
  expect(passwordChecks("Short1!")).toMatchObject({ long: false, classes: 4, ok: false })
  // A space is a symbol to the server: anything not a letter or a digit.
  expect(passwordChecks("correct horse 9")).toMatchObject({ classes: 3, ok: true })
})

test("length is bytes, not characters", () => {
  // Six characters, twelve bytes in UTF-8.
  expect(passwordChecks("ăîșțâă").long).toBe(true)
  expect(passwordChecks("ăîșțâ").long).toBe(false)
})
