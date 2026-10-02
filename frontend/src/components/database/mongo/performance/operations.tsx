"use client"

import { useMemo, useState } from "react"
import { StopCircle } from "@/components/icons"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { useArrivals } from "@/hooks/use-arrivals"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
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
import { nameHue } from "@/components/database/home/kinds"
import { EngineMark } from "@/components/database/kit"
import { killOperation, mongoOperations } from "@/components/database/mongo/api"
import {
  clientWork,
  isHeartbeat,
  leftOutWords,
  operationTitle as title,
  stoppable,
} from "@/components/database/mongo/performance/ops"
import { runningWords } from "@/components/database/mongo/performance/samples"
import type { MongoOperation } from "@/components/database/mongo/types"
import type { Mongo } from "@/components/database/mongo/use-mongo"
import { ReadError } from "@/components/database/redis/read-error"

/** How long an operation runs before its time is a reading worth a hue. */
const LONG_SECONDS = 10

/**
 * What the server is running right now, longest first.
 *
 * Each row is one operation as the server reports it: what it is on, for how
 * long, for whom, with what plan. Stopping one asks the server to interrupt
 * it at its next safe point — not instantly — and is offered to a role that
 * may remove things, on a protected connection too, since it changes no data.
 * The dashboard's own request for this list is left out by the server; the
 * heartbeat each connected driver keeps open and the server's own tasks are
 * left out here, until the reader asks for them — and when they are listed
 * they are named as what they are and cannot be stopped from here: a
 * heartbeat is not work, and stopping one only makes its driver reconnect.
 */
export function OperationsView({
  mongo,
  confirm,
}: {
  mongo: Mongo
  confirm: (request: ConfirmRequest) => void
}) {
  const { id, conn, engine, canKill } = mongo
  const [all, setAll] = useState(false)
  const operations = usePoll((signal) => mongoOperations(id, all, signal), 3000, [id, all])
  // A driver's heartbeat and the server's own tasks are always running and
  // are nobody's work: they are listed only when the reader asks for everything.
  const read = operations.data
  const { shown: rows, leftOut } = useMemo(() => {
    if (!read) return { shown: undefined, leftOut: "" }
    if (all) return { shown: read, leftOut: "" }
    const work = clientWork(read)
    return { shown: work.shown, leftOut: leftOutWords(work.heartbeats, work.internal) }
  }, [read, all])
  const arrived = useArrivals((rows ?? []).map((row, index) => row.opId || `idle:${index}`))

  if (operations.error && !rows) {
    return <ReadError error={operations.error} onRetry={operations.refresh} />
  }
  if (!rows) return <LoadingPanel plain rows={5} />

  const kill = (operation: MongoOperation) =>
    confirm({
      title: "Stop operation",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{title(operation)}</span>,
        facts: (
          <>
            <FormFact label="Operation" mono>
              {operation.opId}
            </FormFact>
            <FormFact label="Running for">{runningWords(operation.secondsRunning)}</FormFact>
            <FormFact label="On">{conn.name}</FormFact>
            {operation.client && (
              <FormFact label="From" mono>
                {operation.client}
              </FormFact>
            )}
          </>
        ),
      },
      description:
        "The server interrupts the operation at its next safe point, which may take a moment. What it had already written stays written.",
      confirmLabel: "Stop operation",
      action: async () => {
        await killOperation(id, operation.opId)
      },
      onDone: operations.refresh,
    })

  return (
    <Panel plain>
      <PanelHeader title="Operations in progress" />
      <div className="pt-3">
        <OptionList>
          <OptionRow
            title="Also list idle connections, drivers' heartbeats and the server's own background work"
            checked={all}
            onCheckedChange={setAll}
          />
        </OptionList>
      </div>
      {operations.error && (
        <p role="status" className="pt-2 text-hint text-warning">
          The list could not be read again just now: {operations.error.message}
        </p>
      )}
      <PanelBody flush className="group-data-[plain]/panel:-mx-4">
        {rows.length === 0 ? (
          <EmptyNote>
            No client has work in progress right now.{leftOut ? ` Not listed: ${leftOut}.` : ""}
          </EmptyNote>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Operation</TableHead>
                <TableHead className="text-right">Running for</TableHead>
                <TableHead>For</TableHead>
                <TableHead className="max-lg:hidden">Plan</TableHead>
                <TableHead>State</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((operation, index) => {
                const key = operation.opId || `idle:${index}`
                const who = operation.appName || operation.user
                return (
                  <TableRow
                    key={key}
                    data-slot="mongo-operation"
                    className={cn(arrived.has(key) && "animate-rise")}
                  >
                    <TableCell className="max-w-96">
                      <p className="truncate font-mono font-medium">{title(operation)}</p>
                      {operation.command && (
                        <p
                          className="truncate font-mono text-hint text-muted-foreground"
                          title={operation.command}
                        >
                          {operation.command}
                          {operation.commandTruncated ? "…" : ""}
                        </p>
                      )}
                      {operation.message && (
                        <p className="truncate text-hint text-muted-foreground">
                          {operation.message}
                        </p>
                      )}
                    </TableCell>
                    <TableCell
                      className={cn(
                        "numeric text-right whitespace-nowrap",
                        operation.active && operation.secondsRunning >= LONG_SECONDS
                          ? "text-warning"
                          : !operation.active && "text-muted-foreground",
                      )}
                    >
                      {operation.active ? runningWords(operation.secondsRunning) : "—"}
                    </TableCell>
                    <TableCell className="max-w-56">
                      {who ? (
                        <p className="truncate font-medium" style={{ color: nameHue(who) }}>
                          {who}
                        </p>
                      ) : null}
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {operation.client || (who ? "" : "the server itself")}
                      </p>
                    </TableCell>
                    <TableCell className="max-w-48 truncate font-mono text-muted-foreground max-lg:hidden">
                      {operation.planSummary || "—"}
                    </TableCell>
                    <TableCell>
                      <span className="flex">
                        {operation.killPending ? (
                          <Status tone="notice" label="Stopping" />
                        ) : operation.waitingForLock ? (
                          <Status tone="warning" label="Waiting for a lock" />
                        ) : operation.active ? (
                          <Status tone="running" label="Running" />
                        ) : (
                          <Status tone="stopped" label="Idle" />
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="py-1 text-right">
                      {isHeartbeat(operation) ? (
                        // Where Stop would stand, the reason there is none.
                        <span className="flex justify-end">
                          <Tag>heartbeat</Tag>
                        </span>
                      ) : (
                        canKill &&
                        engine.can("cancel") &&
                        stoppable(operation) && (
                          <IconAction
                            label={`Stop ${title(operation)}`}
                            disabled={operation.killPending}
                            onClick={() => kill(operation)}
                          >
                            <StopCircle />
                          </IconAction>
                        )
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
        {rows.length > 0 && leftOut && (
          <p className="px-4 pt-2 text-hint text-muted-foreground">Not listed: {leftOut}.</p>
        )}
      </PanelBody>
    </Panel>
  )
}
