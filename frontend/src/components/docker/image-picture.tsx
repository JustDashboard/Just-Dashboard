"use client"

import { Box, Globe, Layers } from "@/components/icons"
import { bytes, plural, relativeTime } from "@/lib/format"
import { hueFor, LANES } from "@/lib/hue"
import type { ImageDetail, ImageUpdateStatus } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { ReferenceWords } from "@/components/docker/image-marks"
import {
  FRESHNESS_COLOR,
  primaryTag,
  registryName,
  shortId,
  splitReference,
  type Freshness,
} from "@/components/docker/images"
import { ProductGlyph, hasProductLogo, hostProduct, imageProduct } from "@/components/product-logo"
import { StatusDot } from "@/components/status-dot"
import { Skeleton } from "@/components/ui/skeleton"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"

/** How many containers the picture names before the rest are one "N more". */
const NAMED = 2

/**
 * An image as what it stands between: where it comes from on the left — its
 * registry with that registry's answer, or this server for one built here —
 * the image in the middle, and the containers made from it on the right, each
 * a way to its page.
 *
 * The line from the registry is amber while a newer copy waits there and
 * carries a pulse only while a pull is bringing one down; a line to a
 * container moves while that container runs and is dashed when it has
 * stopped. The block keeps one height whatever the image has, and holds it
 * while loading, so the views under it do not move when the image lands.
 */
export function ImagePicture({
  detail,
  state,
  update,
  pulling,
  onContainers,
}: {
  /** Nothing until the image has been read: the picture waits at its final height. */
  detail?: ImageDetail
  state?: Freshness
  update?: ImageUpdateStatus
  pulling?: boolean
  /** Opens the view that lists every container, for the "N more" node. */
  onContainers: () => void
}) {
  const tag = detail ? primaryTag(detail) : undefined
  const product = tag ? imageProduct(tag) : undefined
  const sized = detail?.layers.filter((layer) => layer.size > 0).length ?? 0
  // Wide, `WireFlow` draws its own waiting shape. In the phone's rows it
  // waits with the hub's name alone, a line shorter than the name and the
  // size the image lands with, so there the waiting picture is drawn here:
  // the same marks and the same two lines, grey.
  const wide = useMediaQuery("(min-width: 1024px)")
  const waiting = !detail && !wide

  return (
    <WireFlow
      // Three rows of nodes and the hub's words under its mark, whichever is
      // taller: the height the picture has with the most it ever draws.
      className="lg:flex lg:min-h-60 lg:flex-col lg:justify-center"
      phone="marks"
      loading={!detail && wide}
      start={{
        title: "Comes from",
        loadingRows: 1,
        nodes: detail
          ? [origin(detail, tag, state ?? "unknown", update, pulling)]
          : waiting
            ? [WAITING("origin")]
            : [],
      }}
      hub={{
        mark: (size) => (
          <WireMark tone="logo" size={size}>
            {product && hasProductLogo(product) ? (
              <ProductGlyph id={product} />
            ) : (
              <Layers aria-hidden />
            )}
          </WireMark>
        ),
        eyebrow: "Image",
        title: detail ? (
          <span className="font-mono break-all">
            {tag ? <ReferenceWords reference={tag} /> : shortId(detail.id)}
          </span>
        ) : (
          <Words className="w-32" />
        ),
        hint: detail ? (
          <span className="numeric">
            {bytes(detail.size)} · {plural(sized, "layer")}
          </span>
        ) : (
          <Words className="w-24" />
        ),
      }}
      end={{
        title: "Containers",
        loadingRows: 1,
        max: NAMED,
        more: { onPress: onContainers, words: (hidden) => `${hidden} more` },
        nodes: detail
          ? detail.usedBy.length === 0
            ? [NOBODY]
            : detail.usedBy.map(user)
          : waiting
            ? [WAITING("user")]
            : [],
      }}
    />
  )
}

/** A grey bar where a line of words will be, the height of the line it stands in. */
function Words({ className }: { className?: string }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "inline-block h-[0.7em] max-w-full animate-pulse rounded-md bg-muted align-middle",
        className,
      )}
    />
  )
}

/** A mark that has not arrived, in the phone's rows. */
const WAITING = (id: string): WireFlowNode => ({
  id: `waiting:${id}`,
  placeholder: true,
  mark: <Skeleton className="size-9 rounded-xl" />,
  line: { state: "up" },
})

/**
 * Where a container would be, for an image nothing was made from: a dashed
 * ring rather than a sentence, so the picture keeps its shape, on a phone
 * too, where a column is a row of marks.
 */
const NOBODY: WireFlowNode = {
  id: "nobody",
  placeholder: true,
  name: "No container uses it",
  mark: (size) => (
    <WirePlaceholder size={size}>
      <Box aria-hidden />
    </WirePlaceholder>
  ),
  title: <span className="font-normal text-muted-foreground">No container uses it</span>,
  line: { state: "idle" },
}

/** A container made from the image: its stack, its name, its state, and the way to its page. */
function user(container: ImageDetail["usedBy"][number]): WireFlowNode {
  const running = container.state === "running"
  return {
    id: container.id,
    name: container.name,
    href: `/docker/containers/${container.id}`,
    label: `Open ${container.name}`,
    mark: (size) => (
      <span className="relative flex">
        <WireMark size={size}>
          <Box aria-hidden />
        </WireMark>
        <span
          aria-hidden
          className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
        >
          <StatusDot state={container.state} />
        </span>
      </span>
    ),
    eyebrow: container.stack ? (
      <span style={{ color: hueFor(container.stack, LANES) }}>{container.stack}</span>
    ) : (
      "Container"
    ),
    title: container.name,
    hint: container.state,
    line: running
      ? { state: "up", moving: true }
      : { state: container.state === "paused" ? "idle" : "down" },
  }
}

/** Where the image comes from, and what that place says about it. */
function origin(
  detail: ImageDetail,
  tag: string | undefined,
  state: Freshness,
  update: ImageUpdateStatus | undefined,
  pulling: boolean | undefined,
): WireFlowNode {
  if (!tag) {
    return {
      id: "origin",
      placeholder: true,
      name: "No name: its tag moved on",
      mark: (size) => (
        <WirePlaceholder size={size}>
          <Layers aria-hidden />
        </WirePlaceholder>
      ),
      eyebrow: "No name",
      title: <span className="font-normal text-muted-foreground">its tag moved on</span>,
      line: { state: "idle" },
    }
  }
  if (detail.localBuild) {
    return {
      id: "origin",
      name: "This server",
      mark: (size) => <WireHost size={size} />,
      eyebrow: "Built here",
      title: "This server",
      hint: "no registry copy",
      line: { state: "up" },
    }
  }
  const { registry } = splitReference(tag)
  const host = registry ? hostProduct(registry) : "docker"
  const line: WireLine = pulling
    ? { state: "up", moving: true }
    : state === "outdated"
      ? { state: "warning" }
      : state === "current" || state === "pinned"
        ? { state: "up" }
        : { state: "idle" }
  return {
    id: "origin",
    name: registryName(tag),
    mark: (size) => (
      <WireMark tone="logo" size={size}>
        {host && hasProductLogo(host) ? <ProductGlyph id={host} /> : <Globe aria-hidden />}
      </WireMark>
    ),
    eyebrow: "Registry",
    title: registryName(tag),
    // Two short lines: beside a rail the column is too narrow for both on one,
    // and a break in the middle of "asked 12m ago" read as two facts.
    hint: pulling ? (
      "pulling…"
    ) : (
      <span title={update?.reason}>
        <span
          className="block"
          style={{
            color: state === "outdated" || state === "current" ? FRESHNESS_COLOR[state] : undefined,
          }}
        >
          {ANSWER[state]}
        </span>
        {update?.checkedAt && state !== "unused" && (
          <span className="numeric block">asked {relativeTime(update.checkedAt)}</span>
        )}
      </span>
    ),
    line,
  }
}

/** The registry's answer as the few words a node's hint has room for. */
const ANSWER: Record<Freshness, string> = {
  outdated: "update available",
  current: "current",
  pinned: "pinned by digest",
  local: "built here",
  unknown: "not checked",
  unused: "not asked",
  untagged: "untagged",
}
