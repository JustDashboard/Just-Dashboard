"use client"

import { useEffect, useId, useState } from "react"
import { Key, UserPlus } from "@/components/icons"
import { ApiError, errorMessage, post, put } from "@/lib/api"
import { Segments } from "@/components/deploy/settings/segments"
import {
  Disclosure,
  Field,
  FieldRow,
  FormFact,
  FormNote,
  OptionList,
  OptionRow,
  Statement,
} from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { generatePassword } from "@/components/database/connect/rules"
import {
  ATTRIBUTES,
  accountDsn,
  accountKey,
  accountNameProblem,
  type AttributeField,
} from "@/components/database/ops/access-model"
import {
  AccountMark,
  AccountName,
  SecretField,
  SecretShown,
} from "@/components/database/ops/access-parts"
import type {
  DbDatabaseGrantResult,
  DbPresetLevel,
  DbRole,
  DbRoleAltered,
  DbRoleCreateRequest,
  DbRoleCreated,
} from "@/components/database/ops/access-types"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

type Level = DbPresetLevel | "none"

const LEVELS: { value: Level; label: string }[] = [
  { value: "none", label: "Nothing yet" },
  { value: "read", label: "Read" },
  { value: "write", label: "Read and write" },
  { value: "all", label: "Everything" },
]

/** The switches a new account can be made with, besides being an administrator. */
const EXTRA: AttributeField[] = ["createDb", "createRole", "replication", "bypassRls"]

/**
 * What granting a database to an account will run, as the server plans it.
 * The preview executes nothing; it is asked again a moment after the form
 * stops changing.
 */
function useGrantPreview(
  id: number,
  name: string,
  host: string | undefined,
  database: string,
  level: Level,
) {
  const [state, setState] = useState<{
    key: string
    plan?: DbDatabaseGrantResult
    error?: string
  }>()
  const key = level === "none" || !name || !database ? "" : [name, host, database, level].join("\n")
  useEffect(() => {
    if (!key) return
    let cancelled = false
    const timer = setTimeout(() => {
      post<DbDatabaseGrantResult>(
        `/databases/${id}/server/roles/${encodeURIComponent(name)}/grant`,
        { database, level, ...(host ? { host } : {}) },
        { query: { preview: 1 } },
      )
        .then((plan) => !cancelled && setState({ key, plan }))
        .catch((err) => !cancelled && setState({ key, error: errorMessage(err) }))
    }, 350)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [id, key, name, host, database, level])
  return state?.key === key ? state : undefined
}

/**
 * Make an account. It is given a name, a password the dashboard makes (or one
 * typed), what it may do on the server, and — the usual reason for making one
 * — a database at one of three levels, with the statements that grant it
 * shown as the server plans them.
 *
 * The dialog ends on the account's connection string. The password exists in
 * the clear only here: the server keeps a hash, and nothing brings it back.
 */
export function NewAccount({
  roles,
  editable,
  databases,
  onCreated,
  onClose,
}: {
  /** The accounts that exist, for the names taken and for whether accounts have hosts here. */
  roles: DbRole[]
  /** The fields the create route honours on this engine. */
  editable: string[]
  /** The databases of the server, to give one. */
  databases: string[]
  onCreated: () => void
  onClose: () => void
}) {
  const { id, conn, engine, summary } = useDatabase()
  const field = useId()
  // An engine whose accounts are `name@host` lists them with a host.
  const hosted = roles.some((role) => role.host !== undefined)
  const [name, setName] = useState("")
  const [host, setHost] = useState("%")
  const [password, setPassword] = useState(() => generatePassword())
  const [superuser, setSuperuser] = useState(false)
  const [extra, setExtra] = useState<Partial<Record<AttributeField, boolean>>>({})
  const [limit, setLimit] = useState("")
  const [until, setUntil] = useState("")
  const [database, setDatabase] = useState(conn.database)
  const [level, setLevel] = useState<Level>(conn.database ? "read" : "none")
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState<{ notes: string[] }>()

  const typed = name.trim()
  const taken = roles
    .filter((role) => !hosted || (role.host ?? "") === host.trim())
    .map((role) => role.name)
  const problem = accountNameProblem(name, taken)
  const giving = level !== "none" && !superuser && Boolean(database)
  const plan = useGrantPreview(
    id,
    problem ? "" : typed,
    hosted ? host.trim() : undefined,
    database,
    giving ? level : "none",
  )
  const extras = EXTRA.filter((one) => editable.includes(one))
  const limitProblem =
    limit.trim() && !/^\d+$/.test(limit.trim()) ? "A whole number of sessions." : undefined

  const create = async () => {
    setBusy(true)
    setRefusal(undefined)
    const body: DbRoleCreateRequest = {
      name: typed,
      password,
      ...(hosted ? { host: host.trim() || "%" } : {}),
      ...(superuser ? { superuser: true } : {}),
      ...Object.fromEntries(extras.filter((one) => extra[one]).map((one) => [one, true])),
      ...(limit.trim() && editable.includes("connectionLimit")
        ? { connectionLimit: Number(limit.trim()) }
        : {}),
      ...(until && editable.includes("validUntil") ? { validUntil: until } : {}),
      ...(giving ? { database, level: level as DbPresetLevel } : {}),
    }
    try {
      const made = await post<DbRoleCreated>(`/databases/${id}/server/roles`, body)
      onCreated()
      setDone({
        notes: [
          ...(made.skippedSchemas ?? []).map(
            (skipped) => `Nothing was granted on the schema ${skipped.name}: ${skipped.reason}.`,
          ),
          ...(made.notes ?? []),
        ],
      })
    } catch (err) {
      // The account exists and its grant did not go through: it is still an
      // account whose password is shown nowhere else.
      if (err instanceof ApiError && err.code === "grant_failed") {
        onCreated()
        setDone({
          notes: [`The account was made, and granting it ${database} failed: ${err.message}`],
        })
      } else setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <TaskDialog
        title="Account created"
        description={`${typed} was created; its password is shown once`}
        size="lg"
        dirty
        busy={false}
        cancelLabel={null}
        discardQuestion="Close? The password is not shown again."
        stayLabel="Go back"
        discardLabel="Close"
        command="I have saved it"
        onRun={onClose}
        onClose={onClose}
      >
        <SecretShown
          title={`${typed} was created`}
          password={password}
          dsn={accountDsn(conn, { user: typed, password, database: giving ? database : undefined })}
          user={typed}
          database={giving ? database : conn.database}
          notes={done.notes}
        />
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="New account"
      description={`Create an account on the server ${conn.name} connects to`}
      size="lg"
      subject={databaseSubject(
        {
          name: summary?.versionNumber ? `${engine.label} ${summary.versionNumber}` : engine.label,
        },
        engine,
        <FormFact label="Server" mono>
          {conn.host}
          {conn.port ? `:${conn.port}` : ""}
        </FormFact>,
      )}
      dirty={typed !== "" || superuser || level !== (conn.database ? "read" : "none")}
      busy={busy}
      refusal={refusal}
      note="Its password is shown once, after it is made."
      command="Create account"
      commandIcon={UserPlus}
      disabled={Boolean(problem) || !password || Boolean(limitProblem)}
      onRun={() => void create()}
      onClose={onClose}
    >
      <FieldRow columns={hosted ? 3 : 2}>
        <Field
          label="Name"
          htmlFor={`${field}-name`}
          className="sm:col-span-2"
          error={typed ? problem : undefined}
        >
          <Input
            id={`${field}-name`}
            value={name}
            onChange={(event) => setName(event.target.value)}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
        {hosted && (
          <Field label="From host" htmlFor={`${field}-host`} hint="% is anywhere.">
            <Input
              id={`${field}-host`}
              value={host}
              onChange={(event) => setHost(event.target.value)}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
          </Field>
        )}
      </FieldRow>
      <SecretField
        id={`${field}-password`}
        label="Password"
        hint="Made here, thirty characters. Type another if one is wanted."
        value={password}
        onChange={setPassword}
        error={password ? undefined : "An account needs a password."}
      />

      {editable.includes("superuser") && (
        <OptionList>
          <OptionRow
            title="Administrator of the whole server"
            hint="Every privilege on every database, whatever its grants say."
            checked={superuser}
            onCheckedChange={setSuperuser}
            tone={superuser ? "warning" : "default"}
          />
        </OptionList>
      )}

      {!superuser && databases.length > 0 && (
        <div className="space-y-3">
          <Field label="Give it a database">
            <Segments
              label="What it may do in the database"
              value={level}
              onChange={setLevel}
              options={LEVELS}
            />
          </Field>
          {level !== "none" && (
            <Field label="Which database" htmlFor={`${field}-database`}>
              <Select value={database} onValueChange={setDatabase}>
                <SelectTrigger id={`${field}-database`} className="w-full font-mono">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {databases.map((one) => (
                    <SelectItem key={one} value={one} className="font-mono">
                      {one}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}
          {giving && (
            <div className="space-y-1.5" aria-live="polite">
              <Statement
                label="What granting it runs"
                sql={plan?.plan ? plan.plan.statements.join(";\n") : ""}
                placeholder={
                  problem
                    ? "Name the account to see the statements."
                    : plan?.error
                      ? `The statements could not be planned: ${plan.error}`
                      : plan?.plan
                        ? "This engine grants it with a role rather than a statement."
                        : "Reading the statements from the server…"
                }
              />
              {plan?.plan?.schemas && plan.plan.schemas.length > 0 && (
                <FormNote>
                  Covers the {plan.plan.schemas.length === 1 ? "schema" : "schemas"}{" "}
                  <span className="font-mono">{plan.plan.schemas.join(", ")}</span>.
                </FormNote>
              )}
              {plan?.plan?.skippedSchemas?.map((skipped) => (
                <FormNote key={skipped.name}>
                  Leaves <span className="font-mono">{skipped.name}</span> alone: {skipped.reason}.
                </FormNote>
              ))}
              {plan?.plan?.notes?.map((note) => (
                <FormNote key={note}>{note}</FormNote>
              ))}
            </div>
          )}
        </div>
      )}

      {(extras.length > 0 ||
        editable.includes("connectionLimit") ||
        editable.includes("validUntil")) && (
        <Disclosure quiet summary="More it can be made with">
          <div className="space-y-4">
            {extras.length > 0 && !superuser && (
              <OptionList>
                {extras.map((one) => {
                  const attribute = ATTRIBUTES.find((entry) => entry.field === one)!
                  return (
                    <OptionRow
                      key={one}
                      title={attribute.title}
                      hint={attribute.hint}
                      checked={Boolean(extra[one])}
                      onCheckedChange={(on) => setExtra((held) => ({ ...held, [one]: on }))}
                    />
                  )
                })}
              </OptionList>
            )}
            <FieldRow>
              {editable.includes("connectionLimit") && (
                <Field
                  label="Sessions at once"
                  htmlFor={`${field}-limit`}
                  hint="Empty for no limit."
                  error={limitProblem}
                >
                  <Input
                    id={`${field}-limit`}
                    value={limit}
                    onChange={(event) => setLimit(event.target.value)}
                    inputMode="numeric"
                    autoComplete="off"
                    className="font-mono"
                  />
                </Field>
              )}
              {editable.includes("validUntil") && (
                <Field
                  label="Password valid until"
                  htmlFor={`${field}-until`}
                  hint="Empty for no end."
                >
                  <Input
                    id={`${field}-until`}
                    type="date"
                    value={until}
                    onChange={(event) => setUntil(event.target.value)}
                    className="font-mono"
                  />
                </Field>
              )}
            </FieldRow>
          </div>
        </Disclosure>
      )}
    </TaskDialog>
  )
}

/**
 * Give an account another password. Only the password is sent: every other
 * attribute of the account stays exactly as it is.
 *
 * When the account is the one this connection signs in with, the server
 * replaces the connection's saved password too, once it has seen the new one
 * work — and says when it could not, so the connection is not left holding a
 * password that no longer opens anything.
 */
export function ChangePassword({
  role,
  own,
  onChanged,
  onClose,
}: {
  role: Pick<DbRole, "name" | "host">
  /** This connection signs in with it. */
  own: boolean
  onChanged: () => void
  onClose: () => void
}) {
  const { id, conn, href } = useDatabase()
  const field = useId()
  const [password, setPassword] = useState(() => generatePassword())
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const [done, setDone] = useState<{ notes: string[] }>()
  const label = accountKey(role)

  const change = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      const answer = await put<DbRoleAltered>(
        `/databases/${id}/server/roles/${encodeURIComponent(role.name)}`,
        { password, ...(role.host !== undefined ? { host: role.host } : {}) },
      )
      onChanged()
      setDone({
        notes: own
          ? [
              answer.connectionUpdated
                ? `${conn.name} signs in with this account: its saved password was replaced with the new one.`
                : `${conn.name} signs in with this account, and its saved password could not be replaced. Edit the address under Settings, or this connection stops working.`,
            ]
          : [],
      })
    } catch (err) {
      setRefusal(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (done) {
    return (
      <TaskDialog
        title="Password changed"
        description={`${label} has a new password; it is shown once`}
        size="lg"
        dirty
        busy={false}
        cancelLabel={null}
        discardQuestion="Close? The password is not shown again."
        stayLabel="Go back"
        discardLabel="Close"
        command="I have saved it"
        onRun={onClose}
        onClose={onClose}
      >
        <SecretShown
          title={`${label} has a new password`}
          password={password}
          dsn={accountDsn(conn, { user: role.name, password })}
          user={role.name}
          database={conn.database}
          notes={done.notes}
        />
        {own && (
          <FormNote>
            The connection&rsquo;s address is under{" "}
            <a href={`${href("settings")}#connection`} className="rounded-sm underline focus-ring">
              Settings
            </a>
            .
          </FormNote>
        )}
      </TaskDialog>
    )
  }

  return (
    <TaskDialog
      title="Change password"
      description={`Give ${label} another password`}
      subject={{
        mark: <AccountMark name={role.name} />,
        name: <AccountName name={role.name} host={role.host} className="text-body" />,
        facts: own ? <FormFact label="Used by">{conn.name}, this connection</FormFact> : undefined,
      }}
      dirty={false}
      busy={busy}
      refusal={refusal}
      note="Only the password changes. Whatever uses the old one stops signing in."
      command="Change password"
      commandIcon={Key}
      disabled={!password}
      onRun={() => void change()}
      onClose={onClose}
    >
      <SecretField
        id={`${field}-password`}
        label="New password"
        hint="Made here, thirty characters. Type another if one is wanted."
        value={password}
        onChange={setPassword}
        error={password ? undefined : "An account needs a password."}
      />
    </TaskDialog>
  )
}
