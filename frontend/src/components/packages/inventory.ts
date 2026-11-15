import { packageProduct } from "@/components/packages/marks"
import type { InstalledPackage } from "@/lib/types"

const NAMES: Record<string, string> = {
  postgresql: "PostgreSQL",
  nodejs: "Node.js",
  python: "Python",
  linux: "Linux",
  docker: "Docker",
  "docker-compose": "Docker Compose",
  curl: "curl",
  nginx: "nginx",
  php: "PHP",
  go: "Go",
  java: "Java",
  rust: "Rust",
  git: "Git",
  redis: "Redis",
  libs: "Libraries",
  libdevel: "Development libraries",
  oldlibs: "Legacy libraries",
  devel: "Development tools",
  admin: "System tools",
  utils: "Utilities",
  net: "Networking",
  kernel: "Kernel",
  doc: "Documentation",
  misc: "Other software",
}

/** A product's packages stay together; unnamed libraries keep the archive's section. */
export function softwareKey(p: Pick<InstalledPackage, "name" | "section">) {
  const product = packageProduct(p.name)
  return product ? `product:${product}` : `section:${p.section?.split("/").pop() || "misc"}`
}

export function softwareName(key: string) {
  const bare = key.slice(key.indexOf(":") + 1)
  return (
    (Object.hasOwn(NAMES, bare) ? NAMES[bare] : undefined) ??
    bare.replace(
      /(^|[- ])([a-z])/g,
      (_, space, letter) => `${space ? " " : ""}${letter.toUpperCase()}`,
    )
  )
}

export function softwareGroups(packages: InstalledPackage[]) {
  const groups = new Map<
    string,
    { key: string; name: string; sample: InstalledPackage; size: number; count: number }
  >()
  for (const p of packages) {
    const key = softwareKey(p)
    const group = groups.get(key) ?? { key, name: softwareName(key), sample: p, size: 0, count: 0 }
    group.count++
    group.size += Math.max(p.size ?? 0, 0)
    groups.set(key, group)
  }
  return [...groups.values()].sort((a, b) => b.size - a.size || a.name.localeCompare(b.name))
}

/** How the installed list is narrowed; the list page and a package's rail share it. */
export type PackageScope = "explicit" | "all" | "dependencies" | "upgradable" | "security"

/** Where the list's filter is kept, so the rail beside a package reads the one the list was left on. */
export const PACKAGE_FILTER_KEYS = {
  query: "packages.query",
  scope: "packages.scope",
  bySize: "packages.by-size",
  software: "packages.software",
} as const

const SCOPES = new Set<string>(["explicit", "all", "dependencies", "upgradable", "security"])

/**
 * The scope in force. A manager that cannot tell what was installed on
 * purpose reports none, and a default that hides everything is worse than a
 * long list: zypper is the one that cannot; apt, dnf, pacman and apk all can.
 */
export function packageScope(remembered: string, explicitCount: number): PackageScope {
  const scope = (SCOPES.has(remembered) ? remembered : "explicit") as PackageScope
  return scope === "explicit" && explicitCount <= 0 ? "all" : scope
}

export const SCOPE_WORDS: Record<PackageScope, string> = {
  explicit: "installed by hand",
  all: "",
  dependencies: "dependencies",
  upgradable: "behind",
  security: "security",
}

/** The installed packages a filter leaves, in the list's order: by name as read, or largest first. */
export function filterPackages(
  packages: InstalledPackage[],
  filter: { query: string; scope: PackageScope; software: string; bySize: boolean },
): InstalledPackage[] {
  const needle = filter.query.trim().toLowerCase()
  const matched = packages.filter((p) => {
    if (filter.software && softwareKey(p) !== filter.software) return false
    if (filter.scope === "dependencies" && p.explicit) return false
    if (filter.scope === "security" && (!p.upgradable || !p.security)) return false
    if (filter.scope === "explicit" && !p.explicit) return false
    if (filter.scope === "upgradable" && !p.upgradable) return false
    if (!needle) return true
    return (
      p.name.toLowerCase().includes(needle) ||
      (p.summary ?? "").toLowerCase().includes(needle) ||
      (p.section ?? "").toLowerCase().includes(needle)
    )
  })
  return filter.bySize ? [...matched].sort((a, b) => (b.size ?? 0) - (a.size ?? 0)) : matched
}

/** The views of a package's page, as its address names them. */
export const PACKAGE_TABS = ["overview", "files", "manual", "depends", "log"] as const
export type PackageTab = (typeof PACKAGE_TABS)[number]

/**
 * A package's own page. The name is one path segment whatever it holds:
 * `g++` and `libstdc++6` carry a plus, an apt name may carry `:amd64`, and a
 * bare plus in a path is read as a space by more than one thing between here
 * and the server.
 */
export function packageHref(name: string, tab?: string) {
  const path = `/packages/${encodeURIComponent(name)}`
  return tab && tab !== "overview" ? `${path}?tab=${encodeURIComponent(tab)}` : path
}

/** The route's segment as a package name; a name cannot hold `%`, so decoding twice is harmless. */
export function packageName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/**
 * A dependency as the manager prints it, `libc6 (>= 2.34)` or
 * `debconf (>= 0.5) | debconf-2.0`, as the package to open and what is asked
 * of it.
 */
export function dependency(text: string): { name: string; wants: string } {
  const name = text.trim().split(/[ (|<>=]/)[0]
  return { name, wants: text.trim().slice(name.length).trim() }
}

/**
 * The same package in two spellings: apt lists `libc6:amd64` where a
 * dependency says `libc6`.
 */
export function samePackage(a: string, b: string) {
  return a.split(":")[0] === b.split(":")[0]
}
