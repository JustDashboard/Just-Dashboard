import { expect, test } from "bun:test"
import { generatePassword, PASSWORD_ALPHABET, passwordChecks } from "./password"

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

test("a made password is the length asked for, drawn from the shared alphabet", () => {
  const made = generatePassword(30)
  expect(made).toHaveLength(30)
  for (const char of made) expect(PASSWORD_ALPHABET).toContain(char)
  expect(generatePassword()).toHaveLength(30)
  expect(generatePassword(20)).toHaveLength(20)
  // Nothing a reader copying it by eye confuses.
  expect(PASSWORD_ALPHABET).not.toMatch(/[Il1O0]/)
  // Two draws are not the same password.
  expect(generatePassword(30)).not.toBe(generatePassword(30))
})

test("a made password always passes the rule: a draw with no digit is drawn again", () => {
  const random = crypto.getRandomValues
  const draws = []
  // The first draw is all upper case ("AAAA…"), the second mixes every kind.
  crypto.getRandomValues = (array) => {
    const second = draws.length > 0
    for (let index = 0; index < array.length; index += 1)
      array[index] = second ? [0, 30, 50][index % 3] : 0
    draws.push(array.length)
    return array
  }
  try {
    const made = generatePassword(30)
    expect(draws).toEqual([30, 30])
    expect(passwordChecks(made).ok).toBe(true)
    // Too short to hold three kinds, it is not drawn again.
    draws.length = 0
    expect(generatePassword(2)).toBe("AA")
    expect(draws).toEqual([2])
  } finally {
    crypto.getRandomValues = random
  }
})
