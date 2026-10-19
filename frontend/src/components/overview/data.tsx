"use client"

import { useMemo } from "react"
import Link from "next/link"
import { CloudUpload, Play, Servers } from "@/components/icons"
import { bytes, plural } from "@/lib/format"
import type { BackupJob, DbFleet } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import { LastRun, nextLabel } from "@/components/backups/job-card"
import { runJobNow } from "@/components/backups/job-verbs"
import { destinationProduct } from "@/components/backups/marks"
import { engineFor, sectionHref } from "@/components/database/engine"
import { sortFleet } from "@/components/database/fleet/fleet"
import { EngineMark, EnvironmentTag } from "@/components/database/kit"
import { DatabaseStatusMark, fleetStatus } from "@/components/database/shell/status"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { DimActions, IconAction } from "@/components/icon-action"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { EmptyState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"
import { backupOrder, failedBackups } from "./order"
import { SectionLink } from "./section-link"

/** The worst few of each, the way to the rest at the end of the head. */
const SHOWN = 4

/**
 * The databases on the server, worst first — one that does not answer before
 * one that is stopped, before the rest by name, the control center's order —
 * each its engine and version, what it holds and whether it answers. A row
 * opens the database's home.
 */
export function DatabasesPanel({ read }: { read: PollState<DbFleet> }) {
  const { can } = useAuth()
  const { data, error, loading } = read
  const entries = useMemo(() => sortFleet(data?.connections ?? []), [data])
  const down = entries.filter(
    (entry) => !entry.ok && entry.state !== "stopped" && entry.state !== "paused",
  ).length

  return (
    <Panel plain>
      <PanelHeader
        title="Databases"
        actions={
          <div className="flex h-8 items-center gap-3 text-hint">
            {entries.length > 0 && (
              <span className="numeric text-muted-foreground">
                {plural(entries.length, "database")}
                {down > 0 && <span className="text-destructive"> · {down} not answering</span>}
              </span>
            )}
            <SectionLink href="/databases">Control center</SectionLink>
          </div>
        }
      />
      <PanelBody flush>
        {loading && !data ? (
          <LoadingRows rows={3} className="[&>*]:h-14 [&>*]:rounded-xl" />
        ) : !data ? (
          <p className="py-2 text-body text-muted-foreground">
            {error ? "The databases could not be read." : "Nothing to show."}
          </p>
        ) : entries.length === 0 ? (
          <EmptyState
            mark={<ProductLogos ids={["postgresql", "mysql", "redis"]} size="md" />}
            title="No databases yet"
            description="Start one here or connect to one that is already running."
            action={
              can("system.admin") && (
                <Button size="sm" variant="outline" asChild>
                  <Link href="/databases/new">Add a database</Link>
                </Button>
              )
            }
            className="py-8"
          />
        ) : (
          <ChoiceList aria-label="Databases" className="animate-rise">
            {entries.slice(0, SHOWN).map((entry) => {
              const engine = engineFor(entry)
              const version = entry.versionNumber ? ` ${entry.versionNumber}` : ""
              return (
                <ChoiceRow
                  key={entry.id}
                  href={sectionHref(entry.id)}
                  verb={`Open ${entry.name}`}
                  leading={<EngineMark engine={engine} size="sm" />}
                  title={entry.name}
                  description={
                    entry.ok ? (
                      <>
                        {engine.label}
                        {version}
                        {entry.sizesKnown && ` · ${bytes(entry.bytes)}`}
                        {` · ${plural(entry.objects, entry.objectWord.replace(/s$/, ""), entry.objectWord)}`}
                      </>
                    ) : (
                      <span title={entry.error}>
                        {engine.label}
                        {version}
                        {entry.error && ` · ${entry.error}`}
                      </span>
                    )
                  }
                  trailing={
                    <>
                      <EnvironmentTag
                        environment={entry.environment}
                        className="hidden max-w-24 sm:inline-flex"
                      />
                      <DatabaseStatusMark status={fleetStatus(entry)} />
                    </>
                  }
                />
              )
            })}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Every backup job by how its last run went, a failure first: the tile this
 * replaced said "Last run OK" off the newest run on the server, so a nightly
 * job that failed was hidden by an hourly one that succeeded after it. Each
 * row carries its next run, and a reader who may start one has Back up now
 * beside it rather than a page away.
 */
export function BackupsPanel({ read }: { read: PollState<BackupJob[]> }) {
  const { can } = useAuth()
  const { data, error, loading, refresh } = read
  const jobs = useMemo(() => backupOrder(data ?? []), [data])
  const failed = failedBackups(jobs)

  return (
    <Panel plain>
      <PanelHeader
        title="Backups"
        actions={
          <div className="flex h-8 items-center gap-3 text-hint">
            {jobs.length > 0 && (
              <span className="numeric text-muted-foreground">
                {plural(jobs.length, "job")}
                {failed > 0 && <span className="text-destructive"> · {failed} failed</span>}
              </span>
            )}
            <SectionLink href="/backups">Backups</SectionLink>
          </div>
        }
      />
      <PanelBody flush>
        {loading && !data ? (
          <LoadingRows rows={3} className="[&>*]:h-14 [&>*]:rounded-xl" />
        ) : !data ? (
          <p className="py-2 text-body text-muted-foreground">
            {error ? "The backup jobs could not be read." : "Nothing to show."}
          </p>
        ) : jobs.length === 0 ? (
          <EmptyState
            mark={<ProductLogos ids={["aws", "backblaze", "cloudflare"]} size="md" />}
            title="Nothing is backed up"
            description="A job archives folders, volumes and database dumps on a schedule, here or to a bucket."
            action={
              can("system.admin") && (
                <Button size="sm" variant="outline" asChild>
                  <Link href="/backups">Set up a backup</Link>
                </Button>
              )
            }
            className="py-8"
          />
        ) : (
          <ChoiceList aria-label="Backup jobs" className="animate-rise">
            {jobs.slice(0, SHOWN).map((job) => (
              <ChoiceRow
                key={job.id}
                href={`/backups/${job.id}`}
                verb={`Open ${job.name}`}
                busy={job.lastRun?.status === "running"}
                leading={
                  <ProductLogo
                    id={destinationProduct(job)}
                    fallback={job.targetKind === "local" ? Servers : CloudUpload}
                    size="sm"
                  />
                }
                title={job.name}
                description={nextLabel(job)}
                trailing={<LastRun job={job} />}
                actions={
                  can("service.control") &&
                  job.lastRun?.status !== "running" && (
                    <DimActions>
                      <IconAction
                        label={`Back up ${job.name} now`}
                        onClick={() => void runJobNow(job, refresh)}
                      >
                        <Play />
                      </IconAction>
                    </DimActions>
                  )
                }
              />
            ))}
          </ChoiceList>
        )}
      </PanelBody>
    </Panel>
  )
}
