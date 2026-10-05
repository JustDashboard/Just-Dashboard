"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  Archive,
  ArrowRight,
  Copy,
  Database,
  External,
  Information,
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
import { unusableReason } from "@/lib/db-connections"
import { bytes, duration, plural, relativeTime } from "@/lib/format"
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
import { useConfirm } from "@/components/confirm-dialog"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  ContainerIdentity,
  CpuReading,
  MemoryReading,
  stateWord,
  statusDetail,
} from "@/components/docker/container-cells"
import {
  useContainerControl,
  useContainerVerbs,
  type ContainerVerb,
} from "@/components/docker/container-actions"
import type { ConfirmFn } from "@/components/docker/shared"
import { JobCard } from "@/components/backups/job-card"
import { useProject } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"
import { ServiceDetails } from "@/components/deploy/runtime-details"
import { ServiceFailure } from "@/components/deploy/runtime-failure"
import {
  isImageDigest,
  mountTargetProduct,
  publicServices,
  publishedPorts,
  releaseEvents,
  runtimeContainer,
  wantsFailureReading,
} from "@/components/deploy/runtime-model"
import { RuntimeMap, type MapService, type MapStore } from "@/components/deploy/runtime-map"
import { RuntimePorts } from "@/components/deploy/runtime-ports"
import { serviceProduct, volumeProduct } from "@/components/deploy/service-product"
import { RuntimeUsage } from "@/components/deploy/runtime-usage"
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

/** Who owns a route or a mount, as the tag in the line under its name. */
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
 * the order a reader checks it: the services, what they are using now and how
 * they got there, the names they answer on, where their data lives and how it
 * is backed up, and what else they reach.
 *
 * There is no opening row of figures (§15 pass 2's exit): each count sits in
 * the header of the block it counts, and the five live readings that move are
 * Resource usage's tiles, over the charts they move on — the socket's last
 * five minutes, or the recorded history for a longer range. Every row here opens
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
  // Only this release's running containers: the endpoint inspects and reads
  // every container it is asked about, and the host may run fifty.
  const runningIds = running.map((service) => service.containerId).join(",")
  const stats = usePoll(
    (signal) => get<ContainerStats[]>("/docker/containers/stats", { ids: runningIds }, signal),
    10_000,
    [runningIds],
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
  // A lifecycle verb changes what the release engine reports, what Docker's
  // listing says and what the stats socket carries, and each is on a timer;
  // reading them now is what lets the card say what happened rather than what
  // was true a few seconds ago.
  const { confirm, dialog } = useConfirm()
  const refreshProject = project.refresh
  const refreshStats = stats.refresh
  const changed = useCallback(() => {
    refreshProject()
    refreshContainers()
    refreshStats()
  }, [refreshProject, refreshContainers, refreshStats])
  const { pending, act } = useContainerControl(changed)
  const [detailsId, setDetailsId] = useState<string>()
  // Where a service's release, state, CPU and memory stand beside its name and
  // leave the name some 240px: inside the project's own navigation that is
  // the extra-large width. Its ports and its id join them at the 2xl one.
  const wide = useMediaQuery("(min-width: 1280px)")
  const widest = useMediaQuery("(min-width: 1536px)")
  // A domain's route and certificate beside its hostname, once its tags have
  // moved under it: from the large width the hostname keeps some 190px, and
  // below it the line is the hostname's alone.
  const domainsWide = useMediaQuery("(min-width: 1024px)")
  // Where a card's state fits on the line beside its name: all but a phone.
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
  // The selected service's figure is the socket's, every other one the poll's.
  // A frame kept from a service that has since stopped, or from the one
  // selected before, is not this card's figure.
  const statFor = (id: string) =>
    (id === selected?.containerId && id === liveStat?.containerId ? liveStat.stats : undefined) ??
    polledStat(id)
  const detailed = services.find((service) => service.containerId === detailsId)
  const productOf = (service: DeploymentRuntimeService) =>
    product(service.image ?? containerFor(service.containerId)?.image)

  const runs = project.runs
  const environmentId = project.environmentId
  const events = useMemo(
    () =>
      releaseEvents(
        releases,
        runs.filter((run) => run.environmentId === environmentId),
      ),
    [releases, runs, environmentId],
  )

  // Each mount once, with the container that keeps it and the product it is
  // drawn as — read by the storage list and the map alike.
  const mounts = (storage?.mounts ?? []).map((mount) => {
    const volume =
      mount.kind === "volume" ? volumes.data?.find((one) => one.name === mount.source) : undefined
    // The container that keeps its data in the volume, when Docker says which;
    // the live service otherwise.
    const keeper = services.find((service) =>
      volume?.usedBy.some((user) => user.id === service.containerId),
    )
    // Whoever keeps the data names the volume; failing that, the directory it
    // is mounted at does when only one program keeps its data there, and the
    // live service is the last guess. Docker's whale says nothing the volume's
    // glyph does not, so it never names one.
    const keeperImage =
      keeper?.image ?? containerFor(keeper?.containerId ?? volume?.usedBy[0]?.id ?? "")?.image
    const keeperProduct =
      (keeperImage
        ? volumeProduct(keeperImage, deployment.sourceKind, project.product)
        : undefined) ??
      mountTargetProduct(mount.target) ??
      (liveProduct === "docker" ? undefined : liveProduct)
    return { mount, size: volume?.size, keeper, product: keeperProduct }
  })

  // The map's three lanes. The live release's services, or every service
  // while none of them is live; the data the release declares, once the
  // owners have answered.
  const mapped = services.some((service) => service.liveRelease)
    ? services.filter((service) => service.liveRelease)
    : services
  const reached = new Set(
    publicServices(
      mapped.map((service) => ({ ...service, liveRelease: true })),
      (service) => publishedPorts(containerFor(service.containerId)?.exposure).length > 0,
      (service) => Object.hasOwn(DATABASE_ENGINE_LABELS, productOf(service)),
    ).map((service) => service.containerId),
  )
  const mapServices: MapService[] = mapped.map((service) => {
    return {
      id: service.containerId,
      service,
      product: productOf(service),
      stat: statFor(service.containerId),
      release: releaseById.get(service.releaseId)?.number,
      reached: reached.has(service.containerId),
    }
  })
  const storageRead = storage?.status === "available"
  const dependenciesRead = dependencies?.status === "available"
  const mapStores: MapStore[] | undefined =
    loading || (!storageRead && !dependenciesRead)
      ? undefined
      : [
          ...(storageRead ? mounts : []).map(
            ({ mount, size, keeper, product: keeperProduct }): MapStore => ({
              key: `${mount.source}:${mount.target}`,
              kind: mount.kind === "bind" ? "bind" : "volume",
              eyebrow: mount.kind === "bind" ? "Folder on this server" : "Volume",
              title: mount.source,
              detail: `at ${mount.target}${size ? ` · ${bytes(size)}` : ""}`,
              product: mount.kind === "bind" ? undefined : keeperProduct,
              ownerId: keeper?.containerId,
              status: MOUNT_STATUS[mount.status],
            }),
          ),
          ...(dependenciesRead ? databaseItems : []).map((item): MapStore => {
            const link = links.data?.find((one) => String(one.connectionId) === item.resourceId)
            const connection = connections.data?.find((one) => String(one.id) === item.resourceId)
            const driver = link?.driver ?? connection?.driver
            return {
              key: `db:${item.resourceId}`,
              kind: "database",
              eyebrow: (driver && DATABASE_ENGINE_LABELS[driver]) ?? "Database",
              title: link?.name ?? connection?.name ?? item.status ?? `Database ${item.resourceId}`,
              detail: link?.hostname,
              product: driver,
              status: link
                ? LINK_STATUS[link.status]
                : item.available
                  ? { label: "Available", tone: "running" }
                  : { label: "Unavailable", tone: "warning" },
            }
          }),
        ]

  return (
    <div className="space-y-6">
      {available && services.length > 0 && (
        <RuntimeMap
          services={mapServices}
          domains={loading || domains?.status !== "available" ? undefined : domains.domains}
          domainsReason={
            loading
              ? undefined
              : domains?.status !== "available"
                ? (domains?.reason ?? "Proxy evidence could not be read.")
                : undefined
          }
          stores={mapStores}
          storesReason={
            loading || mapStores ? undefined : "The storage and database owners could not be read."
          }
        />
      )}
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
                return (
                  <ServiceCard
                    key={service.containerId}
                    index={index}
                    service={service}
                    container={container}
                    stat={statFor(service.containerId)}
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
                    widest={widest}
                    confirm={confirm}
                    act={act}
                    pending={pending[service.containerId]}
                    onChanged={changed}
                    onDetails={() => setDetailsId(service.containerId)}
                  />
                )
              })}
            </ChoiceList>
          )}
          <Silence subjects={["runtime"]} silences={silences} />
        </PanelBody>
      </Panel>

      {selected && (
        <RuntimeUsage
          key={selected.containerId}
          containerId={selected.containerId}
          name={selected.name || selected.containerId}
          initial={polledStat(selected.containerId)}
          events={events}
          onStats={(frame) => setLiveStat({ containerId: selected.containerId, stats: frame })}
          picker={
            running.length > 1 && (
              <ServicePicker
                services={services}
                selected={selected.containerId}
                onSelect={setPicked}
                productOf={productOf}
                releaseOf={(service) => releaseById.get(service.releaseId)?.number}
              />
            )
          }
        />
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
                {mounts.map(({ mount, size, product: keeperProduct }) => (
                  <MountCard
                    key={`${mount.source}:${mount.target}`}
                    mount={mount}
                    product={keeperProduct}
                    size={size}
                    wide={roomy}
                  />
                ))}
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

      {/* Drawn here and not in a card: a press inside a dialog or a sheet
          reaches the row it was opened from, and a card is a link. */}
      {dialog}
      <ServiceDetails
        service={detailed}
        product={product(detailed?.image ?? containerFor(detailed?.containerId ?? "")?.image)}
        onClose={() => setDetailsId(undefined)}
      />
    </div>
  )
}

/**
 * Which service the usage charts draw: each one as its product and its name,
 * side by side while they fit — four at most — and a select past that, where
 * a row of buttons would wrap under the title. A service that is not running
 * has no socket to read, so it is shown and cannot be picked.
 */
function ServicePicker({
  services,
  selected,
  onSelect,
  productOf,
  releaseOf,
}: {
  services: DeploymentRuntimeService[]
  selected: string
  onSelect: (containerId: string) => void
  productOf: (service: DeploymentRuntimeService) => string
  releaseOf: (service: DeploymentRuntimeService) => number | undefined
}) {
  if (services.length <= 4)
    return (
      <ToggleGroup
        type="single"
        value={selected}
        // Radix reports "" when the pressed item is pressed again; the chart
        // keeps its service rather than drawing nobody's.
        onValueChange={(next) => next && onSelect(next)}
        variant="outline"
        size="sm"
        aria-label="Usage of service"
        className="max-w-full overflow-x-auto"
      >
        {services.map((service) => {
          const name = service.service || service.name
          const number = releaseOf(service)
          return (
            <ToggleGroupItem
              key={service.containerId}
              value={service.containerId}
              disabled={service.state !== "running"}
              className="gap-1.5 px-2.5 text-hint"
            >
              <ProductGlyph id={productOf(service)} />
              <span className="max-w-32 truncate">{name}</span>
              {!service.liveRelease && number !== undefined && (
                <span className="numeric text-muted-foreground">#{number}</span>
              )}
            </ToggleGroupItem>
          )
        })}
      </ToggleGroup>
    )
  return (
    // Its own line on a phone, where a fixed width beside the title would
    // push the title off the start of the header.
    <div className="w-full sm:w-56">
      <Select value={selected} onValueChange={onSelect}>
        <SelectTrigger size="sm" className="w-full" aria-label="Usage of service">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {services.map((service) => {
            const number = releaseOf(service)
            return (
              <SelectItem
                key={service.containerId}
                value={service.containerId}
                disabled={service.state !== "running"}
                hint={number !== undefined ? `#${number}` : undefined}
              >
                <ProductGlyph id={productOf(service)} />
                <span className="truncate">{service.service || service.name}</span>
              </SelectItem>
            )
          })}
        </SelectContent>
      </Select>
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

/** The lifecycle verbs of Docker's menu that belong on a release's card. */
const LIFECYCLE_VERBS = new Set(["start", "unpause", "restart", "stop", "pause"])

/** The verbs kept here never open a tab of Docker's detail page, so this is never called. */
const noTab = () => undefined

/** A Docker verb as the menu draws it: always a word, never inline beside a card's readings. */
function verbOf({ key, label, icon, run, progressive, danger }: ContainerVerb): Verb {
  return { key, label, icon, run, progressive, danger }
}

/**
 * One container of the release, as a card that opens it in Docker — drawn as
 * the product its image is, with the same live readings the Containers page
 * gives it (`container-card.tsx`), and the release it belongs to at its edge.
 *
 * One line from `xl`, so the menu stands on the card's middle: what it runs
 * under the name, and at its other end the release, the state with how long
 * it has been up, CPU and memory — each reading one line, its name in front of
 * its figure, in a fixed measure so a column of cards reads down like a table.
 * The measures stay while a new container's readings are on their way, so its
 * release and state do not stand a column to the right of everyone else's.
 * The id joins the line at `2xl`. Under `xl` the facts are one line under the
 * name and the readings the next, and the state stays beside the name.
 *
 * An image that is only a digest is left out of the line, since it names
 * nothing, and "no health check" is Details' to say. One or two published
 * ports, with who can reach each, join the line from `xl`. What it has no room
 * for goes under it: more ports than that, every port below `xl`, and — for a
 * container that is restarting or has exited — Docker's reading of why. A card
 * with none of those stays one line. The menu starts, stops, restarts and
 * pauses the container under the confirmations Docker's own page asks, and
 * opens the facts Docker records about it; while one of those is in flight the
 * state says so.
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
  widest,
  confirm,
  act,
  pending,
  onChanged,
  onDetails,
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
  /** Room for the release, the state and the readings beside the name. */
  wide: boolean
  /** Room for the container's id beside its name as well. */
  widest: boolean
  confirm: ConfirmFn
  act: ReturnType<typeof useContainerControl>["act"]
  /** What the operator pressed on this container that is still in flight: "Stopping". */
  pending?: string
  onChanged: () => void
  onDetails: () => void
  index: number
}) {
  const router = useRouter()
  const name = service.name || service.containerId
  const listed = useMemo(() => runtimeContainer(service, container), [service, container])
  // Docker's verbs, with the confirmations and the capabilities they already
  // carry: start and resume need `service.control`, stop and restart
  // `destructive`. The rest of that menu — logs, a shell, an update, remove —
  // is either on this card already or is Docker's to do.
  const lifecycle = useContainerVerbs({
    container: listed,
    confirm,
    act,
    onOpenTab: noTab,
    onChanged,
  })
    .filter((verb) => LIFECYCLE_VERBS.has(verb.key))
    .map((verb): Verb => ({
      ...verbOf(verb),
      // One command at a time: a second press on a stop that has not landed
      // is the restart nobody asked for.
      disabled: Boolean(pending),
      group: "Run",
    }))
  const verbs: Verb[] = [
    ...lifecycle,
    {
      key: "details",
      label: "Details",
      icon: Information,
      run: onDetails,
    },
    {
      key: "logs",
      label: "Logs",
      icon: Logs,
      run: () => router.push(`/deploy/${projectId}/logs?service=${service.containerId}`),
    },
    {
      key: "console",
      label: "Console",
      icon: Terminal,
      disabled: service.state !== "running",
      run: () => router.push(`/deploy/${projectId}/console?service=${service.containerId}`),
    },
  ]
  if (service.stack) {
    verbs.push({
      key: "stack",
      label: "Open stack",
      icon: Layers,
      run: () => router.push(`/docker/stacks/${service.stack}`),
    })
  }
  verbs.push({
    key: "docker",
    label: "Open in Docker",
    icon: External,
    run: () => router.push(`/docker/containers/${service.containerId}`),
  })
  // A menu of one group needs no name for it.
  if (lifecycle.length > 0) {
    for (const verb of verbs) verb.group ??= "Open"
  }

  const number = release?.number
  const current = container?.state === service.state ? container : undefined
  const word = stateWord(service.state)
  // How long it has been up and what its check says; "no health check" is
  // Details' to say, not a line on every card.
  // A container Docker keeps restarting, or one the kernel killed for its
  // memory, says so on its card: a "Running" that has restarted forty times
  // is not the same reading as one that never has.
  const history = [
    service.oomKilled && "killed for memory",
    service.restartCount && plural(service.restartCount, "restart"),
  ]
  const detail = [
    !current
      ? serviceHealth(service)
      : current.state === "running"
        ? [current.uptimeSeconds > 0 && duration(current.uptimeSeconds), current.health]
            .filter(Boolean)
            .join(" · ")
        : statusDetail(current),
    ...history,
  ]
    .filter(Boolean)
    .join(" · ")
  const state = (
    <Status
      className="items-baseline"
      state={pending ? "restarting" : service.state}
      label={
        pending ? (
          `${pending}\u2026`
        ) : run ? (
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
  const ports = publishedPorts(container?.exposure)
  // One or two ports are short enough to join the line under the name, which
  // keeps the common card to a single band; more go under it.
  const portsInline = wide && ports.length > 0 && ports.length <= 2
  const identity = container ? (
    // A bare digest names nothing, so it is left out rather than truncated.
    <ContainerIdentity container={container} id={widest} image={!isImageDigest(container.image)} />
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
  const cpu = container && <CpuReading compact stat={stat} container={container} trend={trend} />
  const memory = container && <MemoryReading compact stat={stat} container={container} />
  const failing = wantsFailureReading(service.state)
  const below = !wide || (ports.length > 0 && !portsInline) || failing

  return (
    <ChoiceRow
      verb={name}
      href={`/docker/containers/${service.containerId}`}
      busy={Boolean(run)}
      index={index}
      leading={<ProductLogo id={product} size="sm" />}
      title={name}
      description={
        portsInline ? (
          <span className="flex min-w-0 items-center gap-3">
            {identity}
            <span className="shrink-0">
              <RuntimePorts ports={ports} />
            </span>
          </span>
        ) : (
          identity
        )
      }
      trailing={
        wide ? (
          // One baseline across the four, so the words and figures of different
          // sizes sit on the same line instead of the same centre.
          <span className="flex items-baseline gap-2">
            <span className="w-28 min-w-0">
              <ReleaseCell service={service} release={release} />
            </span>
            <span className="flex w-44 min-w-0 items-baseline gap-2">
              {state}
              <span className="min-w-0 truncate text-hint text-muted-foreground">{detail}</span>
              {runLink}
            </span>
            <span className="w-28">{cpu}</span>
            <span className="w-40">{memory}</span>
          </span>
        ) : (
          state
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${name}`} />}
    >
      {below && (
        <>
          {!wide && (
            // Two lines, the facts and then the readings: they are one group
            // each, so a narrow card never breaks between CPU and memory.
            <div className="flex min-w-0 flex-col gap-1.5 sm:pl-11">
              <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground">
                <ReleaseCell service={service} release={release} />
                {detail && <span className="min-w-0 truncate">{detail}</span>}
                {runLink}
              </span>
              {container && (
                <span className="flex min-w-0 items-center gap-x-5">
                  {cpu}
                  {memory}
                </span>
              )}
            </div>
          )}
          {ports.length > 0 && !portsInline && (
            <div className="min-w-0 sm:pl-11">
              <RuntimePorts ports={ports} />
            </div>
          )}
          {failing && (
            <ServiceFailure
              containerId={service.containerId}
              state={service.state}
              projectId={projectId}
            />
          )}
        </>
      )}
    </ChoiceRow>
  )
}

/**
 * The release a container belongs to: the word for whether it serves
 * traffic, and beside it the number the shell's facts row uses — with, for a
 * container that does not serve, why it is still here. A release the list has
 * not brought yet has no number to say, rather than its id in place of one.
 */
function ReleaseCell({
  service,
  release,
}: {
  service: DeploymentRuntimeService
  release?: DeploymentRelease
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
      className="items-baseline"
      tone={service.liveRelease ? "running" : "stopped"}
      label={service.liveRelease ? "Live" : "Other release"}
    />
  )
  const facts = [release && `#${release.number}`, why].filter(Boolean).join(" · ")
  const line = facts && (
    <span className="numeric truncate text-hint text-muted-foreground">{facts}</span>
  )
  return (
    <span className="inline-flex max-w-full min-w-0 items-baseline gap-1.5">
      {tag}
      {line}
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
 *
 * One line from `lg`: who owns the route, and whether a password stands in
 * front of it, under the hostname; the route and the certificate in fixed
 * measures at the edge, so down a list they are two columns. The tags stood
 * at the edge as well, which needed the extra-large width before the
 * hostname had room, and every card under it was two lines. Below `lg` the
 * line is the hostname's alone — a hostname cut to "www.example…" is the one
 * thing on the card that must not be — and the readings go under it.
 *
 * Which site config serves it is said once, in the block's header; a card
 * repeats it, first under its name, only when another config answers for it.
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
      icon: LockClosed,
      run: () => router.push(certificate),
    })
  }
  verbs.push(
    {
      key: "copy",
      label: "Copy the address",
      icon: Copy,
      run: () => void copyText(url, "Address copied"),
    },
    {
      key: "settings",
      label: "Domain settings",
      icon: SettingsSliders,
      run: () => router.push(`/deploy/${projectId}/settings/domains`),
    },
  )
  const foreign = domain.route === "foreign" || domain.route === "conflict"
  const elsewhere = domain.servedBy && (foreign || domain.servedBy !== siteName)
  const route = <Status tone={ROUTE_TONE[domain.route]} label={ROUTE_LABEL[domain.route]} />

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
        <>
          {elsewhere && (
            <>
              {foreign ? "answered by " : "served by "}
              <span className="font-mono">{domain.servedBy}</span>
              {" · "}
            </>
          )}
          {domain.protected && (
            <>
              <Tag>Password</Tag>
              {" · "}
            </>
          )}
          <Tag>{OWNERSHIP_WORD[domain.ownership]}</Tag>
        </>
      }
      trailing={
        wide && (
          <>
            <span className="flex w-24">{route}</span>
            <span className="flex w-56 min-w-0">
              <CertificateReading domain={domain} issuer={false} />
            </span>
          </>
        )
      }
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${domain.hostname}`} />}
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 sm:pl-11">
          {route}
          <CertificateReading domain={domain} issuer={false} />
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
 *
 * One line: where it is mounted, who owns it, and — when it is not where it
 * should be — why, under the name; its state and then its size at the edge,
 * the size in a fixed measure so down a list the sizes are one column and the
 * states end on one edge. The reason was a second line of its own, which made
 * the one card with something to say twice the height of the rest. A phone
 * has no room for the size beside the name, so it joins the line under it.
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
  const stored = size ? bytes(size) : undefined
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
          {" · "}
          <Tag>{OWNERSHIP_WORD[mount.ownership]}</Tag>
          {!wide && stored && <span className="numeric">{` · ${stored}`}</span>}
          {/* Last, because it is the longest: the mount point and the owner
              are what tell two cards apart, and the state at the edge has
              already said that something is wrong. */}
          {mount.detail && (
            <>
              {" · "}
              <span className={cn(mount.status === "missing" && "text-destructive")}>
                {mount.detail}
              </span>
            </>
          )}
        </>
      }
      trailing={
        <>
          <Status tone={status.tone} label={status.label} />
          {wide && (
            <span className="numeric flex w-16 justify-end text-hint text-muted-foreground">
              {stored}
            </span>
          )}
        </>
      }
    />
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
 * The job's own record is read for all of that, and `JobCard` decides where
 * the gate's reading stands on its line. Without the record the card says
 * what the release engine observed and nothing it cannot know: one line, why
 * the job could not be read under the name and how its last run went at the
 * edge. The gate's tag and its age keep a line under the name there, because
 * beside the outcome they need more than a half-width panel has — and that
 * card lasts only until the job's record arrives. A phone keeps the outcome
 * under the name as well.
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
 * connection exists.
 *
 * One line: the engine and the address under the name, followed by why it
 * cannot be reached when something says so, and the state at the edge. The
 * reason was a second line of its own under a block that is the page's full
 * width, and had the room beside the address. On a phone the state and the
 * reason go under the address, which is the line they would otherwise cut.
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
  // A saved connection that no longer opens says so here: the dependency is
  // on a row nothing can dial, whatever was last observed of it.
  const unusable = connection && unusableReason(connection)
  const note = unusable
    ? `The saved connection cannot be opened: ${unusable}`
    : (link || (database && connection)) && (link?.detail ?? item.detail)
  const facts = link ? (
    <>
      {engine} · <span className="font-mono">{link.hostname}</span>
      {link.database && ` · database ${link.database}`}
    </>
  ) : database && connection ? (
    engine
  ) : (
    item.detail
  )
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
        wide && note ? (
          <>
            {facts} · {note}
          </>
        ) : (
          facts
        )
      }
      trailing={wide && state}
    >
      {!wide && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-hint text-muted-foreground sm:pl-11">
          {state}
          {note && <span>{note}</span>}
        </div>
      )}
    </ChoiceRow>
  )
}
