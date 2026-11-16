import type { IconProps } from "@/components/icons"
import { cn } from "@/lib/utils"

/**
 * Split terminal, as two panes in one window. The bar's split was a bare `+`
 * beside the window strip's `+` (New window) and the rail's `+` (New
 * session), so one glyph stood for three jobs; on a phone two of them sat
 * three slots apart. Drawn on the 16px grid the side-panel toggles use, one
 * pixel wide, so it reads as one of that family.
 */
export function SplitGlyph({ size = 16, title, className, ...props }: IconProps) {
  return (
    <svg
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      className={cn("size-4", className)}
      {...props}
    >
      {title ? <title>{title}</title> : null}
      <rect x="1.5" y="2.5" width="13" height="11" rx="2.5" />
      <path d="M8 2.5v11" />
    </svg>
  )
}
