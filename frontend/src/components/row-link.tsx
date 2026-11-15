import { cn } from "@/lib/utils"

/**
 * A table cell's primary column, rendered as the row's way into its detail
 * view — default text colour, underline only on hover.
 *
 * `Button`'s `variant="link"` doesn't fit here: it colours the text primary
 * and always underlines, which reads as navigation rather than "this is the
 * name of the thing the row is about". Thirteen tables across the app were
 * each retyping the same three classes for exactly this button before this
 * existed.
 */
export function RowLink({
  onClick,
  mono,
  title,
  className,
  children,
}: {
  onClick: () => void
  mono?: boolean
  /** For a title long enough to ellipse — the secondary line beside it has one. */
  title?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={cn(
        // `truncate` alone does nothing on a button: it is inline-block, so it
        // sizes to its text and a process whose name is a 200-character command
        // line paints straight across Owner, CPU and Memory. `max-w-full` binds
        // it to the title column's cap, `min-w-0` lets it shrink where the row
        // lays its title out with flex.
        "max-w-full min-w-0 truncate text-left text-body font-medium hover:underline",
        mono && "font-mono text-xs",
        className,
      )}
    >
      {children}
    </button>
  )
}
