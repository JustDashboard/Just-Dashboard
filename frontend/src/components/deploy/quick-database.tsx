"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { Database, Eye, EyeOff, Copy, Warning } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import type {
  DbConnection,
  DbProvisionOption,
  DeploymentDatabaseConnectionFormat,
} from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { ChoiceGrid, EngineCard, driverKind } from "@/components/choice-card"
import { Field, FieldRow, FormFact, FormFacts, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { RunPhases, phaseStates } from "@/components/run-phases"
import { SidePanelFooter, useInSidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { useCopy } from "@/hooks/use-copy"
import { cn } from "@/lib/utils"

/**
 * A database started on this server in one step: an engine, two optional
 * names, and a connection string to take away — or to hand straight to the
 * project that asked for one.
 *
 * Starting one is three things that take between seconds and a minute each,
 * and it used to be a centred spinner and a sentence that changed under it.
 * The engines stay drawn now, the one being started with a light running
 * round its edge (§11 *live*), and under them the three stages as the
 * release path draws a run's — the one at work lit, the one that failed red
 * — so what is happening and how much is left read at a glance. The result
 * is a plain panel, since both places this is drawn — a sheet, and
 * `/deploy/new`'s focused surface — already give it its edge; in the sheet
 * its commands sit in the sheet's footer, beside the sheet's Cancel.
 */

const PHASES = ["Start the container", "Wait for connections", "Prepare the connection string"]

export function DatabaseQuickDeploy({
  target = "host",
  canConnect = true,
  onConnect,
  resume,
  onStarted,
  onReset,
  initialEngine,
  format,
}: {
  target?: "host" | "container"
  canConnect?: boolean
  resume?: { container: string; engine: string }
  onStarted?: (started: { container: string; engine: string }) => void
  onReset?: () => void
  onConnect?: (connection: DbConnection, url: string) => void
  /** The engine detection found the source connecting to, preselected. */
  initialEngine?: string
  /** The connection shape the application parses when it is not a URL. */
  format?: DeploymentDatabaseConnectionFormat
}) {
  const inSheet = useInSidePanel()
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
  // The stage at work while a start is in flight, and where the last one
  // stopped when it failed.
  const [phase, setPhase] = useState<number>()
  const [failedAt, setFailedAt] = useState<number>()
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
  const inFlight = phase !== undefined

  const create = async () => {
    if (!selected || provisioning.current) return
    provisioning.current = true
    setFailure(undefined)
    setFailedAt(undefined)
    let at = 0
    const reach = (next: number) => {
      at = next
      setPhase(next)
    }
    const fail = (error: unknown) => {
      setFailure(error instanceof Error ? error : new Error(String(error)))
      setFailedAt(at)
      setPhase(undefined)
    }
    reach(0)
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
      reach(1)
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
            fail(
              new Error(
                `${selected.label} started but did not become reachable: ${errorMessage(error)}`,
              ),
            )
            return
          }
          await new Promise((resolve) => setTimeout(resolve, 2000))
        }
      }
      if (!alive.current) return
      reach(2)
      const address = await get<{ url: string; reference?: string }>(
        `/databases/${connection.id}/url`,
        { target, format },
      )
      if (!alive.current) return
      setCreated({ connection, url: address.url, reference: address.reference })
      setPhase(undefined)
    } catch (error) {
      if (!alive.current) return
      fail(error)
    } finally {
      provisioning.current = false
    }
  }

  if (created) {
    const { connection } = created
    const address = connection.host
      ? `${connection.host}${connection.port ? `:${connection.port}` : ""}`
      : undefined
    return (
      <Panel plain className="animate-rise">
        <PanelHeader title={`${connection.name} is ready`} />
        <PanelBody className="space-y-5">
          {/* The ping answered before this panel could be drawn, so the state
              is a reading, not a hope. */}
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <ProductLogo size="sm" id={connection.driver} fallback={Database} />
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="truncate text-body font-medium">{connection.name}</p>
              {(address || connection.database) && (
                <FormFacts>
                  {address && (
                    <FormFact label="Host" mono>
                      {address}
                    </FormFact>
                  )}
                  {connection.database && (
                    <FormFact label="Database" mono>
                      {connection.database}
                    </FormFact>
                  )}
                </FormFacts>
              )}
            </div>
            {/* The one reading this panel exists for, so a phone keeps it —
                under the name when the line has no room beside it. */}
            <Status
              tone="running"
              label="Accepting connections"
              className="max-sm:basis-full max-sm:pl-11"
            />
          </div>
          <Field
            label="Connection string"
            htmlFor="database-connection-string"
            hint={
              target === "container"
                ? "Use this database to connect the application over its managed private network. The saved connection follows replacement database containers."
                : "This address is for processes on the host. Use Add database during project setup to get the address for an application container."
            }
          >
            <InputGroup>
              <InputGroupInput
                id="database-connection-string"
                type={revealed ? "text" : "password"}
                readOnly
                value={created.url}
                className="font-mono"
              />
              <InputGroupAddon align="inline-end" className="gap-0 p-0">
                <InputGroupButton
                  aria-label={revealed ? "Hide connection string" : "Reveal connection string"}
                  onClick={() => setRevealed(!revealed)}
                >
                  {revealed ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
                </InputGroupButton>
                <InputGroupButton
                  aria-label="Copy connection string"
                  onClick={() => copy(created.url, "Connection string copied")}
                >
                  <Copy className="size-3.5" />
                  <span className="max-sm:hidden">Copy</span>
                </InputGroupButton>
              </InputGroupAddon>
            </InputGroup>
          </Field>
        </PanelBody>
        <Foot className="justify-between">
          <Button variant="outline" asChild>
            <Link href={`/databases/overview?conn=${connection.id}`}>Open in Databases</Link>
          </Button>
          {onConnect ? (
            <Button
              disabled={!canConnect}
              onClick={() => onConnect(connection, created.reference || created.url)}
            >
              Use this database
            </Button>
          ) : (
            <Button
              onClick={() => {
                setCreated(undefined)
                setRevealed(false)
                setStartedContainer(undefined)
                setFailedAt(undefined)
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
        </Foot>
      </Panel>
    )
  }

  // A failure while starting is one of two things: nothing was made, or a
  // container was made and the rest did not follow — in which case Retry
  // resumes it rather than starting a second one, and the notice says so.
  const startFailure =
    failure &&
    options &&
    (startedContainer ? (
      <Notice tone="danger" icon={Warning} title="Database container already created">
        <p className="text-foreground/85">{failure.message}</p>
        <p className="mt-1">
          Retry continues setup for {startedContainer}. You can also open it in Docker.
        </p>
      </Notice>
    ) : (
      <ErrorState error={failure} />
    ))

  return (
    <Panel plain>
      <PanelHeader title="Start a database" />
      <PanelBody className="space-y-4">
        {failure && !options && <ErrorState error={failure} />}
        {startFailure}
        {/* The engine's own logo, as the template catalogue draws it: five
            identical database glyphs in front of five names said "database"
            five times and nothing about which. Two to a row on a phone, where
            one to a row put Create below the fold; every card is one height,
            whatever its image tag's length. */}
        <ChoiceGrid
          columns="fill"
          className="grid-cols-2 gap-2 sm:grid-cols-[repeat(auto-fit,minmax(11rem,1fr))]"
        >
          {options?.map((option) => (
            <EngineCard
              key={option.engine}
              engine={option.engine}
              label={option.label}
              kind={driverKind(option.driver)}
              detail={option.image}
              disabled={(Boolean(startedContainer) || inFlight) && engine !== option.engine}
              selected={engine === option.engine}
              working={inFlight && engine === option.engine}
              onClick={() => !inFlight && setEngine(option.engine)}
            />
          ))}
          {!options && !failure && <Spinner />}
        </ChoiceGrid>
        {selected && (
          <FieldRow className="max-w-3xl">
            <Field
              label="Container name"
              htmlFor="db-name"
              hint={`Defaults to an available name starting with jd-${engine}.`}
            >
              <Input
                id="db-name"
                value={name}
                readOnly={Boolean(startedContainer) || inFlight}
                onChange={(event) => setName(event.target.value)}
                placeholder={`jd-${engine}`}
                className="font-mono"
              />
            </Field>
            <Field label="Database name" htmlFor="db-database" hint="Defaults to app.">
              <Input
                id="db-database"
                value={database}
                readOnly={Boolean(startedContainer) || inFlight}
                onChange={(event) => setDatabase(event.target.value)}
                placeholder="app"
                className="font-mono"
              />
            </Field>
          </FieldRow>
        )}
        {(inFlight || failedAt !== undefined) && (
          <div className="min-w-0 animate-rise space-y-2 pt-1">
            <RunPhases
              phases={phaseStates(PHASES, phase ?? failedAt ?? 0, inFlight ? "running" : "failed")}
            />
            {inFlight && phase! < 2 && (
              <FormNote>The first start can take a minute while the image is pulled.</FormNote>
            )}
          </div>
        )}
      </PanelBody>
      <Foot className="justify-end">
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
            // A thumb's height where it stands alone at the panel's foot; in a
            // sheet's footer it is the size of the Cancel beside it.
            className={cn(!inSheet && "h-11 sm:h-9")}
            disabled={!selected || inFlight}
            pending={inFlight}
            onClick={() => void create()}
          >
            <Database className="size-4" />
            {inFlight
              ? "Starting…"
              : startedContainer
                ? "Retry connection setup"
                : "Create database"}
          </Button>
        )}
      </Foot>
    </Panel>
  )
}

/**
 * The commands, in the sheet's footer after its Cancel when this is drawn in
 * a sheet, and at the panel's own foot on `/deploy/new`'s surface.
 */
function Foot({ className, children }: { className?: string; children: React.ReactNode }) {
  return useInSidePanel() ? (
    <SidePanelFooter>{children}</SidePanelFooter>
  ) : (
    <PanelFooter className={className}>{children}</PanelFooter>
  )
}
