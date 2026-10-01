"use client"

import { useId, useState } from "react"
import { Copy, Key, RefreshClockwise, Sparkles } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import type { DbConnection } from "@/lib/types"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import type { Engine } from "@/components/database/engine"
import { instanceWhere } from "@/components/database/connect/inventory"
import {
  ACCOUNT_NAME,
  CONNECTION_NAME,
  generatePassword,
} from "@/components/database/connect/rules"
import type { DbHostGrantRequest, DbInstance } from "@/components/database/fleet/types"
import { EngineMark } from "@/components/database/kit"

/**
 * Connecting a server that is on this machine and states no password.
 *
 * Everything about it is already known — the engine, the address, the account
 * it conventionally uses — except the one thing kept inside the server
 * itself. A container states its credentials in its environment and the
 * dashboard reads them; a PostgreSQL that apt installed keeps its passwords
 * in its own catalogue, and no amount of reading the machine reveals them.
 *
 * So this asks for exactly that. Where the engine authenticates local
 * connections by the operating-system user, there is a second way in, and it
 * is the commoner one for a server installed an hour ago whose account has
 * never had a password at all: the dashboard runs the engine's own client on
 * this machine, makes an account, and connects with that.
 *
 * Either way the server signs in before it saves, so the engine's refusal is
 * shown here, beside the field that caused it, rather than as a connection
 * that exists and answers nothing.
 */
export function FoundConnectDialog({
  instance,
  engine,
  choice,
  message,
  onClose,
  onConnected,
}: {
  instance: DbInstance
  engine: Engine
  /** Whether an account can be made from this machine as well. */
  choice: boolean
  /** What the server said when it was tried with what the instance states. */
  message?: string
  onClose: () => void
  onConnected: (connection: DbConnection) => void
}) {
  const id = useId()
  const [mode, setMode] = useState<"account" | "password">(choice ? "account" : "password")
  const [name, setName] = useState("")
  const [user, setUser] = useState(instance.user ?? "")
  const [password, setPassword] = useState("")
  const [database, setDatabase] = useState(instance.database ?? "")
  const [account, setAccount] = useState("just_dashboard")
  const [accountPassword, setAccountPassword] = useState(generatePassword)
  const [superuser, setSuperuser] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const where = instanceWhere(instance)
  // A key–value store has one password rather than accounts: there is none
  // to make, and what the dashboard does from here is read the one there is.
  const accounts = engine.kind !== "keyvalue"
  const endpoint = instance.endpoints.find((one) => one.primary)
  const nameOk = name.trim() === "" || CONNECTION_NAME.test(name.trim())
  const ready =
    nameOk &&
    (mode === "password"
      ? password !== ""
      : !accounts || (ACCOUNT_NAME.test(account.trim()) && accountPassword.length >= 8))

  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setError(undefined)
    try {
      const connection =
        mode === "account" && endpoint?.port && instance.driver !== ""
          ? await post<DbConnection>("/databases/host/grant", {
              driver: instance.driver,
              host: endpoint.host ?? "127.0.0.1",
              port: endpoint.port,
              user: accounts ? account.trim() : "",
              password: accounts ? accountPassword : "",
              database: database.trim(),
              name: name.trim(),
              superuser,
            } satisfies DbHostGrantRequest)
          : await post<DbConnection>("/databases/inventory/connect", {
              key: instance.key,
              name: name.trim() || undefined,
              user: user.trim() || undefined,
              password,
              database: database.trim() || undefined,
            })
      onConnected(connection)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const databaseField = (suffix: string) => (
    <Field
      label={engine.databaseField}
      htmlFor={`${id}-${suffix}`}
      hint="Empty for the server's default."
    >
      <Input
        id={`${id}-${suffix}`}
        value={database}
        onChange={(event) => setDatabase(event.target.value)}
        className="font-mono"
        autoComplete="off"
        spellCheck={false}
      />
    </Field>
  )

  return (
    <Modal
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={`Connect ${instance.name}`}
      description={`${engine.label} was found on this server and states no password the dashboard can read.`}
      footer={
        <>
          <p className="mr-auto min-w-0 flex-1 basis-40 text-hint text-muted-foreground">
            Nothing is saved until the sign-in works.
          </p>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!ready} pending={busy} onClick={() => void submit()}>
            {mode === "account" ? <Sparkles /> : <Key />}
            {mode === "account"
              ? accounts
                ? "Make the account and connect"
                : "Read it and connect"
              : "Connect"}
          </Button>
        </>
      }
    >
      <div className="grid gap-5">
        <div className="flex min-w-0 items-center gap-3">
          <EngineMark engine={engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate text-body font-medium">
              {engine.label}
              {instance.version ? ` ${instance.version}` : ""}
            </p>
            <FormFacts>
              <FormFact label={where.kind} mono>
                {where.text}
              </FormFact>
              {instance.host?.user && (
                <FormFact label="runs as" mono>
                  {instance.host.user}
                </FormFact>
              )}
            </FormFacts>
          </div>
        </div>

        {error ? (
          <Notice title="It refused the sign-in" tone="danger">
            <span className="break-words whitespace-pre-wrap">{error}</span>
          </Notice>
        ) : (
          message && (
            <Notice title="What it states did not sign in" tone="warning">
              <span className="break-words">{message}</span>
            </Notice>
          )
        )}

        {choice && (
          <ChoiceGrid columns={2} role="group" aria-label="How to sign in">
            <ChoiceCard
              verb={accounts ? "Make an account from this server" : "Read its password"}
              selected={mode === "account"}
              onClick={() => setMode("account")}
              mark={Sparkles}
              title={accounts ? "Make an account" : "Read its password"}
              description={
                accounts
                  ? `Runs ${engine.cli} on this machine as the server's own system account, which needs no password, and creates one there.`
                  : "Reads the password out of the server's configuration file on this machine and connects with it."
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

        {mode === "account" && accounts && (
          <>
            <FieldRow>
              <Field
                label="Account to make"
                htmlFor={`${id}-account`}
                hint="Created if missing; its password is reset if it exists."
                error={
                  account.trim() !== "" && !ACCOUNT_NAME.test(account.trim())
                    ? "Letters, digits and underscores, starting with a letter."
                    : undefined
                }
              >
                <Input
                  id={`${id}-account`}
                  value={account}
                  onChange={(event) => setAccount(event.target.value)}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              {databaseField("account-database")}
            </FieldRow>
            <Field
              label="Its password"
              htmlFor={`${id}-account-password`}
              hint="Kept sealed with the connection, where Connect can show it again."
              trailing={
                <>
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={() => setAccountPassword(generatePassword())}
                  >
                    <RefreshClockwise />
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
                </>
              }
            >
              <Input
                id={`${id}-account-password`}
                value={accountPassword}
                onChange={(event) => setAccountPassword(event.target.value)}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <OptionList>
              <OptionRow
                title="Make it an administrator of the whole server, so accounts, databases and extensions can be managed from here"
                checked={superuser}
                onCheckedChange={setSuperuser}
              />
            </OptionList>
          </>
        )}

        {mode === "password" && (
          <>
            <FieldRow>
              <Field label="User" htmlFor={`${id}-user`}>
                <Input
                  id={`${id}-user`}
                  value={user}
                  onChange={(event) => setUser(event.target.value)}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              {databaseField("database")}
            </FieldRow>
            <Field label="Password" htmlFor={`${id}-password`}>
              <Input
                id={`${id}-password`}
                type="password"
                autoComplete="off"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void submit()
                }}
                className="font-mono"
              />
            </Field>
          </>
        )}

        <Field
          label="Name"
          htmlFor={`${id}-name`}
          hint="How it is listed here. Empty takes the server's own name."
          error={
            nameOk
              ? undefined
              : "Letters, digits, spaces, dots, dashes and underscores, starting with a letter or digit."
          }
        >
          <Input
            id={`${id}-name`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder={instance.name}
            autoComplete="off"
          />
        </Field>
        {mode === "account" && !accounts && (
          <FormNote>
            {engine.label} has one password rather than accounts. A server with none connects as it
            is.
          </FormNote>
        )}
      </div>
    </Modal>
  )
}
