"use client"

import { useMemo } from "react"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { GroupRule } from "@/components/flow"
import { Disclosure } from "@/components/form"
import { EmptyState } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { MagnifyingGlass } from "@/components/icons"
import {
  changeCounts,
  definitionRows,
  groupObjects,
  partsLine,
  type Change,
  type CompareSide,
  type DiffObject,
  type DiffPart,
} from "@/components/database/compare/model"

const CHANGE_TONE: Record<Change, Tone> = {
  added: "success",
  removed: "danger",
  changed: "warning",
}
const CHANGE_WORD: Record<Change, string> = {
  added: "Added",
  removed: "Removed",
  changed: "Changed",
}

type Filter = "all" | Change

/**
 * What differs, by kind of object and then by object: a row says what changed
 * in a phrase and opens to the parts, each read with the "from" side facing
 * the "to" side.
 */
export function DiffList({
  objects,
  from,
  to,
}: {
  objects: DiffObject[]
  from: CompareSide
  to: CompareSide
}) {
  const [filter, setFilter] = useViewState<Filter>("databases.compare.filter", "all")
  const counts = useMemo(() => changeCounts(objects), [objects])
  // A filter left over from another comparison that has nothing under it is no filter.
  const active: Filter = filter !== "all" && counts[filter] === 0 ? "all" : filter
  const shown = useMemo(
    () => groupObjects(active === "all" ? objects : objects.filter((o) => o.change === active)),
    [objects, active],
  )

  return (
    <section aria-label="Differences" className="min-w-0 space-y-4">
      <ChipStrip role="group" aria-label="Show">
        <FilterChip selected={active === "all"} onClick={() => setFilter("all")}>
          All
          <ChipCount>{objects.length}</ChipCount>
        </FilterChip>
        {(["added", "removed", "changed"] as const)
          .filter((change) => counts[change] > 0)
          .map((change) => (
            <FilterChip key={change} selected={active === change} onClick={() => setFilter(change)}>
              {CHANGE_WORD[change]}
              <ChipCount>{counts[change]}</ChipCount>
            </FilterChip>
          ))}
      </ChipStrip>
      {shown.length === 0 ? (
        <EmptyState icon={MagnifyingGlass} title="Nothing under this filter" />
      ) : (
        shown.map((group) => (
          <div key={group.kind} className="space-y-1" data-kind={group.kind}>
            <GroupRule label={group.label} count={group.objects.length} />
            <ul className="min-w-0">
              {group.objects.map((object) => (
                <li
                  key={`${object.kind}:${object.name}`}
                  className="border-b border-hairline last:border-b-0"
                >
                  <ObjectRow object={object} from={from} to={to} />
                </li>
              ))}
            </ul>
          </div>
        ))
      )}
    </section>
  )
}

function ObjectRow({
  object,
  from,
  to,
}: {
  object: DiffObject
  from: CompareSide
  to: CompareSide
}) {
  const phrase = partsLine(object.parts)
  return (
    <Disclosure
      quiet
      summary={
        <span className="flex min-w-0 flex-1 items-baseline gap-3">
          <Tag tone={CHANGE_TONE[object.change]} className="w-16 shrink-0">
            {CHANGE_WORD[object.change]}
          </Tag>
          <span className="min-w-0 truncate font-mono text-body">{object.name}</span>
          {object.change === "changed" && phrase && (
            <span className="numeric ml-auto shrink-0 text-hint font-normal text-muted-foreground max-sm:hidden">
              {phrase}
            </span>
          )}
        </span>
      }
    >
      <PartsTable object={object} from={from} to={to} />
    </Disclosure>
  )
}

/** A text kept on one side or both: a definition, an enum's labels. */
function isText(part: DiffPart) {
  return part.part === "definition"
}

function PartsTable({
  object,
  from,
  to,
}: {
  object: DiffObject
  from: CompareSide
  to: CompareSide
}) {
  if (object.parts.length === 0) {
    return <p className="pb-3 text-hint text-muted-foreground">Nothing more to show.</p>
  }
  return (
    <div className="space-y-3 pb-3">
      <div
        aria-hidden
        className="grid grid-cols-2 gap-x-4 border-b border-hairline pb-1 text-micro text-muted-foreground max-sm:hidden"
      >
        <SideHeading side={from} label="From" />
        <SideHeading side={to} label="To" />
      </div>
      {object.parts.map((part, index) =>
        isText(part) ? (
          <DefinitionDiff
            key={`${part.part}:${part.name}:${index}`}
            part={part}
            from={from}
            to={to}
          />
        ) : (
          <PartRow key={`${part.part}:${part.name}:${index}`} part={part} />
        ),
      )}
    </div>
  )
}

function SideHeading({ side, label }: { side: CompareSide; label: string }) {
  return (
    <p className="min-w-0 truncate">
      <span className="eyebrow">{label}</span>{" "}
      <span className="font-mono">
        {side.name}
        {side.schema && ` · ${side.schema}`}
      </span>
    </p>
  )
}

/** One column, key or index: what it is, and how each side writes it. */
function PartRow({ part }: { part: DiffPart }) {
  return (
    <div className="min-w-0 space-y-1" data-part={part.part}>
      <p className="flex min-w-0 items-baseline gap-2">
        <Tag tone={CHANGE_TONE[part.change]} className="shrink-0">
          {part.part}
        </Tag>
        <span className="min-w-0 truncate font-mono text-xs">{part.name}</span>
        <span className="sr-only">{CHANGE_WORD[part.change].toLowerCase()}</span>
      </p>
      <div className="grid min-w-0 gap-x-4 gap-y-1 sm:grid-cols-2">
        <Cell label="From" text={part.from} />
        <Cell label="To" text={part.to} />
      </div>
    </div>
  )
}

function Cell({ label, text }: { label: string; text: string }) {
  return (
    <p className="min-w-0 font-mono text-xs leading-relaxed break-words">
      <span className="eyebrow mr-2 sm:hidden">{label}</span>
      {text === "" ? (
        <span aria-label={`Not in ${label.toLowerCase()}`} className="text-muted-foreground">
          —
        </span>
      ) : (
        text
      )}
    </p>
  )
}

/**
 * A definition read as text on both sides, lined up by the lines the two
 * share, so a changed view or function shows which lines changed and the rest
 * reads as context.
 */
function DefinitionDiff({
  part,
  from,
  to,
}: {
  part: DiffPart
  from: CompareSide
  to: CompareSide
}) {
  const rows = useMemo(() => definitionRows(part.from, part.to), [part.from, part.to])
  return (
    <div className="min-w-0" data-part="definition">
      <p className="sr-only">
        Definition of {part.name}, {CHANGE_WORD[part.change].toLowerCase()}
      </p>
      <div className="grid min-w-0 sm:grid-cols-2 sm:gap-x-4">
        {(["from", "to"] as const).map((column) => {
          const side = column === "from" ? from : to
          return (
            <div key={column} className="min-w-0">
              <p className="eyebrow pb-1 sm:hidden">
                {column === "from" ? "From" : "To"} · {side.name}
              </p>
              <pre
                data-side={column}
                className="max-h-96 overflow-auto rounded-md border border-hairline bg-surface-header py-1 font-mono text-xs leading-relaxed"
              >
                {rows.map((row, index) => {
                  const text = row[column]
                  return (
                    <span
                      key={index}
                      className={cn(
                        "block min-h-[1.45em] px-2 break-words whitespace-pre-wrap",
                        !row.same &&
                          text !== null &&
                          (column === "from" ? "bg-wash-danger" : "bg-wash-success"),
                      )}
                    >
                      {text ?? ""}
                    </span>
                  )
                })}
              </pre>
            </div>
          )
        })}
      </div>
    </div>
  )
}
