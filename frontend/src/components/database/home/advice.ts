import type { SectionId, SectionParams } from "@/components/database/engine"
import type { DbAdvice } from "@/components/database/home/types"
import { databasePlace } from "@/components/database/shell/routes"

/** Setup preferences and optional instrumentation are not faults in a database. */
export function needsDatabaseAttention(advice: Pick<DbAdvice, "level">) {
  return advice.level === "critical" || advice.level === "warning"
}

export function databaseAdviceTitle(advice: Pick<DbAdvice, "id" | "title">) {
  return advice.id === "no-backup" ? "No dashboard backup recorded" : advice.title
}

export type AdviceDestination = {
  section: SectionId
  params?: SectionParams
  label: string
}

/** A finding opens the control or object it names, within this connection only. */
export function adviceDestination(
  advice: DbAdvice,
  tools: { id: number; has: (section: SectionId) => boolean; canQuery: boolean },
): AdviceDestination | undefined {
  if (advice.link && (!advice.link.startsWith("/") || advice.link.startsWith("//")))
    return undefined
  const link = advice.link ? new URL(advice.link, "http://database.invalid") : undefined
  const place = link ? databasePlace(link.pathname) : null
  if (place && place.id !== tools.id) return undefined
  if (place && tools.has(place.section)) {
    const target = advice.targets?.[0]
    const params: SectionParams = Object.fromEntries(link!.searchParams)
    if (place.section === "performance" && !params.view) {
      const views: Record<string, string> = {
        slot: "replication",
        index: "indexes",
        table: "tables",
        extension: "statements",
      }
      if (target && views[target.kind]) params.view = views[target.kind]
    }
    if (place.section === "settings" && target?.kind === "setting") params.q = target.name
    return {
      section: place.section,
      params,
      label: `Open ${place.section === "performance" && params.view ? params.view : place.section[0].toUpperCase() + place.section.slice(1)}`,
    }
  }
  if (advice.sql && tools.canQuery && tools.has("query")) {
    return { section: "query", params: { sql: advice.sql }, label: "Review the fix in Query" }
  }
  const target = advice.targets?.[0]
  if (target && ["table", "index", "sequence"].includes(target.kind) && tools.has("schema")) {
    return {
      section: "schema",
      params: { schema: target.schema ?? "", table: target.table ?? target.name },
      label: "Inspect schema",
    }
  }
  if (target?.kind === "role" && tools.has("access")) {
    return { section: "access", label: "Review access" }
  }
  return undefined
}
