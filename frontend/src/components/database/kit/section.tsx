"use client"

import { cn } from "@/lib/utils"
import { Page, PageContext } from "@/components/page"
import { ErrorState } from "@/components/state"
import type { SectionId } from "@/components/database/engine"
import { useDatabase } from "@/components/database/shell/database-context"
import { HEADED } from "@/components/database/shell/header-rule"

/**
 * The gutters of a workbench: a grid or a canvas wants the window, so its
 * page takes the narrow inset the terminal and the file manager take, and the
 * strip above it lines up with the same edge.
 */
export const WORKBENCH_FRAME = "gap-2 px-2 py-2 md:px-3 md:py-3"

/**
 * The gutters of a page held to the window under the database's header (the
 * assistant, Logs): the page's own at the sides, so its frame's edges are the
 * header's, as a project's Logs stand under its header.
 */
const UNDER_HEADER_FRAME = "gap-2 px-5 pt-3 pb-2 md:px-8 md:pt-4 md:pb-3"

/**
 * The frame one of a database's pages renders into.
 *
 * It is `Page` with the two things every page here would otherwise decide for
 * itself. The name: an `h1` for assistive technology — "shop · Keys", in the
 * engine's own word — while the rail and the header say it to everybody else.
 * And the shape: the registry knows which pages are held to the window, so
 * such a page is `fill` and a reading page scrolls, and no page picks
 * differently from its neighbours. A workbench that heads itself takes the
 * narrow gutters; one that stands under the database's header takes the
 * page's.
 */
export function SectionFrame({
  section,
  part,
  actions,
  register,
  className,
  children,
}: {
  section: SectionId
  /** A page under the section's own, named after it: Settings' "Network". */
  part?: string
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
      className={cn(
        !workbench
          ? "animate-rise"
          : HEADED.includes(section)
            ? WORKBENCH_FRAME
            : UNDER_HEADER_FRAME,
        className,
      )}
    >
      <PageContext
        eyebrow="Databases"
        title={[conn.name, section !== "home" && page?.title, part].filter(Boolean).join(" · ")}
        actions={actions}
      />
      {children}
    </Page>
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
