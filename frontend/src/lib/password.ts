/**
 * The server's password rule, read as the reader types: at least twelve
 * bytes — Go's `len`, so a character outside ASCII counts for more than one —
 * mixing three of upper case, lower case, digits and anything else. It
 * mirrors `auth.ValidatePasswordStrength`, which is still the one that
 * decides; this only means the form says so before the round trip does.
 */
export function passwordChecks(password: string) {
  const long = new TextEncoder().encode(password).length >= 12
  const classes = [/[A-Z]/, /[a-z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((pattern) =>
    pattern.test(password),
  ).length
  return { long, classes, mixed: classes >= 3, ok: long && classes >= 3 }
}

/**
 * The characters a made password is drawn from: letters and digits every
 * engine, shell and `htpasswd` line takes unquoted, without the pairs a reader
 * copying one by eye confuses (I and l, O and 0, 1).
 */
export const PASSWORD_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

/**
 * A password for an account the dashboard makes — a database's, a system
 * user's, a proxy login's: thirty characters by default, drawn with the
 * browser's own generator. One generator, so every "Generate" in the product
 * makes the same kind of password.
 */
export function generatePassword(length = 30): string {
  const bytes = crypto.getRandomValues(new Uint8Array(length))
  return Array.from(bytes, (byte) => PASSWORD_ALPHABET[byte % PASSWORD_ALPHABET.length]).join("")
}
