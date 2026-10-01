"use client"

import { useId, useState } from "react"
import { useRouter } from "next/navigation"
import { Database, Linked, Router } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { forgetMemoryState, useMemoryState } from "@/lib/view-state"
import type { DbConnection, DbDriver, FilePlaces } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceGrid, EngineCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { engineOf, sectionHref } from "@/components/database/engine"
import {
  EMPTY_CONNECT_FIELDS,
  TLS_WORD,
  composeDsn,
  driverOfUrl,
  maskPasted,
  maskedDsn,
  pastedDsn,
  tlsModes,
  type ConnectFields,
} from "@/components/database/connect/dsn"
import { reveal } from "@/components/database/connect/reveal"
import { CONNECTION_NAME, ENVIRONMENTS, suggestedName } from "@/components/database/connect/rules"
import type { DbCreateRequest, DbTestResponse } from "@/components/database/fleet/types"
import { EngineMark } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

const DRAFT = "databases.new.connect"

type Tested = { dsn: string; answer: DbTestResponse }

/**
 * Connect a database the dashboard did not find: a managed server, another
 * machine, a file.
 *
 * The engine first, as the card every engine picker draws. Then the address
 * as the fields an operator knows — host, port, account, database, whether
 * the connection is encrypted — rendered into the engine's own string, or the
 * string pasted whole for an address with an option the fields do not offer.
 * A file-based engine asks for a path and says which directories it may be in.
 *
 * Test dials the string and reports what answered, which is what turns "did I
 * get the host right" from a save-and-pray into an answer. Connect is live
 * once the string on screen has passed, or once the reader says to save it
 * untested (a server that is down today); either way the server is asked to
 * dial before it saves unless told not to, so the engine's refusal is shown
 * here, beside the fields that caused it.
 *
 * What is typed is kept for the tab in memory only — a password is among it —
 * so looking at another way in and coming back does not mean typing it again.
 */
export function ConnectExisting({
  engine: chosen,
  onChoose,
  onStep,
}: {
  /** The driver the address names. */
  engine: string
  onChoose: (engine: string | null) => void
  onStep: (step: number) => void
}) {
  const id = useId()
  const router = useRouter()
  const { drivers, driversSettled, connections, refresh } = useDatabases()
  const info = drivers?.find((one) => one.id === chosen)
  const driver = info?.id
  const engine = driver ? engineOf(driver, drivers) : undefined
  const fileBased = Boolean(engine?.can("fileBased"))

  const [name, setName] = useMemoryState(`${DRAFT}.${chosen}.name`, "")
  const [shape, setShape] = useMemoryState<"fields" | "url">(`${DRAFT}.${chosen}.shape`, "fields")
  const [fields, setFields] = useMemoryState<ConnectFields>(
    `${DRAFT}.${chosen}.fields`,
    EMPTY_CONNECT_FIELDS,
  )
  const [pasted, setPasted] = useMemoryState(`${DRAFT}.${chosen}.url`, "")
  const [environment, setEnvironment] = useMemoryState(`${DRAFT}.${chosen}.environment`, "")
  const [readOnly, setReadOnly] = useMemoryState(`${DRAFT}.${chosen}.readOnly`, false)
  const [untested, setUntested] = useState(false)
  const [tested, setTested] = useState<Tested>()
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  // Where a database file may live: the directories the dashboard is allowed
  // to open, read only for an engine that is a file.
  const places = usePoll((signal) => get<FilePlaces>("/files/places", undefined, signal), 0, [], {
    enabled: fileBased,
  })

  const set = (patch: Partial<ConnectFields>) => {
    setFields((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }
  const choose = (next: string) => {
    onChoose(next)
    onStep(1)
    setTested(undefined)
    setRefusal(undefined)
    reveal(`${id}-settings`)
  }

  const modes = driver ? tlsModes(driver) : []
  const dsn = !driver
    ? ""
    : fileBased
      ? fields.database.trim()
      : shape === "url"
        ? pastedDsn(driver, pasted)
        : fields.host.trim()
          ? composeDsn(driver, fields)
          : ""
  const typedName = name.trim()
  const offered = engine
    ? suggestedName(
        fields,
        engine.label.toLowerCase(),
        connections.map((conn) => conn.name),
      )
    : ""
  const finalName = typedName || (dsn ? offered : "")
  const nameProblem =
    typedName && !CONNECTION_NAME.test(typedName)
      ? "Letters, digits, spaces, dots, dashes and underscores, starting with a letter or digit."
      : connections.some((conn) => conn.name.toLowerCase() === typedName.toLowerCase()) && typedName
        ? "Another connection already has this name."
        : undefined
  const passed = tested?.dsn === dsn && tested.answer.ok ? tested.answer : undefined
  const failed = tested?.dsn === dsn && !tested.answer.ok ? tested.answer.error : undefined
  const otherEngine = driver && shape === "url" ? driverOfUrl(pasted) : undefined
  const mismatch = otherEngine && otherEngine !== driver ? otherEngine : undefined
  const ready = dsn !== "" && finalName !== "" && !nameProblem && (Boolean(passed) || untested)

  const test = async () => {
    if (!driver || !dsn) return
    setTesting(true)
    setRefusal(undefined)
    try {
      const answer = await post<DbTestResponse>("/databases/test", { driver, dsn })
      setTested({ dsn, answer })
    } catch (err) {
      setTested({ dsn, answer: { ok: false, error: errorMessage(err) } })
    } finally {
      setTesting(false)
    }
  }

  const save = async () => {
    if (!driver || !ready) return
    setSaving(true)
    setRefusal(undefined)
    onStep(2)
    try {
      const connection = await post<DbConnection>("/databases/", {
        name: finalName,
        driver,
        dsn,
        // Asked to dial even after a passing test: the test and the save are
        // two requests, and only the save's own dial is the one that counts.
        probe: !untested,
        environment: environment || undefined,
        readOnly: readOnly || undefined,
      } satisfies DbCreateRequest)
      forgetMemoryState(DRAFT)
      notify.success(`Connected ${connection.name}`)
      refresh()
      router.push(sectionHref(connection.id))
    } catch (err) {
      setRefusal(errorMessage(err))
      onStep(1)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="grid min-w-0 items-start gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_26rem] xl:grid-rows-[minmax(0,1fr)] xl:items-stretch">
      <Panel plain id={`${id}-catalogue`} tabIndex={-1} className="min-w-0 scroll-mt-4 xl:min-h-0">
        <PanelHeader title="Engines" />
        <PanelBody className="flex min-h-0 flex-1 flex-col gap-3">
          {!driversSettled ? (
            <div role="status" aria-label="Loading the engines">
              <LoadingRows rows={4} />
            </div>
          ) : !drivers ? (
            <Notice tone="danger" title="The engines could not be read">
              The list of engines this dashboard opens did not load. Reload the page to ask again.
            </Notice>
          ) : (
            <div className="-mx-3 px-3 pt-1 pb-3 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
              <ChoiceGrid columns={2} role="group" aria-label="Engines">
                {drivers.map((one) => {
                  const kind = engineOf(one.id, drivers)
                  return (
                    <EngineCard
                      key={one.id}
                      engine={one.id}
                      label={one.label}
                      kind={kind.kind}
                      detail={one.defaultPort ? `port ${one.defaultPort}` : "a file on this server"}
                      selected={chosen === one.id}
                      disabled={saving && chosen !== one.id}
                      onClick={() => choose(one.id)}
                    />
                  )
                })}
              </ChoiceGrid>
            </div>
          )}
        </PanelBody>
      </Panel>

      {driver && engine && info ? (
        <FlowPanel
          id={`${id}-settings`}
          aria-label={`${info.label} connection`}
          aria-busy={saving}
          tabIndex={-1}
          className="order-first min-w-0 scroll-mt-4 xl:order-last xl:max-h-full xl:min-h-0 xl:self-start"
        >
          <FlowPanelHeader
            title={
              <span className="flex min-w-0 items-center gap-2.5">
                <EngineMark engine={engine} size="sm" />
                <span className="truncate">{info.label}</span>
              </span>
            }
            actions={
              !fileBased && (
                <Segments
                  label="How the address is given"
                  value={shape}
                  onChange={(next) => {
                    setShape(next)
                    setRefusal(undefined)
                  }}
                  options={[
                    { value: "fields", label: "Fields" },
                    { value: "url", label: "URL" },
                  ]}
                />
              )
            }
          />
          <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
            {refusal && (
              <Notice tone="danger" title="It was not saved">
                <span className="break-words whitespace-pre-wrap">{refusal}</span>
              </Notice>
            )}
            {failed && !refusal && (
              <Notice tone="danger" title="It refused the connection">
                <span className="break-words whitespace-pre-wrap">{failed}</span>
              </Notice>
            )}

            {fileBased ? (
              <Field
                label={engine.databaseField}
                htmlFor={`${id}-path`}
                hint={
                  places.data
                    ? `A path on this server inside ${places.data.roots.join(" or ")}. It is created if it does not exist.`
                    : "A path on this server inside the dashboard's file roots. It is created if it does not exist."
                }
              >
                <Input
                  id={`${id}-path`}
                  value={fields.database}
                  onChange={(event) => set({ database: event.target.value })}
                  placeholder={engine.dsnExample}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
            ) : shape === "url" ? (
              <Field
                label="Connection string"
                htmlFor={`${id}-url`}
                hint="Pasted whole, as the application that uses it would write it."
                error={
                  mismatch ? `That is a ${engineOf(mismatch, drivers).label} address.` : undefined
                }
                trailing={
                  mismatch && (
                    <Button size="xs" variant="ghost" onClick={() => choose(mismatch)}>
                      Switch engine
                    </Button>
                  )
                }
              >
                <Input
                  id={`${id}-url`}
                  // Shown masked once typed: a connection string is a
                  // password with an address round it.
                  type="password"
                  value={pasted}
                  onChange={(event) => {
                    setPasted(event.target.value)
                    setRefusal(undefined)
                  }}
                  placeholder={engine.dsnExample}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
            ) : (
              <>
                <FieldRow columns={3}>
                  <Field label="Host" htmlFor={`${id}-host`} className="sm:col-span-2">
                    <Input
                      id={`${id}-host`}
                      value={fields.host}
                      onChange={(event) => set({ host: event.target.value })}
                      placeholder="db.example.com"
                      className="font-mono"
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </Field>
                  <Field label="Port" htmlFor={`${id}-port`}>
                    <Input
                      id={`${id}-port`}
                      value={fields.port}
                      onChange={(event) => set({ port: event.target.value.replace(/[^0-9]/g, "") })}
                      placeholder={engine.defaultPort}
                      className="font-mono"
                      inputMode="numeric"
                      autoComplete="off"
                    />
                  </Field>
                </FieldRow>
                <FieldRow>
                  <Field label="User" htmlFor={`${id}-user`}>
                    <Input
                      id={`${id}-user`}
                      value={fields.user}
                      onChange={(event) => set({ user: event.target.value })}
                      className="font-mono"
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </Field>
                  <Field label="Password" htmlFor={`${id}-password`}>
                    <Input
                      id={`${id}-password`}
                      type="password"
                      value={fields.password}
                      onChange={(event) => set({ password: event.target.value })}
                      className="font-mono"
                      autoComplete="new-password"
                    />
                  </Field>
                </FieldRow>
                <FieldRow>
                  <Field label={engine.databaseField} htmlFor={`${id}-database`}>
                    <Input
                      id={`${id}-database`}
                      value={fields.database}
                      onChange={(event) => set({ database: event.target.value })}
                      className="font-mono"
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </Field>
                  {engine.kind === "document" && (
                    <Field
                      label="Account's database"
                      htmlFor={`${id}-auth`}
                      hint="Where the account was created, when it is not this one."
                    >
                      <Input
                        id={`${id}-auth`}
                        value={fields.authSource}
                        onChange={(event) => set({ authSource: event.target.value })}
                        placeholder="admin"
                        className="font-mono"
                        autoComplete="off"
                        spellCheck={false}
                      />
                    </Field>
                  )}
                </FieldRow>
                {modes.length > 1 && (
                  <Field
                    label="Encryption"
                    hint="Required encrypts without checking who answers; verified also checks the server's certificate."
                  >
                    <Segments
                      label="Encryption"
                      value={modes.includes(fields.tls) ? fields.tls : modes[0]}
                      onChange={(tls) => set({ tls })}
                      options={modes.map((mode) => ({ value: mode, label: TLS_WORD[mode] }))}
                    />
                  </Field>
                )}
              </>
            )}

            {dsn && !fileBased && (
              <FormNote className="truncate font-mono" data-slot="connection-preview">
                {shape === "url" ? maskPasted(dsn) : maskedDsn(driver as DbDriver, fields)}
              </FormNote>
            )}

            <Field
              label="Name"
              htmlFor={`${id}-name`}
              hint="How it is listed here."
              error={nameProblem}
            >
              <Input
                id={`${id}-name`}
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={offered}
                autoComplete="off"
              />
            </Field>
            <Field label="Environment">
              <Segments
                label="Environment"
                value={environment || "none"}
                onChange={(next) => setEnvironment(next === "none" ? "" : next)}
                options={[
                  { value: "none", label: "None" },
                  ...ENVIRONMENTS.map((word) => ({
                    value: word as string,
                    label: word[0].toUpperCase() + word.slice(1),
                  })),
                ]}
              />
            </Field>
            <OptionList>
              <OptionRow
                title="Protect it: refuse every change made to its data or schema through this dashboard"
                checked={readOnly}
                onCheckedChange={setReadOnly}
              />
              <OptionRow
                title="Save it without testing, for a server that is not answering now"
                checked={untested}
                onCheckedChange={setUntested}
              />
            </OptionList>
          </FlowPanelBody>
          <FlowActions
            note={
              passed ? (
                <span className="flex" data-slot="test-result">
                  <Status tone="running" label={`Answered: ${passed.version}`} />
                </span>
              ) : (
                "The password is sealed on the server and never sent back to a browser."
              )
            }
            secondary={
              <>
                <Button
                  variant="ghost"
                  className="h-11 xl:hidden"
                  onClick={() => reveal(`${id}-catalogue`)}
                >
                  Another engine
                </Button>
                <Button
                  variant="outline"
                  className="h-11 sm:h-9"
                  disabled={!dsn || saving}
                  pending={testing}
                  onClick={() => void test()}
                >
                  <Router />
                  Test
                </Button>
              </>
            }
          >
            <Button
              className="h-11 sm:h-9"
              disabled={!ready}
              pending={saving}
              onClick={() => void save()}
            >
              <Linked />
              Connect
            </Button>
          </FlowActions>
        </FlowPanel>
      ) : (
        <EmptyState
          className="hidden xl:flex xl:self-start"
          icon={Database}
          mark={<ProductLogos ids={["postgres", "mysql", "mongodb"]} size="md" />}
          title="Pick an engine"
          description="Its address opens here. Nothing is saved until it connects."
        />
      )}
    </div>
  )
}
