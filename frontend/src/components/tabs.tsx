import { cn } from "@/lib/utils"

/**
 * One interaction language for every switcher in the product.
 *
 * The problem this fixes is small and everywhere: selection and keyboard focus
 * were drawn the same way. A filter chip that is *active* and one that merely
 * has the caret on it both got the accent outline, so a keyboard user could not
 * tell which filters were applied, and a mouse user tabbing away found the
 * selection had apparently moved.
 *
 * The three states are given three different mechanisms, which is the only way
 * they stay distinguishable:
 *
 *   selected — filled surface and foreground text. A property of the data.
 *   hover    — a faint background. A property of the pointer.
 *   focus    — a ring, drawn outside the control. A property of the keyboard.
 *
 * A control can be all three at once and still be readable, which is the test.
 *
 * These lived under `components/docker/` while Docker was the only section with
 * a switcher inside a panel. Five other sections had since grown one each, so
 * they are here instead.
 *
 * What is *not* here any more is the strip that switched between the pages of
 * a section. Six of them existed, one per section layout, and each said the
 * same thing the sidebar was saying two hundred pixels to the left. The rail
 * drills into a section now and lists its pages there, so a page is a page:
 * everything left in this file switches between views of one page, never
 * between pages.
 */

/**
 * The underlined-tab look, for a switcher between views of the page you are
 * already on — the log console's live feed against its search, the packages
 * page's installed against its updates. Written once so "which one am I on" is
 * the same mark wherever it is asked.
 */
export function tabClasses(selected: boolean | undefined, height: string) {
  return cn(
    // 12px, not 13. A strip of views is chrome — it names them and then gets
    // out of the way — and at body size it was competing with the page title
    // above it and with the panel titles below, three ranks of text set within
    // two pixels of each other. Dropping a step puts the strip where it belongs
    // in the ladder: quieter than the content it switches between, still
    // comfortably above the 11px hint row.
    "inline-flex shrink-0 items-center gap-1.5 border-b-2 px-3 text-body font-medium whitespace-nowrap transition-colors",
    height,
    "focus-ring-inset",
    // The underline is the brand blue: the tab says which view you are
    // looking at, which is what the hue is for. The label stays ink — it is not
    // a command.
    selected
      ? "border-brand text-foreground"
      : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
  )
}

/**
 * A filter chip: which subset of the rows is shown.
 *
 * Squared rather than fully rounded, because a pill is the shape this product
 * reserves for nothing at all — see `components/tag.tsx`.
 *
 * Selected is `bg-accent` with a hairline, which is the same mark the terminal
 * rail puts on the active session and `ToggleGroup` puts on the pressed item.
 * It used to be a primary tint, so a row of filters and a row of toggles two
 * panels apart disagreed about what "chosen" looks like — and the primary tint
 * is the one thing on the page reserved for a *command*.
 */
export function FilterChip({
  selected,
  className,
  ...props
}: React.ComponentProps<"button"> & { selected?: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      data-selected={selected || undefined}
      className={cn(
        "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-hint font-medium whitespace-nowrap transition-colors",
        "focus-ring",
        selected
          ? "border-hairline bg-accent text-foreground"
          : "border-transparent text-muted-foreground hover:bg-muted/60 hover:text-foreground",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A count beside a chip's or a tab's label. Muted inside an unselected control
 * and inherited inside a selected one, so the number never competes with the
 * word — and plain tabular text rather than a second small container, because
 * a chip with a chip inside it is exactly the stacking this pass removed.
 */
export function ChipCount({ children }: { children: React.ReactNode }) {
  return <span className="numeric text-micro tabular-nums opacity-60">{children}</span>
}
