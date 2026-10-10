import type { ReactNode } from "react"

/** One text column for an option and its metadata, independent of menu semantics. */
export function MenuItemText({ children, hint }: { children: ReactNode; hint?: ReactNode }) {
  return (
    <span data-slot="menu-item-text" className="flex min-w-0 flex-1 flex-col gap-0.5">
      <span className="flex min-w-0 items-center gap-2 break-words whitespace-normal">
        {children}
      </span>
      {hint && (
        <span
          data-slot="menu-item-hint"
          className="text-hint break-words whitespace-normal text-muted-foreground"
        >
          {hint}
        </span>
      )}
    </span>
  )
}
