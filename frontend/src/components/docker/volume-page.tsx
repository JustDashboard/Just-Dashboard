"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, Database } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import type {
  BackupJob,
  BackupResourceReport,
  ComposeStack,
  Container,
  VolumeDetail,
} from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { useConfirm } from "@/components/confirm-dialog"
import { useVolumeVerbs } from "@/components/docker/volume-actions"
import { VolumePicture } from "@/components/docker/volume-picture"
import { VolumeRail } from "@/components/docker/volume-rail"
import {
  VolumeBackups,
  VolumeConfiguration,
  VolumeFiles,
  VolumeMounts,
} from "@/components/docker/volume-views"
import {
  SHOW_LABEL,
  STANDING_COLOR,
  STANDING_LABEL,
  VOLUME_FILTER_KEYS,
  VOLUME_TABS,
  backupOf,
  holderHue,
  holderOf,
  holders,
  isAnonymous,
  railCaption,
  shortName,
  sizeReading,
  splitName,
  standing,
  standingWords,
  visibleVolumes,
  volumeHref,
  volumeProduct,
  withLiveStates,
  type Backup,
  type Show,
  type Standing,
  type VolumeSort,
  type VolumeTab,
} from "@/components/docker/volumes"
import { IconAction } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { containerProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

const BACK = (
  <Link
    href="/docker/volumes"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Volumes
  </Link>
)

const TAB_LABEL: Record<VolumeTab, string> = {
  files: "Files",
  mounts: "Mounts",
  backups: "Backups",
  configuration: "Configuration",
}

/**
 * One volume, as a place of its own.
 *
 * It was a `SidePanel` over the Volumes table until 0.8.7: four readings, the
 * containers mounting it, a file browser and what Docker was told, stacked in
 * a drawer with the table it covered behind it, and this address only a
 * redirect to that drawer. A file browser is not a thing glanced at over a
 * list, so a volume is a destination with a way back, the way a container is
 * (`container-detail`) and a service is (`procs/service-page`).
 *
 * It opens on the volume's identity line — the product whose data it holds,
 * its name, where it stands, its size and where it is kept, and its verbs with
 * one command — over a picture of who mounts it and where it is backed up
 * (`volume-picture`), then its views as the underlined strip, in the address
 * as `?tab=`: its files, the containers that mount it, the jobs that back it
 * up, and what Docker was told.
 *
 * The table stays beside it from `xl` (`VolumeRail`) with its search, filters
 * and order, so the next volume is one press, Alt+↑ and Alt+↓ walk the list,
 * and the view on screen goes with you. On a phone the page is the one pane.
 */
export function VolumePage() {
  const params = useParams<{ name: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const name = decodeName(params.name)
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("docker.volume.tab", VOLUME_TABS[0])
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  // The address can change under the page — Back, Forward, a link to another
  // view of the volume on screen — and the view follows it.
  const [seen, setSeen] = useState(requested)
  if (requested !== seen) {
    setSeen(requested)
    setChosen(requested ?? VOLUME_TABS[0])
  }
  const tab: VolumeTab = (VOLUME_TABS as readonly string[]).includes(chosen)
    ? (chosen as VolumeTab)
    : VOLUME_TABS[0]
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      // The address says which view, so a link to a volume's mounts opens on
      // them; replaced rather than pushed, because the view is not a place
      // Back should step through.
      const url = new URL(window.location.href)
      if (next === VOLUME_TABS[0]) url.searchParams.delete("tab")
      else url.searchParams.set("tab", next)
      window.history.replaceState(null, "", `${url.pathname}${url.search}`)
    },
    [remember],
  )
  const open = useCallback((next: string) => router.push(volumeHref(next, tab)), [router, tab])
  // Opened from a row, the page shows the view it was left on; the address
  // says so, so the link copied from it opens the same view.
  useEffect(() => {
    if (new URL(window.location.href).searchParams.has("tab") || tab === VOLUME_TABS[0]) return
    window.history.replaceState(null, "", volumeHref(name, tab))
    // Once, on arrival: afterwards every change of view writes the address itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [name])

  const rail = useRail()
  const at = rail.volumes.findIndex((volume) => volume.name === name)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.volumes[at + direction] : rail.volumes[direction > 0 ? 0 : -1]
    if (next) open(next.name)
  }
  const detail = usePoll(
    (signal) => get<VolumeDetail>(`/docker/volumes/${encodeURIComponent(name)}`, undefined, signal),
    30_000,
    [name],
    { enabled: name !== "" },
  )
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.volumes.length === 0 || at >= rail.volumes.length - 1,
    run: () => adjacent(1),
  }
  const refresh = () => {
    detail.refresh()
    rail.refresh()
  }
  // Inspect is the one read that carries the driver's options; the list has
  // everything else, so a volume opened from its row is drawn at once.
  const listed = rail.all.find((volume) => volume.name === name)
  const read = detail.data ? withLiveStates([detail.data], rail.states)[0] : undefined
  const volume = read ?? (detail.error ? undefined : listed)

  return (
    <Workspace
      name="Volume"
      stateKey="volume.page"
      refresh={refresh}
      commands={[
        {
          id: "previous",
          label: "Previous volume",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next volume",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list stays
          put while the volume scrolls; narrower, the page is the one pane and
          scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <VolumeRail
            className="hidden w-72 shrink-0 xl:flex"
            volumes={rail.volumes}
            current={name}
            currentVolume={volume}
            productOf={rail.productOf}
            loading={rail.loading}
            query={rail.query}
            onQuery={rail.setQuery}
            filtered={rail.filtered}
            onClearFilters={rail.clearFilters}
            caption={rail.caption}
            onOpen={open}
          />
          <VolumeMain
            key={name}
            name={name}
            volume={volume}
            error={detail.error}
            productOf={rail.productOf}
            coverage={rail.coverage}
            backup={rail.backups.get(name)}
            tab={tab}
            setTab={setTab}
            previous={previous}
            next={next}
          />
        </div>
      </Page>
    </Workspace>
  )
}

/** The route's segment as a volume's name; a name cannot hold `%`, so decoding twice is harmless. */
function decodeName(segment: string | undefined): string {
  if (!segment) return ""
  try {
    return decodeURIComponent(segment)
  } catch {
    return segment
  }
}

/**
 * The table's list, read for the rail: the same search, filters and order the
 * table was left on (they are its session's), at the table's cadence, with
 * container state from the socket the table reads.
 */
function useRail() {
  const [query, setQuery] = useSessionState(VOLUME_FILTER_KEYS.query, "")
  const [show, setShow] = useViewState<Show>(VOLUME_FILTER_KEYS.show, "all")
  const [only, setOnly] = useSessionState<Standing | "">(VOLUME_FILTER_KEYS.standing, "")
  const [holder, setHolder] = useSessionState(VOLUME_FILTER_KEYS.holder, "")
  const [sort] = useViewState<VolumeSort>(VOLUME_FILTER_KEYS.sort, { key: "size", desc: true })
  const [live, setLive] = useState<Container[]>()
  const list = usePoll(
    (signal) => get<VolumeDetail[]>("/docker/volumes/", undefined, signal),
    30_000,
  )
  // As the table asks it: rarely, because discovery walks every module.
  const report = usePoll(
    (signal) => get<BackupResourceReport>("/backups/resources", undefined, signal),
    5 * 60_000,
  )
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") setLive(envelope.data as Container[])
  }, [])
  useSocket("/docker/containers/stream", { onMessage })

  const states = useMemo(() => new Map((live ?? []).map((c) => [c.id, c.state])), [live])
  const productOf = useMemo(
    () => new Map((live ?? []).map((c) => [c.id, containerProduct(c)])),
    [live],
  )
  const all = useMemo(() => withLiveStates(list.data ?? [], states), [list.data, states])
  const backups = useMemo(() => {
    const map = new Map<string, Backup>()
    for (const resource of report.data?.resources ?? []) {
      if (resource.kind !== "volume") continue
      const backup = backupOf(resource)
      if (backup) map.set(resource.id, backup)
    }
    return map
  }, [report.data])
  const showing = show === "unprotected" && !report.data ? "all" : show
  const volumes = useMemo(
    () => visibleVolumes(all, { query, show: showing, standing: only, holder, sort, backups }),
    [all, query, showing, only, holder, sort, backups],
  )
  const narrowed = [
    SHOW_LABEL[showing],
    only ? STANDING_LABEL[only].toLowerCase() : "",
    holder ? (holders(all).find((h) => h.key === holder)?.name ?? holder.replace(/^\w+:/, "")) : "",
  ].filter(Boolean)
  const coverage: "reading" | "unreadable" | "read" = report.data
    ? "read"
    : report.error
      ? "unreadable"
      : "reading"
  const refreshList = list.refresh
  const refreshReport = report.refresh
  const refresh = useCallback(() => {
    refreshList()
    refreshReport()
  }, [refreshList, refreshReport])
  return {
    all,
    volumes,
    states,
    productOf,
    backups,
    coverage,
    loading: list.loading && !list.data,
    refresh,
    query,
    setQuery,
    filtered: narrowed.length > 0,
    clearFilters: () => {
      setShow("all")
      setOnly("")
      setHolder("")
    },
    caption: list.data
      ? railCaption({ total: all.length, shown: volumes.length, narrowed, sort })
      : list.error
        ? "The volumes could not be read"
        : "Reading the volumes…",
  }
}

type Step = { disabled: boolean; run: () => void }

function VolumeMain({
  name,
  volume,
  error,
  productOf,
  coverage,
  backup,
  tab,
  setTab,
  previous,
  next,
}: {
  name: string
  volume?: VolumeDetail
  error?: Error
  productOf: Map<string, string>
  coverage: "reading" | "unreadable" | "read"
  backup?: Backup
  tab: VolumeTab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const stacks = usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    60_000,
  )
  // Where each covering job writes and when it last finished. The coverage
  // names the jobs; without this read they are drawn by name alone.
  const jobs = usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 60_000, [], {
    enabled: (backup?.jobs.length ?? 0) > 0,
  })
  const known = useMemo(() => new Set((stacks.data ?? []).map((s) => s.name)), [stacks.data])
  const { verbsFor } = useVolumeVerbs({
    confirm,
    backupOf: () => backup,
    // The page is about a volume that is gone: back to the ones that are not.
    onRemoved: () => router.replace("/docker/volumes"),
  })

  // Whether this page has ever had its volume, so one removed from under it
  // is told apart from an address that never named one.
  const loaded = useRef(false)
  const gone = error instanceof ApiError && error.status === 404
  useEffect(() => {
    if (volume) loaded.current = true
    else if (gone && loaded.current) router.replace("/docker/volumes")
  }, [volume, gone, router])

  const walk = (
    <span className="flex items-center">
      <IconAction label="Previous volume" disabled={previous.disabled} onClick={previous.run}>
        <ChevronUp />
      </IconAction>
      <IconAction label="Next volume" disabled={next.disabled} onClick={next.run}>
        <ChevronDown />
      </IconAction>
      <span className="max-sm:hidden">
        <WorkspaceHelp compact />
      </span>
    </span>
  )

  if (!volume && error) {
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title="Volume" actions={walk} />
        {gone ? (
          <EmptyState
            icon={Database}
            title={`No volume named ${name || "that"} on this server`}
            description="It may have been removed or pruned, or the name in the link is misspelt."
            action={
              <Button size="sm" variant="outline" asChild>
                <Link href="/docker/volumes">Back to the volumes</Link>
              </Button>
            }
          />
        ) : (
          <ErrorState error={error} />
        )}
      </div>
    )
  }

  const verbs = volume ? verbsFor(volume).map((verb) => ({ ...verb, inline: false })) : []
  // The one command is the step the volume is short of — a backup — and where
  // it has one, or the reader may not write one, the way to its files.
  const command =
    verbs.find((verb) => verb.key === "backup") ?? verbs.find((v) => v.key === "files")
  const menu = verbs
    .filter((verb) => verb !== command)
    .sort((a, b) => Number(Boolean(a.danger)) - Number(Boolean(b.danger)))
  const backUp = verbs.find((verb) => verb.key === "backup")?.run
  const counts: Partial<Record<VolumeTab, number>> = {
    mounts: volume?.usedBy.length,
    backups: backup?.jobs.length,
  }

  return (
    // From `xl` this column scrolls and the rail beside it does not. A step of
    // padding keeps a focus ring at its edge from being clipped by the scroll.
    <div className="flex min-w-0 flex-1 flex-col gap-5 xl:-mr-2 xl:min-h-0 xl:overflow-y-auto xl:pr-2">
      <PageContext eyebrow={BACK} title={name} actions={walk} />
      <VolumeIdentity
        name={name}
        volume={volume}
        productOf={productOf}
        backup={backup}
        stacks={known}
        onTab={setTab}
        command={
          command && (
            <Button size="sm" onClick={command.run}>
              <command.icon className="size-3.5" />
              {command.label}
            </Button>
          )
        }
        menu={menu.length > 0 && <VerbMenu verbs={menu} label={`More actions for ${name}`} />}
      />

      <VolumePicture
        volume={volume}
        productOf={productOf}
        coverage={coverage}
        backup={backup}
        jobs={jobs.data}
        stacks={known}
        onTab={setTab}
        onBackUp={backUp}
      />

      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <TabsPrimitive.List
          aria-label={`Views of ${name}`}
          className="flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline [&::-webkit-scrollbar]:hidden"
        >
          {VOLUME_TABS.map((key) => (
            <TabsPrimitive.Trigger
              key={key}
              value={key}
              className={tabClasses(tab === key, "h-10")}
            >
              {TAB_LABEL[key]}
              {(counts[key] ?? 0) > 0 && <ChipCount>{counts[key]}</ChipCount>}
            </TabsPrimitive.Trigger>
          ))}
        </TabsPrimitive.List>

        {VOLUME_TABS.map((key) => (
          <TabsContent key={key} value={key} className="pt-5 pb-2">
            {tab !== key ? null : !volume ? (
              <LoadingRows rows={6} />
            ) : key === "files" ? (
              <VolumeFiles volume={volume} />
            ) : key === "mounts" ? (
              <VolumeMounts volume={volume} productOf={productOf} stacks={known} />
            ) : key === "backups" ? (
              <VolumeBackups
                coverage={coverage}
                backup={backup}
                jobs={jobs.data}
                local={volume.driver === "local" && !volume.mountType}
                onBackUp={backUp}
              />
            ) : (
              <VolumeConfiguration volume={volume} />
            )}
          </TabsContent>
        ))}
      </Tabs>
      {dialog}
    </div>
  )
}

/**
 * The line the page opens on (`HostIdentity`, laid out as a project's header):
 * the volume as the product whose data it holds, its name with a Compose
 * project's prefix a step back, where it stands beside it — a press opens the
 * containers that decide it — and after it the facts a volume is opened to
 * check: how much is in it and where that is kept, whose it is, how old, and
 * whether a backup covers it. It is drawn before the volume is read, holding
 * the same two lines, so the picture under it does not move.
 */
function VolumeIdentity({
  name,
  volume,
  productOf,
  backup,
  stacks,
  onTab,
  command,
  menu,
}: {
  name: string
  volume?: VolumeDetail
  productOf: Map<string, string>
  backup?: Backup
  stacks: Set<string>
  onTab: (tab: string) => void
  command: React.ReactNode
  menu: React.ReactNode
}) {
  if (!volume) {
    return (
      <HostIdentity
        label={`About ${name}`}
        fallback={Database}
        title={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
        // The state's line and the command's height, empty, so the line is as
        // tall as it will be on a phone too, where each has a row of its own.
        state={
          <span aria-hidden className="text-xs">
            &nbsp;
          </span>
        }
        command={<span aria-hidden className="h-8" />}
        facts={<span aria-hidden>&nbsp;</span>}
      />
    )
  }
  const { prefix, rest } = splitName(volume)
  const size = sizeReading(volume)
  const holder = holderOf(volume)
  const words = standingWords(volume)
  return (
    <HostIdentity
      label={`About ${volume.name}`}
      className="animate-rise"
      mark={volumeProduct(volume, productOf)}
      fallback={Database}
      title={
        <span
          className={cn("truncate font-mono", isAnonymous(volume) && "text-muted-foreground")}
          title={volume.name}
        >
          {/* A hash nobody chose is its first twelve characters, as its row
              has it: whole, it pushed the state under the name and the
              picture down. Configuration and Copy name have all of it. */}
          {isAnonymous(volume) ? (
            `${shortName(volume)}…`
          ) : (
            <>
              {prefix && <span className="text-muted-foreground">{prefix}</span>}
              {rest}
            </>
          )}
        </span>
      }
      state={
        <button
          type="button"
          onClick={() => onTab("mounts")}
          title={STANDING_LABEL[standing(volume)]}
          className={cn(
            "inline-flex items-center gap-1.5 rounded-sm text-xs font-medium focus-ring hover:underline",
            words.warning && "text-warning",
          )}
        >
          <span
            aria-hidden
            className="size-1.5 shrink-0 rounded-full"
            style={{ background: STANDING_COLOR[standing(volume)] }}
          />
          {words.word}
        </button>
      }
      command={command}
      menu={menu}
      facts={
        <>
          <span className="numeric font-medium text-foreground">
            {size.bytes !== undefined ? bytes(size.bytes) : size.word}
          </span>
          {/* Where it is kept and how old it is are in the picture and under
              Configuration; on a phone the line keeps to one row without them,
              so the picture is where it was while the volume was read. */}
          <span className="contents max-sm:hidden">
            <FactDot />
          </span>
          <span className="max-sm:hidden">
            {volume.driver === "local" ? (
              volume.mountType ? (
                <span className="font-mono">{volume.mountType} mount</span>
              ) : (
                "on this server"
              )
            ) : (
              <>
                kept by <span className="font-mono">{volume.driver}</span>
              </>
            )}
          </span>
          {holder.kind === "stack" && (
            <>
              <FactDot />
              {stacks.has(holder.name) ? (
                <Link
                  href={`/docker/stacks/${encodeURIComponent(holder.name)}`}
                  className="rounded-sm font-medium focus-ring hover:underline"
                  style={{ color: holderHue(holder) }}
                >
                  {holder.name}
                </Link>
              ) : (
                <span className="font-medium" style={{ color: holderHue(holder) }}>
                  {holder.name}
                </span>
              )}
            </>
          )}
          {volume.createdAt && (
            <span className="contents max-sm:hidden">
              <FactDot />
              <span title={timestamp(volume.createdAt)}>
                created {relativeTime(volume.createdAt)}
              </span>
            </span>
          )}
          {backup && (
            <>
              <FactDot />
              <button
                type="button"
                onClick={() => onTab("backups")}
                title={backup.jobs.map((job) => job.name).join(", ") || undefined}
                className={cn(
                  "rounded-sm focus-ring hover:underline",
                  backup.state === "protected" && "text-success",
                  backup.state === "paused" && "text-warning",
                )}
              >
                {backup.state === "protected"
                  ? backup.lastAt
                    ? `backed up ${relativeTime(backup.lastAt)}`
                    : "backup never ran"
                  : backup.state === "paused"
                    ? "backup paused"
                    : "not backed up"}
              </button>
            </>
          )}
        </>
      }
    />
  )
}
