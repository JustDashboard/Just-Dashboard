/**
 * Props for a field that holds a value the operator stores, not one they sign
 * in with.
 *
 * Chrome ignores `autocomplete="off"` wherever a form has a `type="password"`
 * field: it offers saved sign-ins there, and treats the text field before it as
 * the username. A variable form then opens a list of the operator's passwords
 * on its name, and a pick pours one into a value that gets deployed. So a
 * stored value is never a password field, not even for a moment, because Chrome
 * remembers a field that has been one. It is a text field drawn as dots.
 * `-webkit-text-security` reaches every engine, Firefox since 114.
 */

/** The opt-outs extensions read, since each password manager has its own. */
export const NOT_A_SIGN_IN = {
  autoComplete: "off",
  "data-1p-ignore": true,
  "data-lpignore": "true",
  "data-bwignore": true,
  "data-form-type": "other",
} as const

/**
 * The value input. A text field also gets what `type="password"` gave for free:
 * no capitalised first letter, no correction, no spelling check. The placeholder
 * stays readable, since it says what belongs in the field and is not the value.
 */
export function secretValueField(masked: boolean) {
  return {
    ...NOT_A_SIGN_IN,
    type: "text",
    autoCapitalize: "off",
    autoCorrect: "off",
    spellCheck: false,
    className: masked
      ? "font-mono [-webkit-text-security:disc] placeholder:[-webkit-text-security:none]"
      : "font-mono",
  } as const
}
