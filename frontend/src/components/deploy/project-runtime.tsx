"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Archive,
  ArrowRight,
  Copy,
  Database,
  External,
  Layers,
  LockClosed,
  LockOpen,
  Logs,
  Puzzle,
  SettingsSliders,
  Terminal,
  Warning,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type {
  BackupJob,
  Container,
  ContainerSparkline,
  ContainerStats,
  DbConnection,
  DeploymentBackupJob,
  DeploymentDatabaseLink,
  DeploymentDependencyItem,
  DeploymentDomainRoute,
  DeploymentEngineRun,
  DeploymentOwnership,
  DeploymentRelease,
  DeploymentRuntimeService,
  DeploymentStorageMount,
  VolumeDetail,
} from "@/lib/types"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FormNote } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo, issuerProduct } from "@/components/product-logo"
import { EmptyNote, EmptyState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ContainerUsage } from "@/components/docker/container-usage"
import {
  ContainerId,
  ContainerIdentity,
  CpuReading,
  MemoryReading,
  stateWord,
  statusDetail,
} from "@/components/docker/container-cells"
import { PortList } from "@/components/docker/exposure"
import { JobCard } from "@/components/backups/job-card"
import { useProject } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"
import { serviceProduct } from "@/components/deploy/service-product"
import { UsageTiles } from "@/components/deploy/usage-tiles"
import {
  CertificateReading,
  DATABASE_ENGINE_LABELS,
  LINK_STATUS,
  MOUNT_STATUS,
  MountMark,
  ROUTE_LABEL,
  ROUTE_TONE,
  humanize,
} from "@/components/deploy/vocabulary"

/** Who owns a route or a mount, as the word a row's edge carries. */
const OWNERSHIP_WORD: Record<DeploymentOwnership, string> = {
  managed: "Managed",
  linked: "Linked",
  observed: "Observed",
}

/**
 * A dependency's resource kind, as a reader names it, and the glyph it keeps
 * on its tile when no product names it. Backup jobs, volumes and bind paths
 * have blocks of their own above, and the operations summary leaves them out
 * of this one. A table to index, because a glyph returned from a function is
 * a component made during render to the compiler's lint rule.
 */
const KIND_LABEL: Record<string, string> = { database_connection: "Database" }
const KIND_GLYPH: Record<string, Icon> = { database_connection: Database }

/**
 * Everything Docker and the operational owners say about the live release, in
 * the order a reader checks it: the services and what they are using now, the
 * names they answer on, where their data lives and how it is backed up, what
 * else they reach, and — last, because it is the longest and the least often
 * read — the charts of what they used before you looked.
 *
 * There is no opening row of figures (§15 pass 2's exit): each count sits in
 * the header of the block it counts, and the four live readings that move are
 * the Resource usage tiles, each carrying its last hour. Every row here opens
 * something — a container, a proxy site, a volume, a backup job, a database —
 * so every list is a `ChoiceList` with the lit edge (§16), and every thing in
 * it is drawn as the product it is.
 *
 * Nothing on the page is inset from its title: a block that could not be read
 * says so in its header and says why on the page's own edge, rather than in a
 * banner sixteen pixels in. The old Diagnostics tab's findings moved to the
 * overview — this page is evidence, not verdicts.
 */
export function ProjectRuntime() {
  const project = useProject()
  const { runtime, deployment } = project.detail
  const available = runtime?.status === "available"
  const releases = project.releases
  const releaseById = useMemo(
    () => new Map(releases.map((release) => [release.id, release])),
    [releases],
  )
  // Live first, then newest release first: the one serving traffic is the
  // one a reader came for, and a candidate or a rollback copy is read after.
  // A release the list has not brought yet sorts last rather than by its id,
  // which is not its number.
  const services = useMemo(
    () =>
      [...(available ? runtime.services : [])].sort(
        (a, b) =>
          Number(b.liveRelease) - Number(a.liveRelease) ||
          (releaseById.get(b.releaseId)?.number ?? -1) -
            (releaseById.get(a.releaseId)?.number ?? -1),
      ),
    [available, runtime, releaseById],
  )
  const running = services.filter((service) => service.state === "running")
  const [picked, setPicked] = useSessionState<string | undefined>(
    `deploy.${project.projectId}.runtime.service`,
    undefined,
  )
  // Only a running container has a stats socket with anything to say.
  const selected =
    running.find((service) => service.containerId === picked) ??
    running.find((service) => service.liveRelease) ??
    running[0]
  // The usage tiles' socket frames, so the selected service's card shows the
  // figure the tiles show rather than a poll from a few seconds before it.
  const [liveStat, setLiveStat] = useState<{ containerId: string; stats: ContainerStats }>()

  // What Docker's own pages know about these containers — the image each one
  // runs, its limits, its ports, and its readings — joined by id. Each is
  // optional: without it a card still says what the release engine knows.
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    60_000,
    [],
    { enabled: services.length > 0 },
  )
  // A container a deployment has just created is not in a listing read up to
  // a minute ago, so a change in which containers the release has asks again
  // rather than leaving the newest card without its readings until then.
  const ids = services.map((service) => service.containerId).join()
  const refreshContainers = containers.refresh
  const seenIds = useRef(ids)
  useEffect(() => {
    if (seenIds.current === ids) return
    seenIds.current = ids
    refreshContainers()
  }, [ids, refreshContainers])
  const stats = usePoll(
    (signal) => get<ContainerStats[]>("/docker/containers/stats", undefined, signal),
    10_000,
    [],
    { enabled: running.length > 0 },
  )
  const trends = usePoll(
    (signal) =>
      get<ContainerSparkline[]>(
        "/docker/containers/stats/history",
        { range: "1h", points: 40 },
        signal,
      ),
    120_000,
    [],
    { enabled: running.length > 0 },
  )
  // Where a card holds five readings beside its name and still has room for
  // the name: inside the project's own navigation that is the 2xl width.
  const wide = useMediaQuery("(min-width: 1536px)")
  // A domain's tags, route and certificate beside its hostname: below this,
  // with the sidebar open, they took the whole card and the hostname with it.
  const domainsWide = useMediaQuery("(min-width: 1280px)")
  // Where a card's second line is the name's alone: a phone.
  const roomy = useMediaQuery("(min-width: 640px)")

  const operations = project.operations
  const loading = project.operationsLoading
  const silences = operations?.diagnosis?.silences ?? []
  const domains = operations?.domains
  const storage = operations?.storage
  const backups = operations?.backups
  const dependencies = operations?.dependencies
  const volumeMounts = (storage?.mounts ?? []).filter((mount) => mount.kind === "volume")
  const databaseItems = (dependencies?.items ?? []).filter(
    (item) => item.resourceKind === "database_connection",
  )
  // Sizes come from `docker system df`, which is slow, so they are read once a
  // few minutes and only when a volume is on the page.
  const volumes = usePoll(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    300_000,
    [],
    { enabled: volumeMounts.length > 0 },
  )
  const connections = usePoll(
    (signal) => get<DbConnection[]>("/databases/", undefined, signal),
    60_000,
    [],
    { enabled: databaseItems.length > 0 || (backups?.jobs.length ?? 0) > 0 },
  )
  // What the release itself dials for each database and whether it answered
  // lately — the same reading Settings › Databases & backups gives it, so the
  // two pages never disagree about one connection.
  const links = usePoll(
    (signal) =>
      get<DeploymentDatabaseLink[]>(
        `/deploy/${project.projectId}/environments/${project.environmentId}/database-links`,
        undefined,
        signal,
      ),
    60_000,
    [project.projectId, project.environmentId],
    { enabled: databaseItems.length > 0 },
  )

  const product = (image: string | undefined) =>
    serviceProduct(image, deployment.sourceKind, project.product)
  const containerFor = (id: string) => containers.data?.find((one) => one.id === id)
  const live = running.find((service) => service.liveRelease) ?? running[0]
  const liveProduct = live ? product(live.image ?? containerFor(live.containerId)?.image) : "docker"
  const activeRun = deployment.activeRun
  const polledStat = (id: string) => stats.data?.find((one) => one.id === id)

  return (
    <div className="space-y-6">
      <Panel plain>
        <PanelHeader
          title="Services"
          actions={
            available &&
            services.length > 0 && (
              <span className="numeric text-hint text-muted-foreground">
                <span className="font-medium text-foreground">{running.length}</span> of{" "}
                {services.length} running · observed {relativeTime(runtime.observedAt)}
              </span>
            )
          }
        />
        <PanelBody>
          {!available ? (
            <Notice title="Runtime unavailable" tone="warning" icon={Warning}>
              <p>{runtime?.reason ?? "Docker runtime evidence could not be loaded."}</p>
              <Button size="xs" variant="outline" asChild className="mt-2">
                <Link href="/docker">
                  Open Docker <ArrowRight />
                </Link>
              </Button>
            </Notice>
          ) : services.length === 0 ? (
            <EmptyState
              mark={<ProjectMark deployment={deployment} product={project.product} />}
              title="No managed runtime services"
              description="Docker returned no managed containers for this environment. Observed imports remain under Docker until managed deployment creates a runtime."
              className="border-0 py-6"
            />
          ) : (
            <ChoiceList aria-label="Runtime services">
              {services.map((service, index) => {
                const container = containerFor(service.containerId)
                const streamed =
                  service.containerId === selected?.containerId &&
                  liveStat?.containerId === service.containerId
                    ? liveStat.stats
                    : undefined
                return (
                  <ServiceCard
                    key={service.containerId}
                    index={index}
                    service={service}
                    container={container}
                    stat={streamed ?? polledStat(service.containerId)}
                    trend={
                      container && trends.data?.find((one) => one.name === container.name)?.cpu
                    }
                    product={product(service.image ?? container?.image)}
                    release={releaseById.get(service.releaseId)}
                    run={
                      activeRun?.candidateReleaseId !== undefined &&
                      activeRun.candidateReleaseId === service.releaseId
                        ? activeRun
                        : undefined
                    }
                    projectId={project.projectId}
                    wide={wide}
                    phone={!roomy}
                  />
                )
              })}
            </ChoiceList>
          )}
          <Silence subjects={["runtime"]} silences={silences} />
        </PanelBody>
      </Panel>

      {selected && (
        <Panel plain>
          <PanelHeader title="Resource usage">
            {running.length > 1 && (
              // Its own line on a phone, where a fixed width beside the title
              // would push the title off the start of the header.
              <div className="w-full sm:w-56">
                <Select value={selected.containerId} onValueChange={setPicked}>
                  <SelectTrigger size="sm" className="w-full" aria-label="Runtime usage service">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {services.map((service) => {
                      const number = releaseById.get(service.releaseId)?.number
                      return (
                        <SelectItem
                          key={service.containerId}
                          value={service.containerId}
                          disabled={service.state !== "running"}
                          hint={number !== undefined ? `#${number}` : undefined}
                        >
                          <ProductGlyph
                            id={product(service.image ?? containerFor(service.containerId)?.image)}
                          />
                          <span className="truncate">{service.service || service.name}</span>
                        </SelectItem>
                      )
                    })}
                  </SelectContent>
                </Select>
              </div>
            )}
          </PanelHeader>
          <PanelBody>
            <UsageTiles
              key={selected.containerId}
              containerId={selected.containerId}
              columns={4}
              initial={polledStat(selected.containerId)}
              onStats={(frame) => setLiveStat({ containerId: selected.containerId, stats: frame })}
            />
          </PanelBody>
        </Panel>
      )}

      <Panel plain>
        <PanelHeader
          title="Domains"
          actions={
            <EvidenceHeader loading={loading} block={domains}>
              {domains?.siteName && <Tag mono>{domains.siteName}</Tag>}
              {domains && domains.domains.length > 0 && (
                <Count n={domains.domains.length} noun="domain" />
              )}
            </EvidenceHeader>
          }
        />
        <PanelBody>
          <EvidenceBody
            loading={loading}
            block={domains}
            fallback="Proxy evidence for this deployment could not be read."
            empty={domains?.domains.length === 0 && "This release serves no public domain."}
          >
            <ChoiceList aria-label="Deployment domains">
              {domains?.domains.map((domain) => (
                <DomainCard
                  key={domain.hostname}
                  domain={domain}
                  siteName={domains.siteName}
                  projectId={project.projectId}
                  wide={domainsWide}
                />
              ))}
            </ChoiceList>
          </EvidenceBody>
          <Silence subjects={["domains", "certificates"]} silences={silences} />
        </PanelBody>
      </Panel>

      {/* Side by side only once each half is wide enough to hold a card's
          readings beside its name: inside the project's own navigation that
          is the extra-large width, not the large one. */}
      <div className="grid gap-6 xl:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="Storage"
            actions={
              <EvidenceHeader loading={loading} block={storage}>
                {storage && storage.mounts.length > 0 && (
                  <Count n={storage.mounts.length} noun="mount" />
                )}
              </EvidenceHeader>
            }
          />
          <PanelBody>
            <EvidenceBody
              loading={loading}
              block={storage}
              fallback="The storage owner could not be read."
              empty={storage?.mounts.length === 0 && "This release declares no persistent storage."}
            >
              <ChoiceList aria-label="Persistent storage">
                {storage?.mounts.map((mount) => {
                  const volume =
                    mount.kind === "volume"
                      ? volumes.data?.find((one) => one.name === mount.source)
                      : undefined
                  // The container that keeps its data in the volume, when
                  // Docker says which; the live service otherwise.
                  const keeper = services.find((service) =>
                    volume?.usedBy.some((user) => user.id === service.containerId),
                  )
                  const keeperProduct = keeper
                    ? product(keeper.image ?? containerFor(keeper.containerId)?.image)
                    : liveProduct
                  return (
                    <MountCard
                      key={`${mount.source}:${mount.target}`}
                      mount={mount}
                      product={keeperProduct === "docker" ? undefined : keeperProduct}
                      size={volume?.size}
                      wide={roomy}
                    />
                  )
                })}
              </ChoiceList>
            </EvidenceBody>
            <Silence subjects={["storage"]} silences={silences} />
          </PanelBody>
        </Panel>

        <Panel plain>
          <PanelHeader
            title="Backups"
            actions={
              <EvidenceHeader loading={loading} block={backups}>
                {backups && backups.jobs.length > 0 && <Count n={backups.jobs.length} noun="job" />}
              </EvidenceHeader>
            }
          />
          <PanelBody>
            <EvidenceBody
              loading={loading}
              block={backups}
              fallback="The Backups module could not be read."
              empty={backups?.jobs.length === 0 && "This release declares no backup policy."}
            >
              <ChoiceList aria-label="Backups">
                {backups?.jobs.map((gate) => (
                  <BackupCard
                    key={gate.resourceId}
                    gate={gate}
                    connections={connections.data}
                    wide={roomy}
                  />
                ))}
              </ChoiceList>
            </EvidenceBody>
            <Silence subjects={["backups"]} silences={silences} />
          </PanelBody>
        </Panel>
      </div>

      {(loading ||
        !dependencies ||
        dependencies.status !== "available" ||
        dependencies.items.length > 0) && (
        <Panel plain>
          <PanelHeader
            title="Dependencies"
            actions={
              <EvidenceHeader loading={loading} block={dependencies}>
                {dependencies && dependencies.items.length > 0 && (
                  <Count n={dependencies.items.length} noun="dependency" many="dependencies" />
                )}
              </EvidenceHeader>
            }
          />
          <PanelBody>
            <EvidenceBody
              loading={loading}
              block={dependencies}
              fallback="The owning modules could not be read."
            >
              <ChoiceList aria-label="Dependencies">
                {dependencies?.items.map((item) => {
                  const database = item.resourceKind === "database_connection"
                  return (
                    <DependencyCard
                      key={`${item.resourceKind}:${item.resourceId}`}
                      item={item}
                      wide={roomy}
                      connection={
                        database
                          ? connections.data?.find((one) => String(one.id) === item.resourceId)
                          : undefined
                      }
                      link={
                        database
                          ? links.data?.find((one) => String(one.connectionId) === item.resourceId)
                          : undefined
                      }
                    />
                  )
                })}
              </ChoiceList>
            </EvidenceBody>
            <Silence subjects={["dependencies"]} silences={silences} />
          </PanelBody>
        </Panel>
      )}

      {selected && (
        <ContainerUsage
          key={selected.containerId}
          plain
          containerId={selected.containerId}
          name={selected.name || selected.containerId}
        />
      )}
    </div>
  )
}

function Count({ n, noun, many }: { n: number; noun: string; many?: string }) {
  return <span className="numeric text-hint text-muted-foreground">{plural(n, noun, many)}</span>
}

type Evidence = { status: "available" | "unavailable"; reason?: string }

/**
 * A block's header readings: its count once the evidence is in, and a status
 * word in place of the count when it could not be read — the banner that
 * used to fill the body asked nothing of the reader.
 */
function EvidenceHeader({
  loading,
  block,
  children,
}: {
  loading: boolean
  block: Evidence | undefined
  children: React.ReactNode
}) {
  if (loading) return null
  if (!block || block.status !== "available")
    return <Status tone="unknown" label="Evidence unavailable" />
  return <>{children}</>
}

/**
 * A block's body: skeleton rows while the operations poll is in flight, the
 * reason on the page's edge when the owner could not be read, the empty
 * sentence, or the list — which rises once when it lands (§11 *arrived*).
 */
function EvidenceBody({
  loading,
  block,
  fallback,
  empty,
  children,
}: {
  loading: boolean
  block: Evidence | undefined
  fallback: string
  empty?: string | false
  children: React.ReactNode
}) {
  if (loading) return <LoadingRows rows={2} />
  if (!block || block.status !== "available")
    return <EmptyNote className="px-0 py-2 text-left">{block?.reason ?? fallback}</EmptyNote>
  if (empty) return <EmptyNote className="px-0 py-2 text-left">{empty}</EmptyNote>
  return <div className="animate-rise">{children}</div>
}

/** What the diagnosis chose not to judge here, and why, under the block it concerns. */
function Silence({
  subjects,
  silences,
}: {
  subjects: string[]
  silences: { subject: string; reason: string }[]
}) {
  const matches = silences.filter((silence) => subjects.includes(silence.subject))
  if (matches.length === 0) return null
  return (
    <FormNote className="pt-3">
      <span className="font-medium text-foreground">Not assessed.</span>{" "}
      {matches.map((silence) => silence.reason).join(" ")}
    </FormNote>
  )
}

/**
 * One container of the release, as a card that opens it in Docker — drawn as
 * the product its image is, with the same live readings the Containers page
 * gives it (`container-card.tsx`), and the release it belongs to at its edge.
 *
 * From `2xl` the readings sit beside the name in fixed measures so a column of
 * cards reads down like a table — the measures stay while a new container's
 * readings are still on their way, so its release and state do not stand a
 * column to the right of everyone else's. Below it they go beneath the name
 * and none is dropped; on a phone the image and stack take a line of their
 * own under the name rather than sharing it with the state and the menu.
 *
 * The state word is the release engine's, read every five seconds, and so is
 * the dot beside it: Docker's listing is read once a minute, and its sentence
 * about a container is only used while it still agrees on the state. Its
 * state is polled, not streamed, so its dot does not breathe (§11): only the
 * usage tiles below hold a socket.
 *
 * A candidate for a release that is being deployed right now carries a light
 * around its edge and the words of what it is waiting for, until the run ends.
 */
function ServiceCard({
  service,
  container,
  stat,
  trend,
  product,
  release,
  run,
  projectId,
  wide,
  phone,
  index,
}: {
  service: DeploymentRuntimeService
  container?: Container
  stat?: ContainerStats
  trend?: number[]
  product: string
  release?: DeploymentRelease
  /** The deployment whose candidate this container is, while it runs. */
  run?: DeploymentEngineRun
  projectId: number
  wide: boolean
  phone: boolean
  index: number
}) {
  const router = useRouter()
  const name = service.name || service.containerId
  const verbs: Verb[] = [
    {
      key: "logs",
      label: "Logs",
      detail: "This service's runtime logs.",
      icon: Logs,
      run: () => router.push(`/deploy/${projectId}/logs?service=${service.containerId}`),
    },
    {
      key: "console",
      label: "Console",
      detail: "Open a shell inside this container.",
      icon: Terminal,
      disabled: service.state !== "running",
      run: () => router.push(`/deploy/${projectId}/console?service=${service.containerId}`),
    },
  ]
  if (service.stack) {
    verbs.push({
      key: "stack",
      label: "Open stack",
      detail: "The Compose stack this service belongs to.",
      icon: Layers,
      run: () => router.push(`/docker/stacks/${service.stack}`),
    })
  }
  verbs.push({
    key: "docker",
    label: "Open in Docker",
    detail: "This container, as Docker sees it.",
    icon: External,
    run: () => router.push(`/docker/containers/${service.containerId}`),
  })

  const number = release?.number
  const current = container?.state === service.state ? container : undefined
  const word = stateWord(service.state)
  const detail = current ? statusDetail(current) : serviceHealth(service)
  const state = (
    <Status
      state={service.state}
      label={
        run ? (
          <TextShimmer>{`Candidate for release${number !== undefined ? ` #${number}` : ""}`}</TextShimmer>
        ) : (
          word
        )
      }
    />
  )
  const runLink = run && (
    <Link
      href={`/deploy/${projectId}/runs/${run.id}`}
      className="shrink-0 rounded-sm text-foreground underline-offset-4 focus-ring hover:underline"
    >
      deployment #{run.runNumber} →
    </Link>
  )
  const identity = container ? (
    <ContainerIdentity container={container} id={wide} />
  ) : (
    <span className="flex min-w-0 items-center gap-1.5">
      {service.stack && (
        <>
          <Layers className="size-3 shrink-0" />
          <span className="truncate">
            {service.stack}/{service.service}
          </span>
          <span aria-hidden>·</span>
        </>
      )}
      <span className="shrink-0 font-mono">{service.containerId.slice(0, 12)}</span>
    </span>
  )

  return (
    <ChoiceRow
      verb={name}
      href={`/docker/containers/${service.containerId}`}
      busy={Boolean(run)}
      index={index}
      leading={<ProductLogo id={product} size="sm" />}
      title={name}
      description={phone ? undefined : identity}
      trailing={
        wide ? (
          <>
            <span className="w-32 min-w-0">
              <ReleaseCell service={service} release={release} />
            </span>
            <span className="w-40 min-w-0">
              {state}
              <span className="mt-0.5 flex min-w-0 gap-1.5 text-hint text-muted-foreground">
                <span className="truncate">{detail || " "}</span>
                {runLink}
              </span>
            </span>
            <span className="w-24">
              {container && <CpuReading stat={stat} container={container} trend={trend} />}
            </span>
            <span className="w-36">
              {container && <MemoryReading stat={stat} container={container} />}
            </span>
            <span className="w-32 min-w-0">
              {container && <PortList ports={container.exposure ?? []} max={1} />}
            </span>
          </>
        ) : (
          state
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${name}`} />}
    >
      {!wide && (
        <div className="min-w-0 space-y-2.5 sm:pl-11">
          {phone && (
            <div className="min-w-0 text-hint text-muted-foreground">
              {container ? <ContainerIdentity container={container} id={false} /> : identity}
            </div>
          )}
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
            <ReleaseCell service={service} release={release} inline />
            {detail && <span className="min-w-0 truncate">{detail}</span>}
            {container && <ContainerId container={container} />}
            {runLink}
          </div>
          {container && (
            // The ports take the third column once the card is wide enough,
            // rather than a line of their own under two half-width meters.
            <div className="grid gap-x-8 gap-y-2.5 sm:grid-cols-2 lg:grid-cols-3 lg:items-center">
              <CpuReading stat={stat} container={container} trend={trend} />
              <MemoryReading stat={stat} container={container} />
              <PortList ports={container.exposure ?? []} max={2} />
            </div>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * The release a container belongs to: the word for whether it serves
 * traffic, and under it the number the shell's facts row uses — with, for a
 * container that does not serve, why it is still here. A release the list has
 * not brought yet has no number to say, rather than its id in place of one.
 */
function ReleaseCell({
  service,
  release,
  inline,
}: {
  service: DeploymentRuntimeService
  release?: DeploymentRelease
  inline?: boolean
}) {
  const why =
    service.liveRelease || !release
      ? undefined
      : release.state === "candidate"
        ? "candidate"
        : release.state === "retained"
          ? "kept for rollback"
          : undefined
  // Being the live release is a state, drawn as a run row and the rollback
  // dialog draw it, not a tag beside one.
  const tag = (
    <Status
      tone={service.liveRelease ? "running" : "stopped"}
      label={service.liveRelease ? "Live" : "Other release"}
    />
  )
  const facts = [release && `#${release.number}`, why].filter(Boolean).join(" · ")
  const line = facts && (
    <span className="numeric truncate text-hint text-muted-foreground">{facts}</span>
  )
  if (inline)
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5">
        {tag}
        {line}
      </span>
    )
  return (
    <span className="flex min-w-0 flex-col items-start">
      {tag}
      <span className="mt-0.5 block max-w-full truncate">{line || " "}</span>
    </span>
  )
}

/** What the release engine knows of a container's health, when Docker's listing is not at hand. */
function serviceHealth(service: DeploymentRuntimeService) {
  const health = service.health === "unavailable" ? "health not observed" : service.health
  return [health, service.startedAt && `started ${relativeTime(service.startedAt)}`]
    .filter(Boolean)
    .join(" · ")
}

/**
 * A name the release answers on, as a card that opens the proxy site serving
 * it — led by its certificate's issuer, or a lock open or closed where there
 * is none, and set as the literal it is, as Settings → Domains draws the same
 * name. Visiting it is the verb pressed daily, so it is the
 * one inline; the certificate, the address and the settings wait in the menu.
 * Its readings — the route, the certificate — sit in fixed measures at the
 * edge once the content column has room for them beside the hostname, and
 * beneath it until then.
 *
 * Which site config serves it is said once, in the block's header; a card
 * repeats it only when another config answers for the name.
 */
function DomainCard({
  domain,
  siteName,
  projectId,
  wide,
}: {
  domain: DeploymentDomainRoute
  siteName?: string
  projectId: number
  wide: boolean
}) {
  const router = useRouter()
  const url = `${domain.https ? "https" : "http"}://${domain.hostname}/`
  const verbs: Verb[] = [
    {
      key: "visit",
      label: "Visit",
      detail: "Open the site in a new tab.",
      icon: External,
      inline: true,
      run: () => window.open(url, "_blank", "noopener,noreferrer"),
    },
  ]
  if (domain.certificateLink) {
    const certificate = domain.certificateLink
    verbs.push({
      key: "certificate",
      label: "Open the certificate",
      detail: "The certificate this name is served with, and when it renews.",
      icon: LockClosed,
      run: () => router.push(certificate),
    })
  }
  verbs.push(
    {
      key: "copy",
      label: "Copy the address",
      detail: url,
      icon: Copy,
      run: () => void copyText(url, "Address copied"),
    },
    {
      key: "settings",
      label: "Domain settings",
      detail: "The names this deployment answers on, and how each is served.",
      icon: SettingsSliders,
      run: () => router.push(`/deploy/${projectId}/settings/domains`),
    },
  )
  const foreign = domain.route === "foreign" || domain.route === "conflict"
  const elsewhere = domain.servedBy && (foreign || domain.servedBy !== siteName)
  const route = <Status tone={ROUTE_TONE[domain.route]} label={ROUTE_LABEL[domain.route]} />
  const tags = (
    <>
      {domain.protected && <Tag>Password</Tag>}
      <Tag>{OWNERSHIP_WORD[domain.ownership]}</Tag>
    </>
  )

  return (
    <ChoiceRow
      verb={domain.hostname}
      href={domain.deepLink}
      disabled={!domain.deepLink}
      // The certificate's issuer, as Settings → Domains leads the same name:
      // a hostname is one row wherever it is listed.
      leading={
        <ProductLogo
          size="sm"
          id={issuerProduct(domain.certificateIssuer)}
          fallback={domain.https ? LockClosed : LockOpen}
        />
      }
      title={<span className="font-mono">{domain.hostname}</span>}
      description={
        elsewhere && (
          <>
            {foreign ? "answered by " : "served by "}
            <span className="font-mono">{domain.servedBy}</span>
          </>
        )
      }
      trailing={
        wide && (
          <>
            {tags}
            <span className="w-24">{route}</span>
            <span className="w-56 min-w-0">
              <CertificateReading domain={domain} issuer={false} />
            </span>
          </>
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${domain.hostname}`} />}
    >
      {/* Until then the whole line is the name's: a hostname cut to
          "www.example…" is the one thing on the card that must not be. */}
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 sm:pl-11">
          {route}
          <CertificateReading domain={domain} issuer={false} />
          {tags}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * Where the release keeps what must survive it, as a card that opens it: a
 * volume drawn as the product of the container that keeps its data there, a
 * folder on this server as the file manager draws it (§14). A mount that is
 * missing is not a place to go, so it keeps its card and loses its edge.
 */
function MountCard({
  mount,
  product,
  size,
  wide,
}: {
  mount: DeploymentStorageMount
  product?: string
  size?: number
  wide: boolean
}) {
  const status = MOUNT_STATUS[mount.status]
  const readings = (
    <>
      {size !== undefined && size > 0 && (
        <span className="numeric text-hint text-muted-foreground">{bytes(size)}</span>
      )}
      <Tag>{OWNERSHIP_WORD[mount.ownership]}</Tag>
    </>
  )
  return (
    <ChoiceRow
      verb={mount.source}
      href={mount.deepLink}
      disabled={!mount.deepLink || mount.status === "missing"}
      leading={<MountMark source={mount.source} product={product} />}
      title={
        mount.kind === "bind" ? <span className="font-mono">{mount.source}</span> : mount.source
      }
      description={
        <>
          mounted at <span className="font-mono">{mount.target}</span>
          {mount.readOnly && " · read-only"}
        </>
      }
      trailing={
        <>
          {wide && readings}
          <Status tone={status.tone} label={status.label} />
        </>
      }
    >
      {(mount.detail || !wide) && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground sm:pl-11">
          {!wide && readings}
          {mount.detail && (
            <span className={cn(mount.status === "missing" && "text-destructive")}>
              {mount.detail}
            </span>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/** A backup's last outcome as a state word, for when the job's own record is not at hand. */
function lastRunLabel(status: string) {
  if (status === "success") return "Last run succeeded"
  if (status === "failed") return "Last run failed"
  return `Last run ${humanize(status).toLowerCase()}`
}

/**
 * A backup job the release is protected by, as the Backups page's own card
 * (`JobCard`) — the one Settings › Databases & backups draws the same job
 * with: the engines of the databases it dumps, how its last run went, where it
 * writes and its recent runs as a strip — and, when a deployment waits on it,
 * whether the newest archive is young enough to let one through.
 *
 * The job's own record is read for all of that; without it the card says what
 * the release engine observed and nothing it cannot know.
 */
function BackupCard({
  gate,
  connections,
  wide,
}: {
  gate: DeploymentBackupJob
  connections?: DbConnection[]
  wide: boolean
}) {
  const present = gate.status === "present"
  const job = usePoll(
    (signal) => get<BackupJob>(`/backups/${gate.resourceId}`, undefined, signal),
    60_000,
    [gate.resourceId],
    { enabled: present },
  ).data
  const name = job?.name ?? `Backup job ${gate.resourceId}`
  const engines = [
    ...new Set(
      (job?.databaseDumps ?? []).flatMap((id) => {
        const driver = connections?.find((one) => one.id === id)?.driver
        return driver ? [driver] : []
      }),
    ),
  ]
  const required = gate.required && present && (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Tag>Required before deploy</Tag>
      <Status
        tone={gate.fresh ? "running" : "warning"}
        label={gate.fresh ? "Within its maximum age" : "Older than its maximum age"}
      />
    </span>
  )

  // The Backups page's own card once the job is read, as Settings → Databases
  // draws the same job: one job, one shape.
  if (job)
    return (
      <JobCard
        job={job}
        products={engines}
        verbs={[]}
        verb={`Open the backup job ${name}`}
        note={required}
        working={job.lastRun?.status === "running"}
      />
    )

  const outcome = present ? (
    gate.lastStatus && <Status state={gate.lastStatus} label={lastRunLabel(gate.lastStatus)} />
  ) : (
    <Status tone={MOUNT_STATUS[gate.status].tone} label={MOUNT_STATUS[gate.status].label} />
  )
  const below = (!wide && present && outcome) || required

  return (
    <ChoiceRow
      verb={name}
      href={`/backups/${gate.resourceId}`}
      disabled={gate.status === "missing"}
      leading={<ProductLogo size="sm" fallback={Archive} />}
      title={name}
      description={gate.detail}
      trailing={wide || !present ? outcome : undefined}
    >
      {below && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
          {!wide && present && outcome}
          {required}
        </div>
      )}
    </ChoiceRow>
  )
}

/**
 * Something else the release reaches — a database connection, drawn as its
 * engine at the address the release dials it by, or any other owner's
 * resource by its kind — as a card that opens it where that owner keeps it.
 *
 * A database's state is the link Settings › Databases & backups reads —
 * whether the release answered on its alias lately — so the two pages say
 * one thing about it. Without that reading it says only whether the
 * connection exists. On a phone the state goes under the address, which is
 * the line it would otherwise cut.
 */
function DependencyCard({
  item,
  connection,
  link,
  wide,
}: {
  item: DeploymentDependencyItem
  connection?: DbConnection
  link?: DeploymentDatabaseLink
  wide: boolean
}) {
  const database = item.resourceKind === "database_connection"
  const kind = KIND_LABEL[item.resourceKind] ?? humanize(item.resourceKind)
  // The observer reports a connection's name where other kinds report a
  // state, so the name is never read back as the status word.
  const title = database
    ? (link?.name ?? connection?.name ?? item.status ?? `${kind} ${item.resourceId}`)
    : `${kind} ${item.resourceId}`
  const driver = link?.driver ?? connection?.driver
  const engine = driver && (DATABASE_ENGINE_LABELS[driver] ?? driver)
  const reading = link ? LINK_STATUS[link.status] : undefined
  const state = reading ? (
    <Status tone={reading.tone} label={reading.label} />
  ) : (
    <Status
      tone={item.available ? "running" : "warning"}
      label={item.available ? "Available" : "Unavailable"}
    />
  )
  const note = (link || (database && connection)) && (link?.detail ?? item.detail)
  return (
    <ChoiceRow
      verb={title}
      href={item.deepLink}
      disabled={!item.deepLink}
      leading={
        <ProductLogo
          id={database ? driver : undefined}
          size="sm"
          fallback={KIND_GLYPH[item.resourceKind] ?? Puzzle}
        />
      }
      title={title}
      description={
        link ? (
          <>
            {engine} · <span className="font-mono">{link.hostname}</span>
            {link.database && ` · database ${link.database}`}
          </>
        ) : database && connection ? (
          engine
        ) : (
          item.detail
        )
      }
      trailing={wide && state}
    >
      {(!wide || note) && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground sm:pl-11">
          {!wide && state}
          {note && <span>{note}</span>}
        </div>
      )}
    </ChoiceRow>
  )
}
