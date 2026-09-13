"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { Database, Warning } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import type { DbConnection, DbProvisionOption } from "@/lib/types"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState, Notice, Spinner } from "@/components/state"
import { ChoiceCard } from "@/components/choice-card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { CopyValue, QuickField } from "@/components/deploy/quick-deploy"

/**
 * A database, and then the one thing anyone wants from it afterwards.
 *
 * Provisioning already existed on the databases page and already did the hard
 * part — generate a password nobody types, find a free port, wait for the
 * engine to actually answer. What it did not do was hand back the connection
 * string, so an operator who had just created a Postgres for an application
 * still had to assemble a DSN by hand from four fields and a password they
 * were never shown. Deploying a database and deploying the thing that talks to
 * it are one task; this finishes it.
 */
export function DatabaseQuickDeploy() {
  const [options, setOptions] = useState<DbProvisionOption[]>()
  const [engine, setEngine] = useState("")
  const [name, setName] = useState("")
  const [database, setDatabase] = useState("")
  const [progress, setProgress] = useState("")
  const [failure, setFailure] = useState<Error>()
  const [created, setCreated] = useState<{ connection: DbConnection; url: string }>()

  useEffect(() => {
    get<DbProvisionOption[]>("/databases/provision/options")
      .then(setOptions)
      .catch((error) => setFailure(error instanceof Error ? error : new Error(String(error))))
  }, [])

  const selected = options?.find((option) => option.engine === engine)

  const create = async () => {
    if (!selected) return
    setFailure(undefined)
    setProgress("Starting the container…")
    try {
      const started = await post<{ container: string }>("/databases/provision", {
        engine: selected.engine,
        name: name.trim() || undefined,
        database: database.trim() || undefined,
      })
      setProgress("Waiting for it to accept connections…")
      // The container exists a second after that request; the engine inside it
      // answers somewhere between seconds and a minute later, and MySQL will
      // accept a connection on a temporary server mid-initialisation and then
      // restart. Adopt proves credentials, ping proves it is really up.
      const deadline = Date.now() + 3 * 60_000
      for (;;) {
        try {
          const connection = await post<DbConnection>("/databases/adopt", {
            container: started.container,
          })
          const alive = await get<{ ok: boolean }>(`/databases/${connection.id}/ping`)
          if (!alive.ok) throw new Error("not accepting connections yet")
          const revealed = await get<{ url: string }>(`/databases/${connection.id}/url`)
          setCreated({ connection, url: revealed.url })
          setProgress("")
          return
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
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)))
      setProgress("")
    }
  }

  if (created)
    return (
      <Panel>
        <PanelHeader title={`${created.connection.name} is ready`} />
        <PanelBody className="space-y-4">
          <CopyValue value={created.url} label="connection string" />
          <Notice icon={Warning} title="This server listens on loopback only">
            It is reachable from containers and processes on this host, and from nowhere else. That
            is deliberate: change the port binding on the Docker page if you need otherwise.
          </Notice>
        </PanelBody>
        <PanelFooter className="justify-between">
          <Button variant="outline" asChild>
            <Link href={`/databases?connection=${encodeURIComponent(created.connection.name)}`}>
              Open in Databases
            </Link>
          </Button>
          <Button
            onClick={() => {
              setCreated(undefined)
              setEngine("")
              setName("")
              setDatabase("")
            }}
          >
            <Database className="size-4" />
            Create another
          </Button>
        </PanelFooter>
      </Panel>
    )

  return (
    <Panel>
      <PanelHeader title="Start a database" />
      <PanelBody className="space-y-4">
        {failure && <ErrorState error={failure} />}
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
                    onChange={(event) => setName(event.target.value)}
                    placeholder={`jd-${engine}`}
                    className="font-mono"
                  />
                </QuickField>
                <QuickField id="db-database" label="Database name" hint="Defaults to app.">
                  <Input
                    id="db-database"
                    value={database}
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
          Create database
        </Button>
      </PanelFooter>
    </Panel>
  )
}
