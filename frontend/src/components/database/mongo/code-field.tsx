"use client"

import { cn } from "@/lib/utils"
import { Textarea } from "@/components/ui/textarea"

/**
 * A field that holds a piece of query text: a filter, a stage, an update.
 *
 * It is a plain text area in the mono face that grows with what is typed,
 * rather than a code editor per field: a query bar has seven of these and a
 * pipeline one per stage, and each would be an editor's worth of weight for
 * two lines of text. Tab moves on to the next field, as it does everywhere
 * else in a form; Ctrl or Cmd with Enter runs what the field belongs to.
 */
export function CodeField({
  value,
  onChange,
  onSubmit,
  invalid,
  dense,
  className,
  onKeyDown,
  ...props
}: Omit<React.ComponentProps<"textarea">, "value" | "onChange"> & {
  value: string
  onChange: (value: string) => void
  /** Ctrl+Enter / Cmd+Enter, and plain Enter in a `dense` one-line field. */
  onSubmit?: () => void
  invalid?: boolean
  /** One line that grows: a field of the query bar. Enter runs; Shift+Enter breaks the line. */
  dense?: boolean
}) {
  return (
    <Textarea
      value={value}
      spellCheck={false}
      autoComplete="off"
      autoCapitalize="off"
      autoCorrect="off"
      aria-invalid={invalid || undefined}
      rows={dense ? 1 : undefined}
      className={cn(
        // 16px on a phone, where a smaller field makes the browser zoom in and stay there.
        "max-h-64 resize-none bg-surface-sunken font-mono text-base leading-relaxed shadow-none sm:text-xs dark:bg-surface-sunken",
        dense ? "min-h-8 px-2 py-1.5" : "min-h-20 px-2.5 py-2",
        className,
      )}
      onChange={(event) => onChange(event.target.value)}
      onKeyDown={(event) => {
        onKeyDown?.(event)
        if (event.defaultPrevented || event.key !== "Enter" || !onSubmit) return
        if (event.metaKey || event.ctrlKey || (dense && !event.shiftKey)) {
          event.preventDefault()
          onSubmit()
        }
      }}
      {...props}
    />
  )
}
