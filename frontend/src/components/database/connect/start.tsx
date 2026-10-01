"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { Database, Play } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceGrid, EngineCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import {
  FlowActions,
  FlowPanel,
  FlowPanelBody,
  FlowPanelHeader,
  FlowSteps,
} from "@/components/flow"
import { Disclosure, Field, FieldRow, OptionList, OptionRow } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { engineOf, sectionHref } from "@/components/database/engine"
import {
  EMPTY_PROVISION,
  PHASE_SENTENCE,
  PROVISION_STEPS,
  SHELVES,
  provisionImage,
  provisionProblems,
  provisionRequest,
  shelfOf,
  type ProvisionDraft,
  type ProvisionPhase,
} from "@/components/database/connect/provision"
import { reveal } from "@/components/database/connect/reveal"
import { readInventory, readTemplates } from "@/components/database/fleet/read"
import type { DbProvisionResponse } from "@/components/database/fleet/types"
import { useDatabases } from "@/components/database/shell/databases-context"

/** How long a started engine is asked for before the page says it has not answered. */
const READY_WITHIN_MS = 3 * 60_000

type Work = { phase: ProvisionPhase; since: number }
type Failure = { message: string; container?: string }

/**
 * Start a new database on this server: an engine, and at most four answers.
 *
 * The engines are the server's own templates, shelved by the kind of store
 * they are and each drawn as itself. Choosing one opens the screen's one
 * focused surface beside the catalogue: a name, a version from the closed
 * list the server offers, the first database, and how far it reaches — which
 * is this server only unless the reader says otherwise, because the other
 * answer opens a port to the internet. Everything else a connection string
 * would carry is decided by the server and read back off the container: the
 * port is the next one free, the password is generated.
 *
 * Create starts the container and then keeps asking it to connect until the
 * engine inside answers, showing which of the three steps it is on. Waiting
 * here rather than holding one request open is what keeps a slow first boot
 * looking like progress. When it answers, the page lands on the new
 * database's home.
 */
export function StartNew({
  engine: chosen,
  onChoose,
  onStep,
}: {
  /** The template the address names. */
  engine: string
  onChoose: (engine: string | null) => void
  /** Which step of the sequence the screen is on, for the page's spine. */
  onStep: (step: number) => void
}) {
  const id = useId()
  const router = useRouter()
  const { drivers, driversSettled, refresh } = useDatabases()
  const templates = usePoll(readTemplates, 0)
  // Whether there is a Docker to start a container in: discovery's own
  // reading of it, which costs nothing more than it already spent.
  const inventory = usePoll(readInventory, 0)
  const docker = inventory.data?.scans.find((scan) => scan.source === "docker")
  const noDocker = docker && !docker.ok ? (docker.reason ?? "Docker did not answer.") : undefined

  const [draft, setDraft] = useState<ProvisionDraft>(EMPTY_PROVISION)
  const [work, setWork] = useState<Work | null>(null)
  const [failure, setFailure] = useState<Failure>()
  const [elapsed, setElapsed] = useState(0)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])
  const since = work?.since
  useEffect(() => {
    if (since === undefined) return
    const timer = window.setInterval(
      () => setElapsed(Math.round((Date.now() - since) / 1000)),
      1000,
    )
    return () => window.clearInterval(timer)
  }, [since])

  const template = templates.data?.find((one) => one.engine === chosen)
  const shelves = useMemo(() => {
    const catalogued = drivers !== undefined
    return SHELVES.map((shelf) => ({
      shelf,
      entries: (templates.data ?? []).filter(
        (one) => shelfOf(engineOf(one.flavor, drivers), catalogued) === shelf,
      ),
    })).filter((group) => group.entries.length > 0)
  }, [templates.data, drivers])
  // Three different products for the placeholder's mark: several templates
  // are one engine with an extension, and three elephants say one thing.
  const featured = [
    ...new Set((templates.data ?? []).flatMap((one) => engineOf(one.flavor, drivers).logo ?? [])),
  ].slice(0, 3)

  const set = (patch: Partial<ProvisionDraft>) => setDraft((held) => ({ ...held, ...patch }))
  const choose = (engine: string) => {
    if (work) return
    if (engine !== chosen) {
      setDraft(EMPTY_PROVISION)
      setFailure(undefined)
    }
    onChoose(engine)
    onStep(1)
    reveal(`${id}-settings`)
  }

  /** Keep asking the started container to connect until its engine answers. */
  const settle = async (container: string, label: string, started: number) => {
    const deadline = Date.now() + READY_WITHIN_MS
    for (;;) {
      if (!alive.current) return
      try {
        setWork({ phase: "wait", since: started })
        const connection = await post<DbConnection>("/databases/adopt", { container })
        // Adopting proves the engine accepted one connection, which is not
        // the same as being ready: MySQL and MariaDB accept connections on a
        // temporary server during their first-boot initialisation and then
        // restart. A ping that dials again is what makes "it is ready" true.
        setWork({ phase: "connect", since: started })
        const answer = await get<{ ok: boolean; error?: string }>(
          `/databases/${connection.id}/ping`,
        )
        if (!answer.ok) throw new Error(answer.error || "It is not accepting connections yet.")
        if (!alive.current) return
        notify.success(`${label} is ready`, { description: `Connected as ${connection.name}` })
        refresh()
        router.push(sectionHref(connection.id))
        return
      } catch (err) {
        if (!alive.current) return
        if (Date.now() > deadline) {
          setFailure({ message: errorMessage(err), container })
          setWork(null)
          onStep(1)
          return
        }
        await new Promise((resolve) => window.setTimeout(resolve, 2000))
      }
    }
  }

  const create = async () => {
    if (!template || work) return
    const started = Date.now()
    setElapsed(0)
    setFailure(undefined)
    setWork({ phase: "start", since: started })
    onStep(2)
    try {
      const answer = await post<DbProvisionResponse>(
        "/databases/provision",
        provisionRequest(template, draft),
      )
      if (answer.firewallError) {
        notify.warning("The firewall was not opened", { description: answer.firewallError })
      }
      await settle(answer.container, template.label, started)
    } catch (err) {
      if (!alive.current) return
      setFailure({ message: errorMessage(err) })
      setWork(null)
      onStep(1)
    }
  }

  const problems = template ? provisionProblems(template, draft) : {}
  const image = template ? provisionImage(template, draft) : ""
  const version = template
    ? template.versions.some((one) => one.version === draft.version)
      ? draft.version
      : template.defaultVersion
    : ""
  const blocked = Object.keys(problems).length > 0 || Boolean(noDocker)

  return (
    // The catalogue and the chosen engine's settings side by side, each the
    // height of the window with its own scroll: choosing an engine never
    // scrolls the catalogue away.
    <div className="grid min-w-0 items-start gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_26rem] xl:grid-rows-[minmax(0,1fr)] xl:items-stretch">
      <Panel plain id={`${id}-catalogue`} tabIndex={-1} className="min-w-0 scroll-mt-4 xl:min-h-0">
        <PanelHeader
          title="Engines"
          actions={
            templates.data && (
              <span className="numeric text-hint text-muted-foreground">
                {templates.data.length} to start from
              </span>
            )
          }
        />
        <PanelBody className="flex min-h-0 flex-1 flex-col gap-3">
          {templates.error && !templates.data && (
            <ErrorState error={templates.error} onRetry={templates.refresh} />
          )}
          {(!templates.data && !templates.error) || !driversSettled ? (
            <div role="status" aria-label="Loading the engines">
              <LoadingRows rows={4} />
            </div>
          ) : (
            // Padded by the cards' own lit edge, which would otherwise run
            // under the scrollbar, and pulled back out so they still start
            // where the title does.
            <div className="-mx-3 space-y-5 px-3 pt-1 pb-3 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
              {shelves.length === 0 && (
                <EmptyNote className="px-0 text-left">
                  This server offers no engine to start from here.
                </EmptyNote>
              )}
              {shelves.map((group) => (
                <div key={group.shelf} className="space-y-2">
                  <p className="eyebrow">{group.shelf}</p>
                  <ChoiceGrid columns={2} role="group" aria-label={group.shelf}>
                    {group.entries.map((one) => (
                      <EngineCard
                        key={one.engine}
                        engine={one.engine}
                        label={one.label}
                        kind={engineOf(one.flavor, drivers).kind}
                        detail={one.image}
                        selected={chosen === one.engine}
                        working={Boolean(work) && chosen === one.engine}
                        disabled={Boolean(work) && chosen !== one.engine}
                        onClick={() => choose(one.engine)}
                      />
                    ))}
                  </ChoiceGrid>
                </div>
              ))}
            </div>
          )}
        </PanelBody>
      </Panel>

      {template ? (
        // The one surface with depth on this screen (§16): what is being
        // decided is this engine's answers and the command that leaves.
        <FlowPanel
          id={`${id}-settings`}
          aria-label={`${template.label} settings`}
          aria-busy={Boolean(work)}
          tabIndex={-1}
          className="order-first min-w-0 scroll-mt-4 xl:order-last xl:max-h-full xl:min-h-0 xl:self-start"
        >
          <FlowPanelHeader
            title={
              <span className="flex min-w-0 items-center gap-2.5">
                <ProductLogo id={template.engine} size="sm" fallback={Database} />
                <span className="truncate">{template.label}</span>
              </span>
            }
            actions={
              <Tag mono className="max-w-40 min-w-0 shrink">
                <span className="truncate">{image}</span>
              </Tag>
            }
          />
          {work ? (
            <FlowPanelBody className="space-y-4 py-6">
              <FlowSteps
                steps={[...PROVISION_STEPS]}
                current={PROVISION_STEPS.findIndex((step) => step.key === work.phase)}
              />
              <p className="flex items-baseline justify-between gap-3 text-body" role="status">
                <TextShimmer className="font-medium">{PHASE_SENTENCE[work.phase]}</TextShimmer>
                <span className="numeric shrink-0 text-hint text-muted-foreground">{elapsed}s</span>
              </p>
              <p className="text-hint leading-relaxed text-muted-foreground">
                The first start of an engine pulls its image, which can take a few minutes. It opens
                on the database&apos;s own page when it answers; leave and it is listed under Found
                on this server.
              </p>
            </FlowPanelBody>
          ) : (
            <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
              {noDocker && (
                <Notice tone="warning" title="There is no Docker to start it in">
                  <span className="break-words">{noDocker}</span>
                </Notice>
              )}
              {failure && (
                <Notice
                  tone="danger"
                  title={
                    failure.container
                      ? `${failure.container} started and has not answered`
                      : "It was not started"
                  }
                >
                  <span className="break-words whitespace-pre-wrap">{failure.message}</span>
                  {failure.container && (
                    <span className="mt-2 block">
                      <Button
                        size="xs"
                        variant="outline"
                        onClick={() => {
                          const started = Date.now()
                          const container = failure.container as string
                          setFailure(undefined)
                          setElapsed(0)
                          onStep(2)
                          void settle(container, template.label, started)
                        }}
                      >
                        Keep waiting
                      </Button>
                    </span>
                  )}
                </Notice>
              )}
              <Field
                label="Name"
                htmlFor={`${id}-name`}
                hint="The container's name, and how the database is listed here."
                error={problems.name}
              >
                <Input
                  id={`${id}-name`}
                  value={draft.name}
                  onChange={(event) => set({ name: event.target.value })}
                  placeholder={`jd-${template.engine}`}
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </Field>
              {template.versions.length > 1 && (
                <Field label="Version">
                  <Segments
                    label={`${template.label} version`}
                    value={version}
                    onChange={(next) => set({ version: next })}
                    options={template.versions.map((one) => ({
                      value: one.version,
                      label: one.version,
                      mono: true,
                    }))}
                  />
                </Field>
              )}
              {template.database && (
                <Field
                  label="Database"
                  htmlFor={`${id}-database`}
                  hint="Created empty at the first start."
                  error={problems.database}
                >
                  <Input
                    id={`${id}-database`}
                    value={draft.database}
                    onChange={(event) => set({ database: event.target.value })}
                    placeholder="app"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </Field>
              )}
              <Disclosure quiet summary="Account">
                <FieldRow columns={template.defaultUser ? 2 : undefined}>
                  {template.defaultUser && (
                    <Field label="User" htmlFor={`${id}-user`} error={problems.user}>
                      <Input
                        id={`${id}-user`}
                        value={draft.user}
                        onChange={(event) => set({ user: event.target.value })}
                        placeholder={template.defaultUser}
                        className="font-mono"
                        autoComplete="off"
                        spellCheck={false}
                      />
                    </Field>
                  )}
                  <Field
                    label="Password"
                    htmlFor={`${id}-password`}
                    error={problems.password}
                    hint="Empty generates one, kept sealed with the connection."
                  >
                    <Input
                      id={`${id}-password`}
                      type="password"
                      value={draft.password}
                      onChange={(event) => set({ password: event.target.value })}
                      placeholder="generated"
                      className="font-mono"
                      autoComplete="new-password"
                    />
                  </Field>
                </FieldRow>
              </Disclosure>
              <OptionList>
                <OptionRow
                  // Amber only while it is on: the risk is the choice, not the option.
                  tone={draft.shared ? "warning" : "default"}
                  title="Publish its port on every interface and open the firewall for it, so anything on the internet can reach it"
                  checked={draft.shared}
                  onCheckedChange={(shared) => set({ shared })}
                />
              </OptionList>
            </FlowPanelBody>
          )}
          {!work && (
            <FlowActions
              note={
                draft.shared
                  ? "It will answer on every address this server has."
                  : "It will answer on this server only."
              }
              secondary={
                <Button
                  variant="ghost"
                  className="h-11 xl:hidden"
                  onClick={() => reveal(`${id}-catalogue`)}
                >
                  Choose another engine
                </Button>
              }
            >
              <Button className="h-11 sm:h-9" disabled={blocked} onClick={() => void create()}>
                <Play />
                Create
              </Button>
            </FlowActions>
          )}
        </FlowPanel>
      ) : (
        // Only at the width that draws the two columns: stacked, a promise
        // above the catalogue is a block of nothing between the reader and it.
        <EmptyState
          className="hidden xl:flex xl:self-start"
          icon={Database}
          mark={featured.length > 0 ? <ProductLogos ids={featured} size="md" /> : undefined}
          title="Pick an engine"
          description="Its settings open here. Nothing is started until you create it."
        />
      )}
    </div>
  )
}
