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
    // 13px, the body size (§8). It was 12 while the page title was 20 and
    // the strip sat within two pixels of the title above it and the panel
    // titles below; with the title at 24 the strip has a rank of its own
    // again, and 12px chrome under a 24px title read as an afterthought.
    // The 2px top padding balances the 2px underline, so the label sits on the
    // strip's centre line with whatever else shares the strip.
    "inline-flex shrink-0 items-center gap-1.5 border-b-2 px-3 pt-0.5 text-body font-medium whitespace-nowrap transition-colors",
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
