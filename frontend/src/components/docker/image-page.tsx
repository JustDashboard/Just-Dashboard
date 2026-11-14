"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Tabs as TabsPrimitive } from "radix-ui"
import { ArrowLeft, ChevronDown, ChevronUp, Copy, Layers } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { DockerImage, ImageDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { useImageActions } from "@/components/docker/image-actions"
import { ImageLogo, ReferenceWords, RegistryState } from "@/components/docker/image-marks"
import { ImagePicture } from "@/components/docker/image-picture"
import { ImageRail, useImageRail } from "@/components/docker/image-rail"
import {
  ImageConfiguration,
  ImageContainers,
  ImageHistory,
  ImageLayers,
} from "@/components/docker/image-views"
import {
  imageFreshness,
  imageHref,
  imageRequest,
  imageTab,
  primaryTag,
  shortId,
  type ImageTab,
} from "@/components/docker/images"
import { IconAction, rowReveal } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, tabClasses } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

const BACK = (
  <Link
    href="/docker/images"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Images
  </Link>
)

/**
 * One image, as a place of its own.
 *
 * It was a `SidePanel` over the image table until 0.8.7: four readings, the
 * reference, who runs it, a bar per layer, how it runs, its labels and its
 * environment in one long drawer, over a table nobody could read behind it.
 * An image is a destination now, the way a container is
 * (`docker/container-detail`): its identity line with its verbs, a picture of
 * where it comes from and who runs it, and its views as the underlined strip
 * with the open one in the address.
 *
 * What the sheet did well, the table beside the thing being read, is the
 * column on the left from `xl` (`ImageRail`): the table's own order, filters
 * and search folded into a list, so the next image is one press, Alt+↑ and
 * Alt+↓ walk it, and the view on screen goes with you. On a phone the page
 * is the one pane, with the way back and the same two arrows.
 *
 * The address names the image by its id (`images.ts`, `imageKey`). A
 * reference in its place, escaped into one segment, is resolved and the
 * address then replaced by the image's own, so one image has one address.
 */
export function ImagePage() {
  const params = useParams<{ id: string }>()
  const search = useSearchParams()
  const router = useRouter()
  const request = imageRequest(params.id ?? "")
  const requested = search.get("tab")
  const [remembered, remember] = useViewState<string>("docker.image.tab", "layers")
  const [chosen, setChosen] = useState<string>(requested ?? remembered)
  const tab = imageTab(chosen)
  const setTab = useCallback(
    (next: string) => {
      setChosen(next)
      remember(next)
      writeTab(imageTab(next))
    },
    [remember],
  )
  // A page opened on the view it was last left on says so in its address too.
  useEffect(() => {
    if (!requested && tab !== "layers") writeTab(tab)
  }, [requested, tab])

  const rail = useImageRail()
  const reading = usePoll<ImageDetail>(
    (signal) =>
      get<ImageDetail>(`/docker/images/${encodeURIComponent(request)}`, undefined, signal),
    30_000,
    [request],
  )
  const detail = reading.data
  const missing = reading.error instanceof ApiError && reading.error.status === 404

  // One image, one address: a reference that resolved is replaced in the
  // address bar by the image's own. Written to the history rather than
  // navigated to, because a navigation mounts the page again and the image
  // just read would blink back to its skeleton.
  useEffect(() => {
    if (detail && detail.id !== request) {
      window.history.replaceState(null, "", imageHref(detail.id, tab))
    }
  }, [detail, request, tab])

  const current = detail?.id ?? (request.startsWith("sha256:") ? request : undefined)
  // What the table's list already says about this image: its name, size, age
  // and users. The line the page opens on is drawn from it at once, so a walk
  // down the rail changes the name with the press, not when the image lands.
  const known = detail
    ? { ...detail, containers: detail.usedBy.length }
    : rail.images.find((image) => image.id === current)
  const open = useCallback((id: string) => router.push(imageHref(id, tab)), [router, tab])
  const at = rail.rows.findIndex((row) => row.id === current)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? rail.rows[at + direction] : rail.rows[direction > 0 ? 0 : -1]
    if (next) open(next.id)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: rail.rows.length === 0 || at >= rail.rows.length - 1,
    run: () => adjacent(1),
  }

  const refreshReading = reading.refresh
  const refreshRail = rail.refresh
  const refresh = useCallback(() => {
    refreshReading()
    refreshRail()
  }, [refreshReading, refreshRail])

  return (
    <Workspace
      name="Image"
      stateKey="image.page"
      refresh={refresh}
      commands={[
        {
          id: "previous",
          label: "Previous image",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next image",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while the picture and a view scroll;
          narrower, the page is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <ImageRail
            className="hidden w-72 shrink-0 xl:flex"
            rail={rail}
            current={current}
            currentRow={known}
            tab={tab}
          />
          <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
            <PageContext
              eyebrow={BACK}
              title={known ? (primaryTag(known) ?? `Image ${shortId(known.id)}`) : "Image"}
              actions={
                <span className="flex items-center">
                  <IconAction
                    label="Previous image"
                    disabled={previous.disabled}
                    onClick={previous.run}
                  >
                    <ChevronUp />
                  </IconAction>
                  <IconAction label="Next image" disabled={next.disabled} onClick={next.run}>
                    <ChevronDown />
                  </IconAction>
                  <span className="max-sm:hidden">
                    <WorkspaceHelp compact />
                  </span>
                </span>
              }
            />
            {missing ? (
              <EmptyState
                icon={Layers}
                title="That image is not on this server"
                description="It may have been removed, or the address names one that was never here."
                action={
                  <Button size="sm" variant="outline" asChild>
                    <Link href="/docker/images">Back to the images</Link>
                  </Button>
                }
              />
            ) : (
              <ImageMain
                detail={detail}
                known={known}
                error={detail ? undefined : reading.error}
                onRetry={reading.refresh}
                rail={rail}
                tab={tab}
                setTab={setTab}
                onChanged={refresh}
              />
            )}
          </div>
        </div>
      </Page>
    </Workspace>
  )
}

/**
 * The address says which view, so a link to an image's history opens on its
 * history; replaced rather than pushed, because the view is not a place Back
 * should step through. Layers is the page's own view and is not said.
 */
function writeTab(tab: ImageTab) {
  const url = new URL(window.location.href)
  if (tab === "layers") url.searchParams.delete("tab")
  else url.searchParams.set("tab", tab)
  window.history.replaceState(null, "", `${url.pathname}${url.search}`)
}

const VIEWS: { key: ImageTab; label: string }[] = [
  { key: "layers", label: "Layers" },
  { key: "containers", label: "Containers" },
  { key: "history", label: "History" },
  { key: "configuration", label: "Configuration" },
]

function ImageMain({
  detail,
  known,
  error,
  onRetry,
  rail,
  tab,
  setTab,
  onChanged,
}: {
  /** Nothing until the image has been read: the picture and the views wait in their final shape. */
  detail?: ImageDetail
  /** The image as the list or its own reading has it: enough for the line the page opens on. */
  known?: DockerImage
  /** The image could not be read. The line stays if the list knows it; the rest says why. */
  error?: Error
  onRetry: () => void
  rail: ReturnType<typeof useImageRail>
  tab: ImageTab
  setTab: (tab: string) => void
  onChanged: () => void
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 640px)")
  const tag = known ? primaryTag(known) : undefined
  const name = known ? (tag ?? `untagged ${shortId(known.id)}`) : "the image"

  // Pulling a tag moves it to the new copy, and the image on screen is then
  // the old one under no name. The reader pulled a name, so the page follows
  // the name to wherever it points now.
  const following = useRef(false)
  const follow = async () => {
    if (!known || !tag || following.current) return
    following.current = true
    try {
      const now = await get<ImageDetail>(`/docker/images/${encodeURIComponent(tag)}`)
      if (now.id !== known.id) router.replace(imageHref(now.id, tab))
    } catch {
      // The name no longer resolves: the page keeps the image it has.
    } finally {
      following.current = false
    }
  }
  const actions = useImageActions({
    confirm,
    onChanged,
    onPulled: () => void follow(),
    onRemoved: () => {
      // The table reads the same list: asked again now, it arrives without
      // the image that has just gone.
      rail.refresh()
      router.replace("/docker/images")
    },
  })

  const state = known ? imageFreshness(known, rail.updates) : undefined
  const update = known
    ? [...known.repoTags, ...known.repoDigests]
        .map((reference) => rail.updates?.[reference])
        .find(Boolean)
    : undefined
  const pulling = tag !== undefined && actions.pulling === tag

  const verbs = known ? actions.verbsFor({ ...known, inUse: known.containers > 0 }) : []
  // One command, brand-faced: Pull where a registry can serve the image
  // again, a second name where none can. The rest are words in the menu,
  // Remove last.
  const command = verbs.find((verb) => verb.key === "pull") ?? verbs.find((v) => v.key === "tag")
  const rest = verbs
    .filter((verb) => verb !== command)
    .map((verb) => ({ ...verb, inline: false }))
    .sort((a, b) => Number(Boolean(a.danger)) - Number(Boolean(b.danger)))
  const remove = verbs.find((verb) => verb.key === "remove")

  const steps = detail?.layers.length ?? 0
  const sized = detail?.layers.filter((layer) => layer.size > 0).length ?? 0
  const counts: Partial<Record<ImageTab, number>> = {
    layers: sized,
    containers: detail?.usedBy.length,
    history: steps,
  }

  if (error && !known) return <ErrorState error={error} onRetry={onRetry} />

  return (
    <>
      <HostIdentity
        slot="image-identity"
        label={known ? `About ${name}` : undefined}
        className="shrink-0 animate-rise"
        logo={
          <ImageLogo
            image={known ?? { repoTags: [] }}
            size="md"
            className="size-10 rounded-lg [&_img]:size-6"
          />
        }
        title={
          !known ? (
            <TextShimmer>Reading the image…</TextShimmer>
          ) : tag ? (
            <span className="min-w-0 truncate font-mono" title={known.repoTags.join(", ")}>
              <ReferenceWords reference={tag} />
            </span>
          ) : (
            // A layer with no name is called by the one handle it has.
            <span className="min-w-0 truncate font-mono">{shortId(known.id)}</span>
          )
        }
        state={
          state ? (
            <RegistryState state={state} pulling={pulling} />
          ) : (
            // Holds the state's line open on a phone, where it has a row of its own.
            <span aria-hidden className="text-xs">
              &nbsp;
            </span>
          )
        }
        facts={
          known ? (
            <>
              <span className="numeric font-medium text-foreground">{bytes(known.size)}</span>
              <FactDot />
              <span title={timestamp(known.created)}>built {relativeTime(known.created)}</span>
              {/* An image nothing runs says so as its state, beside its name. */}
              {known.containers > 0 ? (
                <>
                  <FactDot />
                  <button
                    type="button"
                    onClick={() => setTab("containers")}
                    className="numeric rounded-sm focus-ring hover:text-foreground hover:underline"
                  >
                    used by {plural(known.containers, "container")}
                  </button>
                </>
              ) : (
                state !== "unused" && (
                  <>
                    <FactDot />
                    <span>not in use</span>
                  </>
                )
              )}
              {/* The handle to quote, where the name is not already it. On a
                  phone the line has room for the three readings; the id is
                  whole under Configuration. */}
              {tag && (
                <span className="contents max-sm:hidden">
                  <FactDot />
                  <button
                    type="button"
                    onClick={() => void copyText(known.id, "ID copied")}
                    className="group inline-flex items-center gap-1 rounded-sm font-mono focus-ring hover:text-foreground"
                    aria-label={`Copy image id ${shortId(known.id)}`}
                  >
                    {shortId(known.id)}
                    <Copy aria-hidden className={cn("size-3", rowReveal())} />
                  </button>
                </span>
              )}
            </>
          ) : (
            <span aria-hidden>&nbsp;</span>
          )
        }
        command={
          command && (
            <Button size="sm" onClick={command.run} disabled={pulling}>
              <command.icon className="size-4" />
              {command.label}
            </Button>
          )
        }
        menu={rest.length > 0 && <VerbMenu verbs={rest} label={`More actions for ${name}`} />}
      />

      {/* The picture and the open view scroll together under the line. */}
      {error ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : (
        <div className="flex min-w-0 flex-col xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
          <ImagePicture
            detail={detail}
            state={state}
            update={update}
            pulling={pulling}
            onContainers={() => setTab("containers")}
          />
          <Tabs value={tab} onValueChange={setTab} className="gap-0">
            <TabsPrimitive.List
              aria-label={`Views of ${name}`}
              className="z-20 flex shrink-0 [scrollbar-width:none] gap-1 overflow-x-auto border-b border-hairline bg-background xl:sticky xl:top-0 [&::-webkit-scrollbar]:hidden"
            >
              {VIEWS.map((view) => (
                <TabsPrimitive.Trigger
                  key={view.key}
                  value={view.key}
                  className={tabClasses(tab === view.key, "h-10")}
                >
                  {view.label}
                  {(counts[view.key] ?? 0) > 0 && <ChipCount>{counts[view.key]}</ChipCount>}
                </TabsPrimitive.Trigger>
              ))}
            </TabsPrimitive.List>
            {VIEWS.map((view) => (
              <TabsContent key={view.key} value={view.key} className="pt-6 pb-4">
                {tab !== view.key ? null : !detail ? (
                  <LoadingRows rows={5} />
                ) : view.key === "layers" ? (
                  <ImageLayers detail={detail} />
                ) : view.key === "containers" ? (
                  <ImageContainers
                    detail={detail}
                    onRemove={remove && !remove.disabled ? remove.run : undefined}
                  />
                ) : view.key === "history" ? (
                  <ImageHistory detail={detail} wide={wide} />
                ) : (
                  <ImageConfiguration detail={detail} />
                )}
              </TabsContent>
            ))}
          </Tabs>
        </div>
      )}
      {dialog}
      {actions.dialogs}
    </>
  )
}
