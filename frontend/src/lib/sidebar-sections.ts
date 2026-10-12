/**
 * The sections an account can keep out of its sidebar, and the first-run
 * question that picks them.
 *
 * A dashboard that runs deployments, databases, a router, a firewall and a
 * drawing board shows all of it to somebody who came to ship one app. The
 * question asks what the server is for and keeps the rest out of the rail —
 * out of the *rail*, not out of reach. Nothing here is a permission: every
 * route still answers by role, a link from a deployment's domain into the
 * proxy still opens the proxy, and search still finds every page. What a
 * hidden section loses is its row in the sidebar, its tiles on the Overview
 * and its pages in the palette's empty list.
 *
 * A section is named by the first segment of its path, which is also the
 * name the server stores; Overview, Monitoring, Settings and the account are
 * not in the list, because a rail without them has no way to say how the
 * machine is or to change this answer.
 */

export const SECTION_KEYS = [
  "deploy",
  "databases",
  "docker",
  "excalidraw",
  "stickyboard",
  "terminal",
  "files",
  "git",
  "network",
  "proxy",
  "security",
  "backups",
  "packages",
  "system-users",
  "audit",
] as const

export type SectionKey = (typeof SECTION_KEYS)[number]

const KEYS = new Set<string>(SECTION_KEYS)

/** The section a path is inside, or `null` for a page every rail keeps. */
export function sectionOf(pathname: string): SectionKey | null {
  const first = pathname.split(/[/?#]/)[1] ?? ""
  return KEYS.has(first) ? (first as SectionKey) : null
}

/**
 * The answers to "what will you use this server for?", each the sections it
 * needs. They overlap on purpose: Backups belongs to shipping apps and to
 * running databases alike, and choosing both shows it once.
 */
export const PURPOSES = [
  {
    key: "ship",
    title: "Ship apps",
    products: ["github", "docker", "lets-encrypt"],
    sections: ["deploy", "docker", "proxy", "databases", "git", "backups"],
  },
  {
    key: "databases",
    title: "Run databases",
    products: ["postgres", "mongodb", "redis"],
    sections: ["databases", "backups", "docker"],
  },
  {
    key: "containers",
    title: "Manage containers",
    products: ["docker", "docker-compose", "terminal"],
    sections: ["docker", "files", "terminal"],
  },
  {
    key: "network",
    title: "Route the network",
    products: ["wireguard", "tailscale", "nginx"],
    sections: ["network", "proxy", "security"],
  },
  {
    key: "secure",
    title: "Secure the server",
    products: ["crowdsec", "fail2ban", "netfilter"],
    sections: ["security", "network", "packages", "audit", "backups", "system-users"],
  },
  {
    key: "workspace",
    title: "Work on the server",
    products: ["terminal", "git", "excalidraw"],
    sections: ["terminal", "files", "git", "excalidraw", "stickyboard"],
  },
] as const satisfies readonly {
  key: string
  title: string
  /** Drawn as themselves on the purpose's card and in the picture of the server. */
  products: readonly string[]
  sections: readonly SectionKey[]
}[]

export type PurposeKey = (typeof PURPOSES)[number]["key"]

/** Every section the chosen purposes do not use, in the rail's order. */
export function hiddenFor(purposes: readonly PurposeKey[]): SectionKey[] {
  const used = new Set<SectionKey>(
    PURPOSES.filter((purpose) => purposes.includes(purpose.key)).flatMap(
      (purpose) => purpose.sections,
    ),
  )
  return SECTION_KEYS.filter((key) => !used.has(key))
}
