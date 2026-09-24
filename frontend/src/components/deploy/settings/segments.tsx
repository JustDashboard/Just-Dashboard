"use client"

import { cn } from "@/lib/utils"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * A closed choice of two to five short words, as one segmented control: a
 * Python release, the stage a variable reaches, a health check's kind and
 * phase, an HTTP method. The settings forms drew each of these as a Select —
 * a closed set asked for as if it were open, behind a press that hid the
 * other answers — or as free text that accepted "3.9".
 *
 * Radix lets a second press on the chosen segment clear it; here there is
 * always an answer, so an empty value is ignored rather than passed on.
 *
 * The segments that are not the choice step back to the muted ink. Pressed
 * is `bg-accent` (§3's selection), and on its own that is one step of ground
 * against its neighbours — a comparison the reader has to make, not a mark.
 */
export function Segments<T extends string>({
  label,
  value,
  options,
  onChange,
  disabled,
  fill,
  id,
  className,
}: {
  /** The group's accessible name: "Python version", "Health check 1 kind". */
  label: string
  value: T
  options: { value: T; label: React.ReactNode; mono?: boolean }[]
  onChange: (value: T) => void
  disabled?: boolean
  /** Share the whole width between the segments, for a field's own line. */
  fill?: boolean
  id?: string
  className?: string
}) {
  return (
    <ToggleGroup
      id={id}
      type="single"
      variant="outline"
      size="sm"
      aria-label={label}
      value={value}
      disabled={disabled}
      onValueChange={(next) => {
        if (next) onChange(next as T)
      }}
      className={cn("max-w-full", fill && "w-full", className)}
    >
      {options.map((option) => (
        <ToggleGroupItem
          key={option.value}
          value={option.value}
          className={cn(
            "min-w-0 text-xs data-[state=off]:text-muted-foreground",
            fill && "flex-1",
            option.mono && "font-mono",
          )}
        >
          {option.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}
