"use client"

import { useEffect, useRef, useState } from "react"
import { Database, Plus } from "@/components/icons"
import { plural } from "@/lib/format"
import { Field } from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { EmptyNote, ErrorState } from "@/components/state"
import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"
import { DATABASE_ENGINE_LABELS } from "@/components/deploy/vocabulary"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import type { DbConnection } from "@/lib/types"

/** The keys applications read a connection string from, one press each. */
const VARIABLES = ["DATABASE_URL", "REDIS_URL", "MONGODB_URL"]

/**
 * The trigger and sheet that link a database into a deployment's environment.
 *
 * Used to draw its own "Database" panel with a running list of what it had
 * linked so far — redundant now that the linked variable is a row the
 * environment editor already shows, and its only caller is that editor. What
 * is linked is the row itself; this is just the button that opens the sheet.
 *
 * The sheet asks two things. Where the address goes — a field with the usual
 * keys one press away, as a toggle group so the key in the field reads as
 * chosen and a key under the pointer only as hovered (§6) — and where the
 * database comes from, which is a choice
 * between kinds (§16): start one on this server, or take a connection already
 * saved. The saved connections are rows you take, each drawn as its engine
 * with the address it points at, and taking one is the advance: the row runs a
 * light round its edge while the connection string is fetched. They were
 * options in a select reading "orders-db · postgres", which drew a Postgres and
 * a Redis as the same grey line.
 *
 * A database started from here and not yet connected — the sheet was closed
 * while it came up — is said beside the trigger as a state with the way back
 * to it, rather than as a sentence telling the reader which button to press.
 */
export function ProjectDatabase({
  onConnect,
  target = "container",
  label = "Add database",
  initialEngine,
  initialVariable = "DATABASE_URL",
}: {
  target?: "host" | "container"
  onConnect: (connection: DbConnection, url: string, variable: string) => void
  /** The trigger's text; a detected database names its engine here. */
  label?: string
  initialEngine?: string
  initialVariable?: string
}) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<"create" | "existing">("create")
  const [variable, setVariable] = useState(initialVariable)
  const [started, setStarted] = useState<{ container: string; engine: string }>()
  const [connecting, setConnecting] = useState<number>()
  const [error, setError] = useState<Error>()
  const request = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => request.current?.abort(), [])
  const cancelConnection = () => {
    request.current?.abort()
    setConnecting(undefined)
    setError(undefined)
  }
  const changeOpen = (next: boolean) => {
    if (!next) cancelConnection()
    setOpen(next)
  }
  // Read as the sheet opens, so the second choice can say how many there are.
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [open],
    { enabled: open },
  )
  // A file on this server is no address a container can be handed.
  const usable = connections.data?.filter((connection) => connection.driver !== "sqlite")
  const valid = /^[A-Za-z_][A-Za-z0-9_]*$/.test(variable)
  const connect = (connection: DbConnection, url: string) => {
    onConnect(connection, url, variable)
    setOpen(false)
    // Cleared so a second database started from this same sheet does not
    // silently resume the first one's already-connected container.
    setStarted(undefined)
  }
  const connectExisting = async (connection: DbConnection) => {
    if (!valid) return
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setConnecting(connection.id)
    setError(undefined)
    try {
      const result = await get<{ url: string; reference?: string }>(
        `/databases/${connection.id}/url`,
        { target },
        controller.signal,
      )
      if (!controller.signal.aborted) connect(connection, result.reference || result.url)
    } catch (caught) {
      if (!controller.signal.aborted)
        setError(caught instanceof Error ? caught : new Error(String(caught)))
    } finally {
      if (!controller.signal.aborted) setConnecting(undefined)
    }
  }
  return (
    <>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>
          <Plus className="size-3.5" /> {label}
        </Button>
        {started && (
          <span className="flex flex-wrap items-center gap-2">
            <Status tone="warning" label={`Database ${started.container} has started`} />
            <Button type="button" variant="outline" size="xs" onClick={() => setOpen(true)}>
              Finish connecting
            </Button>
          </span>
        )}
      </div>
      <SidePanel
        open={open}
        onOpenChange={changeOpen}
        title="Add database"
        description="Create a database or link an existing connection to this project."
        width="md"
        // Creating one brings its own command into the footer after this;
        // taking a saved connection is the row itself.
        footer={
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
        }
        // The field holds a sensible key already; landing in it selects the
        // key, which reads as a glitch and invites typing over it.
        initialFocus="body"
      >
        <div className="space-y-6">
          <Field
            label="Environment variable"
            htmlFor="database-variable"
            hint="The key your application reads its connection string from."
            error={
              valid
                ? undefined
                : "Use a letter or underscore first, then letters, numbers or underscores."
            }
          >
            <Input
              id="database-variable"
              value={variable}
              disabled={connecting !== undefined}
              onChange={(event) => setVariable(event.target.value)}
              aria-invalid={!valid}
              className="font-mono"
            />
            <ToggleGroup
              type="single"
              size="sm"
              spacing={1}
              aria-label="Common keys"
              value={VARIABLES.includes(variable) ? variable : ""}
              onValueChange={(next) => next && setVariable(next)}
              disabled={connecting !== undefined}
              className="flex-wrap justify-start"
            >
              {VARIABLES.map((name) => (
                <ToggleGroupItem
                  key={name}
                  value={name}
                  className="h-6 px-2 font-mono text-hint text-muted-foreground max-sm:h-8"
                >
                  {name}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </Field>

          <ChoiceGrid columns={2} role="group" aria-label="Database source">
            <ChoiceCard
              verb="Create new"
              title="Create new"
              mark={Plus}
              description="Start an engine on this server"
              selected={mode === "create"}
              onClick={() => {
                cancelConnection()
                setMode("create")
              }}
            />
            <ChoiceCard
              verb="Use existing"
              title="Use existing"
              mark={Database}
              description={
                usable
                  ? `${plural(usable.length, "saved connection")} on this server`
                  : "The connections saved on this server"
              }
              selected={mode === "existing"}
              onClick={() => {
                setMode("existing")
                connections.refresh()
              }}
            />
          </ChoiceGrid>

          {mode === "create" ? (
            <DatabaseQuickDeploy
              target={target}
              canConnect={valid}
              onConnect={connect}
              resume={started}
              onStarted={setStarted}
              initialEngine={initialEngine}
            />
          ) : (
            <div className="space-y-3">
              {(error || connections.error) && <ErrorState error={error || connections.error!} />}
              {usable?.length === 0 ? (
                <EmptyNote>No saved connections yet. Create a database to get started.</EmptyNote>
              ) : (
                <ChoiceList aria-label="Saved connections">
                  {usable?.map((connection, index) => (
                    <ChoiceRow
                      key={connection.id}
                      verb={`Connect ${connection.name}`}
                      onSelect={() => void connectExisting(connection)}
                      busy={connecting === connection.id}
                      disabled={!valid}
                      index={index}
                      leading={<ProductLogo id={connection.driver} size="sm" fallback={Database} />}
                      title={connection.name}
                      description={
                        address(connection) && (
                          <span className="font-mono">{address(connection)}</span>
                        )
                      }
                      trailing={
                        <span className="text-hint text-muted-foreground max-sm:hidden">
                          {DATABASE_ENGINE_LABELS[connection.driver] ?? connection.driver}
                        </span>
                      }
                    />
                  ))}
                </ChoiceList>
              )}
            </div>
          )}
        </div>
      </SidePanel>
    </>
  )
}

/** Where a saved connection points: `host:port/database`, whichever of those it names. */
function address(connection: DbConnection) {
  const host =
    connection.host && connection.port ? `${connection.host}:${connection.port}` : connection.host
  return [host, connection.database].filter(Boolean).join("/")
}
