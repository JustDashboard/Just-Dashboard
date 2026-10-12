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

/** What each section is for, in the words the question uses beside its switch. */
export const SECTION_HINTS: Record<SectionKey, string> = {
  deploy: "Ship projects from Git, an image, a template or a compose file.",
  databases: "Connect, browse, query and back up the databases on this server.",
  docker: "Containers, compose stacks, images, volumes and networks.",
  excalidraw: "Drawings kept on the server, with cards for what runs on it.",
  stickyboard: "Sticky-note boards for plans and checklists.",
  terminal: "A shell on the server, in the browser.",
  files: "Browse, edit, upload and archive the server's files.",
  git: "Working copies, branches, commits and pull requests.",
  network: "Interfaces, routes, firewall, gateway, VPN and DNS.",
  proxy: "Sites, certificates, streams and ports in front of your apps.",
  security: "SSH, intrusion prevention and sign-ins to the server.",
  backups: "Scheduled backups of files, volumes and databases, and restores.",
  packages: "System packages and their updates.",
  "system-users": "Linux accounts and their SSH keys.",
  audit: "Every change made through the dashboard, and by whom.",
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
    hint: "Deploy from Git, an image or a template, with domains, databases and backups.",
    sections: ["deploy", "docker", "proxy", "databases", "git", "backups"],
  },
  {
    key: "databases",
    title: "Run databases",
    hint: "Start, connect, query and back up PostgreSQL, MySQL, Redis, MongoDB and more.",
    sections: ["databases", "backups", "docker"],
  },
  {
    key: "containers",
    title: "Manage containers",
    hint: "Run and inspect containers and compose stacks, with their files and a shell.",
    sections: ["docker", "files", "terminal"],
  },
  {
    key: "network",
    title: "Route the network",
    hint: "Use the server as a router or gateway: interfaces, firewall, VPN, DNS and the proxy.",
    sections: ["network", "proxy", "security"],
  },
  {
    key: "secure",
    title: "Secure and audit",
    hint: "Harden SSH, stop intrusions, keep packages current and see every change.",
    sections: ["security", "network", "packages", "audit", "backups", "system-users"],
  },
  {
    key: "workspace",
    title: "Work on the server",
    hint: "A terminal, the files, Git, and boards for notes and drawings.",
    sections: ["terminal", "files", "git", "excalidraw", "stickyboard"],
  },
] as const satisfies readonly {
  key: string
  title: string
  hint: string
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
