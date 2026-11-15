"use client"

import { cn } from "@/lib/utils"
import { useField } from "@/components/form"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * A closed choice of two to five short words, as one segmented control: a
 * Python release, the stage a variable reaches, a health check's kind and
 * phase, an HTTP method, a chart's window. The settings forms drew each of
 * these as a Select — a closed set asked for as if it were open, behind a
 * press that hid the other answers — or as free text that accepted "3.9".
 *
 * It is the only single-choice `ToggleGroup`: fifty-five raw ones each
 * re-wrote the guard below and picked their own label size, and eight had no
 * name. A group that may be emptied on purpose is not a segmented control and
 * stays a raw group.
 *
 * Radix lets a second press on the chosen segment clear it; here there is
 * always an answer, so an empty value is not passed on as one. `onReselect`
 * hears that second press, for a segment that opens something (a typed range).
 *
 * It always has a name. Inside a `Field` it is named by the field's visible
 * label (a `<label for>` cannot name a group); elsewhere `label` names it.
 *
 * The segments that are not the choice step back to the muted ink. Pressed
 * is `bg-accent` (§3's selection), and on its own that is one step of ground
 * against its neighbours — a comparison the reader has to make, not a mark.
 */

/**
 * The three heights, one per place it stands: `inline` in a toolbar or a row
 * (24px, 32 under a finger, with the press box made up to 44), `sm` beside a
 * section's title or over a chart (32/40), `field` on a form's line beside an
 * input (36/44).
 */
export type SegmentsSize = "inline" | "sm" | "field"

// Each sets the item's `--control-h` and its coarse step; `ToggleGroupItem`
// draws `h-(--control-h)`. Whole class names, for Tailwind.
const SIZE: Record<SegmentsSize, { group: "sm" | "default"; item: string }> = {
  inline: {
    group: "sm",
    item: "px-2 [--control-h:1.5rem] pointer-coarse:[--control-h:2rem]",
  },
  sm: { group: "sm", item: "" },
  field: { group: "default", item: "" },
}

export type SegmentOption<T extends string> = {
  value: T
  label: React.ReactNode
  /** Drawn before the label at the size the segment's height takes. */
  icon?: React.ReactNode
  mono?: boolean
  /** An option can be shown and refused, as a choice the reader's role does not have. */
  disabled?: boolean
}

export function Segments<T extends string>({
  label,
  value,
  options,
  onChange,
  onReselect,
  size = "sm",
  disabled,
  fill,
  id,
  className,
}: {
  /**
   * The group's accessible name: "Python version", "Health check 1 kind".
   * Inside a `Field` the field's label names it and this may be left out.
   */
  label?: string
  /** The choice; a value that is not one of the options shows none chosen. */
  value: T | ""
  options: SegmentOption<T>[]
  onChange: (value: T) => void
  /** The chosen segment was pressed again. */
  onReselect?: (value: T) => void
  size?: SegmentsSize
  disabled?: boolean
  /** Share the whole width between the segments, for a field's own line. */
  fill?: boolean
  id?: string
  className?: string
}) {
  const field = useField()
  // A label given here is the name the callers' specs and readers know; the
  // field's label stands in only when there is none.
  const named = label
    ? { "aria-label": label }
    : field
      ? { "aria-labelledby": field.labelId }
      : undefined
  if (!named && process.env.NODE_ENV !== "production") {
    console.error("Segments needs a label, or a Field around it to name it.")
  }
  const sized = SIZE[size]
  return (
    <ToggleGroup
      id={id}
      type="single"
      variant="outline"
      size={sized.group}
      {...named}
      aria-describedby={field?.describedBy}
      value={value}
      disabled={disabled}
      onValueChange={(next) => {
        if (next) onChange(next as T)
        else if (value) onReselect?.(value)
      }}
      className={cn("max-w-full", fill && "w-full", className)}
    >
      {options.map((option) => (
        <ToggleGroupItem
          key={option.value}
          value={option.value}
          disabled={option.disabled}
          className={cn(
            "min-w-0 text-xs data-[state=off]:text-muted-foreground",
            // A count inside the chosen segment takes its ink (`ChipCount`).
            "data-[state=on]:[--count-ink:currentColor]",
            sized.item,
            fill && "flex-1",
            option.mono && "font-mono",
          )}
        >
          {option.icon}
          {option.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
