"use client"

import { cn } from "@/lib/utils"
import { Page, PageContext } from "@/components/page"
import { Pane } from "@/components/panel"
import { ErrorState, LoadingPanel, LoadingRows } from "@/components/state"
import type { SectionId } from "@/components/database/engine"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The gutters of a workbench: a grid or a canvas wants the window, so its
 * page takes the narrow inset the terminal and the file manager take, and the
 * strip above it lines up with the same edge.
 */
export const WORKBENCH_FRAME = "gap-2 px-2 py-2 md:px-3 md:py-3"

/**
 * The frame one of a database's pages renders into.
 *
 * It is `Page` with the two things every page here would otherwise decide for
 * itself. The name: an `h1` for assistive technology — "shop · Keys", in the
 * engine's own word — while the rail and the strip say it to everybody else.
 * And the shape: the registry knows which pages are workbenches, so a
 * workbench is held to the window (`fill`) with the narrow gutters and a
 * reading page scrolls, and no page picks differently from its neighbours.
 */
export function SectionFrame({
  section,
  actions,
  register,
  className,
  children,
}: {
  section: SectionId
  /** Controls beside the page's name, for a reading page that has some. */
  actions?: React.ReactNode
  register?: "reading" | "flow"
  className?: string
  children: React.ReactNode
}) {
  const { conn, engine } = useDatabase()
  const page = engine.section(section)
  const workbench = Boolean(page?.workbench)
  return (
    <Page
      fill={workbench}
      register={register}
      className={cn(workbench ? WORKBENCH_FRAME : "animate-rise", className)}
    >
      <PageContext
        eyebrow="Databases"
        title={section === "home" || !page ? conn.name : `${conn.name} · ${page.title}`}
        actions={actions}
      />
      {children}
    </Page>
  )
}

/**
 * A page of a database whose first read has not landed, in the frame it will
 * have: a workbench waits as the pane it becomes, a reading page as the plain
 * blocks it becomes, and the name in the `h1` is already the page's own.
 */
export function SectionLoading({ section, rows = 6 }: { section: SectionId; rows?: number }) {
  const { engine } = useDatabase()
  return (
    <SectionFrame section={section}>
      {engine.section(section)?.workbench ? (
        <Pane className="min-h-0 flex-1">
          <LoadingRows rows={rows} className="p-3" />
        </Pane>
      ) : (
        <LoadingPanel plain rows={rows} />
      )}
    </SectionFrame>
  )
}

/**
 * A page of a database that could not be read, in the same frame: the error
 * where the content would be, under the page's own name, with the way to try
 * again when the server said it is worth one.
 */
export function SectionError({
  section,
  error,
  onRetry,
}: {
  section: SectionId
  error: Error
  onRetry?: () => void
}) {
  return (
    <SectionFrame section={section}>
      <ErrorState error={error} onRetry={onRetry} />
    </SectionFrame>
  )
}
