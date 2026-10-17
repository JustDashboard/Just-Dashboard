"use client"

import { useId, useState } from "react"
import { Puzzle } from "@/components/icons"
import { del, errorMessage } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { usePoll } from "@/hooks/use-poll"
import { Segments } from "@/components/deploy/settings/segments"
import { GroupRule } from "@/components/flow"
import {
  Field,
  FieldRow,
  FormFact,
  FormNote,
  OptionList,
  OptionRow,
  Statement,
} from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { ChangeDialog } from "@/components/database/schema/change-dialog"
import type { DdlRequest } from "@/components/database/schema/changes"
import {
  extensionLogo,
  listWords,
  memberWords,
  type DbExtension,
  type ExtensionDetail,
} from "@/components/database/ops/extensions-model"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The three things done to one extension, each in the dialog its kind takes:
 * enabling and updating are structure changes, whose statement the server
 * plans as the form changes (`ChangeDialog`); disabling destroys what the
 * extension added and whatever was built on it, so it lists that first and
 * asks for CASCADE by name before the statement can include it.
 *
 * Each reads the extension's detail when it opens: the versions the server
 * ships, the schemas it may go in, what it can be updated to, and what
 * depends on it. Nothing here builds SQL; the statement on screen is the
 * route's own.
 */

type DialogProps = {
  extension: DbExtension
  onClose: () => void
  /** Something changed: the list is out of date. */
  onDone: () => void
}

/** The value a Select carries for "let the server choose", which Radix will not take as "". */
const SERVER_CHOICE = "__server_default__"

function useExtensionDetail(name: string) {
  const { id } = useDatabase()
  return usePoll(
    (signal) =>
      read<ExtensionDetail>(
        `/databases/${id}/server/extensions/${encodeURIComponent(name)}`,
        (answer) => Array.isArray(answer.versions),
        undefined,
        signal,
      ),
    0,
    [id, name],
  )
}

/** The extension drawn as itself: the product it is, or a piece of a puzzle. */
export function ExtensionMark({ extension }: { extension: DbExtension }) {
  const { engine } = useDatabase()
  return (
    <ProductLogo
      id={extensionLogo(extension.name, engine.logo, false)}
      size="sm"
      fallback={Puzzle}
    />
  )
}

/** What tells an extension from its neighbours, as a dialog's subject facts. */
function ExtensionFacts({ extension }: { extension: DbExtension }) {
  return (
    <>
      {extension.category && <FormFact label="Category">{extension.category}</FormFact>}
      {extension.installed && extension.version && (
        <FormFact label="Version" mono>
          {extension.version}
        </FormFact>
      )}
      {extension.installed && extension.schema && (
        <FormFact label="In" mono>
          {extension.schema}
        </FormFact>
      )}
    </>
  )
}

/** The read every dialog waits on, said in the dialog's own place. */
function DetailState({ detail }: { detail: { error: Error | undefined; refresh: () => void } }) {
  return detail.error ? (
    <CouldNotRead what="the extension" error={detail.error} onRetry={detail.refresh} />
  ) : (
    <LoadingRows rows={2} />
  )
}

/**
 * Enabling one, with the three options CREATE EXTENSION takes: the version,
 * the schema it goes in, and CASCADE for what it needs that the database
 * lacks. Each is offered only where the server leaves a choice — one version
 * shipped, a schema the control file fixes, nothing missing — and the
 * defaults are the server's.
 */
export function EnableExtension({ extension, onClose, onDone }: DialogProps) {
  const { conn } = useDatabase()
  const ids = useId()
  const detail = useExtensionDetail(extension.name)
  const [version, setVersion] = useState("")
  const [schema, setSchema] = useState("")
  const [cascadeChosen, setCascade] = useState<boolean>()
  const data = detail.data
  const chosen = data?.versions.find((one) => one.version === (version || data.availableVersion))
  const missing = chosen?.missing ?? []
  const cascade = cascadeChosen ?? missing.length > 0
  const fixed = chosen?.schema
  const request: DdlRequest | null = data
    ? {
        method: "POST",
        path: "/server/extensions",
        body: {
          name: extension.name,
          ...(version ? { version } : {}),
          ...(schema && !fixed ? { schema } : {}),
          ...(cascade ? { cascade: true } : {}),
        },
      }
    : null

  return (
    <ChangeDialog
      title={`Enable ${extension.name}`}
      subject={{ name: extension.name, facts: <ExtensionFacts extension={extension} /> }}
      request={request}
      waiting="Reading what the server ships…"
      command="Enable"
      done={`Enabled ${extension.name}`}
      dirty={version !== "" || schema !== "" || cascadeChosen !== undefined}
      onDone={onDone}
      onClose={onClose}
    >
      {(extension.about || extension.comment) && (
        <p className="text-body leading-relaxed text-muted-foreground first-letter:uppercase">
          {extension.about ?? extension.comment}
        </p>
      )}
      {!data ? (
        <DetailState detail={detail} />
      ) : (
        <div className="space-y-4">
          <FieldRow>
            {data.versions.length > 1 && (
              <Field
                label="Version"
                htmlFor={`${ids}-version`}
                hint={`The server's default is ${data.availableVersion}.`}
              >
                <Select
                  value={version || SERVER_CHOICE}
                  onValueChange={(next) => setVersion(next === SERVER_CHOICE ? "" : next)}
                >
                  <SelectTrigger id={`${ids}-version`} className="w-full font-mono text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={SERVER_CHOICE} className="font-mono text-xs">
                      {data.availableVersion} (default)
                    </SelectItem>
                    {data.versions
                      .filter((one) => one.version !== data.availableVersion)
                      .map((one) => (
                        <SelectItem
                          key={one.version}
                          value={one.version}
                          className="font-mono text-xs"
                        >
                          {one.version}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            <Field
              label="Schema"
              htmlFor={fixed ? undefined : `${ids}-schema`}
              hint={
                fixed
                  ? `It is always created in ${fixed}.`
                  : chosen && !chosen.relocatable
                    ? "Where its functions and types go. It cannot be moved afterwards."
                    : "Where its functions and types go."
              }
            >
              {fixed ? (
                <p className="flex h-8 items-center font-mono text-xs">{fixed}</p>
              ) : (
                <Select
                  value={schema || SERVER_CHOICE}
                  onValueChange={(next) => setSchema(next === SERVER_CHOICE ? "" : next)}
                >
                  <SelectTrigger id={`${ids}-schema`} className="w-full font-mono text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={SERVER_CHOICE} className="font-mono text-xs">
                      {data.defaultSchema
                        ? `${data.defaultSchema} (default)`
                        : "The search path's first"}
                    </SelectItem>
                    {data.schemas
                      .filter((one) => one !== data.defaultSchema)
                      .map((one) => (
                        <SelectItem key={one} value={one} className="font-mono text-xs">
                          {one}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
          </FieldRow>
          {missing.length > 0 && (
            <OptionList>
              <OptionRow
                title={`Also enable what it needs: ${listWords(missing)}`}
                hint="CASCADE creates the extensions it requires, in the same statement."
                checked={cascade}
                onCheckedChange={setCascade}
              />
            </OptionList>
          )}
          {missing.length > 0 && !cascade && (
            <FormNote tone="warning">
              Without it the server refuses: {listWords(missing)}{" "}
              {missing.length === 1 ? "is" : "are"} not enabled in {conn.database || conn.name}.
            </FormNote>
          )}
          {chosen?.superuser && !chosen.trusted && !data.superuser && (
            <FormNote tone="warning">
              Only a superuser can create {extension.name}, and the account this connection signs in
              as is not one: the server will refuse.
            </FormNote>
          )}
        </div>
      )}
    </ChangeDialog>
  )
}

/**
 * Updating one, along the paths the server ships update scripts for: to its
 * default version, or to another it can reach. "Update available" on the row
 * is the server's default being newer than what is installed; the versions
 * offered here are the ones with a path.
 */
export function UpdateExtension({ extension, onClose, onDone }: DialogProps) {
  const ids = useId()
  const detail = useExtensionDetail(extension.name)
  const [picked, setPicked] = useState<string>()
  const data = detail.data
  const updates = data?.updates ?? []
  const fallback =
    data && updates.includes(data.availableVersion ?? "") ? data.availableVersion : updates[0]
  const target = picked ?? fallback
  const request: DdlRequest | null =
    data && target
      ? {
          method: "PATCH",
          path: `/server/extensions/${encodeURIComponent(extension.name)}`,
          body: { version: target },
        }
      : null

  return (
    <ChangeDialog
      title={`Update ${extension.name}`}
      subject={{ name: extension.name, facts: <ExtensionFacts extension={extension} /> }}
      request={request}
      waiting={
        data
          ? `The server ships no update for ${extension.name} from ${extension.version}.`
          : "Reading the versions it can be updated to…"
      }
      command="Update"
      done={`Updated ${extension.name} to ${target}`}
      dirty={picked !== undefined}
      onDone={onDone}
      onClose={onClose}
    >
      {!data ? (
        <DetailState detail={detail} />
      ) : updates.length === 0 ? (
        <FormNote>
          {extension.name} is at {extension.version}, and the server has no update script from it.
        </FormNote>
      ) : (
        <Field
          label="Update to"
          htmlFor={`${ids}-target`}
          hint={`From ${extension.version}. The server's default is ${data.availableVersion}; its update scripts run in turn to the version chosen.`}
        >
          {updates.length <= 5 ? (
            <Segments
              id={`${ids}-target`}
              label="Version to update to"
              value={target ?? ""}
              onChange={setPicked}
              className="flex-wrap"
              options={updates.map((one) => ({ value: one, label: one, mono: true }))}
            />
          ) : (
            <Select value={target} onValueChange={setPicked}>
              <SelectTrigger id={`${ids}-target`} className="w-full font-mono text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {updates.map((one) => (
                  <SelectItem key={one} value={one} className="font-mono text-xs">
                    {one === data.availableVersion ? `${one} (default)` : one}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Field>
      )}
    </ChangeDialog>
  )
}

/**
 * Disabling one: what it added goes with it, and so does everything outside
 * it that is built on it — a column of its type, an index on its operator
 * class, another extension that needs it. Those are listed, by the engine's
 * own names for them, before anything can be pressed; while there are any,
 * the command waits for CASCADE to be chosen by name, because without it the
 * engine refuses and with it they are gone.
 */
export function DisableExtension({ extension, onClose, onDone }: DialogProps) {
  const { id, conn } = useDatabase()
  const detail = useExtensionDetail(extension.name)
  const [cascade, setCascade] = useState(false)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const data = detail.data
  const total = data?.dependentsTotal ?? 0
  const statement = (cascade ? data?.dropCascade : data?.drop) ?? ""

  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      const answer = await del<{ statement: string }>(
        `/databases/${id}/server/extensions/${encodeURIComponent(extension.name)}`,
        cascade ? { query: { cascade: 1 } } : {},
      )
      notify.success(`Disabled ${extension.name}`, { description: answer.statement })
      onDone()
      onClose()
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <TaskDialog
      title={`Disable ${extension.name}`}
      description={`Drop ${extension.name} from ${conn.database || conn.name}, with what depends on it`}
      subject={{
        mark: <ExtensionMark extension={extension} />,
        name: <span className="font-mono">{extension.name}</span>,
        facts: <ExtensionFacts extension={extension} />,
      }}
      dirty={cascade}
      busy={busy}
      refusal={refusal}
      command="Disable"
      destructive
      disabled={!data || (total > 0 && !cascade)}
      onRun={() => void run()}
      onClose={onClose}
    >
      {!data ? (
        <DetailState detail={detail} />
      ) : (
        <>
          <p className="text-body leading-relaxed">
            Drops it from {conn.database || conn.name}, with what it added
            {data.members.length > 0 ? `: ${memberWords(data.members)}` : ""}.
            {extension.library &&
              ` The server goes on loading ${extension.library} at start; it is taken out of shared_preload_libraries in Parameters.`}
          </p>
          {total === 0 ? (
            <FormNote>Nothing outside the extension depends on it.</FormNote>
          ) : (
            <div className="space-y-3">
              <GroupRule label="What depends on it" count={total} />
              <ul
                aria-label={`What depends on ${extension.name}`}
                className="max-h-52 divide-y divide-hairline overflow-y-auto"
              >
                {data.dependents.map((dependent) => (
                  <li
                    key={dependent.object}
                    className="flex min-w-0 flex-wrap items-baseline gap-x-2 py-1.5"
                  >
                    <span className="min-w-0 font-mono text-xs wrap-anywhere">
                      {dependent.object}
                    </span>
                    {dependent.via && (
                      <span className="min-w-0 text-hint wrap-anywhere text-muted-foreground">
                        uses {dependent.via}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
              {total > data.dependents.length && (
                <FormNote>
                  And {plural(total - data.dependents.length, "more object")} the engine names when
                  it drops them.
                </FormNote>
              )}
              <OptionList>
                <OptionRow
                  tone="danger"
                  title={`Drop ${total === 1 ? "it" : `these ${total}`} with the extension`}
                  hint="CASCADE removes them in the same statement: a column goes with its data, a view or a function goes whole."
                  checked={cascade}
                  onCheckedChange={setCascade}
                />
              </OptionList>
              {!cascade && (
                <FormNote>
                  Without CASCADE the server refuses while anything depends on it.
                </FormNote>
              )}
            </div>
          )}
          <Statement sql={statement} placeholder="Reading the statement from the server…" />
        </>
      )}
    </TaskDialog>
  )
}
