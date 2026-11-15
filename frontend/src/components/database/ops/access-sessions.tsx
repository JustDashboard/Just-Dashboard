"use client"

import { useEffect, useMemo } from "react"
import { cn } from "@/lib/utils"
import { duration } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote } from "@/components/state"
import { DbClientMark } from "@/components/database/client-mark"
import { clientOf } from "@/components/database/clients"
import {
  BlockLink,
  CardsSkeleton,
  NotUpdating,
  Read,
  staleOf,
} from "@/components/database/home/blocks"
import {
  concernOf,
  orderSessions,
  stateSeconds,
  statusOf,
} from "@/components/database/ops/performance-activity"
import { readActivity } from "@/components/database/ops/performance-api"
import {
  NotAvailable,
  SessionState,
  StatementLine,
} from "@/components/database/ops/performance-parts"
import type { DbActivity } from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"
import { useKnownClients } from "@/components/database/use-known-clients"

/** How often the sessions are read while the view is on screen: Performance's own pace. */
const SESSIONS_POLL = 5000

/**
 * The sessions one account holds now: what Performance lists for the whole
 * server, narrowed to this account.
 *
 * A session is one line — the program it is, what it is running or the state
 * it is in, how long it has been at it — and opening one goes to Performance,
 * where its panel is and where a statement is cancelled or a session ended.
 * Nothing is stopped from here: the account's page reads who is signed in as
 * it, and the page that owns the sessions acts on them.
 */
export function AccountSessions({
  name,
  onCount,
}: {
  name: string
  onCount?: (n: number) => void
}) {
  const { id, href } = useDatabase()
  const known = useKnownClients()
  const activity = usePoll((signal) => readActivity(id, signal), SESSIONS_POLL, [id])
  const own = useMemo(
    () =>
      orderSessions(
        (activity.data?.sessions ?? []).filter(
          (session) => (session.user ?? "").toLowerCase() === name.toLowerCase(),
        ),
      ),
    [activity.data, name],
  )
  const count = activity.data?.supported ? own.length : undefined
  // The strip's count is the list's own once it has been read.
  useEffect(() => {
    if (count !== undefined) onCount?.(count)
  }, [count, onCount])

  return (
    <Panel plain aria-label="Sessions">
      <PanelHeader
        title="Sessions"
        actions={
          <>
            {staleOf(activity) && <NotUpdating error={staleOf(activity)!} />}
            {count !== undefined && count > 0 && (
              <span className="numeric text-hint text-muted-foreground">{count}</span>
            )}
            <BlockLink href={href("performance", { view: "sessions" })}>Every session</BlockLink>
          </>
        }
      />
      <PanelBody>
        <Read poll={activity} what="the sessions" skeleton={<CardsSkeleton count={2} />}>
          {(data) =>
            !data.supported ? (
              <NotAvailable title="The sessions cannot be listed" reason={data.reason} />
            ) : own.length === 0 ? (
              <EmptyNote>Nothing is signed in as {name} now.</EmptyNote>
            ) : (
              <ChoiceList>
                {own.map((session) => (
                  <SessionRow
                    key={session.pid}
                    session={session}
                    product={clientOf(session.application, known)}
                    href={href("performance", { view: "sessions", session: session.pid })}
                  />
                ))}
              </ChoiceList>
            )
          }
        </Read>
      </PanelBody>
    </Panel>
  )
}

function SessionRow({
  session,
  product,
  href,
}: {
  session: DbActivity
  product: { product?: string; projectId?: number }
  href: string
}) {
  const status = statusOf(session)
  const seconds = stateSeconds(session)
  const concern = concernOf(session)
  const where = [session.database, session.client].filter(Boolean).join(" · ")
  return (
    <ChoiceRow
      href={href}
      verb={`Open session ${session.pid} in Performance`}
      leading={
        <DbClientMark product={product.product} projectId={product.projectId} self={session.self} />
      }
      title={
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate">{session.application || "Unnamed client"}</span>
          <span className="shrink-0 font-mono text-hint font-normal text-muted-foreground">
            {session.pid}
          </span>
        </span>
      }
      description={
        session.query && status !== "idle" ? (
          <StatementLine text={session.query} />
        ) : (
          where || "no statement running"
        )
      }
      trailing={
        <span className="flex items-center gap-3">
          <SessionState status={status} late={Boolean(concern)} />
          <span
            className={cn(
              "numeric w-12 text-right text-xs max-sm:hidden",
              concern === "long-blocked"
                ? "font-medium text-destructive"
                : concern
                  ? "font-medium text-warning"
                  : "text-muted-foreground",
            )}
          >
            {seconds === undefined ? "" : seconds < 1 && seconds > 0 ? "<1s" : duration(seconds)}
          </span>
        </span>
      }
    />
  )
}
