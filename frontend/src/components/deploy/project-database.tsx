"use client"

import { useState } from "react"
import { Database, Plus } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { EmptyNote, ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import type { DbConnection } from "@/lib/types"

export function ProjectDatabase({
  onConnect,
  target = "container",
}: {
  target?: "host" | "container"
  onConnect: (connection: DbConnection, url: string, variable: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<"create" | "existing">("create")
  const [selected, setSelected] = useState("")
  const [variable, setVariable] = useState("DATABASE_URL")
  const [linked, setLinked] = useState<string[]>([])
  const [started, setStarted] = useState<{ container: string; engine: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    0,
    [open],
    { enabled: open && mode === "existing" },
  )
  const valid = /^[A-Za-z_][A-Za-z0-9_]*$/.test(variable)
  const connect = (connection: DbConnection, url: string) => {
    onConnect(connection, url, variable)
    setLinked((current) => [...new Set([...current, `${connection.name} → ${variable}`])])
    setOpen(false)
    setStarted(undefined)
  }
  const useExisting = async () => {
    const connection = connections.data?.find((item) => String(item.id) === selected)
    if (!connection || !valid) return
    setBusy(true)
    setError(undefined)
    try {
      const result = await get<{ url: string; reference?: string }>(
        `/databases/${connection.id}/url`,
        {
          target,
        },
      )
      connect(connection, result.reference || result.url)
    } catch (caught) {
      setError(caught instanceof Error ? caught : new Error(String(caught)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Panel plain>
        <PanelHeader
          title="Database"
          actions={
            <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-3.5" /> Add database
            </Button>
          }
        />
        <PanelBody>
          {linked.length ? (
            <ul className="space-y-2">
              {linked.map((name) => (
                <li key={name}>
                  <Status verdict="ok" label={name} />
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm leading-relaxed text-muted-foreground">
              {started
                ? `Database ${started.container} has started. Open Add database to finish connecting it.`
                : "Need a database? Create one or connect an existing database. A saved connection reference keeps its application address stable across container replacement."}
            </p>
          )}
        </PanelBody>
      </Panel>
      <SidePanel
        open={open}
        onOpenChange={setOpen}
        title="Connect a database"
        description="Create a database or link an existing connection to this project."
        width="md"
      >
        <div className="space-y-5">
          <div className="space-y-2">
            <Label htmlFor="database-variable">Environment variable</Label>
            <Input
              id="database-variable"
              value={variable}
              onChange={(event) => setVariable(event.target.value)}
              aria-invalid={!valid}
              className="font-mono"
            />
            <p className="text-xs text-muted-foreground">
              Use DATABASE_URL, REDIS_URL, or the key your application expects.
            </p>
            {!valid && (
              <p role="alert" className="text-xs text-destructive">
                Use a letter or underscore first, then letters, numbers or underscores.
              </p>
            )}
          </div>
          <div role="group" aria-label="Database source" className="flex gap-2">
            <Button
              variant={mode === "create" ? "secondary" : "ghost"}
              onClick={() => setMode("create")}
            >
              Create new
            </Button>
            <Button
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
            />
          ) : (
            <div className="space-y-4">
              {(error || connections.error) && <ErrorState error={error || connections.error!} />}
              <Select value={selected} onValueChange={setSelected}>
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
