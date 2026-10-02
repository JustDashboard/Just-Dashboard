"use client"

import { useId, useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Plus } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { DbConnection } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceRow } from "@/components/flow"
import { Field, FormFact, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { sectionHref } from "@/components/database/engine"
import { CardsSkeleton, CouldNotRead } from "@/components/database/home/blocks"
import { nameHue } from "@/components/database/home/kinds"
import { read } from "@/components/database/home/read"
import { EngineMark } from "@/components/database/kit"
import { isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

/** One database of `GET /databases/{id}/schemas`: what the server holds besides this one. */
type DbServerDatabase = {
  name: string
  /** Bytes; on a server that numbers its databases, the number of keys. */
  size?: number
  owner?: string
  encoding?: string
}

/** A database name the server's route accepts: letters, digits and underscores. */
const DATABASE_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/

/** How many are listed before the rest fold away. */
const LISTED = 8

/**
 * What else the server behind this connection holds. A database that already
 * has a connection is a way to it; one that does not can be given one by an
 * administrator — the same account, the other name — and a new one can be
 * made, where the engine keeps more than one.
 *
 * On a server that numbers its databases there is nothing to make or to
 * connect: every one is a number away from the same connection, so each card
 * opens the keys of that number.
 */
export function ServerDatabasesSection() {
  const { id, conn, engine, readOnly, status, href } = useDatabase()
  const { connections, refresh, engineFor } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const numbered = engine.can("logicalDatabases")
  const down = isDown(status.state)
  const [all, setAll] = useState(false)
  const [creating, setCreating] = useState(false)
  const [connecting, setConnecting] = useState<string>()
  const list = usePoll(
    (signal) =>
      read<DbServerDatabase[]>(
        `/databases/${id}/schemas`,
        (answer) => Array.isArray(answer),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: !down },
  )
  const admin = can("system.admin") && !readOnly
  const mayCreate = admin && engine.can("serverDatabaseCreate")
  const mayConnect = admin && engine.can("serverDatabaseConnect")

  const rows = useMemo(() => {
    const siblings = new Map(
      connections
        .filter(
          (other) =>
            other.driver === conn.driver && other.host === conn.host && other.port === conn.port,
        )
        .map((other) => [other.database, other]),
    )
    const current = (name: string) =>
      numbered ? Number(name) === Number(conn.database || "0") : name === conn.database
    return (list.data ?? [])
      .filter((database) => !numbered || (database.size ?? 0) > 0 || current(database.name))
      .map((database) => ({
        database,
        current: current(database.name),
        saved: numbered ? undefined : siblings.get(database.name),
      }))
      .sort(
        (a, b) =>
          Number(b.current) - Number(a.current) ||
          Number(Boolean(b.saved)) - Number(Boolean(a.saved)) ||
          (numbered
            ? Number(a.database.name) - Number(b.database.name)
            : a.database.name.localeCompare(b.database.name)),
      )
  }, [list.data, connections, conn, numbered])

  const connect = async (database: string) => {
    setConnecting(database)
    try {
      const saved = await post<DbConnection>(`/databases/${id}/server/databases/connect`, {
        database,
      })
      notify.success(`Connected ${saved.name}`, {
        action: { label: "Open", onClick: () => router.push(sectionHref(saved.id)) },
      })
      refresh()
    } catch (err) {
      notify.error(`Could not connect to ${database}`, err)
    } finally {
      setConnecting(undefined)
    }
  }

  const shown = all ? rows : rows.slice(0, LISTED)
  const total = list.data?.length ?? 0
  return (
    <FormSection
      aside
      id="databases"
      title={numbered ? "Numbered databases" : "Databases on this server"}
      hint={
        list.data ? (
          <span className="numeric">
            {numbered ? `${rows.length} of ${total} hold keys` : plural(total, "database")}
          </span>
        ) : undefined
      }
      actions={
        mayCreate && (
          <Button size="xs" variant="outline" onClick={() => setCreating(true)}>
            <Plus />
            New database
          </Button>
        )
      }
    >
      {down ? (
        <FormNote>
          They are read from the running server, and listed once {conn.name} answers again.
        </FormNote>
      ) : !list.data ? (
        list.error ? (
          <CouldNotRead what="the server's databases" error={list.error} onRetry={list.refresh} />
        ) : (
          <CardsSkeleton count={4} className="grid gap-2 sm:grid-cols-2" />
        )
      ) : rows.length === 0 ? (
        <p className="animate-rise text-body text-muted-foreground">
          {numbered
            ? `None of its ${total} numbered databases holds a key yet.`
            : "The server lists no database to this account."}
        </p>
      ) : (
        <div className="animate-rise space-y-2">
          <ul data-slot="choice-list" className="grid min-w-0 gap-2 sm:grid-cols-2">
            {shown.map(({ database, current, saved }) => {
              const goes = numbered
                ? href("data", { db: database.name })
                : saved && !current
                  ? sectionHref(saved.id)
                  : undefined
              const facts: React.ReactNode[] = []
              if (numbered) facts.push(plural(database.size ?? 0, "key"))
              else if (database.size) facts.push(bytes(database.size))
              if (database.owner && !engine.can("fileBased")) {
                facts.push(
                  <>
                    owned by{" "}
                    <span style={{ color: nameHue(database.owner) }}>{database.owner}</span>
                  </>,
                )
              }
              if (database.encoding) facts.push(database.encoding)
              if (saved && !current) facts.push(`saved as ${saved.name}`)
              return (
                <ChoiceRow
                  key={database.name}
                  href={goes}
                  disabled={!goes}
                  verb={
                    numbered
                      ? `Browse the keys of database ${database.name}`
                      : saved
                        ? `Open ${saved.name}`
                        : database.name
                  }
                  leading={
                    <span className={cn("flex", !numbered && !saved && !current && "opacity-45")}>
                      <EngineMark engine={saved ? engineFor(saved) : engine} size="sm" />
                    </span>
                  }
                  title={
                    <span className="font-mono text-xs">
                      {numbered ? `db ${database.name}` : database.name}
                    </span>
                  }
                  description={
                    facts.length > 0
                      ? facts.map((fact, index) => (
                          <span key={index}>
                            {index > 0 && " · "}
                            {fact}
                          </span>
                        ))
                      : undefined
                  }
                  trailing={current ? <Tag>this one</Tag> : undefined}
                  actions={
                    !numbered && !saved && !current && mayConnect ? (
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Connect ${database.name}`}
                        pending={connecting === database.name}
                        disabled={connecting !== undefined}
                        onClick={() => void connect(database.name)}
                      >
                        Connect
                      </Button>
                    ) : undefined
                  }
                />
              )
            })}
          </ul>
          {rows.length > LISTED && (
            <Button
              size="xs"
              variant="ghost"
              className="-ml-2"
              aria-expanded={all}
              onClick={() => setAll(!all)}
            >
              {all ? "Show fewer" : `Show all ${rows.length}`}
            </Button>
          )}
        </div>
      )}

      {creating && (
        <CreateDatabase
          taken={(list.data ?? []).map((database) => database.name)}
          onClose={() => setCreating(false)}
          onCreated={(saved) => {
            setCreating(false)
            list.refresh()
            if (saved) refresh()
          }}
        />
      )}
    </FormSection>
  )
}

/** Make an empty database on the same server, and — where the engine can — save a connection to it. */
function CreateDatabase({
  taken,
  onClose,
  onCreated,
}: {
  taken: string[]
  onClose: () => void
  onCreated: (saved: DbConnection | undefined) => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const router = useRouter()
  const field = useId()
  const canConnect = engine.can("serverDatabaseConnect")
  const [name, setName] = useState("")
  const [connect, setConnect] = useState(canConnect)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const typed = name.trim()
  const problem = !typed
    ? "A database needs a name."
    : !DATABASE_NAME.test(typed)
      ? "Letters, digits and underscores, starting with a letter or an underscore."
      : taken.some((other) => other.toLowerCase() === typed.toLowerCase())
        ? "The server already has a database of that name."
        : undefined

  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      const answer = await post<DbConnection | { ok: true }>(`/databases/${id}/server/databases`, {
        name: typed,
        connect: connect && canConnect,
      })
      const saved = "id" in answer ? answer : undefined
      notify.success(`Created ${typed}`, {
        description: saved ? `Saved as the connection ${saved.name}.` : undefined,
        action: saved
          ? { label: "Open", onClick: () => router.push(sectionHref(saved.id)) }
          : undefined,
      })
      onCreated(saved)
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title="New database"
      description={`Create an empty database on the server ${conn.name} connects to`}
      size="sm"
      subject={databaseSubject(
        {
          name: summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label,
        },
        engine,
        <>
          <FormFact label="Server" mono>
            {conn.host}
            {conn.port ? `:${conn.port}` : ""}
          </FormFact>
          {conn.user && (
            <FormFact label="As" mono>
              {conn.user}
            </FormFact>
          )}
        </>,
      )}
      dirty={typed !== ""}
      busy={busy}
      refusal={refusal}
      command="Create"
      disabled={Boolean(problem)}
      onRun={() => void run()}
      onClose={onClose}
    >
      <Field
        label="Name"
        htmlFor={field}
        hint="Letters, digits and underscores."
        error={typed ? problem : undefined}
      >
        <Input
          id={field}
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
        />
      </Field>
      {canConnect ? (
        <OptionList>
          <OptionRow
            title="Save a connection to it, with the same account"
            checked={connect}
            onCheckedChange={setConnect}
          />
        </OptionList>
      ) : (
        <FormNote>
          {engine.label} reaches every database of a server through one connection, so the new one
          is browsed from here: nothing more is saved.
        </FormNote>
      )}
    </TaskDialog>
  )
}
