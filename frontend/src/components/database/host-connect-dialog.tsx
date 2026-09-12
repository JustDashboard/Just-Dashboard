"use client"

import { useState } from "react"
import { Key, Servers } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection, DbCredentialServer, DbDriver } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"

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
  const [name, setName] = useState(server.name)
  const [user, setUser] = useState(server.user ?? "")
  const [password, setPassword] = useState("")
  const [database, setDatabase] = useState(server.database ?? "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

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
      size="sm"
      icon={Servers}
      title="Connect {server.driver} on this server"
      description={
        <>
          Found listening on{" "}
          <span className="font-mono">
            {server.host}:{server.port}
          </span>
          {server.process ? ` as ${server.process}` : ""}. It is not in a container, so its password
          lives in the server&apos;s own catalogue rather than anywhere this dashboard can read.
        </>
      }
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={busy || !name.trim()} onClick={connect} pending={busy}>
            <Key className="size-4" />
            Connect
          </Button>
        </>
      }
    >
      {error && (
        <Notice title="It refused the connection" tone="danger">
          <span className="break-words whitespace-pre-wrap">{error}</span>
        </Notice>
      )}

      <div className="grid gap-3">
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="host-user">User</Label>
            <Input
              id="host-user"
              value={user}
              onChange={(e) => setUser(e.target.value)}
              className="font-mono text-xs"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="host-db">Database</Label>
            <Input
              id="host-db"
              value={database}
              onChange={(e) => setDatabase(e.target.value)}
              placeholder="(the server's default)"
              className="font-mono text-xs"
            />
          </div>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="host-password">Password</Label>
          <Input
            id="host-password"
            type="password"
            autoFocus
            autoComplete="off"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !busy) void connect()
            }}
            className="font-mono text-xs"
          />
          <p className="text-hint text-muted-foreground">
            Nothing is saved until this connects. The password is sealed on the server with the same
            key as every other stored one, and never sent back.
          </p>
          {/* The commonest reason somebody is stuck here is that the account
                has no password at all — both engines ship authenticating local
                connections by the operating-system user instead, so there has
                never been one to know. The way out is one line in a shell, and
                naming it is the difference between a dialog and a dead end. */}
          {resetHint[server.driver] && (
            <p className="text-hint text-muted-foreground">
              Don&apos;t know it? Set one from a shell on this server:{" "}
              <code className="font-mono break-all">{resetHint[server.driver]}</code>
            </p>
          )}
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="host-name">Name it</Label>
          <Input id="host-name" value={name} onChange={(e) => setName(e.target.value)} />
        </div>
      </div>
    </Modal>
  )
}
