"use client"

import { useMemo } from "react"
import { describeCron, isValidCron, nextCronRun, nextCronRunsIn } from "@/lib/cron"
import { calendarDate, clock } from "@/lib/format"
import { Field, FieldRow, FormNote } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * A schedule built from the words people use for one — every day, at three —
 * rather than typed as five fields, for every form that asks when something
 * runs: a backup job and a deployment's schedule. It was written twice before
 * it was shared, and two builders of the same thing are two ways for "every
 * week" to mean different expressions (§4).
 */

/**
 * The presets. Nothing here runs every minute, and there is no "when the
 * server boots" — a backup or a deploy that fires on every reboot is a
 * surprise, not a policy.
 */
export const SCHEDULE_PRESETS = [
  { key: "manual", label: "Only when I run it" },
  { key: "hourly", label: "Every hour" },
  { key: "daily", label: "Every day" },
  { key: "weekly", label: "Every week" },
  { key: "monthly", label: "Every month" },
  { key: "custom", label: "Custom cron expression" },
] as const

export type SchedulePreset = (typeof SCHEDULE_PRESETS)[number]["key"]

export type ScheduleFields = {
  preset: SchedulePreset
  time: string
  weekday: string
  monthDay: string
  custom: string
}

export const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
]

/** What an existing expression looks like in the builder's fields. */
export function scheduleFields(schedule: string): ScheduleFields {
  const out: ScheduleFields = {
    preset: "daily",
    time: "03:00",
    weekday: "1",
    monthDay: "1",
    custom: "",
  }
  const trimmed = schedule.trim()
  if (!trimmed) return { ...out, preset: "manual" }
  const fields = trimmed.split(/\s+/)
  if (fields.length !== 5) return { ...out, preset: "custom", custom: trimmed }
  const [m, h, dom, month, dow] = fields
  const plainMinute = /^\d{1,2}$/.test(m)
  const plainHour = /^\d{1,2}$/.test(h)
  if (plainMinute && plainHour) out.time = `${h.padStart(2, "0")}:${m.padStart(2, "0")}`
  if (month !== "*") return { ...out, preset: "custom", custom: trimmed }
  if (plainMinute && h === "*" && dom === "*" && dow === "*") return { ...out, preset: "hourly" }
  if (plainMinute && plainHour && dom === "*" && dow === "*") return { ...out, preset: "daily" }
  if (plainMinute && plainHour && dom === "*" && /^[0-7]$/.test(dow)) {
    return { ...out, preset: "weekly", weekday: dow === "7" ? "0" : dow }
  }
  if (plainMinute && plainHour && /^\d{1,2}$/.test(dom) && dow === "*") {
    return { ...out, preset: "monthly", monthDay: dom }
  }
  return { ...out, preset: "custom", custom: trimmed }
}

/** The expression the builder's fields describe; empty for a manual job. */
export function scheduleExpression(fields: ScheduleFields): string {
  const [hh, mm] = fields.time.split(":").map((v) => Number(v))
  const minute = Number.isFinite(mm) ? mm : 0
  const hour = Number.isFinite(hh) ? hh : 0
  switch (fields.preset) {
    case "manual":
      return ""
    case "hourly":
      return `${minute} * * * *`
    case "daily":
      return `${minute} ${hour} * * *`
    case "weekly":
      return `${minute} ${hour} * * ${fields.weekday}`
    case "monthly":
      return `${minute} ${hour} ${fields.monthDay} * *`
    case "custom":
      return fields.custom.trim()
  }
}

export function scheduleValid(expression: string): boolean {
  return expression === "" || isValidCron(expression)
}

/**
 * The next moments a schedule fires: in `timeZone` when the schedule names
 * one, otherwise in this browser's clock.
 */
export function schedulePreview(expression: string, count = 3, timeZone?: string): Date[] {
  if (!expression || !isValidCron(expression)) return []
  if (timeZone) return nextCronRunsIn(expression, timeZone, count)
  const out: Date[] = []
  let from = new Date()
  for (let i = 0; i < count; i++) {
    const next = nextCronRun(expression, from)
    if (!next) break
    out.push(next)
    from = next
  }
  return out
}

/**
 * A moment in the preview, on the clock the schedule is read in. A zoned one
 * is spelled as a deploy schedule's runs are — "Thu 25 Sep 03:00", no year,
 * no seconds — so the form and the sheet it opens from read the same.
 */
function moment(at: Date, timeZone?: string): string {
  if (!timeZone) return `${calendarDate(at.toISOString())} ${clock(at.toISOString())}`
  const day = at.toLocaleDateString("en-GB", {
    weekday: "short",
    day: "numeric",
    month: "short",
    timeZone,
  })
  const time = at.toLocaleTimeString("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone,
  })
  return `${day} ${time}`
}

/**
 * A schedule as a sentence with the moments it fires under it, so "every
 * day at 3" and the expression the server stores cannot drift apart.
 *
 * The fields' ids are `idPrefix` plus `-preset`, `-time`, `-weekday`, `-day`
 * and `-cron`. A schedule that names its zone passes `timeZone`, and its
 * times are read and previewed there rather than on the server's clock.
 * `manual` offers "Only when I run it", for a thing that can also be run by
 * hand — a backup job; a deploy schedule is nothing but its time.
 */
export function ScheduleBuilder({
  fields,
  onChange,
  idPrefix,
  timeZone,
  label = "Repeats",
  manual,
}: {
  fields: ScheduleFields
  onChange: (fields: ScheduleFields) => void
  idPrefix: string
  timeZone?: string
  label?: string
  manual?: boolean
}) {
  const expression = scheduleExpression(fields)
  const valid = scheduleValid(expression)
  const preview = useMemo(() => schedulePreview(expression, 3, timeZone), [expression, timeZone])
  const set = (patch: Partial<ScheduleFields>) => onChange({ ...fields, ...patch })
  const timed =
    fields.preset === "daily" || fields.preset === "weekly" || fields.preset === "monthly"
  return (
    <div className="space-y-3">
      <FieldRow columns={3}>
        <Field label={label} htmlFor={`${idPrefix}-preset`}>
          <Select value={fields.preset} onValueChange={(v) => set({ preset: v as SchedulePreset })}>
            <SelectTrigger id={`${idPrefix}-preset`} size="sm" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SCHEDULE_PRESETS.filter((p) => manual || p.key !== "manual").map((p) => (
                <SelectItem key={p.key} value={p.key}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {(timed || fields.preset === "hourly") && (
          <Field
            label={fields.preset === "hourly" ? "At minute" : "At"}
            htmlFor={`${idPrefix}-time`}
            // The field draws its clock in the browser's own convention, which
            // may be twelve-hour, so the hint says only whose clock it is.
            hint={timeZone ? `In ${timeZone}.` : "Server time."}
          >
            {fields.preset === "hourly" ? (
              <Input
                id={`${idPrefix}-time`}
                type="number"
                min={0}
                max={59}
                value={fields.time.split(":")[1] ?? "0"}
                onChange={(e) => set({ time: `00:${e.target.value.padStart(2, "0")}` })}
                className="font-mono"
              />
            ) : (
              <Input
                id={`${idPrefix}-time`}
                type="time"
                value={fields.time}
                onChange={(e) => set({ time: e.target.value })}
                className="font-mono"
              />
            )}
          </Field>
        )}
        {fields.preset === "weekly" && (
          <Field label="On" htmlFor={`${idPrefix}-weekday`}>
            <Select value={fields.weekday} onValueChange={(v) => set({ weekday: v })}>
              <SelectTrigger id={`${idPrefix}-weekday`} size="sm" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {WEEKDAYS.map((d, i) => (
                  <SelectItem key={d} value={String(i)}>
                    {d}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        {fields.preset === "monthly" && (
          <Field label="On day" htmlFor={`${idPrefix}-day`} hint="Months without that day skip it.">
            <Input
              id={`${idPrefix}-day`}
              type="number"
              min={1}
              max={31}
              value={fields.monthDay}
              onChange={(e) => set({ monthDay: e.target.value })}
              className="font-mono"
            />
          </Field>
        )}
      </FieldRow>
      {fields.preset === "custom" && (
        <Field
          label="Cron expression"
          htmlFor={`${idPrefix}-cron`}
          hint="Five fields: minute, hour, day of month, month, day of week."
          error={!valid ? "That is not an expression cron understands." : undefined}
        >
          <Input
            id={`${idPrefix}-cron`}
            value={fields.custom}
            onChange={(e) => set({ custom: e.target.value })}
            placeholder="0 3 * * *"
            className="font-mono"
          />
        </Field>
      )}
      <FormNote>
        {expression === "" ? (
          "Runs only when you press Run now."
        ) : valid ? (
          <>
            {describeCron(expression)}
            {preview.length > 0 && (
              <span className="text-muted-foreground/80">
                {" · next "}
                {preview.map((d) => moment(d, timeZone)).join(", ")}
              </span>
            )}
          </>
        ) : (
          "Fix the expression to see when it fires."
        )}
      </FormNote>
    </div>
  )
}
