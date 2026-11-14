import { cn } from "@/lib/utils"
import { flagOf } from "@/components/security/intrusion-model"
import { Address } from "@/components/security/marks"

/**
 * A list read down its columns, one line to a row, on hairlines: a blocked
 * address, a decision, an alert. They were framed tables whose cells stacked
 * a second line under the first wherever a row had more to say (a country, a
 * category, "held 2 times"), so rows differed in height and the tables ran
 * off a phone's edge.
 *
 * Every row here is one height whatever it holds: `columns` is the grid every
 * row shares where the list has room for it, so a column lines up down the
 * list without a table, and a cell with nothing to say keeps its place.
 * Where it has not (a phone, a half-width column) a row is two fixed lines:
 * `LineCell main` holds the first, and what it passes as `under` is the
 * second, where the columns that have no room (`wide`) say the one thing
 * worth keeping. Which of the two a list draws follows the list's own width,
 * not the window's: the same list is half a row on a laptop and a whole one
 * on a tablet. `roomy` is for a list whose columns need the wider measure.
 */
export function LineList({
  columns,
  label,
  roomy,
  className,
  children,
}: {
  /** The grid's columns where there is room, as a `grid-template-columns` value. */
  columns: string
  label: string
  /** The columns need 56rem rather than 42rem before they are drawn. */
  roomy?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <ul
      data-slot="line-list"
      data-roomy={roomy || undefined}
      aria-label={label}
      style={{ "--line-columns": columns } as React.CSSProperties}
      className={cn(
        "group/lines @container flex min-w-0 animate-rise flex-col divide-y divide-hairline",
        className,
      )}
    >
      {children}
    </ul>
  )
}

export function LineRow({ className, children, ...props }: React.ComponentProps<"li">) {
  return (
    <li
      data-slot="line-row"
      className={cn(
        "grid h-14 min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4",
        WIDE_ROW,
        className,
      )}
      {...props}
    >
      {children}
    </li>
  )
}

// A list is wide from 42rem of its own width, or from 56rem where it is
// `roomy`. Written out for both, because a class is found by its whole name.
const WIDE_ROW =
  "group-[:not([data-roomy])]/lines:@2xl:h-11 group-[:not([data-roomy])]/lines:@2xl:grid-cols-[var(--line-columns)] group-data-[roomy]/lines:@4xl:h-11 group-data-[roomy]/lines:@4xl:grid-cols-[var(--line-columns)]"
const WIDE_ONLY =
  "hidden group-[:not([data-roomy])]/lines:@2xl:flex group-data-[roomy]/lines:@4xl:flex"
/** Who owns an address: said only where the row has a line to itself. */
const OWNER =
  "hidden group-[:not([data-roomy])]/lines:@2xl:block group-data-[roomy]/lines:@4xl:block"
/** Drawn only where the list is narrow: the second line, a figure that has a column when wide. */
export const WIDE_GONE =
  "group-[:not([data-roomy])]/lines:@2xl:hidden group-data-[roomy]/lines:@4xl:hidden"

/**
 * One cell of a row. `main` is the cell a phone keeps, with `under` as its
 * second line there; `wide` is a column drawn only where there is a grid;
 * `end` is the cell at the row's far end, kept at every width.
 */
export function LineCell({
  main,
  wide,
  end,
  under,
  title,
  className,
  children,
}: {
  main?: boolean
  wide?: boolean
  end?: boolean
  /** The row's second line on a phone. Only read with `main`. */
  under?: React.ReactNode
  title?: string
  className?: string
  children?: React.ReactNode
}) {
  if (main)
    return (
      <div className={cn("min-w-0", className)}>
        <div className="flex min-w-0 items-center gap-2 text-body leading-5">{children}</div>
        <div className={cn("truncate text-hint leading-5 text-muted-foreground", WIDE_GONE)}>
          {under ?? "\u00a0"}
        </div>
      </div>
    )
  return (
    <div
      title={title}
      className={cn(
        "min-w-0 items-center gap-2 text-body",
        wide ? WIDE_ONLY : "flex",
        end && "justify-end",
        className,
      )}
    >
      {children}
    </div>
  )
}

/**
 * An address or a range on one line, with the network it is on before it and
 * where it is from after it: the flag, then the network's owner where the
 * line has room.
 */
export function AddressLine({
  value,
  country,
  owner,
}: {
  value: string
  country?: string
  owner?: string
}) {
  const [base, bits] = value.split("/")
  const flag = flagOf(country)
  return (
    <>
      <span className="inline-flex min-w-0 shrink-0 items-center font-medium">
        <Address ip={base} />
        {bits && <span className="font-mono">/{bits}</span>}
      </span>
      {flag && country && (
        <span
          className="inline-flex shrink-0 items-center gap-1 text-hint text-muted-foreground"
          title={country.toUpperCase()}
        >
          <span aria-hidden>{flag}</span>
          <span>{country.toUpperCase()}</span>
        </span>
      )}
      {owner && (
        <span
          className={cn("min-w-0 truncate text-hint text-muted-foreground", OWNER)}
          title={owner}
        >
          {owner}
        </span>
      )}
    </>
  )
}
