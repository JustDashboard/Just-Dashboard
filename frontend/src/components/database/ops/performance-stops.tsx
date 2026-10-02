"use client"

import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { Well } from "@/components/panel"
import { EngineMark } from "@/components/database/kit"
import { cancelStatement, endSession } from "@/components/database/ops/performance-api"
import { useAsk } from "@/components/database/ops/performance-parts"
import { useDatabase } from "@/components/database/shell/database-context"

/** As much of a session as stopping it needs: which one, and what tells it from its neighbours. */
export type Stoppable = { pid: string; user?: string; application?: string; query?: string }

/**
 * The two ways of stopping a session, for the views that list sessions and
 * the one that draws who blocks whom.
 *
 * Cancelling stops the statement and keeps the session; terminating ends the
 * session, which is the only thing that frees the locks of one that is idle
 * inside a transaction. Both are removals on the server's side, so both are a
 * confirmation that names the session, and both are drawn only for a role
 * that may remove things, on an engine that can do it. They change no data,
 * so a protected connection keeps them: a runaway statement on production is
 * exactly what an operator needs to be able to stop.
 */
export function useStops(onDone: () => void) {
  const { id, conn, engine } = useDatabase()
  const { can } = useAuth()
  // The confirmation gives the keyboard back to the button that asked.
  const { confirm, dialog } = useAsk()
  const may = can("destructive")

  const subject = (session: Stoppable): ConfirmRequest["subject"] => ({
    mark: <EngineMark engine={engine} size="sm" />,
    name: (
      <>
        Session <span className="font-mono">{session.pid}</span>
      </>
    ),
    facts: (
      <>
        {session.user && <FormFact label="As">{session.user}</FormFact>}
        {session.application && <FormFact label="From">{session.application}</FormFact>}
        <FormFact label="On">{conn.name}</FormFact>
      </>
    ),
  })

  const cancel = (session: Stoppable) =>
    confirm({
      title: "Cancel statement",
      subject: subject(session),
      description: (
        <>
          <p>
            The statement stops and what it had done is rolled back. The session stays connected,
            and its application is told the statement was cancelled.
          </p>
          {session.query && (
            <Well className="max-h-32 overflow-auto text-hint whitespace-pre-wrap">
              {session.query}
            </Well>
          )}
        </>
      ),
      // The dialog's own way out is called Cancel; this is the other one.
      confirmLabel: "Stop the statement",
      action: async () => {
        await cancelStatement(id, session.pid)
      },
      onDone,
    })

  const terminate = (session: Stoppable) =>
    confirm({
      title: "Terminate session",
      subject: subject(session),
      description: (
        <p>
          The server ends this session: its statement stops, its open transaction is rolled back and
          the locks it holds are released. An application that reconnects will come back.
        </p>
      ),
      confirmLabel: "Terminate session",
      action: async () => {
        await endSession(id, session.pid)
      },
      onDone,
    })

  return {
    /** What this role may do on this engine. */
    can: { cancel: may && engine.can("cancel"), kill: may && engine.can("kill") },
    cancel,
    terminate,
    dialog,
  }
}

export type Stops = ReturnType<typeof useStops>
