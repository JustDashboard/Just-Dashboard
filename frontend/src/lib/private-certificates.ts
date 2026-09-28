import type { KeyType, LocalCA, LocalCACheck, LocalCALeaf } from "@/lib/types"

/**
 * Readings for the certificates this server makes itself — signing requests,
 * self-signed pairs and the local CA — kept here so they are tested without a
 * browser.
 */

/** The keys offered, as the segmented control labels them and as a sentence names them. */
export const KEY_TYPES: { value: KeyType; label: string; name: string }[] = [
  { value: "ecdsa-p256", label: "P-256", name: "ECDSA P-256" },
  { value: "ecdsa-p384", label: "P-384", name: "ECDSA P-384" },
  { value: "rsa-2048", label: "RSA 2048", name: "RSA 2048" },
  { value: "rsa-3072", label: "RSA 3072", name: "RSA 3072" },
  { value: "rsa-4096", label: "RSA 4096", name: "RSA 4096" },
]

export function keyTypeName(kind: KeyType | undefined): string {
  return KEY_TYPES.find((k) => k.value === kind)?.name ?? "an unusual key"
}

/** The rule the server holds a stored certificate's name to, as it does an import's. */
const NAME = /^[a-z0-9][a-z0-9._-]{0,63}$/
/** Caddy's release copies share the directory, and their shape is refused to anything else. */
const CADDY_COPY = /^caddy-[0-9a-f]{24}$/

/**
 * The name to keep a certificate for these names under, from the first of
 * them: a wildcard's star becomes the word, anything a directory name cannot
 * hold a dash.
 */
export function suggestName(names: string[]): string {
  const first =
    names
      .find((name) => name.trim() !== "")
      ?.trim()
      .toLowerCase() ?? ""
  return first
    .replace(/^\*\./, "wildcard.")
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^[^a-z0-9]+/, "")
    .slice(0, 64)
    .replace(/[._-]+$/, "")
}

/** Why a name cannot be used, before the server says so; undefined when it can. */
export function nameProblem(name: string): string | undefined {
  if (name === "") return undefined
  if (!NAME.test(name)) {
    return "Lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit."
  }
  if (CADDY_COPY.test(name))
    return "That is the shape of Caddy's release copies. Choose another name."
  return undefined
}

/** Names in a sentence: "a", "a and b", "a, b and c". */
export function joinNames(names: string[]): string {
  if (names.length <= 1) return names.join("")
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
}

/**
 * What the last daily pass did, as the end of a sentence that starts with
 * when it ran. A failure is not phrased here: it is a Notice of its own.
 */
export function checkOutcome(check: LocalCACheck): string {
  if (check.renewed.length === 0) return "nothing was due"
  const renewed = `renewed ${joinNames(check.renewed)}`
  return check.reloaded.length > 0
    ? `${renewed}, and reloaded nginx for ${joinNames(check.reloaded)}`
    : renewed
}

/** Whether the last pass needs the reader: it could not finish, or left a certificate unrenewed. */
export function checkTrouble(
  check: LocalCACheck | undefined,
): { tone: "danger" | "warning"; title: string; lines: string[] } | undefined {
  if (!check) return undefined
  if (check.error) {
    return {
      tone: "danger",
      title: "The last renewal check did not finish",
      lines: [check.error, ...check.failed],
    }
  }
  if (check.failed.length > 0) {
    return {
      tone: "warning",
      title:
        check.failed.length === 1
          ? "A certificate could not be renewed"
          : `${check.failed.length} certificates could not be renewed`,
      lines: check.failed,
    }
  }
  return undefined
}

/**
 * Where a certificate of the local CA's stands: one that cannot be renewed
 * says so first, then one past its date, then one the next check renews.
 */
export function leafState(
  leaf: Pick<LocalCALeaf, "error" | "daysLeft" | "notAfter">,
  renewBefore: number,
  now: number = Date.now(),
): "cannot-renew" | "expired" | "due" | "scheduled" {
  if (leaf.error) return "cannot-renew"
  if (new Date(leaf.notAfter).getTime() <= now) return "expired"
  if (leaf.daysLeft <= renewBefore) return "due"
  return "scheduled"
}

/** The root itself has run out: nothing it signed verifies any more, renewed or not. */
export function rootExpired(ca: Pick<LocalCA, "notAfter">, now: number = Date.now()): boolean {
  return Boolean(ca.notAfter) && new Date(ca.notAfter ?? "").getTime() <= now
}
