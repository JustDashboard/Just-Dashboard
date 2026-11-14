import type { Capability } from "@/lib/types"
import { ACCOUNT_SECTION, NAV, navMatches, type NavEntry } from "@/components/nav"

/*
 * Where a missing address would have been: the page of the navigation whose
 * own address it starts with, so `/metrics/nope` offers Metrics and
 * `/docker/images/x/y` offers Images, where the page offered the Overview
 * whatever was asked for.
 */

export type NearestPage = {
  title: string
  href: string
  icon: React.ComponentType<{ className?: string }>
}

/**
 * The deepest page the account may open whose address the path starts with.
 * A section's landing page shares the section's address, so the section's own
 * name wins over "Browse" or "Live". Nothing for an address under no page,
 * and nothing for one the account's role does not reach: offering a page that
 * answers with a refusal is a second dead end.
 */
export function nearestPage(
  pathname: string,
  can: (capability: Capability) => boolean,
): NearestPage | undefined {
  let best: NearestPage | undefined
  const walk = (entries: NavEntry[]) => {
    for (const entry of entries) {
      if (entry.capability && !can(entry.capability)) continue
      if (
        entry.href !== undefined &&
        entry.href !== "/" &&
        navMatches(entry.href, pathname) &&
        entry.href.length > (best?.href.length ?? 0)
      )
        best = { title: entry.title, href: entry.href, icon: entry.icon }
      if (entry.children) walk(entry.children)
    }
  }
  walk([...NAV.flatMap((group) => group.items), ACCOUNT_SECTION])
  return best
}

/** The address as a picture's node says it: its last part, cut where it runs long. */
export function addressTail(pathname: string, most = 24) {
  const last = `/${pathname.split("/").filter(Boolean).at(-1) ?? ""}`
  return last.length > most ? `${last.slice(0, most - 1)}…` : last
}
