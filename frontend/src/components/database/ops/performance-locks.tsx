"use client"

import { useMemo } from "react"
import { CheckCircle, Cross, StopCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { duration, plural } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useColumnWidth } from "@/components/deploy/settings/use-column-width"
import { Disclosure, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  SESSION_STATES,
  THRESHOLDS,
  behind,
  lockTree,
  statusOf,
  stopsFor,
  waitingSessions,
  type LockNode,
} from "@/components/database/ops/performance-activity"
import { readLocks } from "@/components/database/ops/performance-api"
import {
  Named,
  NoFigure,
  NotAvailable,
  Notes,
  Stale,
  StatementLine,
  ViewRead,
  RETURNS_FOCUS,
} from "@/components/database/ops/performance-parts"
import { useStops, type Stops } from "@/components/database/ops/performance-stops"
import type {
  DbActivity,
  DbActivityResponse,
  DbHeldLock,
} from "@/components/database/ops/performance-types"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Who is holding whom up.
 *
 * The server lists one wait per pair — this session waits on that one — and
 * the list is drawn as the tree it describes: at the top a session that
 * holds others up and waits on nobody, under it the sessions waiting on it,
 * under those the ones waiting on them. The top of a tree is the session to
 * deal with, so it carries how long its transaction has been open, how many
 * stand behind it, and the two ways of stopping it.
 *
 * The statement printed beside a blocker is its last one, which is usually
 * not the one that took the lock: a session idle inside a transaction holds
 * what its earlier statements locked. The line says so.
 *
 * A wait names its two sessions by id and account only. What each is doing
 * and which application it belongs to is the session list's, which the page
 * already reads: on a pooled server every node of the tree is the same
 * account, and the application is what tells them apart. It is also what
 * says whether a blocker has a statement to cancel — the lock list's word for
 * a transaction that is merely open reads as "running" on some engines.
 *
 * The lock table itself — every lock held or asked for — is reference, and
 * folds under the tree.
 */
export function LocksView({ activity }: { activity: PollState<DbActivityResponse> }) {
  const { id } = useDatabase()
  const [frame, width] = useColumnWidth<HTMLDivElement>()
  const locks = usePoll((signal) => readLocks(id, signal), 5000, [id])
  const refreshSessions = activity.refresh
  const refreshLocks = locks.refresh
  const stops = useStops(() => {
    refreshLocks()
    refreshSessions()
  })
  const [tableOpen, setTableOpen] = useViewState(`databases.${id}.performance.locktable`, false)
  const waits = locks.data?.waits
  const tree = useMemo(() => lockTree(waits ?? []), [waits])
  const listed = activity.data?.supported ? activity.data.sessions : undefined
  const sessions = useMemo(
    () => new Map((listed ?? []).map((session) => [session.pid, session])),
    [listed],
  )

  return (
    <Panel plain aria-label="Locks" ref={frame} className="focus-ring" {...RETURNS_FOCUS}>
      {stops.dialog}
      <PanelHeader title="Who is waiting on whom" actions={<Stale poll={locks} />} />
      <ViewRead poll={locks} what="the locks" skeleton={<LoadingPanel plain rows={4} />}>
        {(data) => {
          if (!data.supported) {
            return (
              <div className="pt-4">
                <NotAvailable title="The locks cannot be read" reason={data.reason} />
              </div>
            )
          }
          const waiting = waitingSessions(data.waits)
          return (
            <PanelBody className="animate-rise space-y-5">
              {tree.length === 0 ? (
                <div className="flex items-center gap-2.5 text-body text-muted-foreground">
                  <CheckCircle className="size-4 shrink-0 text-success" />
                  <span className="min-w-0">Nothing is waiting on a lock.</span>
                </div>
              ) : (
                <>
                  <p className="text-body">
                    <span className="numeric font-medium">{plural(waiting, "session")}</span>{" "}
                    <span className="text-muted-foreground">
                      {waiting === 1 ? "is" : "are"} waiting behind{" "}
                      {tree.length === 1 ? "one other" : `${tree.length} others`}.
                    </span>
                  </p>
                  <ul aria-label="Blocking sessions" className="space-y-5">
                    {tree.map((node) => (
                      <li key={node.pid}>
                        <LockRow node={node} stops={stops} sessions={sessions} root />
                      </li>
                    ))}
                  </ul>
                </>
              )}

              <Notes notes={data.notes} />

              {data.locks.length > 0 && (
                <Disclosure
                  quiet
                  open={tableOpen}
                  onOpenChange={setTableOpen}
                  summary={<>The lock table · {data.locks.length.toLocaleString()} locks</>}
                  facts={`${data.locks.filter((lock) => !lock.granted).length} not granted`}
                >
                  {tableOpen && (
                    <LockTable locks={data.locks} truncated={data.truncated} width={width} />
                  )}
                </Disclosure>
              )}
            </PanelBody>
          )
        }}
      </ViewRead>
    </Panel>
  )
}

/** One session of the tree, and under it the sessions waiting on it. */
function LockRow({
  node,
  stops,
  sessions,
  root,
}: {
  node: LockNode
  stops: Stops
  /** The session list by id, where the page could read it. */
  sessions: ReadonlyMap<string, DbActivity>
  root?: boolean
}) {
  const waiters = behind(node)
  const wait = node.wait
  const session = sessions.get(node.pid)
  // The server marks a blocker that is not a session — a prepared
  // transaction, a session it no longer lists — by naming no account for it.
  const phantom = root && !node.user && !session
  // Whether there is a statement to cancel. The session list knows; without
  // it, a waiter is running the statement that waits, and a blocker only if
  // the lock list gave it a statement and does not call it idle.
  const offered = phantom
    ? { cancel: false, terminate: false }
    : session
      ? stopsFor(session, stops.can)
      : {
          cancel:
            stops.can.cancel &&
            (!root || (Boolean(node.query) && !/idle|sleep/i.test(node.state ?? ""))),
          terminate: stops.can.kill,
        }
  const running = session
    ? statusOf(session) === "active" || statusOf(session) === "blocked"
    : !root || (Boolean(node.query) && !/idle|sleep/i.test(node.state ?? ""))
  const state = session
    ? SESSION_STATES.find((entry) => entry.id === statusOf(session))?.label.toLowerCase()
    : node.state
  const user = node.user ?? session?.user
  const late = !root && (node.seconds ?? 0) >= THRESHOLDS.blocked
  const subject = {
    pid: node.pid,
    user,
    application: session?.application,
    query: node.query,
  }
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-start gap-3">
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-body">
            <Status
              tone={root ? "warning" : late ? "danger" : "notice"}
              label={
                <>
                  Session <span className="font-mono">{node.pid}</span>
                </>
              }
            />
            {user && <Named name={user} className="text-xs" />}
            {session?.application && (
              <>
                <span aria-hidden className="text-muted-foreground/60">
                  ·
                </span>
                <Named name={session.application} className="text-xs" />
              </>
            )}
            {/* A waiter's state is the row itself; a blocker's is the news. */}
            {root && state && <span className="text-hint text-muted-foreground">{state}</span>}
            {node.seconds !== undefined && (
              <span
                className={cn(
                  "numeric text-xs",
                  late ? "font-medium text-destructive" : "text-muted-foreground",
                )}
              >
                {root ? "transaction open " : "waiting "}
                {duration(node.seconds)}
              </span>
            )}
            {waiters > 0 && (
              <Tag tone={waiters > THRESHOLDS.blocking ? "danger" : "warning"}>
                blocking {waiters.toLocaleString()}
              </Tag>
            )}
            {node.cycle && <Tag tone="danger">waits in a ring</Tag>}
          </div>
          {wait && (wait.object || wait.mode || wait.lockType) && (
            <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-hint text-muted-foreground">
              wants
              {wait.mode && <Tag mono>{wait.mode}</Tag>}
              {wait.lockType && <span>({wait.lockType})</span>}
              {wait.object && (
                <>
                  on
                  <Tag mono className="max-w-full truncate">
                    {wait.object}
                  </Tag>
                </>
              )}
            </p>
          )}
          {node.query ? (
            <StatementLine text={node.query} className="text-hint text-foreground/80" />
          ) : phantom ? (
            <p className="text-hint text-muted-foreground">
              Not a session: a prepared transaction, or one the server no longer lists.
            </p>
          ) : (
            root && (
              <p className="text-hint text-muted-foreground">
                No statement is running: the lock is held by its open transaction.
              </p>
            )
          )}
          {root && node.query && !running && (
            <p className="text-hint text-muted-foreground">
              Its last statement. The lock is held by its open transaction, which may have taken it
              earlier.
            </p>
          )}
        </div>
        <span className="flex shrink-0 items-center gap-0.5">
          {offered.cancel && (
            <IconAction
              label={`Cancel the statement of session ${node.pid}`}
              onClick={() => stops.cancel(subject)}
            >
              <StopCircle />
            </IconAction>
          )}
          {offered.terminate && (
            <IconAction
              label={`Terminate session ${node.pid}`}
              onClick={() => stops.terminate(subject)}
            >
              <Cross />
            </IconAction>
          )}
        </span>
      </div>
      {node.waiters.length > 0 && (
        // The rule down the left is the queue: everything behind it waits on
        // the session above.
        <ul className="mt-2.5 ml-[2.5px] space-y-3 border-l border-border-strong pl-4">
          {node.waiters.map((waiter) => (
            <li key={`${waiter.pid}:${waiter.wait?.blockingPid}`}>
              <LockRow node={waiter} stops={stops} sessions={sessions} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * Every lock held or asked for. A table where the view has the width for
 * one, with the statement beside each lock where it has more; under that the
 * locks are drawn down, since a lock's mode and what it is on are both names
 * that do not shorten.
 */
function LockTable({
  locks,
  truncated,
  width,
}: {
  locks: DbHeldLock[]
  truncated: boolean
  /** The view's own width. */
  width: number
}) {
  const statements = width >= 820
  const held = (lock: DbHeldLock) => (
    <Status
      tone={lock.granted ? "running" : "warning"}
      label={lock.granted ? "Granted" : "Waiting"}
    />
  )
  const mode = (lock: DbHeldLock) => (
    <span className="flex min-w-0 items-center gap-1.5">
      {lock.mode ? (
        <Tag mono className="truncate">
          {lock.mode}
        </Tag>
      ) : (
        <NoFigure />
      )}
      {lock.lockType && (
        <span className="truncate text-hint text-muted-foreground">{lock.lockType}</span>
      )}
    </span>
  )
  return (
    <div className="-mx-4 space-y-2">
      {width < 560 ? (
        <ul className="max-h-[28rem] divide-y divide-hairline overflow-y-auto border-y border-hairline">
          {locks.map((lock, index) => (
            <li
              key={`${lock.pid}:${lock.object}:${lock.mode}:${index}`}
              className="space-y-1 px-4 py-2 text-xs"
            >
              <div className="flex min-w-0 items-center justify-between gap-3">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="font-mono">{lock.pid}</span>
                  {lock.user && <Named name={lock.user} />}
                </span>
                {held(lock)}
              </div>
              {mode(lock)}
              {lock.object && (
                <p
                  className="truncate font-mono text-hint text-muted-foreground"
                  title={lock.object}
                >
                  on {lock.object}
                </p>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <Table containerClassName="max-h-[28rem]" className="table-fixed">
          <colgroup>
            <col className="w-36" />
            <col className="w-56" />
            <col />
            <col className="w-24" />
            {statements && <col className="w-[34%]" />}
          </colgroup>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Session</TableHead>
              <TableHead className="px-2">Lock</TableHead>
              <TableHead className="px-2">On</TableHead>
              <TableHead className="px-2">Held</TableHead>
              {statements && <TableHead>Statement</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {locks.map((lock, index) => (
              <TableRow key={`${lock.pid}:${lock.object}:${lock.mode}:${index}`}>
                <TableCell className="py-2">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="font-mono">{lock.pid}</span>
                    {lock.user && <Named name={lock.user} />}
                  </span>
                </TableCell>
                <TableCell className="px-2 py-2">{mode(lock)}</TableCell>
                <TableCell className="truncate px-2 py-2 font-mono" title={lock.object}>
                  {lock.object || <NoFigure />}
                </TableCell>
                <TableCell className="px-2 py-2">{held(lock)}</TableCell>
                {statements && (
                  <TableCell className="py-2">
                    {lock.query ? <StatementLine text={lock.query} /> : <NoFigure />}
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {truncated && (
        <FormNote className="px-4">
          The server listed more locks than these; the first 500 are shown.
        </FormNote>
      )}
    </div>
  )
}
