"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { Database, Eye, EyeOff, Copy } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import type { DbConnection, DbProvisionOption } from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { ChoiceCard } from "@/components/choice-card"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useCopy } from "@/hooks/use-copy"
import { QuickField } from "@/components/deploy/quick-deploy"

export function DatabaseQuickDeploy({
  target = "host",
  canConnect = true,
  onConnect,
  resume,
  onStarted,
}: {
  target?: "host" | "container"
  canConnect?: boolean
  resume?: { container: string; engine: string }
  onStarted?: (started: { container: string; engine: string }) => void
  onConnect?: (connection: DbConnection, url: string) => void
}) {
  const alive = useRef(true)
  const provisioning = useRef(false)
  const [startedContainer, setStartedContainer] = useState<string | undefined>(resume?.container)
  const [revealed, setRevealed] = useState(false)
  const { copy } = useCopy()
  const [options, setOptions] = useState<DbProvisionOption[]>()
  const [engine, setEngine] = useState(resume?.engine ?? "")
  const [name, setName] = useState("")
  const [database, setDatabase] = useState("")
  const [progress, setProgress] = useState("")
  const [failure, setFailure] = useState<Error>()
  const [created, setCreated] = useState<{
    connection: DbConnection
    url: string
    reference?: string
  }>()

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  useEffect(() => {
    get<DbProvisionOption[]>("/databases/provision/options")
      .then(setOptions)
      .catch((error) => setFailure(error instanceof Error ? error : new Error(String(error))))
  }, [])

  const selected = options?.find((option) => option.engine === engine)

  const create = async () => {
    if (!selected || provisioning.current) return
    provisioning.current = true
    setFailure(undefined)
    setProgress("Starting the container…")
    try {
      const started = startedContainer
        ? { container: startedContainer }
        : await post<{ container: string }>("/databases/provision", {
            engine: selected.engine,
            name: name.trim() || undefined,
            database: database.trim() || undefined,
          })
      onStarted?.({ container: started.container, engine: selected.engine })
      if (!alive.current) return
      setStartedContainer(started.container)
      setProgress("Waiting for it to accept connections…")
      // The container exists a second after that request; the engine inside it
      // answers somewhere between seconds and a minute later, and MySQL will
      // accept a connection on a temporary server mid-initialisation and then
      // restart. Adopt proves credentials, ping proves it is really up.
      const deadline = Date.now() + 3 * 60_000
      let connection: DbConnection
      for (;;) {
        if (!alive.current) return
        try {
          connection = await post<DbConnection>("/databases/adopt", {
            container: started.container,
          })
          const health = await get<{ ok: boolean }>(`/databases/${connection.id}/ping`)
          if (!health.ok) throw new Error("not accepting connections yet")
          break
        } catch (error) {
          if (Date.now() > deadline) {
            setFailure(
              new Error(
                `${selected.label} started but did not become reachable: ${errorMessage(error)}`,
              ),
            )
            setProgress("")
            return
          }
          await new Promise((resolve) => setTimeout(resolve, 2000))
        }
      }
      if (!alive.current) return
      setProgress("Preparing the connection string…")
      const revealed = await get<{ url: string; reference?: string }>(
        `/databases/${connection.id}/url`,
        { target },
      )
      if (!alive.current) return
      setCreated({ connection, url: revealed.url, reference: revealed.reference })
      setProgress("")
    } catch (error) {
      if (!alive.current) return
      setFailure(error instanceof Error ? error : new Error(String(error)))
      setProgress("")
    } finally {
      provisioning.current = false
    }
  }

  if (created)
    return (
      <Panel>
        <PanelHeader title={`${created.connection.name} is ready`} />
        <PanelBody className="space-y-4">
          <Label htmlFor="database-connection-string">Connection string</Label>
          <div className="flex gap-2">
            <Input
              id="database-connection-string"
              type={revealed ? "text" : "password"}
              readOnly
              value={created.url}
              className="font-mono"
            />
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={revealed ? "Hide connection string" : "Reveal connection string"}
              onClick={() => setRevealed(!revealed)}
            >
              {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label="Copy connection string"
              onClick={() => copy(created.url, "Connection string copied")}
            >
              <Copy className="size-4" />
            </Button>
          </div>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {target === "container"
              ? "This private address is reachable by applications on Docker's default bridge. Reconnect the database if its container is replaced."
              : "This address is for processes on the host. Use Add database during project setup to get the address for an application container."}
          </p>
        </PanelBody>
        <PanelFooter className="justify-between">
          <Button variant="outline" asChild>
            <Link href={`/databases?connection=${encodeURIComponent(created.connection.name)}`}>
              Open in Databases
            </Link>
          </Button>
          {onConnect ? (
            <Button
              disabled={!canConnect}
              onClick={() => onConnect(created.connection, created.reference || created.url)}
            >
              Use this database
            </Button>
          ) : (
            <Button
              onClick={() => {
                setCreated(undefined)
                setStartedContainer(undefined)
                setEngine("")
                setName("")
                setDatabase("")
              }}
            >
              <Database className="size-4" />
              Create another
            </Button>
          )}
        </PanelFooter>
      </Panel>
    )

  return (
    <Panel plain>
      <PanelHeader title="Start a database" />
      <PanelBody className="space-y-4">
        {failure && <ErrorState error={failure} />}
        {startedContainer && failure && (
          <Notice title="Database container already created">
            Retry continues setup for {startedContainer}. You can also open it in Docker.
          </Notice>
        )}
        {progress ? (
          <div className="flex flex-col items-center gap-3 py-10 text-sm text-muted-foreground">
            <Spinner className="size-6 text-primary" />
            {progress}
            <p className="text-hint">
              The first start can take a minute while the image is pulled.
            </p>
          </div>
        ) : (
          <>
            <div className="grid gap-2 sm:grid-cols-3">
              {options?.map((option) => (
                <ChoiceCard
                  disabled={Boolean(startedContainer)}
                  key={option.engine}
                  selected={engine === option.engine}
                  onClick={() => setEngine(option.engine)}
                  className="min-h-0 gap-0.5"
                >
                  <span className="flex items-center gap-1.5 text-body font-medium">
                    <Database className="size-3.5 text-muted-foreground" />
                    {option.label}
                  </span>
                  <span className="truncate font-mono text-micro text-muted-foreground">
                    {option.image}
                  </span>
                </ChoiceCard>
              ))}
              {!options && <Spinner />}
            </div>
            {selected && (
              <div className="grid gap-3 sm:grid-cols-2">
                <QuickField id="db-name" label="Container name" hint={`Defaults to jd-${engine}.`}>
                  <Input
                    id="db-name"
                    value={name}
                    readOnly={Boolean(startedContainer)}
                    onChange={(event) => setName(event.target.value)}
                    placeholder={`jd-${engine}`}
                    className="font-mono"
                  />
                </QuickField>
                <QuickField id="db-database" label="Database name" hint="Defaults to app.">
                  <Input
                    id="db-database"
                    value={database}
                    readOnly={Boolean(startedContainer)}
                    onChange={(event) => setDatabase(event.target.value)}
                    placeholder="app"
                    className="font-mono"
                  />
                </QuickField>
              </div>
            )}
          </>
        )}
      </PanelBody>
      <PanelFooter className="justify-end">
        <Button
          className="h-11 sm:h-9"
          disabled={!selected || Boolean(progress)}
          onClick={() => void create()}
        >
          <Database className="size-4" />
          {startedContainer ? "Retry connection setup" : "Create database"}
        </Button>
      </PanelFooter>
    </Panel>
  )
}
