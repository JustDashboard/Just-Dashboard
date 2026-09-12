"use client"

import { useState } from "react"
import { Database, Linked } from "@/components/icons"
import { notify } from "@/lib/toast"
import { errorMessage, get, post } from "@/lib/api"
import type { DbConnection, DbProvisionOption } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/state"
import { Modal } from "@/components/modal"
import { ChoiceCard } from "@/components/choice-card"

/**
 * Making a database, which is the thing somebody on this page actually wants.
 *
 * The databases already running on this server connect themselves, so the only
 * question left is "give me a new one" — and that needs an engine and nothing
 * else. Everything a connection string would have carried is decided here and
 * read back off the container afterwards: the port is the next one free, the
 * password is generated and never shown because nobody has to type it again.
 *
 * The other case has not gone away, it has stopped being the default. A managed
 * Postgres, a database on another machine, a SQLite file — none of those are
 * containers on this host, so none can be detected, and the way to them is one
 * line at the bottom rather than a button of its own competing for the header.
 */
export function NewDatabaseDialog({
  open,
  onOpenChange,
  onCreated,
  onConnectManually,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (name: string) => void
  /** Swap to the connection form for something this host cannot detect. */
  onConnectManually: () => void
}) {
  const options = usePoll(
    (signal) => get<DbProvisionOption[]>("/databases/provision/options", undefined, signal),
    0,
  )
  const [engine, setEngine] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [database, setDatabase] = useState("")
  const [busy, setBusy] = useState<string | null>(null)

  const selected = options.data?.find((o) => o.engine === engine)

  /**
   * Start it, then keep asking to connect until it answers.
   *
   * The container is running a second after the request returns; the engine
   * inside it accepts connections some seconds to a minute later, and there is
   * no way to know which but to ask. Waiting here rather than holding the
   * request open is what keeps a slow first boot looking like progress instead
   * of a hung dashboard.
   */
  const create = async () => {
    if (!selected) return
    setBusy("Starting the container…")
    try {
      const started = await post<{ container: string }>("/databases/provision", {
        engine: selected.engine,
        name: name.trim() || undefined,
        database: database.trim() || undefined,
      })
      setBusy("Waiting for it to accept connections…")
      const deadline = Date.now() + 3 * 60_000
      for (;;) {
        try {
          const conn = await post<DbConnection>("/databases/adopt", {
            container: started.container,
          })
          // Adopting proves the engine accepted one connection, which is not
          // the same as being ready: MySQL and MariaDB accept connections on a
          // temporary server during their own first-boot initialisation and
          // then restart, so the first thing the dashboard asks for after that
          // fails. Waiting for a ping that actually dials is what makes "it is
          // ready" true.
          const alive = await get<{ ok: boolean }>(`/databases/${conn.id}/ping`)
          if (!alive.ok) throw new Error("not accepting connections yet")
          notify.success(`${selected.label} is ready`, { description: `Connected as ${conn.name}` })
          onCreated(conn.name)
          onOpenChange(false)
          return
        } catch (err) {
          if (Date.now() > deadline) {
            notify.error(
              `${selected.label} started but did not become reachable`,
              errorMessage(err),
            )
            onOpenChange(false)
            return
          }
          await new Promise((r) => setTimeout(r, 2000))
        }
      }
    } catch (err) {
      notify.error("Could not create the database", err)
    } finally {
      setBusy(null)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title="New database"
      footer={
        <>
          {/* The manual form has not gone away, it has stopped being the
              default: a managed Postgres or a database on another machine is
              not a container here and cannot be detected. */}
          <Button
            variant="link"
            size="sm"
            className="px-0 text-muted-foreground"
            disabled={busy !== null}
            onClick={() => {
              onOpenChange(false)
              onConnectManually()
            }}
          >
            <Linked className="size-3.5" />
            Connect one somewhere else instead
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" disabled={busy !== null} onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button disabled={!selected || busy !== null} onClick={create}>
              <Database className="size-3.5" />
              Create
            </Button>
          </div>
        </>
      }
    >
      {busy ? (
        <div className="flex flex-col items-center gap-3 py-8 text-sm text-muted-foreground">
          <Spinner className="size-6 text-primary" />
          {busy}
          <p className="text-hint">
            It connects itself when it is ready. This can take a minute the first time, while the
            image is pulled.
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-2">
            {options.data?.map((o) => (
              <ChoiceCard
                key={o.engine}
                selected={engine === o.engine}
                onClick={() => setEngine(o.engine)}
                className="min-h-0 gap-0.5"
              >
                <span className="flex items-center gap-1.5 text-body font-medium">
                  <Database className="size-3.5 text-muted-foreground" />
                  {o.label}
                </span>
                <span className="truncate font-mono text-micro text-muted-foreground">
                  {o.image}
                </span>
              </ChoiceCard>
            ))}
            {!options.data && <Spinner />}
          </div>

          {selected && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="newdb-name">Name</Label>
                <Input
                  id="newdb-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={`jd-${selected.engine}`}
                  className="font-mono text-xs"
                />
              </div>
              {selected.driver !== "redis" && (
                <div className="space-y-1.5">
                  <Label htmlFor="newdb-database">Database</Label>
                  <Input
                    id="newdb-database"
                    value={database}
                    onChange={(e) => setDatabase(e.target.value)}
                    placeholder="app"
                    className="font-mono text-xs"
                  />
                </div>
              )}
            </div>
          )}

          <p className="text-hint leading-relaxed text-muted-foreground">
            Runs on this server, published to localhost only, with a generated password you never
            have to type. It appears in the picker as soon as it answers.
          </p>
        </div>
      )}
    </Modal>
  )
}
