"use client"

import { useMemo, useState } from "react"
import {
  Box,
  ChevronRight,
  CodeBracket,
  Copy,
  Eye,
  EyeOff,
  FolderClosed,
  Hash,
  Heart,
  Layers,
  NetworkDevice,
  Servers,
  SettingsSliders,
  ShieldOff,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { SearchInput } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { CODE } from "@/components/deploy/run-evidence"
import {
  EVERYTHING,
  MASK,
  SUMMARY,
  filterJson,
  inspectSections,
  leaves,
  maskInspect,
  openingSection,
  sizeOf,
  stringKind,
  type InspectSection,
  type Json,
} from "@/components/docker/inspect"

/** Each section as the thing it describes, the way the rest of the page draws them. */
const GLYPH: Record<string, Icon> = {
  [SUMMARY]: Box,
  Config: SettingsSliders,
  State: Heart,
  HostConfig: Servers,
  NetworkSettings: NetworkDevice,
  Mounts: FolderClosed,
  GraphDriver: Layers,
  [EVERYTHING]: CodeBracket,
}

/**
 * The Engine's own inspect output, as a workbench rather than a wall.
 *
 * Every panel on this page is a chosen subset of something, and eventually
 * somebody needs the field nobody chose. This is also the check on the rest
 * of the page: an operator who suspects the dashboard is misreporting
 * something can see what it was reading.
 *
 * It was one well of white JSON, two hundred lines of it for a running
 * container, with HostConfig's sixty fields between the reader and the one
 * they came for. It is the run page's Details shape now (§7): one frame, a
 * rail of the document's sections — the container's own fields, Config,
 * State, HostConfig, NetworkSettings, Mounts and the rest, with the whole
 * document last — and an inspector of the picked one, coloured as code in the
 * `CODE` hues with each object and list folding to its size. A filter over
 * the strip narrows every section at once and the rail counts what each still
 * holds, so "where is the restart policy" is one word rather than a scroll.
 *
 * Credential-shaped environment values are masked on the server for anyone
 * below system.admin — the raw route is not a way around that — and masked
 * again here for the admin who is allowed to read them, so that opening a tab
 * is never by itself what puts a master key on screen. The filter reads what
 * is drawn, so a hidden value cannot be found by searching for it either.
 */
export function ContainerInspectTab({ containerId }: { containerId: string }) {
  const { data, error, loading } = usePoll<Record<string, unknown>>(
    (signal) =>
      get<Record<string, unknown>>(`/docker/containers/${containerId}/raw`, undefined, signal),
    0,
    [containerId],
  )
  const [revealed, setRevealed] = useState(false)
  const [query, setQuery] = useState("")
  const [picked, setPicked] = useState<string>()
  const masked = useMemo(() => (data ? maskInspect(data) : undefined), [data])
  const shown = (revealed ? data : masked?.doc) as Record<string, Json> | undefined
  const sections = useMemo(() => (shown ? inspectSections(shown) : []), [shown])

  if (error) return <ErrorState error={error} />
  if (loading || !shown || !masked) return <LoadingRows />

  const opening = openingSection(sections)
  const current =
    sections.find((section) => section.key === picked) ??
    sections.find((section) => section.key === opening)!
  const filtering = query.trim() !== ""
  const narrowed = filterJson(current.value, query)
  const hidden = masked.masked > 0 && !revealed

  return (
    <div className="flex h-full min-h-[28rem] min-w-0 flex-col overflow-hidden rounded-xl border bg-card">
      <PaneHeader className="flex-wrap gap-x-3 gap-y-2 px-3 py-2">
        <SearchInput
          dense
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Filter fields and values"
          aria-label="Filter the inspect document"
          containerClassName="sm:w-64"
        />
        {masked.masked > 0 && (
          <p className="flex min-w-0 flex-1 basis-64 items-center gap-2 text-hint text-muted-foreground">
            <ShieldOff aria-hidden className="size-3.5 shrink-0" />
            <span>
              {revealed
                ? `${masked.masked} credential-shaped ${masked.masked === 1 ? "value is" : "values are"} on screen. Hide ${masked.masked === 1 ? "it" : "them"} before sharing it.`
                : `${masked.masked} ${masked.masked === 1 ? "value looks" : "values look"} like a credential and ${masked.masked === 1 ? "is" : "are"} hidden here too, the same as on the Environment tab.`}
            </span>
          </p>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {masked.masked > 0 && (
            <Button size="xs" variant="ghost" onClick={() => setRevealed((on) => !on)}>
              {revealed ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
              {revealed ? "Hide" : "Reveal"}
            </Button>
          )}
          {/* Copies what is on screen, masked included: a raw inspect pasted
              into a ticket is the other way these values get away. */}
          <Button
            size="xs"
            variant="outline"
            onClick={() =>
              void copyText(
                JSON.stringify(narrowed ?? null, null, 2),
                hidden ? "Copied, credentials masked" : "Copied",
              )
            }
          >
            <Copy className="size-3" />
            Copy
          </Button>
        </div>
      </PaneHeader>

      <div className="grid min-h-0 flex-1 max-lg:grid-rows-[auto_minmax(0,1fr)] lg:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]">
        <Pane flush className="max-lg:border-b lg:border-r">
          <nav
            aria-label="Sections of the inspect document"
            className="flex min-h-0 [scrollbar-width:none] gap-1 overflow-x-auto p-2 lg:flex-col lg:overflow-y-auto [&::-webkit-scrollbar]:hidden"
          >
            {sections.map((section) => (
              <SectionButton
                key={section.key}
                section={section}
                selected={section.key === current.key}
                found={filtering ? leaves(filterJson(section.value, query)) : undefined}
                onSelect={() => setPicked(section.key)}
              />
            ))}
          </nav>
        </Pane>

        <Pane flush>
          <div className="flex min-w-0 shrink-0 items-baseline gap-2 border-b border-hairline px-4 py-2.5">
            <span className="truncate text-body font-medium">{current.label}</span>
            {current.hint && (
              <span className="min-w-0 truncate text-hint text-muted-foreground max-sm:hidden">
                {current.hint}
              </span>
            )}
            <span className="numeric ml-auto shrink-0 text-hint text-muted-foreground">
              {filtering
                ? `${leaves(narrowed)} found`
                : `${sizeOf(current.value)} ${sizeOf(current.value) === 1 ? "field" : "fields"}`}
            </span>
          </div>
          <div className="min-h-0 flex-1 overflow-auto px-4 py-3 font-mono text-xs leading-relaxed">
            {narrowed === undefined ? (
              <EmptyNote className="font-sans">
                Nothing in {current.label} matches &ldquo;{query.trim()}&rdquo;.
              </EmptyNote>
            ) : (
              <JsonInspector
                // A section of its own folds afresh: what was opened in one is
                // no guide to what is wanted in the next.
                key={current.key}
                value={narrowed}
                openDepth={current.key === EVERYTHING ? 1 : 2}
                forced={filtering}
              />
            )}
          </div>
        </Pane>
      </div>
    </div>
  )
}

/** One section on the rail: its glyph, its name, what it holds, and how many fields. */
function SectionButton({
  section,
  selected,
  found,
  onSelect,
}: {
  section: InspectSection
  selected: boolean
  /** Under a filter, how many values in it still match. */
  found?: number
  onSelect: () => void
}) {
  const Glyph = GLYPH[section.key] ?? Hash
  const count = found ?? sizeOf(section.value)
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={onSelect}
      className={cn(
        "flex min-w-0 shrink-0 items-center gap-2.5 rounded-lg px-2.5 py-2 text-left focus-ring-inset transition-colors lg:w-full",
        selected ? "bg-accent" : "hover:bg-row-hover",
        found === 0 && "opacity-50",
      )}
    >
      <Glyph
        aria-hidden
        className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-body font-medium">{section.label}</span>
        {section.hint && (
          <span className="block truncate text-hint text-muted-foreground max-lg:hidden">
            {section.hint}
          </span>
        )}
      </span>
      <span className="numeric shrink-0 text-hint text-muted-foreground">{count}</span>
    </button>
  )
}

/**
 * A value as JSON, coloured by what each part is and folding where it nests.
 * Every object and list at `openDepth` or deeper starts folded to its size —
 * HostConfig's device lists and ulimits are rarely the question — and a
 * filter opens everything it kept, since what it kept is what was asked for.
 */
function JsonInspector({
  value,
  openDepth,
  forced,
}: {
  value: Json
  openDepth: number
  forced: boolean
}) {
  const [toggled, setToggled] = useState<Record<string, boolean>>({})
  const fold = (path: string, open: boolean) => setToggled((held) => ({ ...held, [path]: open }))
  if (value === null || typeof value !== "object") return <Scalar value={value} />
  return (
    <ul className="pl-4">
      <Entries
        value={value}
        path=""
        depth={0}
        openDepth={openDepth}
        forced={forced}
        toggled={toggled}
        onFold={fold}
      />
    </ul>
  )
}

type Folding = {
  depth: number
  openDepth: number
  forced: boolean
  toggled: Record<string, boolean>
  onFold: (path: string, open: boolean) => void
}

function Entries({
  value,
  path,
  ...folding
}: { value: Json[] | { [key: string]: Json }; path: string } & Folding) {
  const named = !Array.isArray(value)
  const entries: [string, Json][] = Array.isArray(value)
    ? value.map((item, index) => [String(index), item])
    : Object.entries(value)
  return entries.map(([key, child], index) => (
    <Entry
      key={key}
      name={named ? key : undefined}
      value={child}
      path={`${path}/${key}`}
      last={index === entries.length - 1}
      {...folding}
    />
  ))
}

function Entry({
  name,
  value,
  path,
  last,
  depth,
  openDepth,
  forced,
  toggled,
  onFold,
}: {
  /** An object's key; a list's items have none. */
  name?: string
  value: Json
  path: string
  last: boolean
} & Folding) {
  const label = name !== undefined && (
    <>
      <span className={CODE.key}>&quot;{name}&quot;</span>
      <span className={CODE.punct}>: </span>
    </>
  )
  const comma = !last && <span className={CODE.punct}>,</span>
  const size = value !== null && typeof value === "object" ? sizeOf(value) : 0

  if (value === null || typeof value !== "object" || size === 0) {
    return (
      <li className="break-all whitespace-pre-wrap">
        {label}
        {value !== null && typeof value === "object" ? (
          <span className={CODE.punct}>{Array.isArray(value) ? "[]" : "{}"}</span>
        ) : (
          <Scalar value={value} />
        )}
        {comma}
      </li>
    )
  }

  const list = Array.isArray(value)
  const open = forced || (toggled[path] ?? depth < openDepth)
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => onFold(path, !open)}
        className="-ml-4 inline-flex max-w-full items-baseline rounded-sm text-left focus-ring transition-colors hover:bg-row-hover"
      >
        <span aria-hidden className="flex w-4 shrink-0 justify-center self-center">
          <ChevronRight
            className={cn("size-3 text-muted-foreground transition-transform", open && "rotate-90")}
          />
        </span>
        <span className="min-w-0 break-all">
          {label}
          <span className={CODE.punct}>{list ? "[" : "{"}</span>
          {!open && (
            <>
              <span className="text-muted-foreground"> … </span>
              <span className={CODE.punct}>{list ? "]" : "}"}</span>
              {comma}
              <span className="ml-2 font-sans text-hint text-muted-foreground/70">
                {size} {list ? (size === 1 ? "item" : "items") : size === 1 ? "key" : "keys"}
              </span>
            </>
          )}
        </span>
      </button>
      {open && (
        <>
          {/* The hairline down the left is the fold's extent, so a long
              object's closing brace is found by following it. */}
          <ul className="ml-1.5 border-l border-hairline pl-[9px]">
            <Entries
              value={value}
              path={path}
              depth={depth + 1}
              openDepth={openDepth}
              forced={forced}
              toggled={toggled}
              onFold={onFold}
            />
          </ul>
          <span className={CODE.punct}>{list ? "]" : "}"}</span>
          {comma}
        </>
      )}
    </li>
  )
}

/**
 * A plain value in its hue: a string by what it is, a number in the number's,
 * true, false and null as literals. A masked credential steps back, so the
 * dots are not read as a value.
 */
function Scalar({ value }: { value: string | number | boolean | null }) {
  if (typeof value === "string") {
    const kind = stringKind(value)
    return (
      <span
        className={cn(
          value.includes(MASK)
            ? "text-muted-foreground"
            : kind === "path"
              ? CODE.path
              : kind === "digest"
                ? CODE.digest
                : CODE.string,
        )}
      >
        {JSON.stringify(value)}
      </span>
    )
  }
  if (typeof value === "number") return <span className={CODE.number}>{String(value)}</span>
  return <span className={CODE.literal}>{String(value)}</span>
}
