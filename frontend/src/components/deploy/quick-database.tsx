"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { Database, Eye, EyeOff, Copy } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import type { DbConnection, DbProvisionOption } from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { ChoiceGrid, EngineCard, driverKind } from "@/components/choice-card"
import { Field } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useCopy } from "@/hooks/use-copy"

export function DatabaseQuickDeploy({
  target = "host",
  canConnect = true,
  onConnect,
  resume,
  onStarted,
  onReset,
  initialEngine,
}: {
  target?: "host" | "container"
  canConnect?: boolean
  resume?: { container: string; engine: string }
  onStarted?: (started: { container: string; engine: string }) => void
  onReset?: () => void
  onConnect?: (connection: DbConnection, url: string) => void
  /** The engine detection found the source connecting to, preselected. */
  initialEngine?: string
}) {
  const alive = useRef(true)
  const provisioning = useRef(false)
  const [startedContainer, setStartedContainer] = useState<string | undefined>(resume?.container)
  const [revealed, setRevealed] = useState(false)
  const { copy } = useCopy()
  const [options, setOptions] = useState<DbProvisionOption[]>()
  const [optionsAttempt, setOptionsAttempt] = useState(0)
  const [engine, setEngine] = useState(resume?.engine ?? initialEngine ?? "")
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
    const controller = new AbortController()
    get<DbProvisionOption[]>("/databases/provision/options", undefined, controller.signal)
      .then(setOptions)
      .catch((error) => {
        if (!controller.signal.aborted)
          setFailure(error instanceof Error ? error : new Error(String(error)))
      })
    return () => controller.abort()
  }, [optionsAttempt])

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
            // The application reaches it over the deployment network; nothing
            // outside this server needs the port.
            exposure: "local",
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
          const health = await get<{ ok: boolean; error?: string }>(
            `/databases/${connection.id}/ping`,
          )
          // The engine's own refusal, when there is one: "password
          // authentication failed" is a different problem from "not up yet",
          // and hiding it behind the same sentence sent people looking at
          // the wrong thing for three minutes.
          if (!health.ok) throw new Error(health.error || "not accepting connections yet")
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
          <Field
            label="Connection string"
            htmlFor="database-connection-string"
            hint={
              target === "container"
                ? "Use this database to connect the application over its managed private network. The saved connection follows replacement database containers."
                : "This address is for processes on the host. Use Add database during project setup to get the address for an application container."
            }
          >
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
          </Field>
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
                setRevealed(false)
                setStartedContainer(undefined)
                setEngine("")
                setName("")
                setDatabase("")
                onReset?.()
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
            {/* The engine's own logo, as the template catalogue draws it: five
                identical database glyphs in front of five names said
                "database" five times and nothing about which. Every card is
                one height, whatever its image tag's length. */}
            <ChoiceGrid
              columns="fill"
              className="grid-cols-[repeat(auto-fit,minmax(11rem,1fr))] gap-2"
            >
              {options?.map((option) => (
                <EngineCard
                  key={option.engine}
                  engine={option.engine}
                  label={option.label}
                  kind={driverKind(option.driver)}
                  detail={option.image}
                  disabled={Boolean(startedContainer)}
                  selected={engine === option.engine}
                  onClick={() => setEngine(option.engine)}
                />
              ))}
              {!options && !failure && <Spinner />}
            </ChoiceGrid>
            {selected && (
              <div className="grid max-w-3xl gap-3 sm:grid-cols-2">
                <Field
                  label="Container name"
                  htmlFor="db-name"
                  hint={`Defaults to an available name starting with jd-${engine}.`}
                >
                  <Input
                    id="db-name"
                    value={name}
                    readOnly={Boolean(startedContainer)}
                    onChange={(event) => setName(event.target.value)}
                    placeholder={`jd-${engine}`}
                    className="font-mono"
                  />
                </Field>
                <Field label="Database name" htmlFor="db-database" hint="Defaults to app.">
                  <Input
                    id="db-database"
                    value={database}
                    readOnly={Boolean(startedContainer)}
                    onChange={(event) => setDatabase(event.target.value)}
                    placeholder="app"
                    className="font-mono"
                  />
                </Field>
              </div>
            )}
          </>
        )}
      </PanelBody>
      <PanelFooter className="justify-end">
        {!options && failure ? (
          <Button
            onClick={() => {
              setFailure(undefined)
              setOptionsAttempt((attempt) => attempt + 1)
            }}
          >
            Retry loading database engines
          </Button>
        ) : (
          <Button
            className="h-11 sm:h-9"
            disabled={!selected || Boolean(progress)}
            onClick={() => void create()}
          >
            <Database className="size-4" />
            {startedContainer ? "Retry connection setup" : "Create database"}
          </Button>
        )}
      </PanelFooter>
    </Panel>
  )
}
