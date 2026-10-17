"use client"

import { Fragment, useEffect, useId, useMemo, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { Database, Play } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbConnection, DbDriverInfo, Job, JobLine } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceGrid, EngineCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Disclosure, Field, FieldRow, OptionList, OptionRow } from "@/components/form"
import { JobConsole } from "@/components/job-console"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogos } from "@/components/product-logo"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { BorderBeam } from "@/components/ui/border-beam"
import { DatabaseProgress } from "@/components/database/connect/progress"
import { engineOf, sectionHref } from "@/components/database/engine"
import {
  EMPTY_PROVISION,
  SHELVES,
  chosenRelease,
  needsBuild,
  provisionImage,
  provisionProblems,
  provisionRequest,
  releaseShelves,
  shelfOf,
  type ProvisionDraft,
  type ProvisionPhase,
} from "@/components/database/connect/provision"
import { reveal } from "@/components/database/connect/reveal"
import { readInventory, readTemplates } from "@/components/database/fleet/read"
import type {
  DbProvisionRelease,
  DbProvisionResponse,
  DbProvisionTemplate,
} from "@/components/database/fleet/types"
import { EngineMark } from "@/components/database/kit"
import { supportWords } from "@/components/database/support"
import { useDatabases } from "@/components/database/shell/databases-context"

/** How long a started engine is asked for before the page says it has not answered. */
const READY_WITHIN_MS = 3 * 60_000

/** How often a build is read while it runs: one cheap read of the server's memory. */
const BUILD_EVERY_MS = 1000

/** `build`: this start builds its image first. */
type Work = { phase: ProvisionPhase; since: number; build: boolean }
type Failure = { message: string; phase: ProvisionPhase; build: boolean }
type Watched = { job: Job; lines: JobLine[] }

const pause = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms))

/** The Databases page opens the saved connection as soon as it answers. */
export function StartNew({
  engine,
  onChoose,
  onStep,
}: {
  engine: string
  onChoose: (engine: string | null) => void
  onStep: (step: number) => void
}) {
  const router = useRouter()
  const { drivers, driversSettled, refresh } = useDatabases()
  return (
    <DatabaseCreation
      engine={engine}
      onChoose={onChoose}
      onStep={onStep}
      drivers={drivers}
      driversSettled={driversSettled}
      onReady={async (connection) => {
        notify.success(`${connection.name} is ready`)
        refresh()
        router.push(sectionHref(connection.id))
      }}
    />
  )
}

/**
 * The same engine catalogue, settings surface and startup sequence on both
 * creation pages. The caller decides where a verified connection goes next.
 */
export function DatabaseCreation({
  engine: chosen,
  onChoose,
  onStep,
  drivers,
  driversSettled = true,
  resume,
  onStarted,
  onReady,
  showAddress = false,
}: {
  engine: string
  onChoose: (engine: string | null) => void
  onStep?: (step: number) => void
  drivers?: DbDriverInfo[]
  driversSettled?: boolean
  resume?: { container: string; engine: string }
  onStarted?: (started: { container: string; engine: string }) => void
  onReady: (connection: DbConnection) => Promise<void>
  showAddress?: boolean
}) {
  const id = useId()
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
  const [container, setContainer] = useState(resume?.container)
  // The image build this start is watching, and the images built since the
  // page read the catalogue — which still says they are not.
  const [building, setBuilding] = useState<Watched>()
  const [built, setBuilt] = useState<ReadonlySet<string>>(() => new Set())
  const provisioning = useRef(false)
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
    if (work || container) return
    if (engine !== chosen) {
      setDraft(EMPTY_PROVISION)
      setFailure(undefined)
    }
    onChoose(engine)
    onStep?.(1)
    reveal(`${id}-settings`)
  }

  /** Adoption proves credentials; a fresh ping rules out temporary first-boot servers. */
  const settle = async (startedContainer: string, started: number, build: boolean) => {
    const deadline = Date.now() + READY_WITHIN_MS
    let connection: DbConnection
    let phase: ProvisionPhase = "wait"
    for (;;) {
      if (!alive.current) return
      try {
        phase = "wait"
        setWork({ phase: "wait", since: started, build })
        connection = await post<DbConnection>("/databases/adopt", { container: startedContainer })
        if (!alive.current) return
        phase = "connect"
        setWork({ phase: "connect", since: started, build })
        const answer = await get<{ ok: boolean; error?: string }>(
          `/databases/${connection.id}/ping`,
        )
        if (!answer.ok) throw new Error(answer.error || "It is not accepting connections yet.")
        break
      } catch (err) {
        if (!alive.current) return
        if (Date.now() > deadline) {
          setFailure({ message: errorMessage(err), phase, build })
          setWork(null)
          onStep?.(1)
          return
        }
        await pause(2000)
      }
    }
    if (!alive.current) return
    await onReady(connection)
  }

  /**
   * Builds a release's image and watches the build to its end. It is a job
   * on the server, so leaving the page does not stop it, and asking again
   * while it runs answers the same job — which is how a reader who came back
   * picks it up. False when the page was left first.
   */
  const buildImage = async (engine: string, release: DbProvisionRelease) => {
    const job = await post<Job>("/databases/provision/build", {
      engine,
      version: release.version,
    })
    for (;;) {
      if (!alive.current) return false
      let reading: Watched
      try {
        reading = await get<Watched>(`/jobs/${encodeURIComponent(job.id)}`)
      } catch (err) {
        // The server keeps its last fifty jobs: one it no longer has is gone,
        // not failing. Anything else is a read that may recover.
        if (err instanceof ApiError && err.status === 404) throw err
        await pause(BUILD_EVERY_MS * 3)
        continue
      }
      if (!alive.current) return false
      setBuilding(reading)
      if (reading.job.status === "succeeded") {
        setBuilt((held) => new Set(held).add(release.image))
        return true
      }
      if (reading.job.status !== "running") {
        throw new Error(reading.job.error || `The build was ${reading.job.status}.`)
      }
      await pause(BUILD_EVERY_MS)
    }
  }

  const stopBuild = async () => {
    if (!building) return
    try {
      await post(`/jobs/${encodeURIComponent(building.job.id)}/cancel`, {})
    } catch (err) {
      notify.error("Could not stop the build", err)
    }
  }

  const create = async () => {
    if (!template || provisioning.current || Object.keys(provisionProblems(template, draft)).length)
      return
    provisioning.current = true
    const started = Date.now()
    let startedContainer = container
    const release = chosenRelease(template, draft)
    const build = !startedContainer && needsBuild(release, built)
    let phase: ProvisionPhase = startedContainer ? "wait" : build ? "build" : "start"
    setElapsed(0)
    setFailure(undefined)
    setBuilding(undefined)
    setWork({ phase, since: started, build })
    onStep?.(2)
    try {
      if (build && release) {
        if (!(await buildImage(template.engine, release))) return
        phase = "start"
        setWork({ phase, since: started, build })
      }
      if (!startedContainer) {
        const answer = await post<DbProvisionResponse>(
          "/databases/provision",
          provisionRequest(template, draft),
        )
        startedContainer = answer.container
        // Persist ownership even if the reader changed source during the pull.
        onStarted?.({ container: startedContainer, engine: template.engine })
        if (!alive.current) return
        setContainer(startedContainer)
        if (answer.firewallError) {
          notify.warning("The firewall was not opened", { description: answer.firewallError })
        }
      }
      await settle(startedContainer, started, build)
    } catch (err) {
      if (!alive.current) return
      setFailure({ message: errorMessage(err), phase: startedContainer ? "connect" : phase, build })
      setWork(null)
      onStep?.(1)
    } finally {
      provisioning.current = false
    }
  }

  const problems = template ? provisionProblems(template, draft) : {}
  const image = template ? provisionImage(template, draft) : ""
  const release = template ? chosenRelease(template, draft) : undefined
  const version = release?.version ?? ""
  const releases = template ? releaseShelves(template) : { current: [], older: [] }
  const olderChosen = releases.older.some((one) => one.version === version)
  // Made on this server from the dashboard's recipe, never pulled.
  const recipe = Boolean(release?.build)
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
          {templates.error && !templates.data && <ErrorState error={templates.error} />}
          {templates.error && !templates.data && (
            <Button variant="outline" onClick={templates.refresh}>
              Retry loading database engines
            </Button>
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
                        // Drawn as the product that answers, through the
                        // registry: a flavour with artwork of its own gets
                        // it, and one with none keeps the database glyph
                        // rather than its driver's logo.
                        engine={engineOf(one.flavor, drivers).logo ?? ""}
                        label={one.label}
                        kind={engineOf(one.flavor, drivers).kind}
                        detail={one.image}
                        selected={chosen === one.engine}
                        working={Boolean(work) && chosen === one.engine}
                        disabled={(Boolean(work) || Boolean(container)) && chosen !== one.engine}
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
          className="relative order-first min-w-0 scroll-mt-4 xl:order-last xl:max-h-full xl:min-h-0 xl:self-start"
        >
          {work && <BorderBeam size={80} duration={6} />}
          <FlowPanelHeader
            title={
              <span className="flex min-w-0 items-center gap-2.5">
                <EngineMark engine={engineOf(template.flavor, drivers)} size="sm" />
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
              <DatabaseProgress
                phase={work.phase}
                elapsed={elapsed}
                build={work.build}
                built={recipe}
              />
              {work.phase === "build" && building && (
                <JobConsole
                  job={building.job}
                  lines={building.lines}
                  onCancel={() => void stopBuild()}
                />
              )}
              <p className="text-hint leading-relaxed text-muted-foreground">
                {work.phase === "build" ? (
                  <>
                    The image is built once, on this server, and that takes a few minutes. Leave and
                    the build goes on; Create here picks it up again.
                  </>
                ) : (
                  <>
                    {recipe
                      ? "It opens "
                      : "The first start of an engine pulls its image, which can take a few minutes. It opens "}
                    {showAddress
                      ? "with a verified connection string when it answers"
                      : "on the database's own page when it answers"}
                    ; leave and it is listed under Found on this server.
                  </>
                )}
              </p>
            </FlowPanelBody>
          ) : (
            <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
              {noDocker && (
                <Notice tone="warning" title="There is no Docker to start it in">
                  <span className="break-words">{noDocker}</span>
                </Notice>
              )}
              {container && !failure && (
                <Notice title="Database container already created">
                  Continue connection setup for {container}; it will use the same container.
                </Notice>
              )}
              {failure && (
                <>
                  <DatabaseProgress
                    phase={failure.phase}
                    elapsed={elapsed}
                    build={failure.build}
                    built={recipe}
                    failed
                  />
                  <Notice
                    tone="danger"
                    title={container ? "Database container already created" : "It was not started"}
                  >
                    <span className="break-words whitespace-pre-wrap">{failure.message}</span>
                    {container && (
                      <span className="mt-2 block">Retry continues setup for {container}.</span>
                    )}
                  </Notice>
                  {/* What the build printed is the answer to why it stopped. */}
                  {failure.phase === "build" && building && (
                    <JobConsole job={building.job} lines={building.lines} />
                  )}
                </>
              )}
              {recipe && release && !failure && (
                <BuiltHere
                  template={template}
                  release={release}
                  product={engineOf(template.flavor, drivers).label}
                  ready={!needsBuild(release, built)}
                />
              )}
              <fieldset disabled={Boolean(container)} className="min-w-0 space-y-4">
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
                {(releases.current.length > 1 || releases.older.length > 0) && (
                  <Field
                    label="Version"
                    // The chosen release's support status, as a plain fact.
                    hint={release?.endOfLife && supportWords(release.endOfLife)}
                  >
                    {releases.current.length > 0 && (
                      <Segments
                        label={`${template.label} version`}
                        value={olderChosen ? "" : version}
                        onChange={(next) => set({ version: next })}
                        options={releases.current.map((one) => ({
                          value: one.version,
                          label: one.version,
                          mono: true,
                        }))}
                      />
                    )}
                  </Field>
                )}
                {releases.older.length > 0 && (
                  <Disclosure
                    quiet
                    summary="Older versions"
                    facts={releases.older.map((one) => one.version).join(", ")}
                    open={olderChosen}
                  >
                    <div className="space-y-2">
                      <Segments
                        label={`Older ${template.label} versions`}
                        value={olderChosen ? version : ""}
                        onChange={(next) => set({ version: next })}
                        options={releases.older.map((one) => ({
                          value: one.version,
                          label: one.version,
                          mono: true,
                        }))}
                      />
                      <ul className="space-y-0.5 text-hint leading-relaxed text-muted-foreground">
                        {releases.older.map((one) => (
                          <li key={one.version}>
                            <span className="font-mono text-foreground">{one.version}</span>
                            {one.endOfLife && ` — ${supportWords(one.endOfLife)}`}
                          </li>
                        ))}
                      </ul>
                      <p className="text-hint leading-relaxed text-muted-foreground">
                        A release past its end of life gets no more fixes, security fixes included.
                      </p>
                    </div>
                  </Disclosure>
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
              </fieldset>
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
                {container ? "Retry connection setup" : "Create"}
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

/**
 * What a template whose image is made on this server says before it is
 * created: what the image is, whether it is built yet, and where its
 * extensions are turned on. The build is the step a reader would otherwise
 * meet unannounced, as minutes of output after pressing Create.
 */
function BuiltHere({
  template,
  release,
  product,
  ready,
}: {
  template: DbProvisionTemplate
  release: DbProvisionRelease
  product: string
  ready: boolean
}) {
  const extensions = template.extensions ?? []
  return (
    <Notice
      title={
        ready
          ? "Its image is built"
          : release.build?.job
            ? "Its image is being built now"
            : "Its image is built here first"
      }
    >
      The official {product} image with{" "}
      {extensions.map((name, index) => (
        <Fragment key={name}>
          {index > 0 && (index === extensions.length - 1 ? " and " : ", ")}
          <span className="font-mono text-foreground">{name}</span>
        </Fragment>
      ))}
      , from the {product} project&apos;s own packages.{" "}
      {ready
        ? "It starts like any other."
        : release.build?.job
          ? "Create waits for that build, then starts it."
          : "The first build takes a few minutes and is kept for the next server."}{" "}
      Each extension is enabled under Settings → Extensions once it runs.
    </Notice>
  )
}
