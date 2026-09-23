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
