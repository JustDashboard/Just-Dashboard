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
