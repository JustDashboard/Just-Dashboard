"use client"

import { useEffect, useMemo } from "react"
import { Box, Database, NetworkDevice } from "@/components/icons"
import { useViewState } from "@/lib/view-state"
import type { ComposeStack, Container, DockerNetwork, VolumeDetail } from "@/lib/types"
import { cn } from "@/lib/utils"
import { WireMark } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo, imageProducts } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { WireFlow, type WireFlowNode, type WireState } from "@/components/wire-flow"
import {
  enginePicture,
  engineRelated,
  engineWords,
  type EngineNode,
} from "@/components/docker/engine-picture-model"

const EYEBROW: Record<EngineNode["kind"], string> = {
  stack: "Stack",
  container: "Container",
  network: "Network",
  volume: "Volume",
}

const DOT: Record<WireState, DotTone> = {
  danger: "danger",
  warning: "warning",
  up: "running",
  down: "stopped",
  idle: "unknown",
}

/**
 * The engine and what it holds, as one picture: the compose projects and the
 * containers started by hand on the left, Docker in the middle, and the
 * networks and volumes it keeps on the right (`WireFlow`).
 *
 * The line is the state, in the wiring vocabulary every picture speaks: it
 * moves where the thing at its end is running, it is red where one of a
 * project's containers is failing and amber where a project is partly up or
 * a volume has nothing mounting it (a prune would delete it), and it is
 * dashed where the thing is stopped or was never deployed. Pointing at a
 * project lights the networks and volumes that are its own.
 *
 * It replaced a table of every container, which `/docker/containers` draws:
 * the overview's question is what the engine is made of and whether any of
 * it is in trouble, and twelve rows of readings answered a different one.
 */
export function EnginePicture({
  containers,
  stacks,
  networks,
  volumes,
  oomKilled,
  version,
  loading,
}: {
  containers: Container[]
  stacks: ComposeStack[]
  /** Undefined while unread, and for an account the route refuses. */
  networks?: DockerNetwork[]
  volumes?: VolumeDetail[]
  /** The containers the kernel killed for memory, which the listing alone cannot tell from a stop. */
  oomKilled?: Set<string>
  version?: string
  /** Not everything is read yet: the picture is drawn at the size it last had. */
  loading: boolean
}) {
  const picture = useMemo(
    () => enginePicture({ containers, stacks, networks, volumes, oomKilled }),
    [containers, stacks, networks, volumes, oomKilled],
  )
  const related = useMemo(() => engineRelated([...picture.start, ...picture.end]), [picture])

  // The rows each side had on the last visit, so the picture waits at the
  // height it is about to have and nothing under it moves when it lands.
  const [rows, rememberRows] = useViewState("docker.overview.picture.rows", { start: 4, end: 4 })
  const start = picture.start.length + (picture.more.start.length > 0 ? 1 : 0)
  const end =
    picture.end.length +
    (picture.more.networks.length > 0 ? 1 : 0) +
    (picture.more.volumes.length > 0 ? 1 : 0)
  useEffect(() => {
    if (!loading && (rows.start !== start || rows.end !== end)) rememberRows({ start, end })
  }, [loading, rows.start, rows.end, start, end, rememberRows])

  const networksShown = picture.end.filter((node) => node.kind === "network")
  const volumesShown = picture.end.filter((node) => node.kind === "volume")

  return (
    <WireFlow
      className="animate-rise"
      loading={loading}
      related={related}
      start={{
        title: "Stacks and containers",
        heading: "always",
        count: picture.totals.stacks + picture.totals.standalone,
        loadingRows: rows.start,
        nodes: [
          ...picture.start.map(node),
          ...(picture.more.start.length > 0
            ? [more("start", picture.more.start, "more", "/docker/containers")]
            : []),
        ],
      }}
      hub={{
        mark: (
          <WireMark tone="logo" size="lg">
            <ProductGlyph id="docker" />
          </WireMark>
        ),
        eyebrow: "Engine",
        title: version ? `Docker ${version}` : "Docker",
        hint: <span className="numeric">{engineWords(containers)}</span>,
        className: "lg:pb-16",
      }}
      end={{
        title: "Networks and volumes",
        heading: "always",
        count: picture.totals.networks + picture.totals.volumes,
        loadingRows: rows.end,
        empty: "No networks or volumes to read",
        nodes: [
          ...networksShown.map(node),
          ...(picture.more.networks.length > 0
            ? [more("networks", picture.more.networks, "more networks", "/docker/networks")]
            : []),
          ...volumesShown.map(node),
          ...(picture.more.volumes.length > 0
            ? [more("volumes", picture.more.volumes, "more volumes", "/docker/volumes")]
            : []),
        ],
      }}
    />
  )
}

function node(item: EngineNode): WireFlowNode {
  const off = item.state === "down" || item.state === "idle"
  return {
    id: item.id,
    name: `${item.prefix ?? ""}${item.name}`,
    label: `Open ${item.prefix ?? ""}${item.name}`,
    href: item.href,
    mark: (size) => (
      <span className="relative flex">
        <WireMark
          tone={item.state === "danger" ? "danger" : item.state === "warning" ? "warning" : "logo"}
          size={size}
        >
          <Glyph item={item} className={off ? "opacity-60 grayscale" : undefined} />
        </WireMark>
        {(item.kind === "stack" || item.kind === "container") && (
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={DOT[item.state]} live={item.state === "up"} />
          </span>
        )}
      </span>
    ),
    eyebrow: EYEBROW[item.kind],
    title: (
      <span
        className={cn(
          "block truncate",
          (item.kind === "network" || item.kind === "volume") && "font-mono",
        )}
      >
        {item.prefix && <span className="text-muted-foreground">{item.prefix}</span>}
        {item.name}
      </span>
    ),
    hint: (
      <span
        className={cn(
          "numeric block truncate",
          item.state === "danger" && "text-destructive",
          item.state === "warning" && "text-warning",
        )}
      >
        {item.words}
      </span>
    ),
    line: {
      state: item.state,
      // A line moves only where both of its ends are up: the engine is, or
      // there would be no picture.
      moving: item.state === "up" && (item.kind === "stack" || item.kind === "container"),
    },
  }
}

/** The thing as the product it runs, or the glyph of its kind. */
function Glyph({ item, className }: { item: EngineNode; className?: string }) {
  if (item.kind === "network") return <NetworkDevice aria-hidden className={className} />
  if (item.kind === "volume") return <Database aria-hidden className={className} />
  const product = imageProducts(item.images).find((id) => id !== "docker" && hasProductLogo(id))
  if (product) return <ProductGlyph id={product} className={className} />
  if (item.kind === "stack") return <ProductGlyph id="docker-compose" className={className} />
  return <Box aria-hidden className={className} />
}

/**
 * What a side had no room for, as a count that opens the page listing all of
 * it, with their names under it. It has a node's three lines, so the row is
 * the height of the ones above it and of the row that waited in its place.
 */
function more(side: string, names: string[], words: string, href: string): WireFlowNode {
  const said = `${names.length} ${words}`
  return {
    id: `more:${side}`,
    name: said,
    href,
    placeholder: true,
    mark: (size) => (
      <span className="flex shrink-0 rounded-full bg-background">
        <span
          className={cn(
            "numeric flex items-center justify-center rounded-full border border-hairline text-hint text-muted-foreground",
            size === "sm" ? "size-9" : "size-11",
          )}
        >
          +{names.length}
        </span>
      </span>
    ),
    eyebrow: "Not drawn",
    title: <span className="font-normal text-muted-foreground">{said}</span>,
    hint: <span className="block truncate">{names.join(", ")}</span>,
    line: { state: "idle" },
  }
}
