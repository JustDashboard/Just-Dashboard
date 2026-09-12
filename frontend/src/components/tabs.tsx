"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"

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
 * they are here instead and the section strip they all hand-rolled is here too.
 */

/**
 * The underlined-tab look, shared by the three things that wear it: the sticky
 * section strip, a tab inside a panel, and the anchor form of that tab. Written
 * once so "which one am I on" is the same mark at every level.
 */
function tabClasses(selected: boolean | undefined, height: string) {
  return cn(
    "inline-flex shrink-0 items-center gap-1.5 border-b-2 px-3 text-body font-medium whitespace-nowrap transition-colors",
    height,
    "focus-ring-inset",
    // The underline is the brand blue: a section tab says where you are, which
    // is what the hue is for. The label stays ink — the tab is not a command.
    selected
      ? "border-brand text-foreground"
      : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
  )
}

export type SectionTab = { title: string; href: string }

/**
 * The strip under the top bar that switches between the pages of one section —
 * Docker's seven, Security's eight, the proxy's six.
 *
 * It existed five times, once per section layout, and had drifted: three of the
 * five had lost the focus ring, two had lost the hover border, and the active
 * rule was written two different ways. The exact-match rule for the section
 * root is the one piece of logic in here, and it was the same copy in all five.
 */
export function SectionNav({ tabs, root }: { tabs: SectionTab[]; root?: string }) {
  const pathname = usePathname()
  const base = root ?? tabs[0]?.href

  return (
    <div className="sticky top-0 z-10 border-b border-hairline bg-background/85 backdrop-blur-md">
      <nav
        aria-label="Section"
        className="mx-auto flex w-full max-w-[1600px] gap-1 overflow-x-auto px-4 md:px-6"
      >
        {tabs.map((tab) => {
          const active = tab.href === base ? pathname === base : pathname.startsWith(tab.href)
          return (
            <Link
              key={tab.href}
              href={tab.href}
              aria-current={active ? "page" : undefined}
              className={tabClasses(active, "h-11")}
            >
              {tab.title}
            </Link>
          )
        })}
      </nav>
    </div>
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
 * The section strip at panel height, for the switchers whose tabs are routes
 * but which sit inside a page header rather than under the top bar — the
 * databases section, whose strip has to carry the selected connection in the
 * query string and so cannot use `SectionNav`'s plain hrefs.
 */
export function TabLink({
  selected,
  className,
  ...props
}: React.ComponentProps<typeof Link> & { selected?: boolean }) {
  return (
    <Link
      aria-current={selected ? "page" : undefined}
      className={cn(tabClasses(selected, "h-9"), className)}
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
