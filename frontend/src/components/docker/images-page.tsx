"use client"

import { useMemo, useState } from "react"
import { Download, Layers, Trash, Wrench } from "@/components/icons"
import { get, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Container, DockerDiskUsage, DockerImage, ImageUpdateStatus } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { BuildDialog } from "@/components/docker/build-dialog"
import { Term } from "@/components/docker/explain"
import { useImageActions } from "@/components/docker/image-actions"
import { ImageBand } from "@/components/docker/image-band"
import { ImageTable } from "@/components/docker/image-table"
import {
  FRESHNESS_LABEL,
  imageFreshness,
  visibleImages,
  type Freshness,
  type ImageSort,
  type Use,
} from "@/components/docker/images"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

/**
 * Reading register, design-system §15, on the Packages page's shape: the
 * engine's identity line, a band of two questions, and one framed table.
 *
 * The page was a disk panel of four grey lines, a notice and a column of
 * cards, and nothing on it moved or was told apart by anything but its words.
 * Each part went where it is said better. The disk lines are one bar of what
 * Docker holds, the reclaimable part of each kind drawn inside it; the notice
 * about images with newer versions is the registry's half of the band, where
 * every image has one answer and a press narrows the table to it; and the
 * cards are a table again (§12), each row naming the containers that run the
 * image, its registry's answer, its age and its size.
 *
 * An image's own detail was a sheet over this table (`?image=`). It is a page
 * now, `/docker/images/<id>`, with these images as a rail beside it; the old
 * address still arrives there (the route's `page.tsx` redirects it).
 */

const SORT_LABEL: Record<ImageSort, string> = {
  size: "Largest first",
  created: "Newest first",
  name: "By name",
}
const NEXT_SORT: Record<ImageSort, ImageSort> = { size: "created", created: "name", name: "size" }

export function ImagesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 1280px)")
  const [query, setQuery] = useSessionState("docker.images.query", "")
  const [use, setUse] = useViewState<Use>("docker.images.use", "all")
  const [state, setState] = useSessionState<Freshness | "">("docker.images.state", "")
  const [sort, setSort] = useViewState<ImageSort>("docker.images.sort", "size")
  const [building, setBuilding] = useState(false)
  const [checking, setChecking] = useState(false)

  // The three reads the table and an image's rail both make are named, so
  // each is one request between them and the rail beside an image is drawn
  // from what this table last read.
  const images = usePoll(
    (signal) => get<DockerImage[]>("/docker/images/", undefined, signal),
    30_000,
    [],
    { key: "/docker/images/" },
  )
  /*
   * Whether the tags in use still point where they did when they were pulled —
   * the one question a self-hoster has about an image, which Watchtower
   * answers by restarting things unasked and Portainer behind a licence. One
   * registry request per tag, cached half an hour on the server; slow here
   * because the answer changes when a publisher pushes, and Docker Hub
   * rate-limits by address.
   */
  const updates = usePoll(
    (signal) => get<Record<string, ImageUpdateStatus>>("/docker/images/updates", undefined, signal),
    15 * 60_000,
    [],
    { key: "/docker/images/updates" },
  )
  const usage = usePoll(
    (signal) => get<DockerDiskUsage>("/docker/disk-usage", undefined, signal),
    60_000,
  )
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    30_000,
    [],
    { key: "/docker/containers/" },
  )
  const engine = usePoll(
    (signal) => get<{ serverVersion?: string }>("/docker/ping", undefined, signal),
    0,
  ).data

  const list = useMemo(() => images.data ?? [], [images.data])
  const states = useMemo(
    () => new Map(list.map((image) => [image.id, imageFreshness(image, updates.data)] as const)),
    [list, updates.data],
  )
  const users = useMemo(() => {
    const map = new Map<string, Container[]>()
    for (const c of containers.data ?? []) map.set(c.imageId, [...(map.get(c.imageId) ?? []), c])
    return map
  }, [containers.data])
  const visible = useMemo(
    () => visibleImages(list, { query, use, state, sort, states }),
    [list, query, use, state, sort, states],
  )
  const arrived = useArrivals(list.map((i) => i.id))

  const refresh = () => {
    images.refresh()
    usage.refresh()
    containers.refresh()
    updates.refresh()
  }

  const recheck = async () => {
    setChecking(true)
    try {
      await get("/docker/images/updates", { refresh: true })
      updates.refresh()
      notify.success("Checked with the registries")
    } catch (err) {
      notify.error("Could not check for updates", err)
    } finally {
      setChecking(false)
    }
  }

  const pruneUntagged = () =>
    confirm({
      title: "Prune untagged images",
      confirmLabel: "Prune",
      workingLabel: "Removing untagged images…",
      description: (
        <p>
          Removes <Term name="dangling">dangling images</Term> — layers left behind when a tag moved
          to a newer copy. Nothing references them and no container can need them.
        </p>
      ),
      action: async () => {
        const rep = await post<{ spaceReclaimed: number }>("/docker/images/prune")
        notify.success(`Reclaimed ${bytes(rep.spaceReclaimed)}`)
        refresh()
      },
    })

  const actions = useImageActions({ confirm, onChanged: refresh, onRemoved: refresh })
  // Docker's own count can miss a stopped container; the container list does not.
  const rowVerbs = (image: DockerImage) =>
    actions.verbsFor({
      ...image,
      inUse: image.containers > 0 || (users.get(image.id)?.length ?? 0) > 0,
    })

  const inUse = list.filter((i) => i.containers > 0).length
  const untagged = list.filter((i) => i.dangling).length
  const unused = list.filter((i) => i.containers === 0 && !i.dangling).length
  const outdated = list.filter((i) => states.get(i.id) === "outdated").length
  const unverified = list.filter((i) => i.containers > 0 && states.get(i.id) === "unknown").length
  const verified = list.filter((i) => i.containers > 0 && states.get(i.id) === "current").length
  const checkedAt = Object.values(updates.data ?? {})
    .map((u) => u.checkedAt)
    .sort()
    .at(-1)

  return (
    <Workspace
      name="Images"
      refresh={refresh}
      escape={() => {
        if (state) {
          setState("")
          return true
        }
        if (!query) return false
        setQuery("")
        return true
      }}
    >
      <Page>
        {dialog}
        <PageContext eyebrow="Docker" title="Images" />

        {/* The engine the images belong to, on the line a project opens on:
            Docker as the mark, the registry's verdict beside its name (a
            press narrows the table to what is behind), what Docker holds as
            facts, and at the far end the one command, Pull, with the other
            way an image arrives behind the menu. */}
        <HostIdentity
          className="animate-rise"
          mark="docker"
          fallback={Layers}
          title={
            <span className="inline-flex min-w-0 items-baseline gap-2">
              Docker
              {engine?.serverVersion && (
                <span className="font-mono text-body font-normal text-muted-foreground">
                  {engine.serverVersion}
                </span>
              )}
            </span>
          }
          state={
            outdated > 0 ? (
              <button
                type="button"
                aria-label={`Only the ${plural(outdated, "image")} with an update`}
                onClick={() => {
                  setState("outdated")
                  setUse("all")
                }}
                // A flex box, so the button is the height of its dot and
                // words and the line does not grow when the verdict lands.
                className="flex items-center rounded-sm focus-ring hover:underline"
              >
                <Status verdict="warning" label={`${plural(outdated, "update")} available`} />
              </button>
            ) : updates.error ? (
              <Status verdict="warning" label="Registry checks unavailable" />
            ) : unverified > 0 ? (
              <Status verdict="warning" label={`${plural(unverified, "image")} unverified`} />
            ) : verified > 0 && checkedAt ? (
              <Status verdict="ok" label="Tags in use are current" />
            ) : null
          }
          facts={
            images.data ? (
              <>
                <span className="numeric font-medium text-foreground">
                  {plural(list.length, "image")}
                </span>
                {usage.data && (
                  <>
                    <FactDot />
                    <span className="numeric">{bytes(usage.data.layersSize)} of layers</span>
                  </>
                )}
                <FactDot />
                <span className="numeric">{inUse} in use</span>
                {untagged > 0 && (
                  <>
                    <FactDot />
                    <span className="numeric">{untagged} untagged</span>
                  </>
                )}
              </>
            ) : (
              // Holds the line open, so the band under it does not move when the list lands.
              <span aria-hidden>&nbsp;</span>
            )
          }
          aside={
            <span className="max-sm:hidden">
              <WorkspaceHelp compact />
            </span>
          }
          command={
            can("service.control") && (
              <Button size="sm" onClick={() => actions.openPull("")}>
                <Download className="size-4" />
                Pull image
              </Button>
            )
          }
          menu={
            can("service.control") && (
              <VerbMenu
                label="More image actions"
                verbs={[
                  {
                    key: "build",
                    label: "Build image…",
                    icon: Wrench,
                    run: () => setBuilding(true),
                  },
                ]}
              />
            )
          }
        />

        {/* Here before anything else is, saying so, so the page does not move
            when the list lands. */}
        <ImageBand
          usage={usage.data}
          usageLoading={usage.loading}
          usageError={usage.error}
          images={list}
          imagesLoading={images.loading && !images.data}
          imagesFailed={images.error !== undefined && !images.data}
          states={states}
          checkedAt={checkedAt}
          checking={checking}
          updatesError={updates.error}
          onCheck={recheck}
          state={state}
          onState={(next) => {
            setState(next)
            setUse("all")
          }}
          confirm={confirm}
          onPruned={refresh}
        />

        {/* Framed, as every table is (§2): its container scrolls, and an
            edge says where the columns that did not fit went. */}
        {images.loading && !images.data ? (
          <LoadingPanel />
        ) : images.error && !images.data ? (
          <ErrorState error={images.error} onRetry={images.refresh} />
        ) : (
          <Panel className="animate-rise">
            <PanelHeader
              title="Images"
              actions={
                <>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    aria-label={`Order: ${SORT_LABEL[sort]}`}
                    onClick={() => setSort(NEXT_SORT[sort])}
                  >
                    {SORT_LABEL[sort]}
                  </Button>
                  {can("destructive") && untagged > 0 && (
                    <Button size="sm" variant="outline" onClick={pruneUntagged}>
                      <Trash className="size-4" />
                      Prune untagged
                    </Button>
                  )}
                </>
              }
            />
            <PanelToolbar>
              <SearchInput
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Name, version or id"
                aria-label="Find images"
              />
              <div className="flex min-w-0 flex-wrap items-center gap-1">
                <FilterChip selected={use === "all"} onClick={() => setUse("all")}>
                  All <ChipCount>{list.length}</ChipCount>
                </FilterChip>
                <FilterChip selected={use === "running"} onClick={() => setUse("running")}>
                  In use <ChipCount>{inUse}</ChipCount>
                </FilterChip>
                <FilterChip selected={use === "unused"} onClick={() => setUse("unused")}>
                  Not in use <ChipCount>{unused}</ChipCount>
                </FilterChip>
                {untagged > 0 && (
                  <FilterChip selected={use === "untagged"} onClick={() => setUse("untagged")}>
                    Untagged <ChipCount>{untagged}</ChipCount>
                  </FilterChip>
                )}
              </div>
              {state && (
                <FilterChip
                  selected
                  onClick={() => setState("")}
                  aria-label="Clear registry filter"
                >
                  {FRESHNESS_LABEL[state]} ×
                </FilterChip>
              )}
            </PanelToolbar>
            <PanelBody flush>
              {list.length === 0 ? (
                <EmptyState
                  icon={Layers}
                  className="m-4"
                  title="No images on this server"
                  description="Pull one from a registry, or build one from a directory on this server."
                  action={
                    can("service.control") && (
                      <Button size="sm" onClick={() => actions.openPull("")}>
                        <Download className="size-4" />
                        Pull image
                      </Button>
                    )
                  }
                />
              ) : visible.length === 0 ? (
                <EmptyState
                  icon={Layers}
                  className="m-4"
                  title="Nothing matches"
                  description="Clear the search or pick All to see every image."
                />
              ) : (
                <ImageTable
                  images={visible}
                  users={users}
                  updates={updates.data}
                  states={states}
                  wide={wide}
                  arrived={arrived}
                  pulling={actions.pulling}
                  verbs={rowVerbs}
                />
              )}
            </PanelBody>
          </Panel>
        )}

        <BuildDialog open={building} onOpenChange={setBuilding} onBuilt={refresh} />
        {actions.dialogs}
      </Page>
    </Workspace>
  )
}
