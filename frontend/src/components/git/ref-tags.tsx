"use client"

import { Tag } from "@/components/tag"

type Ref = { label: string; kind: "head" | "tag" | "branch" }

// git's %D reads "HEAD -> main, origin/main, tag: v1.0". The arrow marks the
// checked-out branch; "tag:" marks a tag; everything else is a branch tip.
export function parseRefs(refs?: string): Ref[] {
  if (!refs) return []
  const out: Ref[] = []
  for (const raw of refs.split(", ")) {
    const entry = raw.trim()
    if (!entry) continue
    // origin/HEAD rides along with origin/main on the same commit — a symref,
    // not a branch worth its own mark.
    if (entry.endsWith("/HEAD")) continue
    if (entry.startsWith("HEAD -> ")) {
      out.unshift({ label: entry.slice("HEAD -> ".length), kind: "head" })
    } else if (entry === "HEAD") {
      out.unshift({ label: "HEAD", kind: "head" })
    } else if (entry.startsWith("tag: ")) {
      out.push({ label: entry.slice("tag: ".length), kind: "tag" })
    } else {
      out.push({ label: entry, kind: "branch" })
    }
  }
  return out
}

/**
 * The branch and tag names sitting on a commit, as `mono` tags — literal
 * strings from the repository, drawn on the quiet recessed ground that keeps
 * three of them in a row apart. The checked-out branch is green and a tag
 * amber, which is colour as a reading of what the name is, not decoration.
 */
export function RefTags({
  refs,
  className,
  max,
}: {
  refs?: string
  className?: string
  /** Names past this many are counted rather than drawn, so a row keeps room for its subject. */
  max?: number
}) {
  const parsed = parseRefs(refs)
  if (parsed.length === 0) return null
  const shown = max === undefined ? parsed : parsed.slice(0, max)
  const rest = parsed.slice(shown.length)
  return (
    <span className={className ?? "flex min-w-0 flex-wrap items-center gap-1"}>
      {shown.map((r) => (
        <Tag
          key={r.label}
          mono
          tone={r.kind === "head" ? "success" : r.kind === "tag" ? "warning" : "default"}
          className="max-w-[12rem] truncate"
          title={r.kind === "tag" ? `tag ${r.label}` : r.label}
        >
          {r.label}
        </Tag>
      ))}
      {rest.length > 0 && (
        <span
          className="numeric text-hint text-muted-foreground"
          title={rest.map((r) => r.label).join(", ")}
        >
          +{rest.length}
        </span>
      )}
    </span>
  )
}
