"use client"

import { useEffect, useRef } from "react"
import {
  Archive,
  Box,
  Check,
  Gauge,
  Globe,
  Heart,
  Key,
  ShieldCheck,
  Wrench,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import type { Icon } from "@/components/icons"
import { ProductGlyph, ProductLogo, hasProductLogo } from "@/components/product-logo"
import {
  SourceMark,
  StepMark,
  WORKLOAD_LABELS,
  WorkloadMark,
  frameworkLabel,
  projectProduct,
  sourceProduct,
  type ReleaseNodeState,
} from "@/components/deploy/vocabulary"
import { BranchChip, ShortSha } from "@/components/git/marks"
import { CODE, ShellWords } from "@/components/deploy/run-evidence"
import {
  CONFIGURE_STEPS,
  type ConfigureFlow,
  type ConfigureStepKey,
} from "@/components/deploy/new-project/draft"
import type {
  PlanGlyph,
  PlanGroup,
  PlanRow,
  PlanTone,
} from "@/components/deploy/new-project/plan-reading"

/** The glyph a row is drawn on when no product names what it is. */
const GLYPH: Record<Exclude<PlanGlyph, "source">, Icon> = {
  build: Wrench,
  container: Box,
  address: Globe,
  checks: Heart,
  limits: Gauge,
  storage: Archive,
  variables: Key,
  preflight: ShieldCheck,
}

/** How a row stands, in the marks the run page's rail gives a step. */
const MARK: Partial<Record<PlanTone, ReleaseNodeState>> = {
  danger: "failed",
  warning: "warning",
  passed: "passed",
}

/**
 * The rail's line from one row's tile to the next: lit through the steps
 * behind the reader, the brand through the one they are on, a hairline
 * through what is left — the spine over the page, read down.
 */
const LINE = { done: "bg-flow-lit/60", here: "bg-brand/50", ahead: "bg-hairline" } as const

type Position = keyof typeof LINE

/**
 * What this setup will create, read down: the project it becomes, then each of
 * the four steps holding the parts of the plan it decides.
 *
 * It replaces the drawing that stood beside the form — source, build, runtime,
 * address, four nodes on beams — in the shape the run page's Details settled
 * on: a rail whose stages sit under their segment of the spine, each row on
 * the tile of the product it is (the forge, the framework, the runtime, Let's
 * Encrypt for a name served over HTTPS), with a line from tile to tile in how
 * far the sequence has got. The drawing could carry four things; a health
 * check, the limits, the storage, the environment and the server's own check
 * were hints under somebody else's node or nowhere. Each has a row now, and a
 * row that wants a look before Deploy says so in its tile's corner.
 *
 * Every row is pressable and opens the fields that decide it, which keeps the
 * one job the drawing had: it is the form's table of contents.
 */
export function PlanRail({
  flow,
  groups,
  current,
  onOpen,
}: {
  flow: ConfigureFlow
  groups: PlanGroup[]
  current: ConfigureStepKey
  onOpen: (row: PlanRow) => void
}) {
  // Past the last group once the project exists, so every step reads as done.
  const here = current === "done" ? CONFIGURE_STEPS.length : CONFIGURE_STEPS.indexOf(current)
  // The step being worked on stays in the rail's view: on a short window the
  // Variables and Review groups sit past its fold. Scrolled inside the rail's
  // own column only — `scrollIntoView` would move the page under the reader.
  const list = useRef<HTMLOListElement>(null)
  useEffect(() => {
    const group = list.current?.querySelector<HTMLElement>('[aria-current="step"]')
    const column = list.current?.closest<HTMLElement>("[data-slot='plan-rail-column']")
    if (!group || !column || column.scrollHeight <= column.clientHeight) return
    const box = column.getBoundingClientRect()
    const at = group.getBoundingClientRect()
    if (at.top < box.top || at.bottom > box.bottom)
      column.scrollTop += at.top - box.top - Math.max(0, (box.height - at.height) / 2)
  }, [current])
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <PlanIdentity flow={flow} />
      <ol ref={list} aria-label="What this setup will create" className="min-w-0">
        {groups.map((group) => {
          const at = CONFIGURE_STEPS.indexOf(group.step)
          const position: Position = at < here ? "done" : at === here ? "here" : "ahead"
          return (
            <li
              key={group.step}
              aria-current={position === "here" ? "step" : undefined}
              className="min-w-0"
            >
              <GroupHead label={group.label} position={position} />
              <ol aria-label={group.label} className="min-w-0">
                {group.rows.map((row, index) => (
                  <PlanRowItem
                    key={row.key}
                    row={row}
                    source={flow.source}
                    position={position}
                    first={index === 0}
                    last={index === group.rows.length - 1}
                    onOpen={() => onOpen(row)}
                  />
                ))}
              </ol>
            </li>
          )
        })}
      </ol>
    </div>
  )
}

/**
 * The project this setup becomes, in the shape its own pages will open on: its
 * tile, its name as it is being typed, and what kind of thing it is.
 */
function PlanIdentity({ flow }: { flow: ConfigureFlow }) {
  const { source, configuration, candidate } = flow
  const framework = candidate?.framework ?? configuration.build.framework
  const product = projectProduct(
    {
      sourceKind: source.kind,
      sourceRef: source.blueprintId ?? source.ref ?? "",
      sourceRepository: source.image ?? source.repository ?? "",
      buildMethod: configuration.build.method,
      recipe: configuration.build.recipe,
      framework,
      images: [],
    },
    undefined,
  )
  const forge =
    source.kind === "git" || source.kind === "local"
      ? sourceProduct(
          {
            sourceKind: source.kind,
            sourceRef: source.ref ?? "",
            sourceRepository: source.repository ?? "",
            sourceRemote: source.url ?? "",
          },
          source,
        )
      : undefined
  return (
    <div
      data-slot="plan-identity"
      className="flex min-w-0 items-center gap-3 border-b border-hairline px-2 pb-4"
    >
      {/* A repository's project carries its forge in the tile's corner, the
          way `ProjectMark` does once it has a favicon: the framework is what
          it is, the forge is where it comes from. */}
      <span aria-hidden className="relative flex shrink-0">
        <WorkloadMark profile={flow.profile} product={product} />
        {hasProductLogo(forge) && forge !== product && (
          <span className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background">
            <ProductGlyph id={forge} className="size-2.5" />
          </span>
        )}
      </span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-title font-semibold tracking-tight">
          {flow.name.trim() || <span className="text-muted-foreground">Unnamed project</span>}
        </p>
        <p className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
          <span className="truncate">
            {[WORKLOAD_LABELS[flow.profile] ?? flow.profile, framework && frameworkLabel(framework)]
              .filter(Boolean)
              .join(" · ")}
          </span>
        </p>
      </div>
    </div>
  )
}

/** A step's name over its segment of the spine, the run page's stage head. */
function GroupHead({ label, position }: { label: string; position: Position }) {
  return (
    <div aria-hidden className="flex min-w-0 items-center gap-3 px-2 pt-3 pb-1">
      <span
        className={cn(
          "eyebrow flex shrink-0 items-center gap-1",
          position === "here" && "text-foreground",
        )}
      >
        {position === "done" && <Check className="size-3 text-brand" />}
        {label}
      </span>
      <span
        className={cn(
          "block h-0.5 min-w-0 flex-1 rounded-sm",
          position === "done" && "bg-flow-lit",
          position === "here" && "bg-brand",
          position === "ahead" && "bg-meter-track",
        )}
      />
    </div>
  )
}

function PlanRowItem({
  row,
  source,
  position,
  first,
  last,
  onOpen,
}: {
  row: PlanRow
  source: ConfigureFlow["source"]
  position: Position
  first: boolean
  last: boolean
  onOpen: () => void
}) {
  const mark = MARK[row.tone]
  return (
    <li className="relative min-w-0">
      {/* The timeline through the tiles' centres, from the tile above into
          this one and on to the next. */}
      {!first && (
        <span
          aria-hidden
          className={cn(
            "pointer-events-none absolute top-0 left-6 z-10 h-1.5 w-px",
            LINE[position],
          )}
        />
      )}
      {!last && (
        <span
          aria-hidden
          className={cn(
            "pointer-events-none absolute top-9.5 bottom-0 left-6 z-10 w-px",
            LINE[position],
          )}
        />
      )}
      <button
        type="button"
        aria-label={row.verb}
        onClick={onOpen}
        className="relative flex w-full min-w-0 items-start gap-3 rounded-lg px-2 py-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
      >
        <span aria-hidden className="relative flex shrink-0 self-start">
          {/* A source no product names keeps the chooser's glyph for its
              kind, on the same tile. */}
          {row.glyph === "source" && !row.product ? (
            <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background">
              <SourceMark source={source} className="size-4 text-muted-foreground" />
            </span>
          ) : (
            <ProductLogo
              id={row.product}
              size="sm"
              fallback={row.glyph === "source" ? Box : GLYPH[row.glyph]}
              className={cn(
                row.tone === "danger" && "border-rule-danger",
                position === "here" && row.tone === "default" && "border-border-strong",
              )}
            />
          )}
          {mark && (
            <span className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background">
              <StepMark state={mark} className="size-3 [&_svg]:size-3" />
            </span>
          )}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-body leading-snug font-medium">{row.label}</span>
          <span
            className={cn(
              "flex min-w-0 items-baseline gap-1.5 text-hint",
              row.tone === "warning"
                ? "text-warning"
                : row.tone === "danger"
                  ? "text-destructive"
                  : "text-muted-foreground",
            )}
          >
            <span className="min-w-0 truncate">{row.reading}</span>
            {row.facts && <span className="shrink-0 text-muted-foreground/80">{row.facts}</span>}
          </span>
          {row.ref && (
            <span className="mt-0.5 flex min-w-0 items-center gap-1.5">
              {row.ref.branch && <BranchChip branch={row.ref.branch} className="max-w-36" />}
              {row.ref.revision && <ShortSha sha={revisionText(row.ref.revision)} />}
            </span>
          )}
          {row.code &&
            (row.codeKind === "path" || row.codeKind === "name" ? (
              <code
                className={cn(
                  "mt-0.5 block truncate font-mono text-hint",
                  row.codeKind === "path" ? CODE.path : CODE.key,
                )}
              >
                {row.code}
              </code>
            ) : (
              <ShellWords command={row.code} className="mt-0.5 block truncate text-hint" />
            ))}
        </span>
      </button>
    </li>
  )
}

/** A commit cut as the Git page cuts one; an image digest to what tells two apart. */
function revisionText(revision: string) {
  return revision.startsWith("sha256:") ? revision.slice(7, 19) : revision
}
