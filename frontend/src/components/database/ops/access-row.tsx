"use client"

/**
 * The list's width from which an account's tags stand beside its name rather
 * than under it. Measured on the list itself: the rail beside the page takes
 * its share of every window.
 */
export const TAGS_BESIDE_FROM = 640

/**
 * What an account is, on the line under its name where the list is too
 * narrow to hold it beside: a phone's card.
 *
 * Always one line of one height, whatever it holds. It was drawn only for the
 * accounts that had a tag, or drawn empty for the ones that had none, so two
 * cards of one list were two heights; and a fourth tag wrapped to a second
 * line. What does not fit is cut at the card's edge: the account's own page
 * has all of it.
 */
export function TagsUnder({ children }: { children: React.ReactNode }) {
  return (
    <span
      data-slot="account-tags"
      className="flex h-[1.125rem] min-w-0 items-center gap-x-2.5 overflow-hidden pl-12 whitespace-nowrap"
    >
      {children}
    </span>
  )
}

/** The quiet word a card's line holds when the account has no tag to show. */
export function QuietWord({ children }: { children: React.ReactNode }) {
  return <span className="text-hint text-muted-foreground">{children}</span>
}
