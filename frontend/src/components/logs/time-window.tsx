"use client"

import { useState } from "react"
import { Check, ChevronDown, Clock } from "@/components/icons"
import { cn } from "@/lib/utils"
import { minuteSpan } from "@/lib/format"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"

export type WindowPreset = { id: string; label: string; short: string }

/**
 * datetime-local wants the browser's own clock — to the second, since a
 * column zoomed out of a fifteen-minute chart is fifteen seconds wide and at
 * the minute its two ends were the same time.
 */
function localTime(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 19)
}

/**
 * Which stretch of time a log is read over, as one control.
 *
 * It replaces a select whose last choice, "Custom range", opened a second row
 * of two bare date fields under the bar — the window said "Custom range"
 * while the times that made it sat a row lower, and nothing said the stream
 * was not reading a window at all. Here the trigger always names what is on
 * screen: *Live* with the socket's dot while a tail is followed, a preset's
 * name, or the span itself. The popover holds every way to change it — the
 * tail first where a pane offers one, the presets one press each, and the
 * span typed as two times with one Apply — so a window is never half-chosen.
 */
export function TimeWindow({
  presets,
  value,
  since,
  until,
  live,
  onLive,
  onPreset,
  onCustom,
  className,
}: {
  presets: WindowPreset[]
  /** A preset's id, or "custom" for the span in `since` and `until`. */
  value: string
  since?: string
  until?: string
  /** Set where the pane can follow its source: whether it is following it now. */
  live?: boolean
  onLive?: () => void
  onPreset: (id: string) => void
  onCustom: (since: Date, until: Date) => void
  className?: string
}) {
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const preset = presets.find((p) => p.id === value)
  const custom = value === "custom"
  const fromAt = from ? new Date(from) : undefined
  const toAt = to ? new Date(to) : undefined
  const backwards = fromAt && toAt && fromAt >= toAt
  const ready = Boolean(fromAt && toAt && !backwards)

  const label = live
    ? "Live"
    : custom
      ? since
        ? minuteSpan(since, until || undefined)
        : "Pick a range"
      : (preset?.label ?? "Window")

  const pick = (fn: () => void) => {
    fn()
    setOpen(false)
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next) {
          // The span on screen, or the last hour up to now: an empty pair of
          // fields made the reader type two whole timestamps to look at ten
          // minutes.
          const end = custom && until ? new Date(until) : new Date()
          const start = custom && since ? new Date(since) : new Date(end.getTime() - 60 * 60_000)
          setFrom(localTime(start))
          setTo(localTime(end))
        }
        setOpen(next)
      }}
    >
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          aria-label={`Time window: ${label}`}
          className={cn(
            "h-8 max-w-64 min-w-0 shrink-0 gap-2 px-2.5 text-body font-normal max-sm:h-10",
            className,
          )}
        >
          {live ? (
            <StatusDot tone="running" live />
          ) : (
            <Clock className="size-3.5 text-muted-foreground" />
          )}
          <span className="numeric truncate">{label}</span>
          <ChevronDown
            aria-hidden
            className="size-3.5 text-muted-foreground transition-transform in-data-[state=open]:rotate-180"
          />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0">
        {onLive && (
          <button
            type="button"
            aria-pressed={live}
            onClick={() => pick(onLive)}
            className={cn(
              "flex w-full items-center gap-3 border-b border-hairline px-3 py-2.5 text-left focus-ring-inset transition-colors",
              live ? "bg-accent" : "hover:bg-menu-hover",
            )}
          >
            <StatusDot tone="running" live />
            <span className="min-w-0 flex-1">
              <span className="block text-body font-medium">Live tail</span>
              <span className="block text-hint text-muted-foreground">
                New lines as they are written
              </span>
            </span>
            {live && <Check className="size-3.5 shrink-0" />}
          </button>
        )}
        <div className="space-y-2 px-3 pt-3 pb-3">
          <p className="eyebrow">Look back over</p>
          <div className="grid grid-cols-3 gap-1.5" role="group" aria-label="Presets">
            {presets.map((p) => {
              const on = !live && value === p.id
              return (
                <button
                  key={p.id}
                  type="button"
                  aria-pressed={on}
                  title={p.label}
                  onClick={() => pick(() => onPreset(p.id))}
                  className={cn(
                    "numeric h-8 rounded-md border text-body focus-ring transition-colors",
                    on
                      ? "border-border-strong bg-accent font-medium text-foreground"
                      : "border-input bg-control text-muted-foreground hover:bg-control-hover hover:text-foreground",
                  )}
                >
                  {p.short}
                </button>
              )
            })}
          </div>
        </div>
        <form
          className="space-y-2 border-t border-hairline px-3 pt-3 pb-3"
          onSubmit={(event) => {
            event.preventDefault()
            if (fromAt && toAt && ready) pick(() => onCustom(fromAt, toAt))
          }}
        >
          <p className="eyebrow">Or between</p>
          <div className="grid grid-cols-[2.5rem_1fr] items-center gap-x-2 gap-y-1.5">
            <Label htmlFor="window-from" className="text-hint text-muted-foreground">
              From
            </Label>
            <Input
              id="window-from"
              type="datetime-local"
              step={1}
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="numeric h-8 text-body"
            />
            <Label htmlFor="window-to" className="text-hint text-muted-foreground">
              To
            </Label>
            <Input
              id="window-to"
              type="datetime-local"
              step={1}
              value={to}
              onChange={(e) => setTo(e.target.value)}
              aria-invalid={backwards || undefined}
              className="numeric h-8 text-body"
            />
          </div>
          <div className="flex items-center justify-between gap-3 pt-1">
            <span
              className={cn("text-hint", backwards ? "text-destructive" : "text-muted-foreground")}
            >
              {backwards ? "The start must be before the end." : "Your local time."}
            </span>
            <Button type="submit" size="sm" disabled={!ready}>
              Apply
            </Button>
          </div>
        </form>
      </PopoverContent>
    </Popover>
  )
}
