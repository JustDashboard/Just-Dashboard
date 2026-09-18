"use client"

import { useId, useState } from "react"
import { Copy, Key } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import type { DbConnection, DbCredentialServer, DbDriver } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Field, FieldRow, FormFact, FormFacts, FormNote } from "@/components/form"

/**
 * Connecting a database that is installed on the server rather than running in
 * a container.
 *
 * Everything about it is already known — the engine, the address, the account
 * it conventionally uses — except the one thing that is kept inside the server
 * itself. A container states its credentials in its environment and the
 * dashboard reads them; a Postgres that apt installed keeps its passwords in
 * its own catalogue, and no amount of reading the machine reveals them.
 *
 * So this asks for exactly that, and the request it makes **dials before it
 * saves**. The version before it filled a connection string into the general
 * form with the password left out, which could be saved as it stood: the
 * result was a connection that existed, looked connected, and answered
 * "password authentication failed for user postgres" to everything asked of it
 * afterwards. The engine's refusal belongs here, next to the field that caused
 * it, rather than in a red badge on a row somebody then has to delete.
 */

/**
 * How to give the account a password, per engine, for the case where it has
 * never had one: a stock Postgres and MySQL both authenticate local
 * connections by the operating-system user, so `postgres` and `root` are
 * reachable from a shell and from nowhere else.
 */
const resetHint: Record<string, string> = {
  postgres: `sudo -u postgres psql -c "ALTER USER postgres PASSWORD 'choose-one'"`,
  mysql: `sudo mysql -e "ALTER USER 'root'@'localhost' IDENTIFIED BY 'choose-one'"`,
}

const engineLabel: Record<string, string> = {
  postgres: "PostgreSQL",
  mysql: "MySQL",
  mongodb: "MongoDB",
  redis: "Redis",
  clickhouse: "ClickHouse",
  sqlserver: "SQL Server",
  oracle: "Oracle",
}

export function HostConnectDialog({
  server,
  onOpenChange,
  onConnected,
}: {
  /** The detected server. Mounting with a `key` gives each its own state. */
  server: DbCredentialServer
  onOpenChange: (open: boolean) => void
  onConnected: (name: string) => void
}) {
  const id = useId()
  const [name, setName] = useState(server.name)
  const [user, setUser] = useState(server.user ?? "")
  const [password, setPassword] = useState("")
  const [database, setDatabase] = useState(server.database ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const label = engineLabel[server.driver] ?? server.driver

  const connect = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const conn = await post<DbConnection>("/databases/host", {
        driver: server.driver as DbDriver,
        host: server.host,
        port: server.port,
        user,
        password,
        database,
        name: name.trim(),
      })
      notify.success(`Connected ${conn.name}`)
      onConnected(conn.name)
      onOpenChange(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title={`Connect ${label} on this server`}
      description={`Found listening on ${server.host}:${server.port}. It is not in a container, so its password lives in the server's own catalogue rather than anywhere this dashboard can read.`}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={busy || !name.trim()} onClick={connect} pending={busy}>
            <Key />
            Connect
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <FormFacts>
          <FormFact label="Listening on" mono>
            {server.host}:{server.port}
          </FormFact>
          {server.process && (
            <FormFact label="Process" mono>
              {server.process}
            </FormFact>
          )}
        </FormFacts>

        {error && (
          <Notice title="It refused the connection" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        )}

        <FieldRow>
          <Field label="User" htmlFor={`${id}-user`}>
            <Input
              id={`${id}-user`}
              value={user}
              onChange={(e) => setUser(e.target.value)}
              className="font-mono"
              autoComplete="off"
            />
          </Field>
          <Field label="Database" htmlFor={`${id}-db`} hint="Empty for the server's default.">
            <Input
              id={`${id}-db`}
              value={database}
              onChange={(e) => setDatabase(e.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <Field
          label="Password"
          htmlFor={`${id}-password`}
          hint="Nothing is saved until this connects. The password is sealed on the server with the same key as every other stored one, and never sent back."
        >
          <Input
            id={`${id}-password`}
            type="password"
            autoFocus
            autoComplete="off"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !busy) void connect()
            }}
            className="font-mono"
          />
        </Field>
        {/* The commonest reason somebody is stuck here is that the account has
            no password at all — both engines ship authenticating local
            connections by the operating-system user instead, so there has
            never been one to know. The way out is one line in a shell, and
            naming it is the difference between a dialog and a dead end. */}
        {resetHint[server.driver] && (
          <div className="space-y-1.5">
            <div className="flex items-center justify-between gap-3">
              <p className="eyebrow">Don&apos;t know it?</p>
              <Button
                size="xs"
                variant="ghost"
                onClick={() => void copyText(resetHint[server.driver], "Command copied")}
              >
                <Copy />
                Copy
              </Button>
            </div>
            <Well className="text-hint break-all whitespace-pre-wrap">
              {resetHint[server.driver]}
            </Well>
            <FormNote>Sets one from a shell on this server, then come back here.</FormNote>
          </div>
        )}
        <Field label="Name" htmlFor={`${id}-name`} hint="How it is listed in the picker.">
          <Input id={`${id}-name`} value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
      </div>
    </Modal>
  )
}
