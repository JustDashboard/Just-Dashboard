"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { RotateCounterClockwise } from "@/components/icons"
import { errorMessage, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { GroupRule } from "@/components/flow"
import { Disclosure, FormNote, InfoTip, OptionList, OptionRow, Statement } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { EngineGlyph } from "@/components/database/kit"
import { isDown } from "@/components/database/ops/performance-parts"
import {
  configBody,
  parameterChanges,
  rangeWords,
  settingBody,
  shownValue,
  startValue,
  valueFacts,
  type ParameterEdit,
  type ParameterEdits,
} from "@/components/database/ops/settings-parameters-edit"
import {
  boolWords,
  changeWords,
  filterParameters,
  fromRedisConfig,
  fromSettings,
  groupParameters,
  isOn,
  parameterProblem,
  type DbSettingChange,
  type DbSettings,
  type Parameter,
  type ParameterFilter,
  type RedisConfig,
  type RedisConfigChange,
} from "@/components/database/ops/settings-parameters-model"
import { usePendingReport } from "@/components/database/ops/settings-pending"
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

/** What one change did, kept on the page beside the statements that ran. */
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

/** Values the server has just taken, shown until the list that carries them is read. */
type Held = { against: Listed | undefined; values: Record<string, string> }

/** How many rows a filtered list draws before the rest wait behind a press. */
const SHOWN = 120

/**
 * The server's own parameters: every one the engine lists, under the engine's
 * own headings, searchable, with the ones that differ from their default and
 * the ones waiting for a restart a press away.
 *
 * A parameter is changed where it is listed. Its value is the control its
 * type asks for (a yes/no as two words, a closed set as its words, a number
 * or a string as a field) and the page's one Save sends every row that
 * differs, one request each, so several are changed together and the server
 * restarted once. Each was a dialog with a Save of its own. What the server
 * ran is shown afterwards, as the route reports it, and a value outside what
 * the server publishes is said under its control before it is sent. Editing
 * is drawn for an administrator, on a connection that is not protected, and
 * only on a parameter the server says it would accept.
 *
 * What is stored and waiting for a restart is the strip at the head of the
 * page (`settings-pending.tsx`), which carries Restart. It was a status in
 * this section's head and a notice under it, and neither could restart
 * anything.
 *
 * A SQL engine's list is `GET /settings`; a key–value server's is its own
 * `GET /redis/config`, whose values it sets without types or defaults. A
 * document database answers `/settings` with the counters of its status
 * command (readings, which belong to Performance) so the page does not draw
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
  const [edits, setEdits] = useState<ParameterEdits>({})
  const [refusals, setRefusals] = useState<Record<string, string>>({})
  const [held, setHeld] = useState<Held>({ against: undefined, values: {} })
  const [done, setDone] = useState<Done[]>([])
  const [rewrite, setRewrite] = useState(true)
  const [saving, setSaving] = useState(false)
  const [undoing, setUndoing] = useState<string>()

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
  const pending = parameters.filter((one) => one.pending)
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
  const rewrites = data?.source === "config" && Boolean(data.rewritable) && rewrite

  // What was just saved is what a row shows until the list that carries it is read.
  const kept = held.against === data ? held.values : {}
  const changes = parameterChanges(parameters, edits, kept)
  const invalid = changes.some((change) => change.problem)
  const dirty = changes.length > 0

  // A file has no server: its parameters are the file's own.
  const title = engine.can("fileBased") ? "Parameters" : "Server parameters"
  const noun = "parameter"

  const edit = (name: string, next: ParameterEdit | undefined) => {
    setEdits((all) => {
      const rest = { ...all }
      if (next) rest[name] = next
      else delete rest[name]
      return rest
    })
    setRefusals((all) => {
      if (!(name in all)) return all
      const rest = { ...all }
      delete rest[name]
      return rest
    })
  }

  /** The bar's Save: one request a row, in the list's order; a refused one keeps its draft. */
  const save = async () => {
    if (!data || changes.length === 0) return true
    if (invalid) return false
    setSaving(true)
    const report: Done[] = []
    const left = { ...edits }
    const refused: Record<string, string> = {}
    const taken: Record<string, string> = {}
    for (const change of changes) {
      const { parameter } = change
      try {
        if (data.source === "config") {
          const body = configBody(change, rewrites)
          const answer = await put<RedisConfigChange>(`/databases/${id}/redis/config`, body)
          const said = answer.rewritten
            ? "In effect now, and written to the configuration file."
            : answer.rewriteError
              ? `In effect now. It was not written to the configuration file: ${answer.rewriteError}`
              : "In effect now. It lasts until the server restarts."
          report.push({
            name: parameter.name,
            statements: [
              `CONFIG SET ${parameter.name} ${answer.secret ? "••••••" : body.value}`,
              ...(answer.rewritten ? ["CONFIG REWRITE"] : []),
            ],
            words: [said, answer.notice].filter(Boolean).join(" "),
            tone: answer.notice || answer.rewriteError ? "warning" : "default",
            previous: parameter.secret ? undefined : parameter.value,
            rewrite: answer.rewritten,
          })
          if (!parameter.secret) taken[parameter.name] = body.value
        } else {
          const reset = "reset" in change.edit
          const answer = await put<DbSettingChange>(
            `/databases/${id}/settings`,
            settingBody(change),
          )
          report.push({
            name: parameter.name,
            statements: answer.statements,
            words: `${reset ? "Reset to" : "Set to"} ${answer.value || "empty"}${parameter.unit ? ` ${parameter.unit}` : ""}. ${changeWords(answer)}`,
            tone: answer.restartRequired || !answer.persisted ? "warning" : "default",
            previous: reset || parameter.redacted ? undefined : parameter.value,
          })
          // A value that waits for a restart is not the one in force: the row keeps the old one.
          if (!answer.restartRequired) taken[parameter.name] = answer.value
        }
        delete left[parameter.name]
      } catch (err) {
        refused[parameter.name] = errorMessage(err)
      }
    }
    setEdits(left)
    setRefusals(refused)
    setHeld({ against: data, values: { ...kept, ...taken } })
    if (report.length > 0) {
      setDone(report)
      // PostgreSQL applies a change after its reload signal, a moment after
      // the route answers: the list is read again then, not now.
      window.setTimeout(list.refresh, 1200)
    }
    setSaving(false)
    return Object.keys(refused).length === 0
  }

  const putBack = async (change: Done) => {
    if (change.previous === undefined || !data) return
    setUndoing(change.name)
    try {
      let next: Done
      if (data.source === "config") {
        const answer = await put<RedisConfigChange>(`/databases/${id}/redis/config`, {
          name: change.name,
          value: change.previous,
          ...(change.rewrite ? { rewrite: true } : {}),
        })
        next = {
          name: change.name,
          statements: [
            `CONFIG SET ${change.name} ${change.previous}`,
            ...(answer.rewritten ? ["CONFIG REWRITE"] : []),
          ],
          words: `Put back to ${change.previous}.`,
          tone: "default",
        }
      } else {
        const answer = await put<DbSettingChange>(`/databases/${id}/settings`, {
          name: change.name,
          value: change.previous,
        })
        next = {
          name: change.name,
          statements: answer.statements,
          words: `Put back to ${answer.value}. ${changeWords(answer)}`,
          tone: answer.restartRequired ? "warning" : "default",
        }
      }
      setDone((all) => all.map((one) => (one.name === change.name ? next : one)))
      setHeld({ against: data, values: { ...kept, [change.name]: change.previous } })
      notify.success(`Put ${change.name} back`)
      window.setTimeout(list.refresh, 1200)
    } catch (err) {
      notify.error(`Could not put ${change.name} back`, err)
    } finally {
      setUndoing(undefined)
    }
  }

  usePendingReport("parameters", {
    waiting: pending.map((one) => ({ name: one.name, mono: true, state: "restart" })),
    edits: changes.map((change) => ({
      name: change.parameter.name,
      mono: true,
      state: "reset" in change.edit ? "reset" : "changed",
    })),
    refresh: list.refresh,
    ready: data !== undefined || down || list.error !== undefined,
  })

  const rows = (parameters: Parameter[]) => (
    <ul className="divide-y divide-hairline">
      {parameters.map((parameter) => (
        <ParameterRow
          key={parameter.name}
          parameter={parameter}
          editable={editable(parameter)}
          aligned={mayEdit}
          resets={Boolean(resets)}
          edit={edits[parameter.name]}
          held={kept[parameter.name]}
          refusal={refusals[parameter.name]}
          busy={saving}
          onEdit={(next) => edit(parameter.name, next)}
        />
      ))}
    </ul>
  )

  return (
    <SettingForm
      name={title}
      onSave={save}
      dirty={dirty}
      changes={changes.length}
      saving={saving}
      invalid={invalid}
      canEdit={mayEdit}
      applies="immediately"
      onDiscard={() => {
        setEdits({})
        setRefusals({})
      }}
    >
      <SettingSection
        id="parameters"
        title={title}
        state={
          data?.supported ? (
            <span className="numeric flex flex-wrap items-center gap-x-1.5">
              <EngineGlyph engine={engine} className="size-3.5" />
              {plural(parameters.length, noun)}
              {changed > 0
                ? ` · ${changed} changed from the default`
                : " · every one at its default"}
            </span>
          ) : undefined
        }
        status={settingStatus({ dirty, refused: Object.keys(refusals).length > 0 })}
      >
        {down ? (
          <FormNote>
            {title} are read from the running server; they are listed once {conn.name} answers
            again.
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
            {done.length > 0 && (
              <div className="space-y-1.5" data-slot="parameter-change" role="status">
                {done.some((one) => one.statements.length > 0) && (
                  <Statement
                    label="What ran"
                    sql={done.flatMap((one) => one.statements).join(";\n")}
                    placeholder=""
                  />
                )}
                {done.map((one) => (
                  <div
                    key={one.name}
                    className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1"
                  >
                    <FormNote tone={one.tone} className="min-w-0 flex-1">
                      <span className="font-mono text-foreground">{one.name}</span>: {one.words}
                    </FormNote>
                    {mayEdit && !resets && one.previous !== undefined && (
                      <Button
                        type="button"
                        size="xs"
                        variant="outline"
                        pending={undoing === one.name}
                        disabled={Boolean(undoing)}
                        onClick={() => void putBack(one)}
                      >
                        Put back <span className="font-mono">{one.previous || "empty"}</span>
                      </Button>
                    )}
                  </div>
                ))}
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
                // The page's form is around it: Enter here filters, it does not save.
                onKeyDown={(event) => {
                  if (event.key === "Enter") event.preventDefault()
                }}
              />
              {(changed > 0 || pending.length > 0 || only !== "") && (
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
                  {(pending.length > 0 || only === "pending") && (
                    <FilterChip
                      selected={only === "pending"}
                      onClick={() => narrow(only === "pending" ? "" : "pending")}
                    >
                      Waiting for a restart
                      <ChipCount>{pending.length}</ChipCount>
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
            {data.source === "config" && mayEdit && data.rewritable && (
              <OptionList>
                <OptionRow
                  title="Write changes to the configuration file too, so they survive a restart"
                  checked={rewrite}
                  onCheckedChange={setRewrite}
                />
              </OptionList>
            )}
            {data.source === "config" && mayEdit && !data.rewritable && (
              <FormNote>
                The server was started without a configuration file, so a change made here lasts
                until it restarts.
              </FormNote>
            )}
            {mayEdit && !resets && (
              <FormNote>
                {engine.label} publishes no default, so nothing resets: a change is put back from
                the line it leaves here.
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
                      {rows(group.parameters)}
                    </div>
                  ))}
                  {!all && matches.length > SHOWN && (
                    <Button
                      type="button"
                      size="xs"
                      variant="ghost"
                      className="-ml-2"
                      onClick={() => setAll(true)}
                    >
                      Show all {matches.length}
                    </Button>
                  )}
                </div>
              )
            ) : (
              <div className="space-y-0.5">
                {groups.map((group) => {
                  const unsaved = changes.filter(
                    (change) => change.parameter.group === group.name,
                  ).length
                  return (
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
                          {unsaved > 0 ? ` · ${unsaved} unsaved` : ""}
                        </span>
                      }
                    >
                      {/* Drawn once it is open: a closed heading holds no rows, and
                          a server has several hundred parameters. */}
                      {opened.includes(group.name) && rows(group.parameters)}
                    </Disclosure>
                  )
                })}
              </div>
            )}
          </div>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * One parameter, in one height whatever it has to say: its name with what is
 * true of it (waiting, changed, unsaved) over one line of what it means, and
 * at the row's end its value over one line of what that value comes to. Where
 * it can be changed the value is the control.
 *
 * What a dialog's head listed (the heading it is under, its default, its
 * range, where it may be set) and the whole of a description the row has cut
 * short are behind the ⓘ beside its name.
 */
/** The room a row's unit and reset take at its end, left on the lines that have neither. */
const SLOTS = "sm:pr-[4.5rem]"

function ParameterRow({
  parameter,
  editable,
  aligned,
  resets,
  edit,
  held,
  refusal,
  busy,
  onEdit,
}: {
  parameter: Parameter
  editable: boolean
  /** The reader may change parameters here: every row keeps the controls' edge, so values line up. */
  aligned: boolean
  /** The server can put a parameter back to a default it publishes. */
  resets: boolean
  edit: ParameterEdit | undefined
  held: string | undefined
  refusal: string | undefined
  busy: boolean
  onEdit: (next: ParameterEdit | undefined) => void
}) {
  const field = useId()
  const shown = shownValue(parameter, edit, held)
  const resetting = Boolean(edit && "reset" in edit)
  const unsaved = edit !== undefined && (resetting || shown !== (held ?? startValue(parameter)))
  const problem =
    unsaved && edit && !("reset" in edit)
      ? (refusal ?? parameterProblem(parameter, edit.value))
      : refusal
  const facts = valueFacts(parameter, shown)
  const range = rangeWords(parameter)
  const set = (value: string) => onEdit({ value })

  return (
    <li
      data-parameter={parameter.name}
      className="grid min-w-0 items-center gap-x-4 gap-y-1.5 py-2.5 sm:grid-cols-[minmax(0,1fr)_18rem]"
    >
      <div className="min-w-0 space-y-0.5">
        {/* A fixed line: a tag is a pixel taller than the name beside it. */}
        <p className="flex h-5 min-w-0 items-center gap-x-2">
          <span className="min-w-0 truncate font-mono text-body font-medium" title={parameter.name}>
            {parameter.name}
          </span>
          {parameter.pending && (
            <Tag tone="warning" className="shrink-0">
              waiting for a restart
            </Tag>
          )}
          {parameter.changed && !parameter.pending && <Tag className="shrink-0">changed</Tag>}
          {parameter.restart && <Tag className="shrink-0">restart to apply</Tag>}
          {unsaved && (
            <Tag tone="warning" className="shrink-0">
              {resetting ? "resets" : "unsaved"}
            </Tag>
          )}
          <InfoTip label={`About ${parameter.name}`}>
            <span className="block space-y-1">
              {parameter.description && <span className="block">{parameter.description}</span>}
              <span className="block">
                Under{" "}
                {parameter.subgroup
                  ? `${parameter.group} / ${parameter.subgroup}`
                  : parameter.group}
                {parameter.default !== undefined && ` · default ${parameter.default || "empty"}`}
                {range && ` · ${range}${parameter.unit ? ` ${parameter.unit}` : ""}`}
                {parameter.context && ` · set by ${parameter.context}`}
              </span>
              {parameter.restart && (
                <span className="block">
                  A change is stored at once and takes effect when the server is restarted.
                </span>
              )}
              {parameter.secret && (
                <span className="block">Write-only: the server never sends it back.</span>
              )}
            </span>
          </InfoTip>
        </p>
        <p
          className="truncate text-xs leading-relaxed text-muted-foreground"
          title={parameter.description}
        >
          {parameter.description || " "}
        </p>
      </div>
      <div className="flex min-w-0 flex-col gap-0.5 sm:items-end">
        {editable ? (
          <div className="flex h-11 w-full min-w-0 items-center gap-1.5 sm:h-8 sm:justify-end">
            {parameter.type === "bool" ? (
              <Segments
                label={`Change ${parameter.name}`}
                value={isOn(shown) ? boolWords(parameter.value).on : boolWords(parameter.value).off}
                disabled={busy}
                onChange={set}
                options={[
                  { value: boolWords(parameter.value).on, label: boolWords(parameter.value).on },
                  { value: boolWords(parameter.value).off, label: boolWords(parameter.value).off },
                ].map((option) => ({ ...option, mono: true }))}
              />
            ) : parameter.type === "enum" && parameter.options && parameter.options.length <= 3 ? (
              <Segments
                label={`Change ${parameter.name}`}
                value={shown}
                disabled={busy}
                onChange={set}
                options={parameter.options.map((option) => ({
                  value: option,
                  label: option,
                  mono: true,
                }))}
              />
            ) : parameter.type === "enum" && parameter.options?.length ? (
              <Select value={shown} onValueChange={set} disabled={busy}>
                <SelectTrigger
                  size="sm"
                  aria-label={`Change ${parameter.name}`}
                  className="w-full font-mono text-xs sm:w-44"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {parameter.options.map((option) => (
                    <SelectItem key={option} value={option} className="font-mono text-xs">
                      {option}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <>
                <Input
                  id={field}
                  aria-label={`Change ${parameter.name}`}
                  aria-invalid={Boolean(problem) || undefined}
                  type={parameter.secret ? "password" : "text"}
                  value={shown}
                  disabled={busy}
                  placeholder={
                    parameter.secret ? (parameter.secret.set ? "set" : "not set") : "empty"
                  }
                  onChange={(event) => set(event.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                  className="min-w-0 flex-1 font-mono sm:h-8 sm:text-right sm:text-xs"
                />
              </>
            )}
            {/* The unit's place and the reset's, kept on every row so the values end on one edge. */}
            <span className="w-8 shrink-0 truncate text-hint text-muted-foreground">
              {parameter.type !== "bool" && parameter.type !== "enum" ? parameter.unit : ""}
            </span>
            <span className="flex w-7 shrink-0 justify-center">
              {resets && parameter.changed && !resetting && (
                <IconAction
                  label={`Reset ${parameter.name} to its default`}
                  disabled={busy}
                  onClick={() => onEdit({ reset: true })}
                >
                  <RotateCounterClockwise />
                </IconAction>
              )}
            </span>
          </div>
        ) : (
          <div
            className={cn(
              "flex min-w-0 items-center sm:h-8 sm:justify-end",
              // Beside rows that hold a control it is the control's height; a list
              // nobody may edit is set closer.
              aligned ? cn("h-11", SLOTS) : "h-8",
            )}
          >
            <ParameterValue parameter={parameter} />
          </div>
        )}
        {/* One line in every state: the refusal takes the place of what the value comes to. */}
        <p
          role={problem ? "alert" : undefined}
          title={problem ?? facts}
          className={cn(
            "numeric w-full truncate text-hint sm:text-right",
            aligned && SLOTS,
            problem ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {problem ?? (resetting ? "back to the default" : facts) ?? ""}
          {" "}
        </p>
      </div>
    </li>
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
  if (parameter.type === "bool") {
    return (
      <Status
        tone={isOn(parameter.value) ? "running" : "stopped"}
        label={parameter.value}
        className="font-mono"
      />
    )
  }
  return (
    <span className="block truncate font-mono text-xs" title={parameter.value}>
      {parameter.value}
      {parameter.unit && <span className="text-muted-foreground"> {parameter.unit}</span>}
    </span>
  )
}
