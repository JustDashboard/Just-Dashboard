"use client"

import { useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import { FindingList, type Finding } from "@/components/finding-list"
import type { SectionId } from "@/components/database/engine"
import { RowsSkeleton, Block, BlockLink, Read, staleOf } from "@/components/database/home/blocks"
import { ranked, type Concern } from "@/components/database/home/attention"
import { read } from "@/components/database/home/read"
import type { DbAdvice, DbAdviseReport } from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { databasePlace } from "@/components/database/shell/routes"

/** How many findings the home shows before sending the reader to the page that holds them all. */
const SHOWN = 5

/**
 * What the server's advisor found, worst first: the top of the report the
 * Advisor page holds in full. Each finding opens to what was measured, what
 * to do, and the one action that does it — the page of this database the
 * advisor points at, or the fix as a statement handed to Query for review.
 * Nothing is run from here.
 */
export function AdvisorAttention() {
  const { id, engine, href, goto } = useDatabase()
  const report = usePoll(
    (signal) =>
      read<DbAdviseReport>(
        `/databases/${id}/advisor`,
        (answer) => Array.isArray(answer.findings),
        undefined,
        signal,
      ),
    300_000,
    [id],
  )
  const total = report.data?.findings.length ?? 0
  const silences = report.data?.silences ?? []

  const findings = useMemo<Finding[]>(
    () =>
      (report.data?.findings ?? []).slice(0, SHOWN).map((advice, index) => ({
        // The advisor's ids name the check, and one check can report twice.
        id: `${advice.id}:${index}`,
        level: advice.level,
        title: advice.title,
        detail: advice.detail,
        advice: advice.advice,
        meta: advice.category,
        extra: <Objects advice={advice} />,
        action: adviceAction(advice, {
          id,
          canQuery: engine.has("query"),
          title: (section) => engine.section(section)?.title,
          open: (section) => goto(section),
          review: (sql) => goto("query", { sql }),
        }),
      })),
    [report.data, id, engine, goto],
  )

  return (
    <Block
      title="Needs attention"
      stale={staleOf(report)}
      actions={
        <BlockLink href={href("advisor")}>
          {total > SHOWN ? `All ${total} findings` : "Advisor"}
        </BlockLink>
      }
    >
      <Read poll={report} what="the advisor's findings" skeleton={<RowsSkeleton mark={false} />}>
        {(data) => (
          <>
            <FindingList
              findings={findings}
              emptyLabel={`The advisor checked ${data.tablesChecked.toLocaleString()} ${data.tablesChecked === 1 ? engine.nouns.object : engine.nouns.objects} and the server's settings, and found nothing to fix`}
            />
            {silences.length > 0 && (
              <p className="mt-3 text-hint text-muted-foreground">
                {silences.length === 1
                  ? "One check could not be made: "
                  : `${silences.length} checks could not be made. The first: `}
                {silences[0]}
              </p>
            )}
          </>
        )}
      </Read>
    </Block>
  )
}

/** The objects a finding names, as the literals they are. */
function Objects({ advice }: { advice: DbAdvice }) {
  const names = advice.objects ?? []
  if (names.length === 0) return null
  const shown = names.slice(0, 6)
  return (
    <p className="font-mono text-hint wrap-anywhere text-foreground/80">
      {shown.join(", ")}
      {names.length > shown.length ? ` and ${names.length - shown.length} more` : ""}
    </p>
  )
}

function adviceAction(
  advice: DbAdvice,
  tools: {
    id: number
    canQuery: boolean
    title: (section: SectionId) => string | undefined
    open: (section: SectionId) => void
    review: (sql: string) => void
  },
): Finding["action"] {
  // The advisor links to pages of the dashboard by path. Only one that is a
  // page of this database is followed, and by its name rather than its text:
  // the path came from the server, and a press here must not leave for
  // somewhere it merely spelled.
  const place = advice.link ? databasePlace(advice.link) : null
  const title = place?.id === tools.id ? tools.title(place.section) : undefined
  if (place && title) {
    return { label: `Open ${title}`, onClick: () => tools.open(place.section) }
  }
  if (advice.sql && tools.canQuery) {
    const sql = advice.sql
    return { label: "Review the fix in Query", onClick: () => tools.review(sql) }
  }
  return undefined
}

/**
 * The same block for an engine whose server has no advisor: what the home's
 * own readings say has crossed a line. The list is derived from answers the
 * page already holds, so it has no read of its own to fail — `pending` is
 * those answers not having landed yet.
 */
export function ConcernAttention({
  concerns,
  pending,
  quiet,
}: {
  concerns: readonly Concern[]
  pending: boolean
  /** What to say when nothing needs anybody. */
  quiet: string
}) {
  const { engine, goto } = useDatabase()
  const findings = useMemo<Finding[]>(
    () =>
      ranked(concerns).map((concern) => {
        const section = concern.section && engine.section(concern.section)
        return {
          id: concern.id,
          level: concern.level,
          title: concern.title,
          detail: concern.detail,
          advice: concern.advice,
          meta: concern.about,
          action: section
            ? { label: `Open ${section.title}`, onClick: () => goto(section.id) }
            : undefined,
        }
      }),
    [concerns, engine, goto],
  )
  return (
    <Block title="Needs attention">
      {pending && findings.length === 0 ? (
        <RowsSkeleton mark={false} />
      ) : (
        <div className="animate-rise">
          <FindingList findings={findings} emptyLabel={quiet} />
        </div>
      )}
    </Block>
  )
}
