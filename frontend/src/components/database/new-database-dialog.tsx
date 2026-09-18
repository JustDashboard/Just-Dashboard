"use client"

import { useId, useState } from "react"
import { Database, Linked } from "@/components/icons"
import { notify } from "@/lib/toast"
import { errorMessage, get, post } from "@/lib/api"
import type { DbConnection, DbProvisionOption } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/state"
import { Modal } from "@/components/modal"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"

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
  const id = useId()
  const options = usePoll(
    (signal) => get<DbProvisionOption[]>("/databases/provision/options", undefined, signal),
    0,
  )
  const [engine, setEngine] = useState<string | null>(null)
  const [name, setName] = useState("")
  const [database, setDatabase] = useState("")
  // On by default: a database made here exists to be handed to somebody, and
  // a string that only works from this server is the wrong thing to hand over.
  const [shared, setShared] = useState(true)
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
      const started = await post<{ container: string; firewallError?: string }>(
        "/databases/provision",
        {
          engine: selected.engine,
          name: name.trim() || undefined,
          database: database.trim() || undefined,
          exposure: shared ? "public" : "local",
        },
      )
      if (started.firewallError)
        notify.warning("The firewall was not opened", { description: started.firewallError })
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
          const alive = await get<{ ok: boolean; error?: string }>(`/databases/${conn.id}/ping`)
          if (!alive.ok) throw new Error(alive.error || "not accepting connections yet")
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
      size="lg"
      title="New database"
      description="Starts a database in a container on this server and connects it."
      footer={
        <>
          {/* The manual form has not gone away, it has stopped being the
              default: a managed Postgres or a database on another machine is
              not a container here and cannot be detected. */}
          <Button
            variant="ghost"
            size="sm"
            className="mr-auto text-muted-foreground"
            disabled={busy !== null}
            onClick={() => {
              onOpenChange(false)
              onConnectManually()
            }}
          >
            <Linked />
            Connect one somewhere else instead
          </Button>
          <Button variant="ghost" disabled={busy !== null} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!selected || busy !== null} onClick={create} pending={busy !== null}>
            <Database />
            Create
          </Button>
        </>
      }
    >
      {busy ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <Spinner className="size-5 text-muted-foreground" />
          <p className="text-body font-medium">{busy}</p>
          <FormNote className="max-w-sm">
            It connects itself when it is ready. This can take a minute the first time, while the
            image is pulled.
          </FormNote>
        </div>
      ) : (
        <div className="grid gap-5">
          <FormSection title="Engine">
            {!options.data ? (
              <Spinner className="text-muted-foreground" />
            ) : (
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                {options.data.map((o) => (
                  <ChoiceCard
                    key={o.engine}
                    selected={engine === o.engine}
                    onClick={() => setEngine(o.engine)}
                    className="min-h-0 gap-0.5 px-2.5 py-2"
                  >
                    <ChoiceCardTitle className="truncate">{o.label}</ChoiceCardTitle>
                    <ChoiceCardHint className="w-full truncate font-mono">{o.image}</ChoiceCardHint>
                  </ChoiceCard>
                ))}
              </div>
            )}
          </FormSection>

          <FieldRow>
            <Field
              label="Name"
              htmlFor={`${id}-name`}
              hint="The container's name, and how it is listed here."
            >
              <Input
                id={`${id}-name`}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={selected ? `jd-${selected.engine}` : "jd-postgres"}
                className="font-mono"
                disabled={!selected}
              />
            </Field>
            <Field
              label="Database"
              htmlFor={`${id}-database`}
              hint={
                selected?.driver === "redis"
                  ? "Redis numbers its keyspaces itself."
                  : "Created at first boot, empty."
              }
            >
              <Input
                id={`${id}-database`}
                value={database}
                onChange={(e) => setDatabase(e.target.value)}
                placeholder="app"
                className="font-mono"
                disabled={!selected || selected.driver === "redis"}
              />
            </Field>
          </FieldRow>

          <OptionList>
            <OptionRow
              title="Reachable from anywhere"
              hint="Publishes the port on every interface and opens the firewall for it, so the connection string works from your own machine or anyone you share it with. Off keeps it to this server; it can be opened later under Maintenance."
              checked={shared}
              onCheckedChange={setShared}
              disabled={!selected}
            />
          </OptionList>

          <FormNote>
            Runs on this server with a generated password you never have to type. It appears in the
            picker as soon as it answers.
          </FormNote>
        </div>
      )}
    </Modal>
  )
}
