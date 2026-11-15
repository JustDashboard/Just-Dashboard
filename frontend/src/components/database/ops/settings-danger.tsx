"use client"

import { useRouter } from "next/navigation"
import { Trash, WarningFill } from "@/components/icons"
import { del } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { DangerRow, DangerRows } from "@/components/settings/danger-row"
import { SettingSection } from "@/components/settings/setting-card"
import { FormFact } from "@/components/form"
import { LogoGlyph } from "@/components/logo"
import { ProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { EngineMark } from "@/components/database/kit"
import {
  LinkedNotice,
  createLinked,
  refusingLinked,
  useForgetConnection,
} from "@/components/database/ops/settings-forget"
import { dropEffect, dropPhrase, type DropKind } from "@/components/database/ops/settings-model"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"
import { DATABASES_HREF } from "@/components/database/shell/routes"

/** What `DELETE /databases/{id}/database` answers. */
type Dropped = {
  detail?: string
  database: string
  connectionRemoved: boolean
  container?: string
  warnings?: string[]
}

/** The dashboard's own mark, for the one act that touches only what the dashboard keeps. */
function DashboardGlyph({ className }: { className?: string }) {
  return <LogoGlyph small className={cn(className, "w-auto text-brand")} />
}

/**
 * The acts that end a connection or a database: forget the connection, delete
 * the database it names, remove the container that runs its server.
 *
 * One frame, in the danger rule, around all of them (`DangerRows`), each act
 * a `DangerRow` with its sentence and its button beside it, as a
 * deployment's danger zone draws them. No line under the heading: each
 * sentence says what its own act cannot undo. Forgetting is asked with the ordinary
 * confirmation; deleting the database is the one act of the section that is
 * typed for, and its phrase is the name of what goes. A connection that names
 * no database offers no delete at all — there is no dialog to open without a
 * subject — and removing a container is its own act with its own phrase.
 *
 * An act the role may not take is not drawn; one that cannot be taken here
 * stays, quiet, saying why.
 */
export function DangerSection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, summary, readOnly, status } = useDatabase()
  const { refresh } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const forget = useForgetConnection(confirm)
  const mayDestroy = can("system.admin") && can("destructive")

  const kind: DropKind = {
    fileBased: engine.can("fileBased"),
    numbered: engine.can("logicalDatabases"),
    documents: engine.kind === "document",
  }
  const phrase = dropPhrase(conn, kind)
  const container =
    summary?.container && !summary.container.composeProject ? summary.container : undefined
  // The server types its phrase from the database a request names; a
  // connection that names none sends the container's own name there.
  const containerPhrase = phrase || container?.name || ""
  const lastDump = summary?.lastBackup
    ? `Its last dump was taken ${relativeTime(summary.lastBackup)}.`
    : "No dump of it is kept on this server."

  const gone = (answer: Dropped, what: string) => {
    for (const warning of answer.warnings ?? []) notify.warning("Kept", { description: warning })
    notify.success(what, { description: answer.detail })
    refresh()
    if (answer.connectionRemoved) router.push(DATABASES_HREF)
    else status.refresh()
  }

  const subject = (label: string, value: string): ConfirmRequest["subject"] => ({
    mark: <EngineMark engine={engine} size="sm" />,
    name: conn.name,
    facts: (
      <>
        <FormFact label={label} mono>
          {value}
        </FormFact>
        {conn.host && (
          <FormFact label="On" mono>
            {conn.host}
            {conn.port ? `:${conn.port}` : ""}
          </FormFact>
        )}
      </>
    ),
  })

  const drop = () => {
    const linked = createLinked()
    confirm({
      title: "Delete database",
      confirmLabel: "Delete for good",
      phrase,
      subject: subject(engine.databaseField, conn.database || "0"),
      description: (
        <>
          <LinkedNotice linked={linked} what="deleted" />
          <p>{dropEffect(kind, engine.nouns.row)}</p>
          <p>Nothing here brings it back. {lastDump}</p>
        </>
      ),
      action: async (typed) => {
        const answer = await refusingLinked(id, conn.name, linked, () =>
          del<Dropped>(`/databases/${id}/database`, { confirm: typed, body: {} }),
        )
        gone(answer, `Deleted ${answer.database}`)
        return "reported"
      },
    })
  }

  const removeContainer = () => {
    if (!container) return
    const linked = createLinked()
    confirm({
      title: "Remove the container",
      confirmLabel: "Remove for good",
      phrase: containerPhrase,
      subject: subject("Container", container.name),
      description: (
        <>
          <LinkedNotice linked={linked} what="deleted" />
          <p>
            Stops and removes the container <span className="font-mono">{container.name}</span> and
            the volumes it wrote to. Every database on that server goes with it, and so does this
            connection.
          </p>
          <p>
            Nothing here brings it back. {lastDump} A volume another container still uses is kept,
            and said.
          </p>
        </>
      ),
      action: async (typed) => {
        const answer = await refusingLinked(id, conn.name, linked, () =>
          del<Dropped>(`/databases/${id}/database`, {
            confirm: typed,
            body: { removeContainer: true, ...(phrase ? {} : { database: container.name }) },
          }),
        )
        gone(answer, `Removed ${answer.container ?? container.name}`)
        return "reported"
      },
    })
  }

  const rows = [
    mayDestroy && (
      <DangerRow
        key="forget"
        mark={<ProductLogo size="sm" fallback={DashboardGlyph} />}
        title="Forget this connection"
        sentence="Removes the saved address, its password and its saved queries from the dashboard. The database and its data are not touched."
        action={
          <Button variant="outline" size="sm" onClick={forget}>
            <Trash />
            Forget…
          </Button>
        }
      />
    ),
    mayDestroy && (
      <DangerRow
        key="drop"
        mark={<EngineMark engine={engine} size="sm" />}
        title="Delete the database"
        sentence={
          readOnly
            ? "This connection is protected, so nothing in it is deleted from here. Turn Protection off on General first."
            : phrase
              ? dropEffect(kind, engine.nouns.row)
              : "This connection names no database, so there is nothing here to delete by name."
        }
        facts={
          !readOnly && phrase ? (
            <>
              Typed to confirm: <span className="font-mono text-foreground">{phrase}</span>
            </>
          ) : undefined
        }
        action={
          !readOnly && phrase ? (
            <Button variant="destructive" size="sm" onClick={drop}>
              <WarningFill />
              Delete…
            </Button>
          ) : undefined
        }
      />
    ),
    mayDestroy && container && (
      <DangerRow
        key="container"
        mark={<ProductLogo id="docker" size="sm" />}
        title="Remove its container"
        sentence={
          readOnly
            ? "This connection is protected, so its server is not removed from here."
            : "Removes the container that runs this server, with the volumes it wrote to: the whole server, not one database. It needs no password, so it works when the connection no longer does."
        }
        facts={
          <>
            Container <span className="font-mono text-foreground">{container.name}</span>
          </>
        }
        action={
          !readOnly ? (
            <Button variant="destructive" size="sm" onClick={removeContainer}>
              <WarningFill />
              Remove…
            </Button>
          ) : undefined
        }
      />
    ),
  ].filter(Boolean)

  if (rows.length === 0) return null
  return (
    <SettingSection id="danger" tone="danger" title="Danger zone">
      <DangerRows>{rows}</DangerRows>
    </SettingSection>
  )
}
