"use client"

import type { EngineKind, SectionId } from "@/components/database/engine"
import { BlockedState } from "@/components/database/kit/blocked-state"
import { SectionFrame } from "@/components/database/kit/section"
import { useDatabase } from "@/components/database/shell/database-context"

type Area = React.ComponentType

/**
 * One of a database's pages: the component its kind of engine has for the
 * section, or the statement that it has none.
 *
 * Every `page.tsx` under `/databases/[id]` is this and a list of components,
 * so the choice is made in one place and made before anything mounts. A
 * section the engine lacks is not in the rail, and its address — typed, or
 * left in a tab from another database — says so in the engine's own terms
 * without a request being sent: a SQL page is never mounted over a key–value
 * store to find out from the error.
 */
export function SectionPage({
  section,
  any,
  ...byKind
}: {
  section: SectionId
  /** The component every kind of engine shares. A kind's own wins over it. */
  any?: Area
} & Partial<Record<EngineKind, Area>>) {
  const { engine, href } = useDatabase()
  const Component = engine.has(section) ? (byKind[engine.kind] ?? any) : undefined
  if (Component) return <Component />

  const { thing, reason } = engine.missing(section)
  const data = engine.section("data")
  return (
    <SectionFrame section={section}>
      <BlockedState
        engine={engine}
        thing={thing}
        href={href(data ? "data" : "home")}
        action={data ? `Open ${data.title}` : "Open Home"}
      >
        {reason}
      </BlockedState>
    </SectionFrame>
  )
}
