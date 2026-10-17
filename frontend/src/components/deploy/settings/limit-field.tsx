"use client"

import { get } from "@/lib/api"
import type { ContainerHistory } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Field } from "@/components/form"
import { Meter, utilisationTone } from "@/components/meter"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"

/**
 * A limit, and — while the container has an hour of history — how close it
 * came to it: a meter of its peak against the limit being typed, so a limit
 * set below what it already uses is seen before it is saved rather than found
 * in an out-of-memory kill. A project's Runtime and a database's Resources
 * draw the same field; `facts` are the lines a caller has to add under it —
 * what the machine has, what the engine keeps for itself.
 */
export function LimitField({
  id,
  label,
  unit,
  step,
  value,
  max,
  peak,
  peakLabel,
  info,
  facts,
  error,
  disabled,
  onChange,
}: {
  id: string
  label: string
  unit: string
  step: number
  value: number
  max?: number
  peak?: number
  peakLabel: (peak: number) => string
  info: string
  facts?: React.ReactNode
  error?: string
  disabled: boolean
  onChange: (value: number) => void
}) {
  const pct = peak !== undefined && value > 0 ? (peak / value) * 100 : undefined
  // Keyed on which line it is rather than on its text, so "no limit" rises as
  // the field is cleared and a peak rises when its hour lands, but a figure
  // that only moves is not re-announced.
  const reading =
    peak !== undefined ? (
      <span key="peak" className="block animate-rise">
        peak {peakLabel(peak)} in the last hour
      </span>
    ) : value === 0 ? (
      <span key="unlimited" className="block animate-rise">
        no limit
      </span>
    ) : undefined
  return (
    <Field
      label={label}
      htmlFor={id}
      info={info}
      hint={
        reading || facts ? (
          <>
            {reading}
            {facts}
          </>
        ) : undefined
      }
      error={error}
    >
      <InputGroup>
        <InputGroupInput
          id={id}
          type="number"
          min={0}
          max={max}
          step={step}
          value={value}
          readOnly={disabled}
          aria-invalid={Boolean(error)}
          // No native spinner: the field steps by arrow key like every other
          // input in the product, and the arrows crowded the unit beside it.
          className="[appearance:textfield] font-mono [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
          onChange={(event) => onChange(Math.max(0, Number(event.target.value)))}
        />
        <InputGroupAddon align="inline-end">
          <InputGroupText>{unit}</InputGroupText>
        </InputGroupAddon>
      </InputGroup>
      {pct !== undefined && (
        <Meter
          value={pct}
          tone={utilisationTone(pct)}
          size="thin"
          // Named for the peak, not the limit, so the field keeps the label to itself.
          label={`${label.replace(/ limit$/, "")} peak in the last hour`}
          className="animate-rise"
        />
      )}
    </Field>
  )
}

/** A container's last hour: its peaks, for the limits drawn against them. */
export type ContainerPeaks =
  | { state: "none" | "loading" }
  | { state: "ready"; memory: number; cpus: number; processes: number }

/**
 * One hour of a container's history, polled each minute for the whole page:
 * the same recorded history the Overview's usage tiles read, reduced to the
 * three peaks the limits are set against. A poll that fails after one landed
 * keeps the peaks it has, rather than dropping every meter on the page and
 * raising them again a minute later.
 */
export function useContainerPeaks(containerId: string | undefined): ContainerPeaks {
  const history = usePoll(
    (signal) =>
      get<ContainerHistory>(
        `/docker/containers/${encodeURIComponent(containerId ?? "")}/stats/history`,
        { points: 60 },
        signal,
      ),
    60000,
    [containerId],
    { enabled: Boolean(containerId) },
  )
  if (!containerId || (history.error && !history.data)) return { state: "none" }
  if (!history.data) return { state: "loading" }
  const points = history.data.points
  if (points.length === 0) return { state: "none" }
  return {
    state: "ready",
    memory: Math.max(...points.map((point) => point.memBytesPeak)),
    // Docker's CPU percentage counts one core as 100.
    cpus: Math.max(0, ...points.map((point) => point.cpuPeak ?? 0)) / 100,
    processes: Math.max(...points.map((point) => point.pids)),
  }
}
