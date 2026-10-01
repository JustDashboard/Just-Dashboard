"use client"

import { Pane } from "@/components/panel"
import { EmptyState } from "@/components/state"
import type { SectionId } from "@/components/database/engine"
import { SectionFrame } from "@/components/database/kit/section"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * What stands in a database's page until its area is built: the page's real
 * frame — a workbench held to the window, a reading page that scrolls — with
 * one block saying what goes here and which directory builds it.
 *
 * It exists so the section builds and navigates end to end while the areas
 * are written side by side. An area replaces its stub's body and stops
 * importing this; the file goes with the last of them.
 */
export function AreaStub({
  section,
  area,
  children,
}: {
  section: SectionId
  /** The directory under `components/database/` that builds the page. */
  area: string
  /** What the page will hold, in a sentence. */
  children: React.ReactNode
}) {
  const { engine } = useDatabase()
  const page = engine.section(section)
  const Icon = page?.icon
  const block = (
    <EmptyState
      icon={Icon}
      title={`${engine.label} ${page?.title.toLowerCase() ?? section}`}
      description={
        <>
          {children} <span className="font-mono text-hint">components/database/{area}</span>
        </>
      }
      className={page?.workbench ? "border-0" : undefined}
    />
  )
  return (
    <SectionFrame section={section}>
      {page?.workbench ? (
        // One pane, the size of the window that is left: the frame every
        // workbench here fills.
        <Pane className="min-h-0 flex-1 items-center justify-center">{block}</Pane>
      ) : (
        block
      )}
    </SectionFrame>
  )
}
