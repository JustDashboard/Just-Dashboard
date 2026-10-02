"use client"

import { useMemo } from "react"
import { CheckCircle, Cross, StopCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { duration, plural } from "@/lib/format"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
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
  THRESHOLDS,
  behind,
  lockTree,
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
} from "@/components/database/ops/performance-parts"
import { useStops, type Stops } from "@/components/database/ops/performance-stops"
import type { DbHeldLock } from "@/components/database/ops/performance-types"
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
 * The lock table itself — every lock held or asked for — is reference, and
 * folds under the tree.
 */
export function LocksView() {
  const { id } = useDatabase()
  const locks = usePoll((signal) => readLocks(id, signal), 5000, [id])
  const stops = useStops(locks.refresh)
  const [tableOpen, setTableOpen] = useViewState(`databases.${id}.performance.locktable`, false)
  const waits = locks.data?.waits
  const tree = useMemo(() => lockTree(waits ?? []), [waits])

  return (
    <Panel plain aria-label="Locks">
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
                        <LockRow node={node} stops={stops} root />
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
                  {tableOpen && <LockTable locks={data.locks} truncated={data.truncated} />}
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
function LockRow({ node, stops, root }: { node: LockNode; stops: Stops; root?: boolean }) {
  const waiters = behind(node)
  const wait = node.wait
  // A session that is idle has no statement to cancel: only ending it lets go.
  const running = root ? !/idle/i.test(node.state ?? "") : true
  const late = !root && (node.seconds ?? 0) >= THRESHOLDS.blocked
  const subject = { pid: node.pid, user: node.user, query: node.query }
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
            {node.user && <Named name={node.user} className="text-xs" />}
            {node.state && <span className="text-hint text-muted-foreground">{node.state}</span>}
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
          ) : (
            root && (
              <p className="text-hint text-muted-foreground">
                Not a session: a prepared transaction, or one the server no longer lists.
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
          {stops.can.cancel && running && (
            <IconAction
              label={`Cancel the statement of session ${node.pid}`}
              onClick={() => stops.cancel(subject)}
            >
              <StopCircle />
            </IconAction>
          )}
          {stops.can.kill && (
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
              <LockRow node={waiter} stops={stops} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function LockTable({ locks, truncated }: { locks: DbHeldLock[]; truncated: boolean }) {
  return (
    <div className="-mx-4 space-y-2">
      <Table containerClassName="max-h-[28rem]">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Session</TableHead>
            <TableHead className="px-2">Lock</TableHead>
            <TableHead className="px-2">On</TableHead>
            <TableHead className="px-2">Held</TableHead>
            <TableHead>Statement</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {locks.map((lock, index) => (
            <TableRow key={`${lock.pid}:${lock.object}:${lock.mode}:${index}`}>
              <TableCell className="py-2">
                <span className="flex items-center gap-2">
                  <span className="font-mono">{lock.pid}</span>
                  {lock.user && <Named name={lock.user} />}
                </span>
              </TableCell>
              <TableCell className="px-2 py-2">
                <span className="flex items-center gap-1.5">
                  {lock.mode ? <Tag mono>{lock.mode}</Tag> : <NoFigure />}
                  {lock.lockType && (
                    <span className="text-hint text-muted-foreground">{lock.lockType}</span>
                  )}
                </span>
              </TableCell>
              <TableCell className="max-w-64 truncate px-2 py-2 font-mono" title={lock.object}>
                {lock.object || <NoFigure />}
              </TableCell>
              <TableCell className="px-2 py-2">
                <Status
                  tone={lock.granted ? "running" : "warning"}
                  label={lock.granted ? "Granted" : "Waiting"}
                />
              </TableCell>
              <TableCell className="w-full max-w-0 py-2">
                {lock.query ? <StatementLine text={lock.query} /> : <NoFigure />}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {truncated && (
        <FormNote className="px-4">
          The server listed more locks than these; the first 500 are shown.
        </FormNote>
      )}
    </div>
  )
}
