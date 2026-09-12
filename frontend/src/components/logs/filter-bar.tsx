"use client"

import { useEffect, useRef, useState } from "react"
import {
  Check,
  ChevronDown,
  Cross,
  LoaderCircle,
  MagnifyingGlass,
  Rss,
  SettingsSliders,
  SlashForward,
  TextUppercase,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes } from "@/lib/format"
import type { LogJournalUnit, LogSource } from "@/lib/types"
import {
  LEVEL_HINT,
  LEVEL_LABEL,
  LOG_LEVELS,
  TIME_RANGES,
  logTimeInput,
  type LogLevel,
} from "@/lib/log-filter"
import type { LogFilterState, LogMode, LogTimeRange } from "@/components/logs/types"
import { Panel, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
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

/**
 * One filter for both questions.
 *
 * Live and Search used to be the same box meaning two things: the page had a
 * server-side grep with an Apply button *and* a client-side one inside the
 * pane, and neither said which lines it was actually looking at. There is one
 * filter here, it means the same thing in both modes, and switching modes keeps
 * it — because "these errors are scrolling past, when did they start" is one
 * thought, not two forms.
 */
export function FilterBar({
  mode,
  onModeChange,
  filter,
  onFilterChange,
  onSubmit,
  searching,
  counts,
  source,
  units,
  unit,
  onUnitChange,
  range,
  onRangeChange,
  since,
  until,
  onSinceChange,
  onUntilChange,
  context,
  onContextChange,
  archives,
  onArchivesChange,
  boot,
  onBootChange,
}: {
  mode: LogMode
  onModeChange: (mode: LogMode) => void
  filter: LogFilterState
  onFilterChange: (filter: LogFilterState) => void
  onSubmit: () => void
  searching: boolean
  counts: Record<string, number>
  source: LogSource | null
  units: LogJournalUnit[]
  unit: string
  onUnitChange: (unit: string) => void
  range: LogTimeRange
  onRangeChange: (range: LogTimeRange) => void
  since: string
  until: string
  onSinceChange: (value: string) => void
  onUntilChange: (value: string) => void
  context: number
  onContextChange: (value: number) => void
  archives: boolean
  onArchivesChange: (value: boolean) => void
  boot: boolean
  onBootChange: (value: boolean) => void
}) {
  const [open, setOpen] = useState(false)
  const queryRef = useRef<HTMLInputElement>(null)

  // "/" is the search key everywhere a log is read; without it the operator's
  // hand leaves the keyboard for every narrowing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      const typing = target?.tagName === "INPUT" || target?.tagName === "TEXTAREA"
      if (e.key === "/" && !typing && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        queryRef.current?.focus()
        queryRef.current?.select()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  const set = (patch: Partial<LogFilterState>) => onFilterChange({ ...filter, ...patch })
  const isJournal = source?.kind === "journal"
  const hasArchives = (source?.archives ?? 0) > 0
  const advancedCount =
    (filter.exclude ? 1 : 0) +
    (mode === "search" && range !== "24h" ? 1 : 0) +
    (context > 0 ? 1 : 0) +
    (archives && hasArchives ? 1 : 0) +
    (boot && isJournal ? 1 : 0) +
    (unit ? 1 : 0)

  return (
    <Panel className="shrink-0">
      <PanelToolbar className="border-b-0">
        <ToggleGroup
          type="single"
          value={mode}
          onValueChange={(v) => v && onModeChange(v as LogMode)}
          variant="outline"
          size="sm"
        >
          <ToggleGroupItem value="live" className="gap-1.5 px-2.5 text-xs">
            <Rss className="size-3.5" />
            Live
          </ToggleGroupItem>
          <ToggleGroupItem value="search" className="gap-1.5 px-2.5 text-xs">
            <MagnifyingGlass className="size-3.5" />
            History
          </ToggleGroupItem>
        </ToggleGroup>

        <form
          className="relative flex min-w-56 flex-1 items-center"
          onSubmit={(e) => {
            e.preventDefault()
            onSubmit()
          }}
        >
          <SearchInput
            ref={queryRef}
            value={filter.q}
            onChange={(e) => set({ q: e.target.value })}
            placeholder={
              mode === "live"
                ? "Filter the stream — matched on the server, press / to focus"
                : "Search this log's history — press Enter"
            }
            className="pr-[4.5rem]"
            containerClassName="sm:w-full"
            trailing={
              <>
                {filter.q && (
                  <Button
                    type="button"
                    size="icon"
                    variant="ghost"
                    className="size-6"
                    aria-label="Clear the filter"
                    onClick={() => set({ q: "" })}
                  >
                    <Cross className="size-3" />
                  </Button>
                )}
                <InputToggle
                  active={filter.regex}
                  onClick={() => set({ regex: !filter.regex })}
                  icon={SlashForward}
                  hint="Treat the search as a regular expression (RE2, the same one the server runs)"
                />
                <InputToggle
                  active={!filter.ignoreCase}
                  onClick={() => set({ ignoreCase: !filter.ignoreCase })}
                  icon={TextUppercase}
                  hint="Match case exactly"
                />
              </>
            }
          />
        </form>

        {mode === "search" && (
          <Button size="sm" onClick={onSubmit} disabled={searching} className="h-8">
            {searching ? (
              <LoaderCircle className="size-3.5 animate-spin" />
            ) : (
              <MagnifyingGlass className="size-3.5" />
            )}
            Search
          </Button>
        )}

        <Button
          size="sm"
          variant={open || advancedCount > 0 ? "secondary" : "ghost"}
          className="h-8 gap-1.5"
          onClick={() => setOpen((v) => !v)}
        >
          <SettingsSliders className="size-3.5" />
          More
          {advancedCount > 0 && <span className="numeric text-micro">{advancedCount}</span>}
          <ChevronDown className={cn("size-3 transition-transform", open && "rotate-180")} />
        </Button>
      </PanelToolbar>

      <PanelToolbar className="border-b-0 pt-0">
        <span className="eyebrow">Levels</span>
        <ToggleGroup
          type="multiple"
          value={filter.levels}
          onValueChange={(v) => set({ levels: v as LogLevel[] })}
          variant="outline"
          size="sm"
        >
          {LOG_LEVELS.map((level) => (
            <Tooltip key={level}>
              <TooltipTrigger asChild>
                <ToggleGroupItem value={level} className="gap-1 px-2 text-hint">
                  {LEVEL_LABEL[level]}
                  {counts[level] > 0 && (
                    <span className="numeric opacity-60">{counts[level].toLocaleString()}</span>
                  )}
                </ToggleGroupItem>
              </TooltipTrigger>
              <TooltipContent>{LEVEL_HINT[level]}</TooltipContent>
            </Tooltip>
          ))}
        </ToggleGroup>
        {filter.levels.length > 0 && (
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-xs"
            onClick={() => set({ levels: [] })}
          >
            Show every level
          </Button>
        )}
      </PanelToolbar>

      {open && (
        <PanelToolbar className="border-b-0 pt-0">
          <Field label="Hide lines containing">
            <Input
              value={filter.exclude}
              onChange={(e) => set({ exclude: e.target.value })}
              placeholder="e.g. /healthz"
              className="h-8 w-48 text-body"
            />
          </Field>

          {isJournal && (
            <>
              <Field label="Unit">
                <UnitPicker units={units} value={unit} onChange={onUnitChange} />
              </Field>
              <label className="flex items-center gap-2 text-xs">
                <Switch checked={boot} onCheckedChange={onBootChange} />
                <span>
                  This boot only
                  <span className="text-muted-foreground">
                    {" "}
                    — everything since the machine came up
                  </span>
                </span>
              </label>
            </>
          )}

          {mode === "search" && (
            <>
              <Field label="Window">
                <Select value={range} onValueChange={(v) => onRangeChange(v as LogTimeRange)}>
                  <SelectTrigger size="sm" className="w-44">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TIME_RANGES.map((r) => (
                      <SelectItem key={r.id} value={r.id}>
                        {r.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              {range === "custom" && (
                <>
                  <Field label="From">
                    <Input
                      type="datetime-local"
                      value={logTimeInput(since)}
                      step="0.001"
                      onChange={(e) => onSinceChange(e.target.value)}
                      className="h-8 w-52 text-body"
                    />
                  </Field>
                  <Field label="To">
                    <Input
                      type="datetime-local"
                      value={logTimeInput(until)}
                      step="0.001"
                      onChange={(e) => onUntilChange(e.target.value)}
                      className="h-8 w-52 text-body"
                    />
                  </Field>
                </>
              )}

              <Field label="Context lines">
                <ToggleGroup
                  type="single"
                  value={String(context)}
                  onValueChange={(v) => v && onContextChange(Number(v))}
                  variant="outline"
                  size="sm"
                >
                  {CONTEXT_CHOICES.map((n) => (
                    <ToggleGroupItem key={n} value={String(n)} className="px-2 text-hint">
                      {n === 0 ? "none" : `±${n}`}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              </Field>

              {hasArchives && (
                <label className="flex items-center gap-2 text-xs">
                  <Switch checked={archives} onCheckedChange={onArchivesChange} />
                  <span>
                    Include {source?.archives} rotated{" "}
                    {source?.archives === 1 ? "archive" : "archives"}
                    <span className="text-muted-foreground"> ({bytes(source?.archiveBytes)})</span>
                  </span>
                </label>
              )}
            </>
          )}
        </PanelToolbar>
      )}
    </Panel>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <Label className="text-hint text-muted-foreground">{label}</Label>
      {children}
    </div>
  )
}

function InputToggle({
  active,
  onClick,
  icon: Icon,
  hint,
}: {
  active: boolean
  onClick: () => void
  icon: React.ComponentType<{ className?: string }>
  hint: string
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="icon"
          variant={active ? "secondary" : "ghost"}
          className="size-6"
          aria-label={hint}
          aria-pressed={active}
          onClick={onClick}
        >
          <Icon className="size-3.5" />
        </Button>
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
        <Button variant="outline" size="sm" className="h-8 w-56 justify-between font-normal">
          <span className="truncate">{value || "Every unit"}</span>
          <ChevronDown className="size-3 opacity-60" />
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
