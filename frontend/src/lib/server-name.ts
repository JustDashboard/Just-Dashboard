/** The kernel's limit on a host name. */
const MAX_LENGTH = 64

/**
 * The name as the server will carry it: host names are lower-case, and the
 * server folds what it is given, so the form says the same before it asks.
 */
export function serverName(value: string) {
  return value.trim().toLowerCase()
}

/**
 * Why the server cannot take this name, or undefined when it can. The same
 * rules as `hostname.Normalize` on the server, in the same words, so a name
 * is refused under the field it was typed in rather than after a request.
 */
export function serverNameProblem(value: string): string | undefined {
  const name = serverName(value)
  if (!name) return undefined
  if (name.length > MAX_LENGTH) return `A server's name is at most ${MAX_LENGTH} characters.`
  if (name === "localhost" || name.startsWith("localhost."))
    return "localhost already means this machine to every program on it; pick another name."
  for (const label of name.split(".")) {
    if (!label) return "A name cannot start or end with a dot, or have two in a row."
    if (label.length > 63) return "Each part of a name between dots is at most 63 characters."
    if (label.startsWith("-") || label.endsWith("-"))
      return "A name cannot start or end with a hyphen."
    if (!/^[a-z0-9-]+$/.test(label)) return "A name holds only letters, digits and hyphens."
  }
  return undefined
}
