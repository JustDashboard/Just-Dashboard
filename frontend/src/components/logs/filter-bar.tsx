"use client"

import { useEffect, useRef, useState } from "react"
import { Check, ChevronDown, Cross, MagnifyingGlass, SettingsSliders } from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes } from "@/lib/format"
import type { LogJournalUnit, LogSource } from "@/lib/types"
import {
  LEVEL_DOT,
  LEVEL_HINT,
  LEVEL_LABEL,
  LOG_LEVELS,
  withFieldTokens,
  type LogLevel,
} from "@/lib/log-filter"
import { LENS_CHOICES, lensFor } from "@/lib/log-lenses"
import type { LogFilterState, LogMode } from "@/components/logs/types"
import { ChipCount, FilterChip } from "@/components/tabs"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const CONTEXT_CHOICES = [0, 2, 5, 10]

// Which workspace "/" belongs to. A page can show two logs at once — a
// deployment's output beside its proxy's — and one keydown listener per
// search box meant the key focused whichever box registered last, not the
// pane the reader was in. The pane pressed or focused last owns it, and the
// first one mounted until then.
const panes: string[] = []
let activePane: string | null = null

/** Called by a workspace on any press or focus inside it. */
export function claimPane(id: string) {
  activePane = id
}

function ownsSlash(id: string) {
  const owner = activePane && panes.includes(activePane) ? activePane : panes[0]
  return owner === id
}

/** The settings behind Options that narrow or widen what is read, as the filter row draws them. */
export type LogOptions = {
  context: number
  archives: boolean
  boot: boolean
  /** The lens the reader forced: "" for the detected one, "none" for none. */
  lens: string
}

/**
 * One filter for both questions.
 *
 * Live and Search used to be the same box meaning two things: the page had a
 * server-side grep with an Apply button *and* a client-side one inside the
 * pane, and neither said which lines it was actually looking at. There is one
 * filter here, it means the same thing in both modes, and switching modes keeps
 * it — because "these errors are scrolling past, when did they start" is one
 * thought, not two forms.
 *
 * The bar is one row of controls that share one face: the search field with
 * its two switches and its Search inside the field's own edge, the unit, the
 * window, and Options. It used to be four kinds of control in a row — a brand
 * Search button standing alone, a filled select, a ghost More, and two bare
 * glyphs at the end of the field that read as a keyboard hint ("/") and a
 * letter ("B") — so nothing said which of them did what. Options is a popover
 * now rather than a row that opened under the bar and pushed the lines down,
 * and what it holds is drawn in the filter row while it is set, so a
 * narrowing behind it is never out of sight.
 */
export function FilterBar({
  mode,
  filter,
  onFilterChange,
  onSubmit,
  searching,
  source,
  units,
  unit,
  onUnitChange,
  window: timeWindow,
  context,
  onContextChange,
  archives,
  onArchivesChange,
  boot,
  onBootChange,
  paneId,
  fieldKeys,
  lens,
  pageLens,
  detectedLens,
  onLensChange,
}: {
  mode: LogMode
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  /** Enter, or Search: with the filter as it stands once `key:value` words became fields. */
  onSubmit: (filter: LogFilterState) => void
  searching: boolean
  source: LogSource | null
  units: LogJournalUnit[]
  unit: string
  onUnitChange: (unit: string) => void
  /** The time window control, where the reading on screen has one. */
  window?: React.ReactNode
  context: number
  onContextChange: (value: number) => void
  archives: boolean
  onArchivesChange: (value: boolean) => void
  boot: boolean
  onBootChange: (value: boolean) => void
  /** The workspace this bar belongs to, for "/" (`claimPane`). */
  paneId?: string
  /** The keys a `key:value` word in the search box may name. */
  fieldKeys?: ReadonlySet<string>
  /** The lens the reader forced: "" for the detected one, "none" for none. */
  lens?: string
  /**
   * The lens the page reads its source through, which is where the pane
   * starts rather than a setting the reader changed: Options does not count it.
   */
  pageLens?: string
  /** The lens the source was detected as, for the Auto choice's words. */
  detectedLens?: string
  /** Offers "Read as" when set. */
  onLensChange?: (lens: string) => void
}) {
  const [open, setOpen] = useState(false)
  // The exclusion as Options opened on it: closed with another, History and
  // Insights ask again, as Enter in the field does.
  const [excludeAtOpen, setExcludeAtOpen] = useState(filter.exclude)
  const queryRef = useRef<HTMLInputElement>(null)

  // "/" is the search key everywhere a log is read; without it the operator's
  // hand leaves the keyboard for every narrowing.
  useEffect(() => {
    const id = paneId ?? ""
    panes.push(id)
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      const typing =
        target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.isContentEditable
      if (e.key === "/" && !typing && !e.metaKey && !e.ctrlKey && ownsSlash(id)) {
        e.preventDefault()
        queryRef.current?.focus()
        queryRef.current?.select()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => {
      window.removeEventListener("keydown", onKey)
      panes.splice(panes.indexOf(id), 1)
      if (activePane === id) activePane = null
    }
  }, [paneId])

  const set = (patch: Partial<LogFilterState>) => onFilterChange({ ...filter, ...patch })
  const submit = () => {
    const next = fieldKeys ? withFieldTokens(filter, fieldKeys) : filter
    if (next !== filter) onFilterChange(next)
    onSubmit(next)
  }
  const isJournal = source?.kind === "journal"
  const hasArchives = (source?.archives ?? 0) > 0
  // History and Insights both read a window of the file; Live reads its end.
  const windowed = mode === "search" || mode === "insights"
  // What is set and applies to the reading on screen: context lines are
  // History's, the rotated files a window's.
  const optionCount =
    (filter.exclude ? 1 : 0) +
    (context > 0 && mode === "search" ? 1 : 0) +
    (archives && hasArchives && windowed ? 1 : 0) +
    (boot && isJournal ? 1 : 0) +
    (onLensChange && (lens ?? "") !== (pageLens ?? "") ? 1 : 0)
  const detected = lensFor(detectedLens)?.label

  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-hairline px-3 py-2">
      <form
        className="flex min-w-56 flex-1 basis-80"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <InputGroup className="sm:h-8">
          <InputGroupAddon className="border-r-0 pr-0">
            <MagnifyingGlass />
          </InputGroupAddon>
          <InputGroupInput
            ref={queryRef}
            data-workspace-search
            value={filter.q}
            onChange={(e) => set({ q: e.target.value })}
            aria-label={mode === "live" ? "Filter the stream" : "Search this log's history"}
            placeholder={
              mode === "live"
                ? "Filter the live stream · Enter searches the history"
                : fieldKeys?.size
                  ? "Search words, or a field like user:postgres"
                  : "Search this log's history"
            }
            className="sm:text-body"
          />
          {filter.q && (
            <InputGroupAddon align="inline-end" className="border-l-0 px-1">
              <Button
                type="button"
                size="icon-xs"
                variant="ghost"
                aria-label="Clear the search"
                onClick={() => {
                  set({ q: "" })
                  queryRef.current?.focus()
                }}
              >
                <Cross />
              </Button>
            </InputGroupAddon>
          )}
          <InputGroupAddon align="inline-end" className="gap-0 p-0">
            <MatchToggle
              active={filter.regex}
              onClick={() => set({ regex: !filter.regex })}
              glyph=".*"
              label="Regular expression"
              hint="Read the search as a regular expression (RE2, the one the server runs)"
            />
            <MatchToggle
              active={!filter.ignoreCase}
              onClick={() => set({ ignoreCase: !filter.ignoreCase })}
              glyph="Aa"
              label="Match case"
              hint="Match upper and lower case exactly"
            />
            {windowed && (
              <InputGroupButton
                type="submit"
                variant="default"
                pending={searching}
                className="gap-1.5 px-3 font-medium"
              >
                Search
              </InputGroupButton>
            )}
          </InputGroupAddon>
        </InputGroup>
      </form>

      {isJournal && units.length > 0 && (
        <UnitPicker units={units} value={unit} onChange={onUnitChange} />
      )}

      {timeWindow}

      <Popover
        open={open}
        onOpenChange={(next) => {
          if (next) setExcludeAtOpen(filter.exclude)
          else if (windowed && filter.exclude !== excludeAtOpen) submit()
          setOpen(next)
        }}
      >
        <PopoverTrigger asChild>
          <Button
            size="sm"
            variant="outline"
            className="h-8 shrink-0 gap-1.5 px-2.5 font-normal max-sm:h-10"
            aria-label="Options"
          >
            <SettingsSliders className="size-3.5 text-muted-foreground" />
            {/* On a phone the word goes and the glyph and count stay: the bar
                is the field over the window and this. */}
            <span className="text-body max-sm:sr-only">Options</span>
            {optionCount > 0 && (
              <ChipCount className="text-signal opacity-100">{optionCount}</ChipCount>
            )}
            <ChevronDown
              aria-hidden
              className="size-3.5 text-muted-foreground transition-transform in-data-[state=open]:rotate-180"
            />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="w-80 space-y-4 p-4">
          <OptionField label="Hide lines containing" htmlFor="log-exclude">
            <Input
              id="log-exclude"
              value={filter.exclude}
              onChange={(e) => set({ exclude: e.target.value })}
              placeholder="e.g. /healthz"
              className="h-8 text-body"
            />
          </OptionField>

          {mode === "search" && (
            <OptionField label="Lines around each match">
              <ToggleGroup
                type="single"
                value={String(context)}
                onValueChange={(v) => v && onContextChange(Number(v))}
                variant="outline"
                size="sm"
                aria-label="Context lines"
                className="w-full"
              >
                {CONTEXT_CHOICES.map((n) => (
                  <ToggleGroupItem key={n} value={String(n)} className="flex-1 text-hint">
                    {n === 0 ? "None" : `±${n}`}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </OptionField>
          )}

          {onLensChange && (
            <OptionField label="Read as" htmlFor="log-read-as">
              <Select
                value={lens || "auto"}
                onValueChange={(v) => onLensChange(v === "auto" ? "" : v)}
              >
                <SelectTrigger id="log-read-as" size="sm" className="w-full" aria-label="Read as">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto" hint={detected ? `detected ${detected}` : undefined}>
                    Auto
                  </SelectItem>
                  {LENS_CHOICES.map((choice) => (
                    <SelectItem key={choice.id} value={choice.id}>
                      {choice.label}
                    </SelectItem>
                  ))}
                  <SelectItem value="none" hint="the text as written">
                    None
                  </SelectItem>
                </SelectContent>
              </Select>
            </OptionField>
          )}

          {(windowed && hasArchives) || isJournal ? (
            <div className="space-y-3 border-t border-hairline pt-3">
              {windowed && hasArchives && (
                <SwitchRow
                  checked={archives}
                  onCheckedChange={onArchivesChange}
                  label={`Include ${source?.archives} rotated ${source?.archives === 1 ? "file" : "files"}`}
                  hint={`${bytes(source?.archiveBytes)} of older lines`}
                />
              )}
              {isJournal && (
                <SwitchRow
                  checked={boot}
                  onCheckedChange={onBootChange}
                  label="This boot only"
                  hint="Everything since the machine came up"
                />
              )}
            </div>
          ) : null}
        </PopoverContent>
      </Popover>
    </div>
  )
}

/**
 * Which levels are shown, with how many of each there are.
 *
 * The chips carry the counts, so one group answers both "what am I looking
 * at" and "show me only the bad ones". Each is keyed by its level's dot — the
 * colour the level column and the histogram draw it in — and a count above
 * zero takes that colour too, so "error 238" is red before it is read. They
 * lead the filter row, with the questions the lens asks after them, so every
 * narrowing is in one row rather than split across the chart.
 */
export function LevelChips({
  filter,
  onFilterChange,
  counts,
}: {
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  counts: Record<string, number>
}) {
  const toggle = (level: LogLevel) =>
    onFilterChange({
      ...filter,
      levels: filter.levels.includes(level)
        ? filter.levels.filter((l) => l !== level)
        : [...filter.levels, level],
    })

  return (
    <div className="flex shrink-0 items-center gap-1" role="group" aria-label="Levels">
      {LOG_LEVELS.map((level) => (
        <Tooltip key={level}>
          <TooltipTrigger asChild>
            <FilterChip
              selected={filter.levels.includes(level)}
              onClick={() => toggle(level)}
              className="px-2"
            >
              <span aria-hidden className={cn("size-2 rounded-full", LEVEL_DOT[level])} />
              {LEVEL_LABEL[level]}
              {counts[level] > 0 && (
                <ChipCount className={cn(COUNT_TONE[level], COUNT_TONE[level] && "opacity-100")}>
                  {counts[level].toLocaleString()}
                </ChipCount>
              )}
            </FilterChip>
          </TooltipTrigger>
          <TooltipContent>{LEVEL_HINT[level]}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

const COUNT_TONE: Partial<Record<LogLevel, string>> = {
  critical: "text-destructive",
  error: "text-destructive",
  warn: "text-warning",
}

function OptionField({
  label,
  htmlFor,
  children,
}: {
  label: string
  htmlFor?: string
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-hint text-muted-foreground">
        {label}
      </Label>
      {children}
    </div>
  )
}

function SwitchRow({
  checked,
  onCheckedChange,
  label,
  hint,
}: {
  checked: boolean
  onCheckedChange: (value: boolean) => void
  label: string
  hint: string
}) {
  return (
    <label className="flex cursor-pointer items-start justify-between gap-3">
      <span className="min-w-0">
        <span className="block text-body">{label}</span>
        <span className="block text-hint text-muted-foreground">{hint}</span>
      </span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} className="mt-0.5" />
    </label>
  )
}

/**
 * A switch on how the words are matched, inside the field's edge: the
 * editors' own `.*` and `Aa`, which a reader of logs already knows, rather
 * than a slash that read as the `/` shortcut's hint and a glyph that read as
 * a "B". On, it takes the pressed ground and the brand ink, `InputGroupToggle`'s
 * selection.
 */
function MatchToggle({
  active,
  onClick,
  glyph,
  label,
  hint,
}: {
  active: boolean
  onClick: () => void
  glyph: string
  label: string
  hint: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-pressed={active}
          onClick={onClick}
          className={cn(
            "flex h-full w-9 items-center justify-center font-mono text-hint font-medium focus-ring-inset transition-colors",
            active
              ? "bg-accent text-brand"
              : "text-muted-foreground hover:bg-accent hover:text-foreground",
          )}
        >
          {glyph}
        </button>
      </TooltipTrigger>
      <TooltipContent>{hint}</TooltipContent>
    </Tooltip>
  )
}

/**
 * The journal is one source with a thousand faces, so the unit lives here
 * rather than as a thousand rows in the source rail. It is searchable because
 * a host has hundreds of units and nobody scrolls to `systemd-resolved`.
 */
function UnitPicker({
  units,
  value,
  onChange,
}: {
  units: LogJournalUnit[]
  value: string
  onChange: (unit: string) => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="h-8 w-48 justify-between font-normal max-sm:h-10"
          aria-label="Unit"
        >
          <span className="truncate text-body">{value || "Every unit"}</span>
          <ChevronDown className="size-3.5 text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-0" align="start">
        <Command>
          <CommandInput placeholder="Find a unit…" />
          <CommandList>
            <CommandEmpty>No unit by that name.</CommandEmpty>
            <CommandGroup>
              <CommandItem
                value="every unit"
                onSelect={() => {
                  onChange("")
                  setOpen(false)
                }}
              >
                <Check className={cn("size-3.5", value ? "opacity-0" : "opacity-100")} />
                Every unit
              </CommandItem>
              {units.map((u) => (
                <CommandItem
                  key={u.name}
                  value={`${u.name} ${u.description}`}
                  onSelect={() => {
                    onChange(u.name)
                    setOpen(false)
                  }}
                >
                  <Check
                    className={cn("size-3.5", value === u.name ? "opacity-100" : "opacity-0")}
                  />
                  <span className="min-w-0 flex-1 truncate">{u.name}</span>
                  <span className="shrink-0 text-micro text-muted-foreground">{u.active}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
