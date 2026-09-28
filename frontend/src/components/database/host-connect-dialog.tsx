"use client"

import { useId, useState } from "react"
import { Copy, Key, Sparkles } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import type { DbConnection, DbCredentialServer, DbDriver } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { suggestPassword } from "@/components/database/server-tab"

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
 * So this asks for exactly that — or, since the dashboard has a root shell on
 * this machine, *makes* one: the second card runs the engine's own client as
 * its system account over the Unix socket, where peer authentication needs
 * no password, creates (or resets) an account with a password generated
 * here, and connects with that. It is the way in for the commonest case,
 * a server installed with apt an hour ago whose `postgres` role has never
 * been given a password at all.
 *
 * Either way the request **dials before it saves**. The version before it filled a connection string into the general
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

/** The engines whose account can be made from the host's own shell. */
const CAN_GRANT = new Set(["postgres", "mysql", "mongodb", "clickhouse", "redis"])

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
  const grantable = CAN_GRANT.has(server.driver)
  const [mode, setMode] = useState<"password" | "grant">(grantable ? "grant" : "password")
  const [name, setName] = useState(server.name)
  const [user, setUser] = useState(server.user ?? "")
  const [password, setPassword] = useState("")
  const [database, setDatabase] = useState(server.database ?? "")
  // The account the dashboard makes for itself, and the password it gives it.
  const [account, setAccount] = useState("just_dashboard")
  const [accountPassword, setAccountPassword] = useState(suggestPassword)
  const [superuser, setSuperuser] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const label = engineLabel[server.driver] ?? server.driver
  const redis = server.driver === "redis"

  const connect = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const conn =
        mode === "grant"
          ? await post<DbConnection>("/databases/host/grant", {
              driver: server.driver as DbDriver,
              host: server.host,
              port: server.port,
              user: redis ? "" : account,
              password: redis ? "" : accountPassword,
              database,
              name: name.trim(),
              superuser,
            })
          : await post<DbConnection>("/databases/host", {
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
          <Button
            disabled={
              busy ||
              !name.trim() ||
              (mode === "grant" && !redis && (!account.trim() || accountPassword.length < 8))
            }
            onClick={connect}
            pending={busy}
          >
            {mode === "grant" ? <Sparkles /> : <Key />}
            {mode === "grant" ? "Make the account and connect" : "Connect"}
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

        {grantable && (
          <ChoiceGrid columns={2}>
            <ChoiceCard
              verb="Make an account from this server"
              selected={mode === "grant"}
              onClick={() => setMode("grant")}
              mark={Sparkles}
              title={
                redis ? "Read its password from the server" : "Make an account from this server"
              }
              description={
                redis
                  ? "The dashboard reads requirepass out of the server's configuration file as root and connects with it."
                  : "The dashboard runs the engine's own client as its system account over the socket, where no password is needed, and creates one there."
              }
            />
            <ChoiceCard
              verb="I know a password"
              selected={mode === "password"}
              onClick={() => setMode("password")}
              mark={Key}
              title="I know a password"
              description="Sign in with an account that already has one."
            />
          </ChoiceGrid>
        )}

        {mode === "grant" && !redis && (
          <>
            <FieldRow>
              <Field
                label="Account to make"
                htmlFor={`${id}-account`}
                hint="Created if missing; its password is reset if it exists."
              >
                <Input
                  id={`${id}-account`}
                  value={account}
                  onChange={(e) => setAccount(e.target.value)}
                  className="font-mono"
                  autoComplete="off"
                />
              </Field>
              <Field label="Database" htmlFor={`${id}-gdb`} hint="Empty for the server's default.">
                <Input
                  id={`${id}-gdb`}
                  value={database}
                  onChange={(e) => setDatabase(e.target.value)}
                  className="font-mono"
                />
              </Field>
            </FieldRow>
            <Field
              label="Its password"
              htmlFor={`${id}-gpw`}
              hint="Generated here, sealed on the server with every other stored one, and never shown again — copy it now if something else will use this account."
              trailing={
                <span className="flex items-center gap-1">
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() => setAccountPassword(suggestPassword())}
                  >
                    Generate
                  </Button>
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() => void copyText(accountPassword, "Password copied")}
                  >
                    <Copy />
                    Copy
                  </Button>
                </span>
              }
            >
              <Input
                id={`${id}-gpw`}
                value={accountPassword}
                onChange={(e) => setAccountPassword(e.target.value)}
                className="font-mono"
                autoComplete="new-password"
              />
            </Field>
            <OptionList>
              <OptionRow
                title="Administrator of the whole server"
                hint="What the Server page needs to manage accounts, databases and extensions. Off makes a plain login that can be granted databases later."
                checked={superuser}
                onCheckedChange={setSuperuser}
              />
            </OptionList>
          </>
        )}

        {mode === "grant" && redis && (
          <FormNote>
            Redis has one password rather than accounts. It is read from the server&apos;s
            configuration on this machine; an open server connects as it is.
          </FormNote>
        )}

        {mode === "password" && (
          <>
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
          </>
        )}
        {mode === "password" && resetHint[server.driver] && (
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
