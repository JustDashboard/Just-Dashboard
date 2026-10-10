"use client"

import Link from "next/link"
import { CheckCircle, Layers, Question } from "@/components/icons"
import { RowLink } from "@/components/page"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import {
  FRESHNESS_COLOR,
  FRESHNESS_LABEL,
  imageVersion,
  primaryTag,
  registryName,
  shortId,
  splitReference,
  type Freshness,
} from "@/components/docker/images"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, plural, relativeTime, timestamp } from "@/lib/format"
import type { Container, DockerImage, ImageUpdateStatus } from "@/lib/types"
import { cn } from "@/lib/utils"

/** The Images line's hue on the disk bar, so a row's size reads as its share of that span. */
const SIZE_HUE = "var(--chart-1)"

/** How many containers a row names before it counts the rest. */
const NAMED = 3

/**
 * The images as a table: each row a reading of one image — what it is, who
 * runs it, what its registry says, how old and how large — that opens the
 * image's sheet.
 *
 * It was a column of cards, on §12's argument that a row you open is a place
 * to go. The operator asked for the table back, and the Processes, Packages and
 * Services tables are the precedent for it: a row of readings that also opens
 * a sheet is still read down its columns, and "which of these is largest,
 * oldest, unused or behind" is a question asked of a column. Fixed columns,
 * so a long registry path ellipses rather than pushing Size off the panel.
 *
 * Wide, every reading has its column. Below `xl` the containers and the
 * registry's answer go to the name's second line and the row keeps its size
 * and its menu — chosen once by the page rather than drawn twice and hidden.
 */
export function ImageTable({
  images,
  users,
  updates,
  states,
  wide,
  arrived,
  pulling,
  verbs,
  onOpen,
}: {
  images: DockerImage[]
  users: Map<string, Container[]>
  updates: Record<string, ImageUpdateStatus> | undefined
  states: Map<string, Freshness>
  wide: boolean
  arrived: Set<string>
  /** The reference a pull is moving right now, so its row can say so. */
  pulling?: string
  verbs: (image: DockerImage) => Verb[]
  onOpen: (id: string) => void
}) {
  const largest = Math.max(...images.map((i) => i.size), 1)
  return (
    <Table className="table-fixed">
      <TableHeader>
        <TableRow>
          <TableHead>Image</TableHead>
          {wide && <TableHead className="w-[21%]">Containers</TableHead>}
          {wide && <TableHead className="w-40">Registry</TableHead>}
          {wide && <TableHead className="w-28">Created</TableHead>}
          <TableHead className={cn("text-right", wide ? "w-28" : "w-20")}>Size</TableHead>
          <TableHead className={wide ? "w-28" : "w-12"}>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {images.map((image) => {
          const tag = primaryTag(image)
          const state = states.get(image.id) ?? "unknown"
          const running = users.get(image.id) ?? []
          return (
            <TableRow
              key={image.id}
              data-workspace-item={image.id}
              data-workspace-name={tag ?? shortId(image.id)}
              onActivate={() => onOpen(image.id)}
              className={cn("group", arrived.has(image.id) && "animate-rise")}
            >
              <TableCell>
                <ImageName image={image} onOpen={() => onOpen(image.id)} />
                <p className="mt-1 flex min-w-0 items-center gap-1.5 truncate pl-11 text-hint text-muted-foreground">
                  {wide ? (
                    <ImageFacts image={image} />
                  ) : (
                    <>
                      {/* Narrow: who runs it is a dot per state and a count, so
                          the registry's word beside it keeps its room. */}
                      {image.containers > 0 ? (
                        <span
                          className="numeric inline-flex shrink-0 items-center gap-1"
                          title={running.map((c) => c.name).join(", ")}
                        >
                          <StatusDot
                            state={
                              running.some((c) => c.state === "running") ? "running" : "exited"
                            }
                          />
                          {image.containers}
                        </span>
                      ) : (
                        <span className="truncate">
                          {image.dangling ? shortId(image.id) : "not in use"}
                        </span>
                      )}
                      {state !== "unused" && state !== "untagged" && (
                        <>
                          <span className="text-muted-foreground/40">·</span>
                          <span className="min-w-0 truncate">
                            <FreshnessWord state={state} pulling={pulling === tag} />
                          </span>
                        </>
                      )}
                    </>
                  )}
                </p>
              </TableCell>
              {wide && (
                <TableCell>
                  <Users image={image} containers={running} />
                </TableCell>
              )}
              {wide && (
                <TableCell>
                  <RegistryAnswer
                    state={state}
                    update={tag ? updates?.[tag] : undefined}
                    pulling={tag !== undefined && pulling === tag}
                  />
                </TableCell>
              )}
              {wide && (
                <TableCell className="text-muted-foreground" title={timestamp(image.created)}>
                  {relativeTime(image.created)}
                </TableCell>
              )}
              <TableCell className="numeric text-right">
                <span className="inline-flex w-full flex-col items-end gap-1.5">
                  <span className={cn(image.dangling && "text-muted-foreground")}>
                    {bytes(image.size)}
                  </span>
                  <span
                    aria-hidden
                    className="h-0.5 w-14 overflow-hidden rounded-full bg-meter-track"
                  >
                    <span
                      className="block h-full"
                      style={{ width: `${(image.size / largest) * 100}%`, background: SIZE_HUE }}
                    />
                  </span>
                </span>
              </TableCell>
              <TableCell className="px-2">
                <VerbActions
                  dim
                  className="justify-end"
                  verbs={wide ? verbs(image) : verbs(image).map((v) => ({ ...v, inline: false }))}
                  menuLabel={`More actions for ${tag ?? shortId(image.id)}`}
                />
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

/**
 * The image as the product it is, then its reference — the registry and the
 * tag a step back from the repository, because a column of paths that all
 * begin `ghcr.io/` is read by the part that differs.
 */
function ImageName({ image, onOpen }: { image: DockerImage; onOpen: () => void }) {
  const tag = primaryTag(image)
  if (!tag) {
    return (
      <div className="flex min-w-0 items-center gap-3">
        <ProductLogo fallback={Layers} size="sm" />
        <RowLink onClick={onOpen} className="truncate font-normal text-muted-foreground italic">
          untagged
        </RowLink>
        <Tag className="shrink-0">dangling</Tag>
      </div>
    )
  }
  const { registry, repository, tag: version } = splitReference(tag)
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo id={imageProduct(tag)} fallback={Layers} size="sm" />
      <RowLink mono onClick={onOpen} title={image.repoTags.join(", ")} className="text-body">
        {registry && <span className="text-muted-foreground">{registry}/</span>}
        {repository}
        {version && <span className="text-muted-foreground">:{version}</span>}
      </RowLink>
    </div>
  )
}

/** The id, where it comes from, the release the publisher stamped, and any other names. */
function ImageFacts({ image }: { image: DockerImage }) {
  const tag = primaryTag(image)
  const version = imageVersion(image)
  const others = image.repoTags.filter((t) => t !== tag && t !== "<none>:<none>").length
  return (
    <>
      <span className="shrink-0 font-mono">{shortId(image.id)}</span>
      {tag && (
        <>
          <span className="text-muted-foreground/40">·</span>
          <span className="truncate">
            {image.repoDigests.length > 0 ? registryName(tag) : "built here"}
          </span>
        </>
      )}
      {version && !tag?.endsWith(`:${version}`) && (
        <>
          <span className="text-muted-foreground/40">·</span>
          <span className="shrink-0 font-mono">v{version.replace(/^v/, "")}</span>
        </>
      )}
      {others > 0 && (
        <>
          <span className="text-muted-foreground/40">·</span>
          <span className="shrink-0">+{plural(others, "tag")}</span>
        </>
      )}
    </>
  )
}

/**
 * Who runs the image, each by name with its state's dot, opening its page. A
 * count alone said an image was in use and not by what, which is the next
 * question before removing or pulling anything.
 */
function Users({ image, containers }: { image: DockerImage; containers: Container[] }) {
  if (containers.length === 0) {
    return (
      <span className="text-muted-foreground">
        {image.dangling
          ? "—"
          : image.containers > 0
            ? plural(image.containers, "container")
            : "not in use"}
      </span>
    )
  }
  const shown = containers.slice(0, NAMED)
  return (
    <span className="flex min-w-0 items-center gap-x-3 overflow-hidden">
      {shown.map((c) => (
        <Link
          key={c.id}
          href={`/docker/containers/${c.id}`}
          title={`${c.name} — ${c.status}`}
          className="inline-flex min-w-0 shrink items-center gap-1.5 rounded-sm focus-ring hover:underline"
        >
          <StatusDot state={c.state} />
          <span className="truncate">{c.name}</span>
        </Link>
      ))}
      {containers.length > NAMED && (
        <span className="numeric shrink-0 text-muted-foreground">+{containers.length - NAMED}</span>
      )}
    </span>
  )
}

/** The registry's word for one image, coloured by what it means. */
function FreshnessWord({ state, pulling }: { state: Freshness; pulling?: boolean }) {
  if (pulling) return <TextShimmer>Pulling…</TextShimmer>
  return (
    <span
      className="shrink-0"
      style={{ color: state === "unknown" ? undefined : FRESHNESS_COLOR[state] }}
    >
      {FRESHNESS_LABEL[state].toLowerCase()}
    </span>
  )
}

/**
 * The registry column: one answer per image, in the band's colours, with the
 * reason a question went unanswered one hover away — "built here" and "not
 * checked" both mean no answer, and the difference between them is the point.
 */
function RegistryAnswer({
  state,
  update,
  pulling,
}: {
  state: Freshness
  update?: ImageUpdateStatus
  pulling: boolean
}) {
  if (pulling) {
    return (
      <span className="text-xs">
        <TextShimmer>Pulling…</TextShimmer>
      </span>
    )
  }
  if (state === "untagged") return <span className="text-muted-foreground">—</span>
  if (state === "unused") return <span className="text-muted-foreground">not checked</span>
  if (state === "outdated") return <Status verdict="warning" label="update available" />
  if (state === "current") {
    return <Status tone="running" icon={CheckCircle} label="current" />
  }
  const word = (
    <span className="inline-flex cursor-help items-center gap-1.5 text-xs font-medium">
      {state === "unknown" ? (
        <Question className="size-3.5 text-muted-foreground" />
      ) : (
        <span
          aria-hidden
          className="size-1.5 rounded-full"
          style={{ background: FRESHNESS_COLOR[state] }}
        />
      )}
      <span className={state === "unknown" ? "text-muted-foreground" : undefined}>
        {state === "pinned" ? "pinned" : state === "local" ? "built here" : "not checked"}
      </span>
    </span>
  )
  if (!update?.reason) return word
  return (
    <Tooltip>
      <TooltipTrigger asChild>{word}</TooltipTrigger>
      <TooltipContent className="max-w-xs">{update.reason}</TooltipContent>
    </Tooltip>
  )
}
