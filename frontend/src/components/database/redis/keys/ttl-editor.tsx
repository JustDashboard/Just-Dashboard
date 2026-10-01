"use client"

import { useEffect, useId, useState } from "react"
import { Pencil } from "@/components/icons"
import { errorMessage } from "@/lib/api"
import { notify } from "@/lib/toast"
import { Segments } from "@/components/deploy/settings/segments"
import { FormNote } from "@/components/form"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { redisExpire, redisPersist, type RedisTarget } from "@/components/database/redis/api"
import {
  localMoment,
  parseMoment,
  parseTtl,
  remainingMs,
  ttlWord,
} from "@/components/database/redis/ttl"
import type { RedisBytes } from "@/components/database/redis/types"

const PRESETS = ["1m", "1h", "1d", "7d", "30d"]

/** The clock, re-read on an interval: what a countdown is drawn from. */
function useNow(everyMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), everyMs)
    return () => clearInterval(timer)
  }, [everyMs])
  return now
}

/** What is left of an expiry, counting down between reads of the key. */
export function useRemaining(pttl: number, readAt: number): number {
  const now = useNow(1000)
  return remainingMs(pttl, readAt, now)
}

/** How a key's expiry reads: how long is left, or that there is none. */
export function ttlReading(remaining: number): string {
  if (remaining < 0) return "No expiry"
  if (remaining === 0) return "Expiring now"
  return ttlWord(Math.ceil(remaining / 1000))
}

/**
 * A key's expiry as a fact you can press.
 *
 * The reading counts down between reads of the key. Pressing it opens the
 * one place an expiry is changed: a span from now, or a moment, each refused
 * with its reason when it is not one — and removing the expiry is a control
 * of its own. Nothing typed here is ever read as "no expiry": that was the
 * old box's failure, where `60s` and an empty field both cleared it.
 */
export function TtlFact({
  target,
  name,
  pttl,
  readAt,
  editable,
  onChanged,
}: {
  target: RedisTarget
  name: RedisBytes
  /** Milliseconds left when the key was read; -1 for none. */
  pttl: number
  /** When that reading was taken. */
  readAt: number
  editable: boolean
  onChanged: () => void
}) {
  const remaining = useRemaining(pttl, readAt)
  const [open, setOpen] = useState(false)
  const reading = ttlReading(remaining)

  if (!editable) return <span>{reading}</span>
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Expiry: ${reading}. Change it`}
          className="inline-flex items-center gap-1.5 rounded-sm whitespace-nowrap focus-ring-inset transition-colors hover:bg-row-hover"
        >
          <span>{reading}</span>
          <Pencil aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        className="w-80 p-3"
        // The field, not the switch above it: the reader opened this to type a span.
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          ;(event.currentTarget as HTMLElement).querySelector("input")?.focus()
        }}
      >
        <TtlForm
          target={target}
          name={name}
          expires={pttl >= 0}
          onDone={() => {
            setOpen(false)
            onChanged()
          }}
        />
      </PopoverContent>
    </Popover>
  )
}

function TtlForm({
  target,
  name,
  expires,
  onDone,
}: {
  target: RedisTarget
  name: RedisBytes
  expires: boolean
  onDone: () => void
}) {
  const field = useId()
  const [mode, setMode] = useState<"in" | "at">("in")
  const [span, setSpan] = useState("")
  const [moment, setMoment] = useState("")
  const [busy, setBusy] = useState<"set" | "remove" | null>(null)
  const [refused, setRefused] = useState("")
  // Judged only once something is typed: an empty box is not a mistake yet.
  const [touched, setTouched] = useState(false)

  const parsed = mode === "in" ? parseTtl(span) : parseMoment(moment)
  const problem = refused || (touched && !parsed.ok ? parsed.why : "")

  const set = async () => {
    setTouched(true)
    if (!parsed.ok) return
    setBusy("set")
    setRefused("")
    try {
      const answer = await redisExpire(
        target,
        "seconds" in parsed ? { key: name, ttl: parsed.seconds } : { key: name, at: parsed.at },
      )
      notify.success(`Expires in ${ttlWord(Math.ceil(answer.pttl / 1000))}`)
      onDone()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  const remove = async () => {
    setBusy("remove")
    setRefused("")
    try {
      await redisPersist(target, name)
      notify.success("Expiry removed")
      onDone()
    } catch (err) {
      setRefused(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <form
      className="space-y-3"
      onSubmit={(event) => {
        event.preventDefault()
        void set()
      }}
    >
      <Segments
        label="How the expiry is given"
        fill
        value={mode}
        onChange={(next) => {
          setMode(next)
          setRefused("")
          setTouched(false)
        }}
        options={[
          { value: "in", label: "Expires in" },
          { value: "at", label: "Expires at" },
        ]}
      />
      <div className="space-y-1.5">
        <Label htmlFor={field} className="text-body font-medium">
          {mode === "in" ? "Time to live" : "Date and time"}
        </Label>
        {mode === "in" ? (
          <>
            <Input
              id={field}
              value={span}
              spellCheck={false}
              autoComplete="off"
              placeholder="90, 5m, 2h, 7d"
              aria-invalid={Boolean(problem) || undefined}
              className="font-mono"
              onChange={(event) => {
                setSpan(event.target.value)
                setTouched(true)
                setRefused("")
              }}
            />
            <div role="group" aria-label="Common spans" className="flex flex-wrap gap-1">
              {PRESETS.map((preset) => (
                <FilterChip
                  key={preset}
                  selected={span.trim() === preset}
                  className="font-mono"
                  onClick={() => {
                    setSpan(preset)
                    setRefused("")
                  }}
                >
                  {preset}
                </FilterChip>
              ))}
            </div>
          </>
        ) : (
          <Input
            id={field}
            type="datetime-local"
            value={moment}
            min={localMoment(new Date())}
            max="9999-12-31T23:59"
            aria-invalid={Boolean(problem) || undefined}
            onChange={(event) => {
              setMoment(event.target.value)
              setTouched(true)
              setRefused("")
            }}
          />
        )}
        <div aria-live="polite">
          {problem ? (
            <FormNote tone="danger">{problem}</FormNote>
          ) : (
            parsed.ok &&
            "seconds" in parsed && (
              <FormNote>The key is removed {ttlWord(parsed.seconds)} from now.</FormNote>
            )
          )}
        </div>
      </div>
      <div className="flex items-center justify-between gap-2">
        {expires ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            pending={busy === "remove"}
            disabled={busy !== null}
            onClick={remove}
          >
            Remove expiry
          </Button>
        ) : (
          <span />
        )}
        <Button type="submit" size="sm" pending={busy === "set"} disabled={busy !== null}>
          Set expiry
        </Button>
      </div>
    </form>
  )
}
