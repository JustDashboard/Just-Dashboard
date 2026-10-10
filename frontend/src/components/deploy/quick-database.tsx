"use client"

import { useEffect, useRef, useState } from "react"
import { Database, Warning } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import type {
  DbConnection,
  DbProvisionOption,
  DeploymentDatabaseConnectionFormat,
} from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { ChoiceGrid, EngineCard } from "@/components/choice-card"
import { engineOf } from "@/components/database/engine"
import { Field, FieldRow, FormNote } from "@/components/form"
import { RunPhases, phaseStates } from "@/components/run-phases"
import { SidePanelFooter, useInSidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { DatabaseReady } from "@/components/deploy/database-ready"
import { cn } from "@/lib/utils"

/**
 * The compact creation form inside a project's Add database sheet. It waits
 * for adoption and a fresh ping before offering the managed application URL,
 * and resumes the existing container if preparing that URL fails.
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
    return (
      <DatabaseReady
        created={created}
        target={target}
        canConnect={canConnect}
        onConnect={onConnect}
        onReset={() => {
          setCreated(undefined)
          setStartedContainer(undefined)
          setFailedAt(undefined)
          setEngine("")
          setName("")
          setDatabase("")
          onReset?.()
        }}
      />
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
              kind={engineOf(option.driver).kind}
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
