"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { Pencil } from "@/components/icons"
import { errorMessage, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { GroupRule } from "@/components/flow"
import {
  Disclosure,
  Field,
  FormFact,
  FormNote,
  FormSection,
  OptionList,
  OptionRow,
  Statement,
} from "@/components/form"
import { ROW_REVEAL } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { isDown } from "@/components/database/ops/performance-parts"
import { TaskDialog, databaseSubject } from "@/components/database/ops/settings-dialog"
import {
  boolWords,
  changeWords,
  filterParameters,
  fromRedisConfig,
  fromSettings,
  groupParameters,
  isOn,
  parameterProblem,
  readableValue,
  type DbSettingChange,
  type DbSettings,
  type Parameter,
  type ParameterFilter,
  type RedisConfig,
  type RedisConfigChange,
} from "@/components/database/ops/settings-parameters-model"
import { UNDER_STRIP } from "@/components/database/ops/settings-nav"
import { useDatabase } from "@/components/database/shell/database-context"

/** Where the list came from, which decides how one of its parameters is set. */
type Source = "settings" | "config"

type Listed = {
  source: Source
  supported: boolean
  reason?: string
  parameters: Parameter[]
  /** The engine can keep a change. */
  writable: boolean
  /** A key–value server: a change can also be written to its config file. */
  rewritable?: boolean
  configFile?: string
}

/** What the last change did, kept on the page beside the statements that ran. */
type Done = {
  name: string
  statements: string[]
  words: string
  tone: "default" | "warning"
  /**
   * What it was before, on a server with no reset: the change can be put
   * back from where it is reported. Absent for a secret, which was never read.
   */
  previous?: string
  /** A key–value change that was also written to the configuration file. */
  rewrite?: boolean
}

/** How many rows a filtered list draws before the rest wait behind a press. */
const SHOWN = 120

/**
 * The server's own parameters: every one the engine lists, under the engine's
 * own headings, searchable, with the ones that differ from their default and
 * the ones waiting for a restart a press away.
 *
 * A parameter is changed in a dialog that says what it is, what it may be and
 * when a change takes effect; what the server ran is shown afterwards, as the
 * route reports it. Editing is drawn for an administrator, on a connection
 * that is not protected, and only on a parameter the server says it would
 * accept.
 *
 * A SQL engine's list is `GET /settings`; a key–value server's is its own
 * `GET /redis/config`, whose values it sets without types or defaults. A
 * document database answers `/settings` with the counters of its status
 * command — readings, which belong to Performance — so the page does not draw
 * this section for one.
 *
 * Where the server publishes no default (its configuration, or an engine
 * whose list carries none) there is nothing to reset to: the change just made
 * can be put back to what it was, from the line that reports it.
 */
export function ParametersSection() {
  const { id, conn, engine, readOnly, status, param, select } = useDatabase()
  const { can } = useAuth()
  const keyvalue = engine.kind === "keyvalue"
  const down = isDown(status.state)
  const [search, setSearch] = useState(() => param("q"))
  const only = (param("only") || "") as ParameterFilter
  const [all, setAll] = useState(false)
  const [opened, setOpened] = useState<string[]>([])
  const [editing, setEditing] = useState<Parameter>()
  const [done, setDone] = useState<Done>()
  const [undoing, setUndoing] = useState(false)

  const list = usePoll<Listed>(
    async (signal) => {
      if (keyvalue) {
        const config = await read<RedisConfig>(
          `/databases/${id}/redis/config`,
          (answer) => Array.isArray(answer.groups),
          undefined,
          signal,
        )
        return {
          source: "config",
          supported: config.supported,
          reason: config.reason,
          parameters: fromRedisConfig(config),
          writable: true,
          rewritable: config.rewritable,
          configFile: config.configFile,
        }
      }
      const settings = await read<DbSettings>(
        `/databases/${id}/settings`,
        (answer) => Array.isArray(answer.settings),
        { all: 1 },
        signal,
      )
      return {
        source: "settings",
        supported: settings.supported,
        reason: settings.reason,
        parameters: fromSettings(settings.settings),
        writable: Boolean(settings.writable),
      }
    },
    120_000,
    [id, keyvalue],
    { enabled: !down },
  )

  // The search is the field's own text; the address follows it a moment
  // later, so a link to this page can name a parameter. The writer is held in
  // a ref: it is a new function whenever the address moves, and a timer torn
  // down for that would never write what was typed.
  const written = useRef(search)
  const write = useRef(select)
  useEffect(() => {
    write.current = select
  })
  useEffect(() => {
    if (written.current === search) return
    const timer = setTimeout(() => {
      written.current = search
      write.current({ q: search.trim() || null })
    }, 400)
    return () => clearTimeout(timer)
  }, [search])
  // A chip writes the search with it, in one change: pressed inside those
  // 400 ms it would otherwise leave the address on the text before.
  const narrow = (next: ParameterFilter) => {
    written.current = search
    select({ only: next || null, q: search.trim() || null })
  }

  const data = list.data
  const parameters = useMemo(() => data?.parameters ?? [], [data])
  const changed = parameters.filter((one) => one.changed).length
  const pending = parameters.filter((one) => one.pending).length
  const filtering = search.trim() !== "" || only !== ""
  const matches = useMemo(
    () => filterParameters(parameters, search, only),
    [parameters, search, only],
  )
  const groups = useMemo(
    () => groupParameters(filtering ? (all ? matches : matches.slice(0, SHOWN)) : parameters),
    [filtering, all, matches, parameters],
  )

  const mayEdit =
    can("system.admin") &&
    !readOnly &&
    Boolean(data?.writable) &&
    (data?.source === "settings" || engine.can("settingsWrite"))
  const editable = (parameter: Parameter) => mayEdit && parameter.editable && !parameter.redacted

  // A list with no default and no row marked as changed is one the server
  // cannot reset: its changes are put back instead.
  const resets =
    data?.source === "settings" &&
    parameters.some((one) => one.changed || one.default !== undefined)

  // A file has no server: its parameters are the file's own.
  const title = engine.can("fileBased") ? "Parameters" : "Server parameters"
  const noun = "parameter"

  const putBack = async (change: Done) => {
    if (change.previous === undefined || !data) return
    setUndoing(true)
    try {
      if (data.source === "config") {
        const answer = await put<RedisConfigChange>(`/databases/${id}/redis/config`, {
          name: change.name,
          value: change.previous,
          ...(change.rewrite ? { rewrite: true } : {}),
        })
        setDone({
          name: change.name,
          statements: [
            `CONFIG SET ${change.name} ${change.previous}`,
            ...(answer.rewritten ? ["CONFIG REWRITE"] : []),
          ],
          words: `Put back to ${change.previous}.`,
          tone: "default",
        })
      } else {
        const answer = await put<DbSettingChange>(`/databases/${id}/settings`, {
          name: change.name,
          value: change.previous,
        })
        setDone({
          name: change.name,
          statements: answer.statements,
          words: `Put back to ${answer.value}. ${changeWords(answer)}`,
          tone: answer.restartRequired ? "warning" : "default",
        })
      }
      notify.success(`Put ${change.name} back`)
      window.setTimeout(list.refresh, 1200)
    } catch (err) {
      notify.error(`Could not put ${change.name} back`, err)
    } finally {
      setUndoing(false)
    }
  }

  return (
    <FormSection
      aside
      id="parameters"
      className={UNDER_STRIP}
      title={title}
      hint={
        data?.supported ? (
          <>
            <span className="numeric block">
              {plural(parameters.length, noun)}
              {changed > 0 ? ` · ${changed} changed` : ""}
            </span>
            {pending > 0 && (
              <span className="flex pt-1">
                <Status verdict="warning" label={`${pending} waiting for a restart`} />
              </span>
            )}
          </>
        ) : undefined
      }
    >
      {down ? (
        <FormNote>
          {title} are read from the running server; they are listed once {conn.name} answers again.
        </FormNote>
      ) : !data ? (
        list.error ? (
          <CouldNotRead what={`the ${noun}s`} error={list.error} onRetry={list.refresh} />
        ) : (
          <LoadingRows rows={6} />
        )
      ) : !data.supported ? (
        <Notice title={`The ${noun}s cannot be read`}>
          {data.reason ?? `This server does not list its ${noun}s to this connection.`}
        </Notice>
      ) : (
        <div className="animate-rise space-y-4">
          {pending > 0 && (
            <Notice
              tone="warning"
              title={`${plural(pending, "parameter")} ${pending === 1 ? "is" : "are"} stored and not yet in effect`}
            >
              {pending === 1 ? "It takes" : "They take"} effect when the server is restarted.
            </Notice>
          )}
          {done && (
            <div className="space-y-1.5" data-slot="parameter-change" role="status">
              {done.statements.length > 0 && (
                <Statement label="What ran" sql={done.statements.join(";\n")} placeholder="" />
              )}
              <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                <FormNote tone={done.tone} className="min-w-0 flex-1">
                  <span className="font-mono text-foreground">{done.name}</span> — {done.words}
                </FormNote>
                {mayEdit && !resets && done.previous !== undefined && (
                  <Button
                    size="xs"
                    variant="outline"
                    pending={undoing}
                    onClick={() => void putBack(done)}
                  >
                    Put back <span className="font-mono">{done.previous || "empty"}</span>
                  </Button>
                )}
              </div>
            </div>
          )}

          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
            <SearchInput
              aria-label={`Filter the ${noun}s`}
              placeholder={`Filter ${parameters.length} ${noun}s`}
              value={search}
              onChange={(event) => {
                setSearch(event.target.value)
                setAll(false)
              }}
            />
            {(changed > 0 || pending > 0 || only !== "") && (
              <ChipStrip role="group" aria-label={`Which ${noun}s are listed`}>
                <FilterChip selected={only === ""} onClick={() => narrow("")}>
                  All
                  <ChipCount>{parameters.length}</ChipCount>
                </FilterChip>
                {(changed > 0 || only === "changed") && (
                  <FilterChip
                    selected={only === "changed"}
                    onClick={() => narrow(only === "changed" ? "" : "changed")}
                  >
                    Changed
                    <ChipCount>{changed}</ChipCount>
                  </FilterChip>
                )}
                {(pending > 0 || only === "pending") && (
                  <FilterChip
                    selected={only === "pending"}
                    onClick={() => narrow(only === "pending" ? "" : "pending")}
                  >
                    Waiting for a restart
                    <ChipCount>{pending}</ChipCount>
                  </FilterChip>
                )}
              </ChipStrip>
            )}
          </div>

          {!data.writable && (
            <FormNote>
              {engine.label} lists its parameters here and takes changes to them in its own
              configuration.
            </FormNote>
          )}
          {data.source === "config" && mayEdit && !data.rewritable && (
            <FormNote>
              The server was started without a configuration file, so a change made here lasts until
              it restarts.
            </FormNote>
          )}

          {filtering ? (
            matches.length === 0 ? (
              <p className="py-4 text-body text-muted-foreground">
                No {noun} matches{search.trim() ? ` “${search.trim()}”` : ""}.
              </p>
            ) : (
              <div className="space-y-4">
                {groups.map((group) => (
                  <div key={group.name}>
                    <GroupRule label={group.name} count={group.parameters.length} />
                    <ParameterRows
                      parameters={group.parameters}
                      editing={mayEdit}
                      editable={editable}
                      onEdit={setEditing}
                    />
                  </div>
                ))}
                {!all && matches.length > SHOWN && (
                  <Button size="xs" variant="ghost" className="-ml-2" onClick={() => setAll(true)}>
                    Show all {matches.length}
                  </Button>
                )}
              </div>
            )
          ) : (
            <div className="space-y-0.5">
              {groups.map((group) => (
                <Disclosure
                  key={group.name}
                  quiet
                  open={opened.includes(group.name)}
                  onOpenChange={(open) =>
                    setOpened((held) =>
                      open
                        ? held.includes(group.name)
                          ? held
                          : [...held, group.name]
                        : held.filter((name) => name !== group.name),
                    )
                  }
                  summary={group.name}
                  facts={
                    <span className="numeric">
                      {group.parameters.length}
                      {group.changed > 0 ? ` · ${group.changed} changed` : ""}
                      {group.pending > 0 ? ` · ${group.pending} waiting` : ""}
                    </span>
                  }
                >
                  {/* Drawn once it is open: a closed heading holds no rows, and
                      a server has several hundred parameters. */}
                  {opened.includes(group.name) && (
                    <ParameterRows
                      parameters={group.parameters}
                      editing={mayEdit}
                      editable={editable}
                      onEdit={setEditing}
                    />
                  )}
                </Disclosure>
              ))}
            </div>
          )}
        </div>
      )}

      {editing && data && (
        <EditParameter
          parameter={editing}
          source={data.source}
          resets={Boolean(resets)}
          rewritable={Boolean(data.rewritable)}
          onClose={() => setEditing(undefined)}
          onDone={(result) => {
            setEditing(undefined)
            setDone(result)
            // PostgreSQL applies a change after its reload signal, a moment
            // after the route answers: the list is read again then, not now.
            window.setTimeout(list.refresh, 1200)
          }}
        />
      )}
    </FormSection>
  )
}

/**
 * One heading's parameters: name and what it means on the left, its value at
 * the right. Where a parameter can be changed its value is the control — the
 * whole of it is pressed, and a pencil appears beside it under the pointer —
 * so a list of forty rows is forty values, not forty glyphs.
 */
function ParameterRows({
  parameters,
  editing,
  editable,
  onEdit,
}: {
  parameters: Parameter[]
  /** The reader may change parameters here: every row keeps the pencil's place, so values line up. */
  editing: boolean
  editable: (parameter: Parameter) => boolean
  onEdit: (parameter: Parameter) => void
}) {
  return (
    <ul className="divide-y divide-hairline">
      {parameters.map((parameter) => {
        const readable = readableValue(parameter)
        const value = (
          <>
            <ParameterValue parameter={parameter} />
            {readable && (
              <span className="numeric block truncate text-hint text-muted-foreground">
                {readable}
              </span>
            )}
          </>
        )
        return (
          <li
            key={parameter.name}
            data-parameter={parameter.name}
            className="group flex min-w-0 items-start gap-3 py-2"
          >
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-0.5">
                <span className="min-w-0 font-mono text-xs wrap-anywhere">{parameter.name}</span>
                {parameter.pending && <Tag tone="warning">waiting for a restart</Tag>}
                {parameter.changed && !parameter.pending && <Tag>changed</Tag>}
                {parameter.restart && <Tag>restart to apply</Tag>}
              </p>
              {parameter.description && (
                <p className="line-clamp-2 text-hint leading-relaxed text-muted-foreground">
                  {parameter.description}
                </p>
              )}
            </div>
            {editable(parameter) ? (
              <button
                type="button"
                aria-label={`Change ${parameter.name}`}
                onClick={() => onEdit(parameter)}
                className="-my-1 -mr-2 flex max-w-[48%] min-w-0 shrink-0 items-start gap-2 rounded-md px-2 py-1 text-right focus-ring transition-colors hover:bg-row-hover"
              >
                <span className="min-w-0">{value}</span>
                <Pencil
                  className={cn("mt-0.5 size-3.5 shrink-0 text-muted-foreground", ROW_REVEAL)}
                />
              </button>
            ) : (
              <div
                className={cn("max-w-[45%] min-w-0 shrink-0 text-right", editing && "pr-[22px]")}
              >
                {value}
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}

/** A value as the server prints it: never empty where it is hidden, unset or blank. */
function ParameterValue({ parameter }: { parameter: Parameter }) {
  if (parameter.redacted) {
    return <span className="block text-xs text-muted-foreground italic">hidden</span>
  }
  if (parameter.secret) {
    return (
      <span className="block text-xs text-muted-foreground italic">
        {parameter.secret.set ? "set" : "not set"}
      </span>
    )
  }
  if (parameter.value === "") {
    return <span className="block text-xs text-muted-foreground italic">empty</span>
  }
  return (
    <span className="block truncate font-mono text-xs" title={parameter.value}>
      {parameter.value}
      {parameter.unit && <span className="text-muted-foreground"> {parameter.unit}</span>}
    </span>
  )
}

/**
 * Change one parameter. The dialog opens on the parameter: what it means,
 * what it is now, what it may be. The control is the one its type asks for —
 * a yes/no as two words, a closed set as its words, a number or a string as a
 * field — and a value outside what the server publishes is said before it is
 * sent. A parameter that is not the default can be put back to it.
 */
function EditParameter({
  parameter,
  source,
  resets,
  rewritable,
  onClose,
  onDone,
}: {
  parameter: Parameter
  source: Source
  /** The server can put a parameter back to a default it publishes. */
  resets: boolean
  rewritable: boolean
  onClose: () => void
  onDone: (done: Done) => void
}) {
  const { id, conn, engine } = useDatabase()
  const field = useId()
  const words = boolWords(parameter.value)
  const start = parameter.secret
    ? ""
    : parameter.type === "bool"
      ? isOn(parameter.value)
        ? words.on
        : words.off
      : parameter.value
  const [value, setValue] = useState(start)
  const [rewrite, setRewrite] = useState(rewritable)
  const [busy, setBusy] = useState<"save" | "reset">()
  const [refusal, setRefusal] = useState<string>()
  const problem = parameterProblem(parameter, value)
  const dirty = value !== start

  const send = async (reset: boolean) => {
    setBusy(reset ? "reset" : "save")
    setRefusal(undefined)
    try {
      if (source === "config") {
        const answer = await put<RedisConfigChange>(`/databases/${id}/redis/config`, {
          name: parameter.name,
          value: value.trim(),
          ...(rewrite ? { rewrite: true } : {}),
        })
        const kept = answer.rewritten
          ? "In effect now, and written to the configuration file."
          : answer.rewriteError
            ? `In effect now. It was not written to the configuration file: ${answer.rewriteError}`
            : "In effect now. It lasts until the server restarts."
        notify.success(`Set ${parameter.name}`, { description: answer.notice })
        onDone({
          name: parameter.name,
          statements: [
            `CONFIG SET ${parameter.name} ${answer.secret ? "••••••" : value.trim()}`,
            ...(answer.rewritten ? ["CONFIG REWRITE"] : []),
          ],
          words: [kept, answer.notice].filter(Boolean).join(" "),
          tone: answer.notice || answer.rewriteError ? "warning" : "default",
          previous: parameter.secret ? undefined : parameter.value,
          rewrite: answer.rewritten,
        })
        return
      }
      const answer = await put<DbSettingChange>(
        `/databases/${id}/settings`,
        reset
          ? { name: parameter.name, reset: true }
          : { name: parameter.name, value: value.trim() },
      )
      notify.success(reset ? `Reset ${parameter.name}` : `Set ${parameter.name}`, {
        description: `Now ${answer.value}${parameter.unit ? ` ${parameter.unit}` : ""}.`,
      })
      onDone({
        name: parameter.name,
        statements: answer.statements,
        words: changeWords(answer),
        tone: answer.restartRequired || !answer.persisted ? "warning" : "default",
        previous: reset || parameter.redacted ? undefined : parameter.value,
      })
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(undefined)
    }
  }

  const range =
    parameter.min !== undefined || parameter.max !== undefined
      ? `${parameter.min ?? "…"} to ${parameter.max ?? "…"}`
      : undefined

  return (
    <TaskDialog
      title="Change a parameter"
      description={`Change ${parameter.name} on the server ${conn.name} connects to`}
      subject={{
        ...databaseSubject(conn, engine),
        name: <span className="font-mono">{parameter.name}</span>,
        facts: (
          <>
            <FormFact label="Under">
              {parameter.subgroup ? `${parameter.group} / ${parameter.subgroup}` : parameter.group}
            </FormFact>
            {parameter.default !== undefined && (
              <FormFact label="Default" mono>
                {parameter.default === "" ? "empty" : parameter.default}
              </FormFact>
            )}
            {range && <FormFact label="Range">{range}</FormFact>}
            {parameter.context && <FormFact label="Set">{parameter.context}</FormFact>}
          </>
        ),
      }}
      dirty={dirty}
      busy={busy !== undefined}
      refusal={refusal}
      note="It changes the server for every database on it."
      secondary={
        source === "settings" &&
        parameter.changed && (
          <Button
            type="button"
            variant="outline"
            disabled={busy !== undefined}
            pending={busy === "reset"}
            onClick={() => void send(true)}
          >
            Reset to default
          </Button>
        )
      }
      command="Save"
      disabled={!dirty || Boolean(problem)}
      onRun={() => void send(false)}
      onClose={onClose}
    >
      {parameter.description && (
        <p className="text-body leading-relaxed text-muted-foreground">{parameter.description}</p>
      )}

      {parameter.type === "bool" ? (
        <Field label="Value">
          <Segments
            label={parameter.name}
            value={isOn(value) ? words.on : words.off}
            onChange={setValue}
            options={[
              { value: words.on, label: words.on, mono: true },
              { value: words.off, label: words.off, mono: true },
            ]}
          />
        </Field>
      ) : parameter.type === "enum" && parameter.options && parameter.options.length <= 6 ? (
        <Field label="Value">
          <Segments
            label={parameter.name}
            value={value}
            onChange={setValue}
            className="flex-wrap"
            options={parameter.options.map((option) => ({
              value: option,
              label: option,
              mono: true,
            }))}
          />
        </Field>
      ) : (
        <Field
          label={parameter.secret ? "New value" : "Value"}
          htmlFor={field}
          hint={
            parameter.options?.length
              ? `One of ${parameter.options.join(", ")}.`
              : parameter.unit
                ? `In ${parameter.unit}${range ? `, ${range}` : ""}.`
                : range
                  ? `${range}.`
                  : parameter.secret
                    ? "It is write-only: the server never sends it back."
                    : undefined
          }
          error={dirty ? problem : undefined}
        >
          <Input
            id={field}
            type={parameter.secret ? "password" : "text"}
            value={value}
            onChange={(event) => {
              setValue(event.target.value)
              setRefusal(undefined)
            }}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </Field>
      )}

      {parameter.restart && (
        <FormNote tone="warning">
          A change to this one is stored now and takes effect only when the server is restarted.
        </FormNote>
      )}
      {source === "config" && rewritable && (
        <OptionList>
          <OptionRow
            title="Write it to the configuration file too, so it survives a restart"
            checked={rewrite}
            onCheckedChange={setRewrite}
          />
        </OptionList>
      )}
      {source === "config" && !rewritable && (
        <FormNote>
          There is no configuration file to write it to: it lasts until the server restarts.
        </FormNote>
      )}
      {!resets && (
        <FormNote>
          {engine.label} publishes no default for {source === "config" ? "its configuration" : "it"}
          , so there is no reset. A change made here can be put back to what it was from the line it
          leaves on the page.
        </FormNote>
      )}
    </TaskDialog>
  )
}
