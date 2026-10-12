"use client"

import { useMemo } from "react"
import { usePoll } from "@/hooks/use-poll"
import { FindingList, type Finding } from "@/components/finding-list"
import { RowsSkeleton, Block, BlockLink, Read, staleOf } from "@/components/database/home/blocks"
import { concernParams, ranked, type Concern } from "@/components/database/home/attention"
import {
  adviceDestination,
  databaseAdviceTitle,
  needsDatabaseAttention,
} from "@/components/database/home/advice"
import { read } from "@/components/database/home/read"
import type { DbAdvice, DbAdviseReport } from "@/components/database/home/types"
import { useDatabase } from "@/components/database/shell/database-context"
import { useAuth } from "@/hooks/use-auth"

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
  const { id, engine, href, goto, readOnly } = useDatabase()
  const { can } = useAuth()
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
  const relevant = useMemo(
    () => (report.data?.findings ?? []).filter(needsDatabaseAttention),
    [report.data],
  )
  const total = relevant.length
  const silences = report.data?.silences ?? []

  const findings = useMemo<Finding[]>(
    () =>
      relevant.slice(0, SHOWN).map((advice, index) => ({
        // The advisor's ids name the check, and one check can report twice.
        id: `${advice.id}:${index}`,
        level: advice.level,
        title: databaseAdviceTitle(advice),
        detail: advice.detail,
        advice: advice.advice,
        extra: <Objects advice={advice} />,
        action: (() => {
          const destination = adviceDestination(advice, {
            id,
            has: (section) => engine.has(section),
            canQuery: can("service.control") && !readOnly,
          })
          return destination
            ? {
                label: destination.label,
                onClick: () => goto(destination.section, destination.params),
              }
            : { label: "Inspect finding", onClick: () => goto("advisor") }
        })(),
      })),
    [relevant, id, engine, goto, can, readOnly],
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
              emphasize
              findings={findings}
              emptyLabel={`No priority issues found in ${data.tablesChecked.toLocaleString()} ${data.tablesChecked === 1 ? engine.nouns.object : engine.nouns.objects} and the completed server checks`}
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

/**
 * The same block for an engine whose server has no advisor: what the home's
 * own readings say has crossed a line. The list is derived from answers the
 * page already holds, so it has no read of its own to fail — `pending` is
 * those answers not having landed yet.
 */
export function ConcernAttention({
  concerns,
  pending,
}: {
  concerns: readonly Concern[]
  pending: boolean
}) {
  const { engine, goto } = useDatabase()
  const findings = useMemo<Finding[]>(
    () =>
      ranked(concerns.filter(needsDatabaseAttention)).map((concern) => {
        const section = concern.section && engine.section(concern.section)
        return {
          id: concern.id,
          level: concern.level,
          title: concern.title,
          detail: concern.detail,
          advice: concern.advice,
          action: section
            ? {
                label: `Open ${section.title}`,
                onClick: () => goto(section.id, concernParams(concern, engine.can)),
              }
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
          <FindingList
            emphasize
            findings={findings}
            emptyLabel={
              pending
                ? "Reading the current evidence…"
                : "No priority issues found in the available readings"
            }
          />
        </div>
      )}
    </Block>
  )
}
