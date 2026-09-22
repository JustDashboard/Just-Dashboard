"use client"

import { useEffect, useRef, useState } from "react"
import { Database, Plus } from "@/components/icons"
import { Field } from "@/components/form"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EmptyNote, ErrorState } from "@/components/state"
import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import type { DbConnection } from "@/lib/types"

/**
 * The trigger and sheet that link a database into a deployment's environment.
 *
 * Used to draw its own "Database" panel with a running list of what it had
 * linked so far — redundant now that the linked variable is a row the
 * environment editor already shows, and its only caller is that editor. What
 * is linked is the row itself; this is just the button that opens the sheet.
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
  const [selected, setSelected] = useState("")
  const [variable, setVariable] = useState(initialVariable)
  const [started, setStarted] = useState<{ container: string; engine: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const request = useRef<AbortController | undefined>(undefined)
  useEffect(() => () => request.current?.abort(), [])
  const cancelConnection = () => {
    request.current?.abort()
    setBusy(false)
    setError(undefined)
  }
  const changeOpen = (next: boolean) => {
    if (!next) cancelConnection()
    setOpen(next)
  }
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [open],
    { enabled: open && mode === "existing" },
  )
  const valid = /^[A-Za-z_][A-Za-z0-9_]*$/.test(variable)
  const connect = (connection: DbConnection, url: string) => {
    onConnect(connection, url, variable)
    setOpen(false)
    // Cleared so a second database started from this same sheet does not
    // silently resume the first one's already-connected container.
    setStarted(undefined)
  }
  const useExisting = async () => {
    const connection = connections.data?.find((item) => String(item.id) === selected)
    if (!connection || !valid || busy) return
    request.current?.abort()
    const controller = new AbortController()
    request.current = controller
    setBusy(true)
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
      if (!controller.signal.aborted) setBusy(false)
    }
  }
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)}>
          <Plus className="size-3.5" /> {label}
        </Button>
        {started && (
          <p className="text-hint text-muted-foreground">
            Database {started.container} has started. Open Add database to finish connecting it.
          </p>
        )}
      </div>
      <SidePanel
        open={open}
        onOpenChange={changeOpen}
        title="Connect a database"
        description="Create a database or link an existing connection to this project."
        width="md"
      >
        <div className="space-y-5">
          <Field
            label="Environment variable"
            htmlFor="database-variable"
            hint="Use DATABASE_URL, REDIS_URL, or the key your application expects."
            error={
              valid
                ? undefined
                : "Use a letter or underscore first, then letters, numbers or underscores."
            }
          >
            <Input
              id="database-variable"
              value={variable}
              disabled={busy}
              onChange={(event) => setVariable(event.target.value)}
              aria-invalid={!valid}
              className="font-mono"
            />
          </Field>
          <div role="group" aria-label="Database source" className="flex gap-2">
            <Button
              type="button"
              variant={mode === "create" ? "secondary" : "ghost"}
              onClick={() => {
                cancelConnection()
                setMode("create")
              }}
            >
              Create new
            </Button>
            <Button
              type="button"
              variant={mode === "existing" ? "secondary" : "ghost"}
              onClick={() => {
                setMode("existing")
                connections.refresh()
              }}
            >
              Use existing
            </Button>
          </div>
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
            <div className="space-y-4">
              {(error || connections.error) && <ErrorState error={error || connections.error!} />}
              <Select value={selected} onValueChange={setSelected} disabled={busy}>
                <SelectTrigger aria-label="Existing database" className="w-full">
                  <SelectValue placeholder="Choose a database" />
                </SelectTrigger>
                <SelectContent>
                  {(connections.data ?? [])
                    .filter((connection) => connection.driver !== "sqlite")
                    .map((connection) => (
                      <SelectItem key={connection.id} value={String(connection.id)}>
                        {connection.name} · {connection.driver}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
              {connections.data?.filter((connection) => connection.driver !== "sqlite").length ===
                0 && (
                <EmptyNote>No saved connections yet. Create a database to get started.</EmptyNote>
              )}
              <Button disabled={!selected || !valid} pending={busy} onClick={useExisting}>
                <Database className="size-4" /> Connect database
              </Button>
            </div>
          )}
        </div>
      </SidePanel>
    </>
  )
}
