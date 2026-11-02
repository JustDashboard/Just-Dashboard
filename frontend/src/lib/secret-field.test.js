import { describe, expect, test } from "bun:test"
import { NOT_A_SIGN_IN, secretValueField } from "./secret-field"

describe("secretValueField", () => {
  test("a masked value is a text field drawn as dots, never a password field", () => {
    const field = secretValueField(true)
    expect(field.type).toBe("text")
    expect(field.className).toContain("[-webkit-text-security:disc]")
  })

  test("a shown value keeps the same field type and loses only the dots", () => {
    const field = secretValueField(false)
    expect(field.type).toBe("text")
    expect(field.className).not.toContain("text-security")
  })

  test("it asks every password manager to stay out", () => {
    expect(secretValueField(true)).toMatchObject(NOT_A_SIGN_IN)
  })
})
