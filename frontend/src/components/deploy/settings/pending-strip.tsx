"use client"

import Link from "next/link"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Disclosure } from "@/components/form"
import { Tag } from "@/components/tag"
import { CHANGE_TONE, ChangeTag } from "@/components/deploy/vocabulary"

const SHOWN = 6

/** One thing a form holds that is not in force yet. */
export type PendingItem = {
  /** The glyph of the page or section that holds it, or the product it names. */
  mark?: React.ReactNode
  name: React.ReactNode
  /** The name is a literal — a variable, a path — and is set in mono. */
  mono?: boolean
  /**
   * What happened to it: `added`, `changed` and `removed` take git's colours,
   * as the release comparison marks the same change once it ships; any other
   * word is a plain tag.
   */
  state: string
  /** Where it is changed: the item is a link there. */
  href?: string
  /** It is on the page the reader is on, so it is named rather than linked. */
  current?: boolean
}

/**
 * The strip at the head of a settings column that says what is saved and not
 * in force yet: a headline (a `Status`, lit while it is going live), a line
 * of detail, and the pending items, each a way to where it is changed. It has
 * no button: the page's command is the one that takes the items live (§3).
 *
 * `deploy/settings/pending-changes.tsx` feeds it a deployment's configuration;
 * any other settings form gives it its own headline and items. On a phone the
 * items fold under the headline — each is a 36px row to press, and three of
 * them put the page's first field below the fold.
 */
export function PendingStrip({
  label,
  headline,
  detail,
  items,
  fold = "What changes",
  className,
}: {
  /** The strip's landmark name: "Changes not live yet". */
  label: string
  headline: React.ReactNode
  detail?: React.ReactNode
  items: PendingItem[]
  /** The phone's fold over the items: "What will go live". */
  fold?: string
  className?: string
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const shown = items.slice(0, SHOWN)
  const list = (
    <ul className="flex w-full min-w-0 flex-row flex-wrap items-center gap-x-4 gap-y-1 sm:w-auto">
      {shown.map((item, index) => (
        <li key={index} className="min-w-0">
          <PendingLine item={item} />
        </li>
      ))}
      {items.length > SHOWN && (
        <li className="text-body text-muted-foreground">and {items.length - SHOWN} more</li>
      )}
    </ul>
  )

  return (
    <section
      aria-label={label}
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-hairline pb-4",
        className,
      )}
    >
      {headline}
      {detail && <p className="min-w-0 text-body text-muted-foreground">{detail}</p>}
      {items.length > 0 &&
        (wide ? (
          list
        ) : (
          <Disclosure quiet summary={fold} className="w-full">
            {list}
          </Disclosure>
        ))}
    </section>
  )
}

/** One item: its mark, its name, and what happened to it, marked as git marks a file. */
function PendingLine({ item }: { item: PendingItem }) {
  const body = (
    <>
      {item.mark}
      <span className={cn("min-w-0 truncate", item.mono && "font-mono")}>{item.name}</span>
      {Object.hasOwn(CHANGE_TONE, item.state) ? (
        <ChangeTag change={item.state as keyof typeof CHANGE_TONE} />
      ) : (
        <Tag>{item.state}</Tag>
      )}
    </>
  )
  const className = "flex min-h-9 min-w-0 items-center gap-1.5 text-body sm:min-h-0"
  if (item.current || !item.href)
    return (
      <span aria-current={item.current ? "page" : undefined} className={className}>
        {body}
      </span>
    )
  return (
    <Link href={item.href} className={cn(className, "rounded-sm focus-ring hover:underline")}>
      {body}
    </Link>
  )
}
