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
  "remote-desktop",
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
 * brings into the rail. No two share a section, and none takes one of the
 * base's: Manage containers used to be Docker, Files and Terminal, which the
 * stable three already brought, so pressing it changed nothing, and Route the
 * network and Secure the server brought the same two rows. Now every card
 * turns on something no other card does.
 *
 * The stable three come first and a new account starts with them chosen; the
 * experimental three work but are still being tested, so they are offered
 * under their own name and taken only by somebody who presses them.
 */
export const PURPOSES = [
  {
    key: "ship",
    title: "Ship apps",
    stage: "stable",
    products: ["github", "nginx", "lets-encrypt"],
    sections: ["deploy", "proxy"],
  },
  {
    key: "databases",
    title: "Run databases",
    stage: "stable",
    products: ["postgres", "mongodb", "redis"],
    sections: ["databases", "backups"],
  },
  {
    key: "workspace",
    title: "Work on the server",
    stage: "stable",
    products: ["terminal", "git", "excalidraw"],
    sections: ["terminal", "files", "git", "excalidraw", "stickyboard", "remote-desktop"],
  },
  {
    key: "containers",
    title: "Manage containers",
    stage: "experimental",
    products: ["docker", "docker-compose", "terminal"],
    sections: ["docker"],
  },
  {
    key: "network",
    title: "Route the network",
    stage: "experimental",
    products: ["wireguard", "tailscale", "adguard"],
    sections: ["network"],
  },
  {
    key: "secure",
    title: "Secure the server",
    stage: "experimental",
    products: ["crowdsec", "fail2ban", "netfilter"],
    sections: ["security"],
  },
] as const satisfies readonly {
  key: string
  title: string
  stage: PurposeStage
  /** Drawn as themselves on the purpose's card and in the picture of the server. */
  products: readonly string[]
  sections: readonly SectionKey[]
}[]

export type PurposeStage = "stable" | "experimental"

/** The two shelves the purposes are offered on, in the order they are offered. */
export const STAGES = [
  { key: "stable", title: "Stable" },
  { key: "experimental", title: "Experimental", hint: "Working, still being tested" },
] as const satisfies readonly { key: PurposeStage; title: string; hint?: string }[]

export type PurposeKey = (typeof PURPOSES)[number]["key"]

/** What the question starts with chosen: every purpose that is finished. */
export const STABLE_PURPOSES: PurposeKey[] = PURPOSES.filter(
  (purpose) => purpose.stage === "stable",
).map((purpose) => purpose.key)

/**
 * The sections every answer keeps, whatever the server is for: the packages,
 * the system accounts and the audit trail are how the machine itself is
 * changed and checked. They stay the account's to switch off afterwards.
 */
export const BASE_SECTIONS = [
  "packages",
  "system-users",
  "audit",
] as const satisfies readonly SectionKey[]

/** Every section the chosen purposes do not use, in the rail's order. */
export function hiddenFor(purposes: readonly PurposeKey[]): SectionKey[] {
  const used = new Set<SectionKey>([
    ...BASE_SECTIONS,
    ...PURPOSES.filter((purpose) => purposes.includes(purpose.key)).flatMap(
      (purpose) => purpose.sections,
    ),
  ])
  return SECTION_KEYS.filter((key) => !used.has(key))
}

/**
 * Whether every section of a purpose that the account is offered is in its
 * rail — which is what a purpose being chosen means once the question is
 * over, since only the sections are stored.
 */
export function purposeShown(
  key: PurposeKey,
  hidden: ReadonlySet<string>,
  offered: ReadonlySet<string>,
): boolean {
  return offeredSections(key, offered).every((section) => !hidden.has(section))
}

/**
 * The hidden sections after a purpose is pressed: one that is wholly shown
 * leaves the rail, and any other comes back whole. Purposes share no section,
 * so a press never touches another card. Sections the account is not offered
 * are left alone — a switch for them would decide nothing.
 */
export function pressPurpose(
  key: PurposeKey,
  hidden: ReadonlySet<string>,
  offered: ReadonlySet<string>,
): Set<string> {
  const next = new Set(hidden)
  const shown = purposeShown(key, hidden, offered)
  for (const section of offeredSections(key, offered)) {
    if (shown) next.add(section)
    else next.delete(section)
  }
  return next
}

function offeredSections(key: PurposeKey, offered: ReadonlySet<string>): SectionKey[] {
  const purpose = PURPOSES.find((p) => p.key === key)
  return (purpose?.sections ?? []).filter((section) => offered.has(section))
}
