import { saysDockerUnreachable, unreadLine } from "@/components/missing-thing-model"

/** A kind the canvas draws whose source failed: its plural name and the source's own words. */
type UnreadKind = { key: string; label: string; reason?: string }

/**
 * What the map could not read, one line per cause rather than one per kind.
 *
 * A server whose Docker does not answer leaves containers, stacks, volumes
 * and deployments unread at once, each with the client's whole sentence, and
 * the map stacked the four over the canvas it could not draw. They are one
 * fact with one way on: the Docker page, which says which fault it is.
 */
export type UnreadNotice = {
  key: string
  /** The cause drawn as itself, where it is a product. */
  thing?: string
  sentence: string
  href?: string
  /** The link's words. */
  label?: string
  /** The sources' own words, behind a fold: never the sentence. */
  raw?: string
}

const STILL = "Still being read"

const words = (kinds: UnreadKind[]) => {
  const names = [...new Set(kinds.map((kind) => kind.label.toLowerCase()))]
  return names.length < 2
    ? (names[0] ?? "")
    : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`
}

export function unreadNotices(unread: UnreadKind[]): UnreadNotice[] {
  const filling = unread.filter((kind) => kind.reason === STILL)
  const docker = unread.filter(
    (kind) => kind.reason !== STILL && saysDockerUnreachable(kind.reason),
  )
  const rest = unread.filter((kind) => !filling.includes(kind) && !docker.includes(kind))
  const out: UnreadNotice[] = []
  if (docker.length > 0) {
    out.push({
      key: "docker",
      thing: "docker",
      sentence: unreadLine(
        "Docker is not answering",
        // "Compose stacks" beside Docker's name is "stacks".
        docker.map((kind) => kind.label.replace(/^compose /i, "")),
      ),
      href: "/docker",
      label: "Open Docker",
      raw: [...new Set(docker.map((kind) => kind.reason).filter(Boolean))].join("\n"),
    })
  }
  // What failed for another reason is said together where the reason is the
  // same, in the order the first of each was listed.
  const byReason = new Map<string, UnreadKind[]>()
  for (const kind of rest) {
    const reason = kind.reason ?? ""
    byReason.set(reason, [...(byReason.get(reason) ?? []), kind])
  }
  for (const [reason, kinds] of byReason) {
    const list = words(kinds)
    out.push({
      key: `unread:${kinds[0].key}`,
      sentence: `${list.charAt(0).toUpperCase()}${list.slice(1)} not read`,
      raw: reason || undefined,
    })
  }
  if (filling.length > 0) {
    out.push({ key: "filling", sentence: `Still reading ${words(filling)}` })
  }
  return out
}
