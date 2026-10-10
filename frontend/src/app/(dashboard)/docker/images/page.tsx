"use client"

import { useMemo, useState } from "react"
import { Copy, Download, GitTag, Layers, Trash, Wrench } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type {
  Container,
  DockerDiskUsage,
  DockerImage,
  ImageDetail,
  ImageUpdateStatus,
} from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { useConfirm } from "@/components/confirm-dialog"
import { BuildDialog } from "@/components/docker/build-dialog"
import { Term } from "@/components/docker/explain"
import { ImageBand } from "@/components/docker/image-band"
import { PullDialog, TagDialog, usePull } from "@/components/docker/image-pull"
import { ImageSheet } from "@/components/docker/image-sheet"
import { ImageTable } from "@/components/docker/image-table"
import {
  FRESHNESS_LABEL,
  freshness,
  imagePhrase,
  primaryTag,
  shortId,
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
import type { Verb } from "@/components/verbs"
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
 * image, its registry's answer, its age and its size against the largest.
 */

const SORT_LABEL: Record<ImageSort, string> = {
  size: "Largest first",
  created: "Newest first",
  name: "By name",
}
const NEXT_SORT: Record<ImageSort, ImageSort> = { size: "created", created: "name", name: "size" }

type Named = Pick<DockerImage, "id" | "repoTags" | "repoDigests"> & { inUse: boolean }

export default function DockerImagesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 1280px)")
  const [query, setQuery] = useSessionState("docker.images.query", "")
  const [use, setUse] = useViewState<Use>("docker.images.use", "all")
  const [state, setState] = useSessionState<Freshness | "">("docker.images.state", "")
  const [sort, setSort] = useViewState<ImageSort>("docker.images.sort", "size")
  const [selected, setSelected] = useQuerySelection("image")
  // null is closed; a string (possibly empty) opens the dialog seeded with it.
  const [pullSeed, setPullSeed] = useState<string | null>(null)
  const [building, setBuilding] = useState(false)
  const [tagging, setTagging] = useState<{ id: string; name: string } | null>(null)
  const [checking, setChecking] = useState(false)
  const pull = usePull()

  const images = usePoll(
    (signal) => get<DockerImage[]>("/docker/images/", undefined, signal),
    30_000,
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
  )
  const usage = usePoll(
    (signal) => get<DockerDiskUsage>("/docker/disk-usage", undefined, signal),
    60_000,
  )
  const containers = usePoll(
    (signal) => get<Container[]>("/docker/containers/", undefined, signal),
    30_000,
  )
  const engine = usePoll(
    (signal) => get<{ serverVersion?: string }>("/docker/ping", undefined, signal),
    0,
  ).data

  const list = useMemo(() => images.data ?? [], [images.data])
  const states = useMemo(
    () =>
      new Map(
        list.map((image) => {
          const tag = primaryTag(image)
          return [image.id, freshness(image, tag ? updates.data?.[tag] : undefined)] as const
        }),
      ),
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

  const remove = (image: Named) =>
    confirm({
      title: "Delete image",
      confirmLabel: "Delete",
      description: (
        <>
          <p>
            Deletes <b>{imagePhrase(image)}</b>.
          </p>
          <p>
            No container is using it. Anything that needs it later has to pull or rebuild it — which
            for an image built here and never pushed means rebuilding from source.
          </p>
        </>
      ),
      action: async (phrase) => {
        await del(`/docker/images/${encodeURIComponent(image.id)}`, { confirm: phrase })
        if (selected === image.id) setSelected(null)
        refresh()
      },
    })

  const pruneUntagged = () =>
    confirm({
      title: "Prune untagged images",
      confirmLabel: "Prune",
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

  /**
   * What can be done to an image, declared once for its row and its sheet
   * (§13). Pull and Remove are the two pressed often enough to be glyphs on a
   * row; naming, copying and tagging are words in its menu. Remove stays drawn
   * while a container uses the image, disabled, so a row does not change
   * shape with its state: Docker refuses it, and forcing it would leave a
   * container whose image is gone and which cannot start again.
   */
  const verbsFor = (image: Named): Verb[] => {
    const tag = primaryTag(image)
    const verbs: Verb[] = []
    if (can("service.control") && tag && image.repoDigests.length > 0) {
      verbs.push({
        key: "pull",
        label: "Pull",
        icon: Download,
        inline: true,
        run: () => setPullSeed(tag),
      })
    }
    if (can("destructive")) {
      verbs.push({
        key: "remove",
        label: "Remove",
        icon: Trash,
        inline: true,
        danger: true,
        disabled: image.inUse,
        run: () => remove(image),
      })
    }
    if (can("service.control")) {
      verbs.push({
        key: "tag",
        label: "Tag…",
        icon: GitTag,
        run: () => setTagging({ id: image.id, name: tag ?? shortId(image.id) }),
      })
    }
    if (tag) {
      verbs.push({
        key: "copy-name",
        label: "Copy name",
        icon: Copy,
        run: () => void copyText(tag, "Name copied"),
      })
    }
    verbs.push({
      key: "copy-id",
      label: "Copy ID",
      icon: Copy,
      run: () => void copyText(image.id, "ID copied"),
    })
    return verbs
  }

  const rowVerbs = (image: DockerImage) => verbsFor({ ...image, inUse: image.containers > 0 })
  // The sheet's footer is a row of words, so the destructive one goes last.
  const sheetVerbs = (detail: ImageDetail) =>
    verbsFor({ ...detail, inUse: detail.usedBy.length > 0 })
      .filter((v) => !v.key.startsWith("copy"))
      .sort((a, b) => Number(Boolean(a.danger)) - Number(Boolean(b.danger)))

  const selectedImage = list.find((i) => i.id === selected)
  const selectedTag = selectedImage ? primaryTag(selectedImage) : undefined
  const inUse = list.filter((i) => i.containers > 0).length
  const untagged = list.filter((i) => i.dangling).length
  const unused = list.filter((i) => i.containers === 0 && !i.dangling).length
  const outdated = list.filter((i) => states.get(i.id) === "outdated").length
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

        {/* The engine the images belong to, as the line Packages opens on:
            Docker as the mark, what it holds as facts, and at the right end
            the registry's verdict — a press narrows the table to what is
            behind — beside the two ways an image arrives. */}
        <HostIdentity
          className="animate-rise sm:[&>div:first-child]:min-w-64 [&>div:last-child]:max-w-full [&>div:last-child]:min-w-0 [&>div:last-child]:shrink"
          mark="docker"
          fallback={Layers}
          title={
            <>
              Docker
              {engine?.serverVersion && (
                <span className="ml-2 font-mono text-body font-normal text-muted-foreground">
                  {engine.serverVersion}
                </span>
              )}
            </>
          }
          facts={
            images.data && (
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
            )
          }
          aside={
            <div className="flex max-w-full flex-wrap items-center gap-2">
              <span className="w-full text-body sm:mr-2 sm:w-auto">
                {outdated > 0 ? (
                  <button
                    type="button"
                    aria-label={`Only the ${plural(outdated, "image")} with an update`}
                    onClick={() => {
                      setState("outdated")
                      setUse("all")
                    }}
                    className="rounded-sm focus-ring hover:underline"
                  >
                    <Status verdict="warning" label={`${plural(outdated, "update")} available`} />
                  </button>
                ) : updates.data && checkedAt ? (
                  <Status verdict="ok" label="Tags in use are current" />
                ) : null}
              </span>
              {can("service.control") && (
                <>
                  <Button size="sm" onClick={() => setPullSeed("")}>
                    <Download className="size-4" />
                    Pull image
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => setBuilding(true)}>
                    <Wrench className="size-4" />
                    Build image
                  </Button>
                </>
              )}
              <WorkspaceHelp compact />
            </div>
          }
        />

        {images.loading && !images.data ? (
          <LoadingPanel />
        ) : images.error && !images.data ? (
          <ErrorState error={images.error} onRetry={images.refresh} />
        ) : (
          <>
            <ImageBand
              usage={usage.data}
              usageLoading={usage.loading}
              images={list}
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
                        <Button size="sm" onClick={() => setPullSeed("")}>
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
                    pulling={pull.ref}
                    verbs={rowVerbs}
                    onOpen={setSelected}
                  />
                )}
              </PanelBody>
            </Panel>
          </>
        )}

        <ImageSheet
          imageId={selected}
          state={selected ? states.get(selected) : undefined}
          update={selectedTag ? updates.data?.[selectedTag] : undefined}
          verbs={sheetVerbs}
          onOpenChange={(open) => !open && setSelected(null)}
        />
        <BuildDialog open={building} onOpenChange={setBuilding} onBuilt={refresh} />
        <PullDialog
          open={pullSeed !== null}
          initial={pullSeed ?? ""}
          pull={pull}
          onClose={() => setPullSeed(null)}
          onDone={() => {
            refresh()
            updates.refresh()
          }}
        />
        <TagDialog image={tagging} onClose={() => setTagging(null)} onTagged={refresh} />
      </Page>
    </Workspace>
  )
}
