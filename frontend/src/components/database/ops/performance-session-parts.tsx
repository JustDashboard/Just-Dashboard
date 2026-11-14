"use client"

import { cn } from "@/lib/utils"
import { duration } from "@/lib/format"
import { DbClientMark } from "@/components/database/client-mark"
import { clientOf, type KnownClient } from "@/components/database/clients"
import { concernOf, stateSeconds, statusOf } from "@/components/database/ops/performance-activity"
import { Named, NoFigure } from "@/components/database/ops/performance-parts"
import type { DbActivity } from "@/components/database/ops/performance-types"

/**
 * How one session is drawn, for the Sessions view and for a session's own
 * page: its figure, the program it is, and who it is. They are one file so
 * that a session reads the same in the list, in the rail beside a session
 * and on the page.
 */

/** What a session's figure measures, said for the pointer and for a reader who cannot see the state beside it. */
export function figureWords(session: DbActivity): string {
  switch (statusOf(session)) {
    case "active":
      return "Its statement has been running for"
    case "blocked":
      return "It has been waiting for"
    case "idle_in_transaction":
      return "Its transaction has been open for"
    case "idle":
      return "It has been idle for"
    case "background":
      return ""
  }
}

export function StateFigure({ session }: { session: DbActivity }) {
  const seconds = stateSeconds(session)
  if (seconds === undefined) return <NoFigure />
  const concern = concernOf(session)
  return (
    <span
      title={`${figureWords(session)} ${duration(seconds)}`}
      className={cn(
        "numeric",
        concern === "long-blocked"
          ? "font-medium text-destructive"
          : concern
            ? "font-medium text-warning"
            : statusOf(session) === "idle" && "text-muted-foreground",
      )}
    >
      {seconds < 1 && seconds > 0 ? "<1s" : duration(seconds)}
    </span>
  )
}

/** The program a session is, as its logo: the deployment it names, or what its name says. */
export function ClientTile({
  session,
  known,
  size,
  tone,
}: {
  session: DbActivity
  known: ReadonlyMap<string, KnownClient>
  /** `md` is the identity line's mark; the row's tile otherwise. */
  size?: "md"
  /** What is wrong with the session, over its logo: only the identity line's mark takes one. */
  tone?: "warning" | "danger"
}) {
  const client = clientOf(session.application, known)
  return (
    <DbClientMark
      product={client.product}
      projectId={client.projectId}
      self={session.self}
      size={size ?? "tile"}
      tone={tone}
    />
  )
}

/** Who a session is: the account and the application, each in its own hue. */
export function Who({ session }: { session: DbActivity }) {
  return (
    <span className="flex min-w-0 items-center gap-1.5">
      {session.user ? <Named name={session.user} className="font-medium" /> : <NoFigure />}
      {session.application && (
        <>
          <span aria-hidden className="text-muted-foreground/60">
            ·
          </span>
          <Named name={session.application} />
        </>
      )}
    </span>
  )
}
