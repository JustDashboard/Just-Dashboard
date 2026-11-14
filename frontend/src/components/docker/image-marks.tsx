"use client"

import { CheckCircle, Layers, Question } from "@/components/icons"
import {
  FRESHNESS_COLOR,
  FRESHNESS_LABEL,
  primaryTag,
  splitReference,
  type Freshness,
} from "@/components/docker/images"
import { ProductLogo, imageProduct } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import type { DockerImage } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The marks an image is drawn with wherever it appears: its row in the table,
 * its row in the rail beside another image's page, and its own page's line.
 */

/** The image as the product it is; a layer with no name keeps the tile with a glyph in it. */
export function ImageLogo({
  image,
  size = "sm",
  className,
}: {
  image: Pick<DockerImage, "repoTags">
  size?: "sm" | "md" | "lg"
  className?: string
}) {
  const tag = primaryTag(image)
  return (
    <ProductLogo
      id={tag ? imageProduct(tag) : undefined}
      fallback={Layers}
      size={size}
      className={className}
    />
  )
}

/**
 * A reference with its registry and its tag a step back from the repository,
 * because a column of paths that all begin `ghcr.io/` is read by the part
 * that differs.
 */
export function ReferenceWords({ reference }: { reference: string }) {
  const { registry, repository, tag, digest } = splitReference(reference)
  return (
    <>
      {registry && <span className="text-muted-foreground">{registry}/</span>}
      {repository}
      {tag && <span className="text-muted-foreground">:{tag}</span>}
      {digest && <span className="text-muted-foreground">@{digest.slice(0, 19)}</span>}
    </>
  )
}

/**
 * The registry's answer for one image as a state: a dot or a glyph in the
 * band's colours and the answer's own words. Green and amber are readings;
 * pinned and built here are choices somebody made, in their `--tag-*` hues;
 * no answer is the muted ink with a question mark.
 */
export function RegistryState({
  state,
  pulling,
  lower,
  className,
}: {
  state: Freshness
  /** Its tag is being pulled right now. */
  pulling?: boolean
  /** In a table's column the answer is a reading, in lower case; beside a title it is a state. */
  lower?: boolean
  className?: string
}) {
  const label = lower ? FRESHNESS_LABEL[state].toLowerCase() : FRESHNESS_LABEL[state]
  if (pulling) {
    return (
      <span className={cn("text-xs font-medium", className)}>
        <TextShimmer>Pulling…</TextShimmer>
      </span>
    )
  }
  if (state === "outdated") return <Status verdict="warning" label={label} className={className} />
  if (state === "current") {
    return <Status tone="running" icon={CheckCircle} label={label} className={className} />
  }
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap",
        className,
      )}
    >
      {state === "unknown" ? (
        <Question className="size-3.5 shrink-0 text-muted-foreground" />
      ) : (
        <span
          aria-hidden
          className="size-1.5 shrink-0 rounded-full"
          style={{ background: FRESHNESS_COLOR[state] }}
        />
      )}
      <span
        className={cn(
          (state === "unknown" || state === "unused" || state === "untagged") &&
            "text-muted-foreground",
        )}
      >
        {label}
      </span>
    </span>
  )
}
