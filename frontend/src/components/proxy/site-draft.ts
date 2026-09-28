import type { DroppedLine, SiteSpec } from "@/lib/types"
import { unifiedDiff } from "@/components/files/diff"
import { BLANK } from "@/components/proxy/site-presets"
import { sendableSpec } from "@/components/proxy/site-save"

/**
 * A site form's draft, and what it is a draft of.
 *
 * The form keeps what is typed for the tab, so a look at a certificate or a
 * port half-way through does not cost the edit. That is only safe while the
 * file on disk is still the one the draft started from: `base` is that file's
 * digest and the spec the form read from it — or, for a new site, the spec
 * the form opened with — which is also what "unsaved changes" is measured
 * against.
 */
export type DraftBase = { digest: string; spec: SiteSpec }

/**
 * A site read back from its file, as the form edits it: the new-site
 * defaults under what the file says, except the two a file can leave out
 * on purpose. The server leaves an unset upload limit and timeout out of
 * the spec, and BLANK's 50m and 60 seconds filled them in, so opening a site
 * without either and pressing Save wrote both into it. A plain-HTTP site
 * reads back with HSTS off, since nothing sends it there; turning TLS on
 * offers it on, as for a new site.
 */
export function specFromServer(read: SiteSpec): SiteSpec {
  return {
    ...BLANK,
    clientMaxBody: undefined,
    proxyTimeout: undefined,
    ...read,
    hsts: read.tls ? read.hsts : BLANK.hsts,
  }
}

/**
 * Whether two specs save the same file. Compared as sent, with a key left
 * out, set to nothing, false or empty counted as the same, since the form
 * sets a switch to false where the file it read never mentioned it.
 */
export function sameSpec(a: SiteSpec, b: SiteSpec): boolean {
  return canonical(sendableSpec(a)) === canonical(sendableSpec(b))
}

function canonical(value: unknown): string {
  return JSON.stringify(value, (_key, v: unknown) => {
    if (v === null || v === false || v === "" || v === 0) return undefined
    if (Array.isArray(v)) return v.length === 0 ? undefined : v
    if (typeof v === "object") {
      const sorted: Record<string, unknown> = {}
      for (const key of Object.keys(v as object).sort()) {
        sorted[key] = (v as Record<string, unknown>)[key]
      }
      return sorted
    }
    return v
  })
}

/**
 * What to do with a draft kept for the tab once the file is read again:
 * keep it while the file is the version it started from, take the file when
 * there is no draft or nothing in it was changed, and otherwise ask — the
 * file changed under an edit.
 */
export function draftFate(
  base: DraftBase | null,
  spec: SiteSpec,
  digest: string,
): "keep" | "take" | "stale" {
  if (!base) return "take"
  if (base.digest === digest) return "keep"
  return sameSpec(spec, base.spec) ? "take" : "stale"
}

/**
 * The lines a save drops, drawn as a diff: each removed at its own line of
 * the file, under where it sits, with neighbours in the same block drawn as
 * one run.
 */
export function droppedDiff(dropped: DroppedLine[]): string {
  const hunks: { start: number; context: string; rows: string[] }[] = []
  let end = 0
  for (const d of dropped) {
    const rows = d.text.split("\n").map((line) => `-${line}`)
    const last = hunks.at(-1)
    if (last && last.context === d.context && d.line === end) last.rows.push(...rows)
    else hunks.push({ start: d.line, context: d.context, rows })
    end = d.line + d.lines
  }
  return hunks
    .flatMap((h) => [`@@ -${h.start},${h.rows.length} +${h.start},0 @@ ${h.context}`, ...h.rows])
    .join("\n")
}

/** How many lines of the file the dropped statements take. */
export function droppedCount(dropped: DroppedLine[]): number {
  return dropped.reduce((n, d) => n + d.lines, 0)
}

/**
 * The dropped lines that can still move into the extra configuration: the
 * movable ones not in it already, so a second press — before the preview
 * catches up — does not add them twice.
 */
export function movableLines(dropped: DroppedLine[], custom: string | undefined): DroppedLine[] {
  return dropped.filter((d) => d.movable && !(custom ?? "").includes(d.text))
}

/** The extra configuration with the movable lines added at its end. */
export function withMovedLines(custom: string | undefined, dropped: DroppedLine[]): string {
  const current = (custom ?? "").replace(/\s+$/, "")
  const moving = movableLines(dropped, custom).map((d) => d.text)
  return [current, ...moving].filter((part) => part !== "").join("\n")
}

/**
 * What saving writes over the file, as a diff `DiffView` reads: "" when the
 * save writes the file as it is, and null when the two are too far apart to
 * line up. The `diff --git` line makes the ---/+++ lines a header, which
 * `DiffView` leaves out, rather than a removed and an added line.
 */
export function changesDiff(disk: string, next: string, name: string): string | null {
  const diff = unifiedDiff(disk, next, name)
  if (!diff) return diff
  return `diff --git a/${name} b/${name}\n${diff}`
}

/** How many lines a diff from `changesDiff` adds and removes. */
export function changeCount(diff: string | null): number {
  if (!diff) return 0
  const body = diff.split("\n")
  const start = body.findIndex((line) => line.startsWith("@@"))
  if (start < 0) return 0
  return body.slice(start).filter((line) => line.startsWith("+") || line.startsWith("-")).length
}
