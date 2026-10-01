"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Database, Plus, Route } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { DbFleet } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { sectionHref } from "@/components/database/engine"
import { sortFleet } from "@/components/database/fleet/fleet"
import { EngineMark, EnvironmentTag, ProtectedTag } from "@/components/database/kit"
import { useDatabases } from "@/components/database/shell/databases-context"

/**
 * The control center: every database this dashboard knows, each a press from
 * its own pages.
 *
 * One read (`GET /databases/fleet`) dials every connection, so each row says
 * what it is and whether it answers, worst first. A poll that fails keeps the
 * rows it has. What the page is for beyond that — everything found on this
 * machine, what needs a hand, what each one feeds — is the control center
 * area's to build on this list.
 */
export function ControlCenter() {
  const { admin, engineFor, newHref } = useDatabases()
  const fleet = usePoll((signal) => get<DbFleet>("/databases/fleet", undefined, signal), 30_000)
  const entries = useMemo(() => sortFleet(fleet.data?.connections ?? []), [fleet.data])

  const add = admin && (
    <Button size="sm" asChild>
      <Link href={newHref()}>
        <Plus className="size-4" />
        Add a database
      </Link>
    </Button>
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Apps" title="Databases" actions={entries.length > 0 && add} />
      {!fleet.data ? (
        fleet.error ? (
          <ErrorState error={fleet.error} onRetry={fleet.refresh} />
        ) : (
          <LoadingPanel plain />
        )
      ) : entries.length === 0 ? (
        <EmptyState
          icon={Database}
          title="No databases yet"
          description="Start one in a container, or connect one that already runs here or somewhere else."
          action={add}
        />
      ) : (
        <>
          {/* Rows you take, so they are lit cards on the page itself (§12):
              a frame around a list of cards would be a frame in a frame. */}
          <ChoiceList>
            {entries.map((entry) => {
              const engine = engineFor(entry)
              return (
                <ChoiceRow
                  key={entry.id}
                  href={sectionHref(entry.id)}
                  verb={`Open ${entry.name}`}
                  title={entry.name}
                  leading={<EngineMark engine={engine} size="sm" />}
                  description={[
                    engine.label,
                    entry.port ? `${entry.host}:${entry.port}` : entry.database,
                    plural(entry.objects, engine.nouns.object, engine.nouns.objects),
                  ].join(" · ")}
                  trailing={
                    <span className="flex items-center gap-3">
                      <EnvironmentTag environment={entry.environment} />
                      {entry.readOnly && <ProtectedTag />}
                      <Status
                        tone={entry.ok ? "running" : "danger"}
                        label={entry.ok ? "connected" : "unreachable"}
                      />
                    </span>
                  }
                />
              )
            })}
          </ChoiceList>
          <p className="text-hint text-muted-foreground">
            checked {relativeTime(fleet.data.checkedAt)}
            {fleet.error ? " · the last check did not finish" : ""}
          </p>
        </>
      )}
    </Page>
  )
}

/** What feeds what: every database and the things that read it, as one picture. */
export function DatabaseMap() {
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Databases" title="Map" />
      <EmptyState
        icon={Route}
        title="Map"
        description={
          <>
            Every database and what reads it, as one picture.{" "}
            <span className="font-mono text-hint">components/database/fleet</span>
          </>
        }
      />
    </Page>
  )
}
