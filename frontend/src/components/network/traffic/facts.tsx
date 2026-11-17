import { Fragment } from "react"
import { Fact, FactLine } from "@/components/fact-line"

/**
 * An identity line's facts in groups. From `sm` they read as one run with a
 * dot between each; on a phone, where the run does not fit, each group is a
 * row of its own and the dot between two groups is not drawn, so no line ends
 * on a separator with its fact on the next, and the line is as many rows
 * before its facts land as after.
 */

/** A row a phone keeps for a group of facts that has not been read yet. */
export function FactRowHeld() {
  return <span className="basis-full sm:hidden">&nbsp;</span>
}

export function FactGroups({ groups }: { groups: React.ReactNode[][] }) {
  const shown = groups
    .map((group) => group.filter((fact) => fact !== null && fact !== undefined && fact !== false))
    .filter((group) => group.length > 0)
  return (
    <>
      {shown.map((group, at) => (
        <Fragment key={at}>
          {at > 0 && (
            <span aria-hidden className="text-faint max-sm:hidden">
              ·
            </span>
          )}
          <FactLine className="max-sm:basis-full">
            {group.map((fact, i) => (
              <Fact key={i}>{fact}</Fact>
            ))}
          </FactLine>
        </Fragment>
      ))}
    </>
  )
}
