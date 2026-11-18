"use client"

import { cn } from "@/lib/utils"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Stale } from "@/components/stale"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * One block of a database's home: a title, a hairline, what was read.
 *
 * Every block on the page is plain (§2) — the gap between two of them is what
 * separates them — and every one answers the same three questions the same
 * way: not read yet is the silhouette of what is coming, could not be read
 * says so with a way to try again, and a poll that fails over something
 * already shown leaves it on screen and says only that it has stopped
 * updating.
 */
export function Block({
  title,
  count,
  link,
  actions,
  aside,
  stale,
  className,
  bodyClassName,
  children,
}: {
  title: string
  /**
   * The fact or count on the title's line: what the figures are of ("share
   * of runtime since …"), how many there are. `PanelHeader`'s `count`, so it
   * is drawn one way and stays at every width.
   */
  count?: React.ReactNode
  /** The way on to the page that holds all of what the block shows the top of. */
  link?: { href: string; label: string }
  actions?: React.ReactNode
  /** Drawn on the head's line between the title and the actions (`PanelHeader`'s children). */
  aside?: React.ReactNode
  /** The last poll failed over what is drawn: it is the reading before, not now. */
  stale?: Error
  className?: string
  bodyClassName?: string
  children: React.ReactNode
}) {
  return (
    <Panel plain aria-label={title} className={className}>
      <PanelHeader
        title={title}
        count={count}
        link={link}
        actions={
          (stale || actions) && (
            <>
              <Stale error={stale} />
              {actions}
            </>
          )
        }
      >
        {aside}
      </PanelHeader>
      <PanelBody className={bodyClassName}>{children}</PanelBody>
    </Panel>
  )
}

function Bone({ width }: { width: string }) {
  return <Skeleton className="inline-block h-[0.7em] max-w-full align-middle" style={{ width }} />
}

/** A list of facts that has not arrived: names down one side, values beside them, a rule under each. */
export function FactsSkeleton({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn("flex flex-col", className)} aria-hidden>
      {Array.from({ length: rows }).map((_, index) => (
        <div
          key={index}
          className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-x-4 border-t border-hairline py-2 text-body first:border-t-0"
        >
          {/* Bones inline in a line of the list's own size, so a row waits
              at the height of the `Detail` that replaces it. */}
          <span>
            <Bone width={`${70 - ((index * 13) % 30)}%`} />
          </span>
          <span>
            <Bone width={`${64 - ((index * 19) % 38)}%`} />
          </span>
        </div>
      ))}
    </div>
  )
}
