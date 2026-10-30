"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowLeftRight,
  ArrowRight,
  ClockRewind,
  Copy,
  Eye,
  EyeOff,
  Key,
  Plus,
  Trash,
} from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type {
  DataCopy,
  DataCopyComparison,
  DataCopyConnection,
  DeploymentRelease,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Modal } from "@/components/modal"
import { SidePanel } from "@/components/side-panel"
import { Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FormFact, FormFacts, FormNote } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { VerbActions, type Verb } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"
import { useNow } from "@/components/deploy/vocabulary"
import {
  DATA_COPY_HOURS,
  NOT_POINTED,
  comparisonSummary,
  copiesPollMs,
  copyLine,
  copyStatus,
  copyBusy,
  dataPointWords,
  orderCopies,
  restorablePoints,
  restoreInPlaceLink,
  restoreInPlaceWords,
  tableDiffers,
  tableDifference,
  utcMoment,
} from "@/components/deploy/data-rollback-model"

/**
 * Data-aware rollback's surfaces (`deploy/data_rollback.go`): restoring each
 * linked database as it stood when a release went live into a throwaway copy
 * beside production, the copies while they last, and the two ways on from one
 * — comparing it with the live database, and restoring production in place
 * through the route that already owns that (and asks its own confirmation).
 *
 * The third way, pointing the rolled-back release at the copy, is not here on
 * purpose and every surface says so: a copy is removed when it expires, and a
 * production that ran against it would lose its database at that moment.
 */

/** The environment's copies, read every five seconds while one is moving. A failed read is silent. */
export function useDataCopies({
  projectId,
  environmentId,
  run,
  enabled = true,
}: {
  projectId: number
  environmentId: number
  /** Only the copies that waited for this run. */
  run?: number
  enabled?: boolean
}) {
  const [busy, setBusy] = useState(false)
  const poll = usePoll(
    async (signal) => {
      const copies = await get<DataCopy[]>(
        `/deploy/${projectId}/environments/${environmentId}/data-copies`,
        run ? { run } : undefined,
        signal,
      )
      const list = Array.isArray(copies) ? copies : []
      setBusy(copiesPollMs(list) < 30_000)
      return list
    },
    busy ? 5_000 : 30_000,
    [projectId, environmentId, run],
    { enabled: enabled && projectId > 0 && environmentId > 0 },
  )
  // Every existing page that never asked for copies answers this route with
  // an error in a mocked or older server; nothing here is worth a banner.
  return { copies: poll.error ? [] : orderCopies(poll.data), refresh: poll.refresh }
}

function copiesBase(projectId: number, environmentId: number) {
  return `/deploy/${projectId}/environments/${environmentId}/data-copies`
}

/**
 * "Restore data as of this release": a confirmation that names what will be
 * restored and from where, and says what it does not touch. Ordinary, with the
 * brand face — it makes a copy beside production and changes nothing there.
 */
export function RestoreDataDialog({
  open,
  onOpenChange,
  projectId,
  environmentId,
  release,
  onStarted,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: number
  environmentId: number
  release?: DeploymentRelease
  onStarted?: (copies: DataCopy[]) => void
}) {
  const [busy, setBusy] = useState(false)
  const points = release?.dataPoints ?? []
  const restorable = restorablePoints(points)
  const skipped = points.filter((point) => point.kind === "none")

  const restore = async () => {
    if (!release) return
    setBusy(true)
    try {
      const copies = await post<DataCopy[]>(
        `/deploy/${projectId}/environments/${environmentId}/releases/${release.id}/data-copies`,
        {},
      )
      const names = restorable.map((point) => point.name).join(", ")
      notify.success(`Restoring ${names} as of release #${release.number}`, {
        description: `The copy runs beside production for ${DATA_COPY_HOURS} hours.`,
      })
      onStarted?.(Array.isArray(copies) ? copies : [])
      onOpenChange(false)
    } catch (error) {
      notify.error("Could not restore the data", error)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={release ? `Restore data as of release #${release.number}` : "Restore data"}
      description="Restore each linked database as it was when this release went live into a side copy."
      footer={
        <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button disabled={restorable.length === 0 || busy} pending={busy} onClick={restore}>
            {restorable.length > 1 ? "Restore copies" : "Restore copy"}
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        <ul className="space-y-3" aria-label="Databases restored">
          {restorable.map((point) => (
            <li key={point.connectionId} className="flex min-w-0 items-center gap-3">
              <ProductLogo id={point.engine} size="sm" />
              <div className="min-w-0 flex-1 space-y-0.5">
                <p className="truncate text-body font-medium">{point.name}</p>
                <p className="truncate text-hint text-muted-foreground">{dataPointWords(point)}</p>
              </div>
            </li>
          ))}
          {skipped.map((point) => (
            <li key={point.connectionId} className="flex min-w-0 items-center gap-3 opacity-70">
              <ProductLogo id={point.engine} size="sm" />
              <div className="min-w-0 flex-1 space-y-0.5">
                <p className="truncate text-body font-medium">{point.name} · not restored</p>
                <p className="text-hint text-muted-foreground">{dataPointWords(point)}</p>
              </div>
            </li>
          ))}
        </ul>
        <Notice title="Production's data is not touched">
          Each database is restored into a throwaway container beside production, on the
          deployment&apos;s network, with credentials of its own. It is removed after{" "}
          {DATA_COPY_HOURS} hours unless you keep it longer.
        </Notice>
        <FormNote>{NOT_POINTED}</FormNote>
      </div>
    </Modal>
  )
}

/**
 * The environment's copies: what each is a copy of and how long it has, and
 * what can be done with it. Drawn only while there is one to speak of.
 */
export function DataCopiesPanel({
  projectId,
  environmentId,
  copies,
  refresh,
}: {
  projectId: number
  environmentId: number
  copies: DataCopy[]
  refresh: () => void
}) {
  const router = useRouter()
  const { can } = useAuth()
  const now = useNow(30_000)
  const { confirm, dialog } = useConfirm()
  const [connection, setConnection] = useState<DataCopy>()
  const [comparing, setComparing] = useState<DataCopy>()
  const [inPlace, setInPlace] = useState<DataCopy>()
  const [working, setWorking] = useState<number>()
  const base = copiesBase(projectId, environmentId)
  if (copies.length === 0) return null

  const extend = async (copy: DataCopy) => {
    setWorking(copy.id)
    try {
      const kept = await post<DataCopy>(`${base}/${copy.id}/extend`, { hours: 1 })
      notify.success(`The copy of ${copy.name} is kept an hour longer`, {
        description: kept?.expiresAt
          ? `It is removed at ${utcMoment(kept.expiresAt, false)}.`
          : undefined,
      })
      refresh()
    } catch (error) {
      notify.error("Could not keep the copy longer", error)
    } finally {
      setWorking(undefined)
    }
  }

  const remove = (copy: DataCopy) =>
    confirm({
      title: "Remove copy",
      confirmLabel: "Remove copy",
      workingLabel: `Removing the copy of ${copy.name}…`,
      subject: {
        mark: <ProductLogo id={copy.engine} size="sm" />,
        name: copy.name,
        facts: <span>{copyLine(copy, now)}</span>,
      },
      description: (
        <p>
          {copy.state === "waiting"
            ? "The copy is not restored when the rollback finishes."
            : "The copy and its volume are removed now."}{" "}
          Production&apos;s database is not touched.
        </p>
      ),
      action: async () => {
        await del(`${base}/${copy.id}`)
        refresh()
      },
    })

  // Everything a copy can be asked is a session's and an administrator's: its
  // credentials, production's tables, its life. A reader sees the copies.
  const admin = can("system.admin")
  const verbsOf = (copy: DataCopy): Verb[] => {
    const verbs: Verb[] = []
    if (admin && copy.state === "ready") {
      verbs.push(
        { key: "url", label: "Connection URL…", icon: Key, run: () => setConnection(copy) },
        {
          key: "compare",
          label: "Compare with live",
          icon: ArrowLeftRight,
          run: () => setComparing(copy),
        },
        {
          key: "extend",
          label: "Keep 1 more hour",
          icon: Plus,
          progressive: "Keeping…",
          disabled: working === copy.id,
          run: () => void extend(copy),
        },
      )
    }
    if (admin && copy.state === "ready" && restoreInPlaceLink(copy.connectionId, copy.source)) {
      verbs.push({
        key: "in-place",
        label: "Restore in place to this point…",
        icon: ClockRewind,
        run: () => setInPlace(copy),
      })
    }
    if (admin && (copy.state === "ready" || copy.state === "waiting")) {
      verbs.push({
        key: "remove",
        label: copy.state === "waiting" ? "Cancel copy" : "Remove now",
        icon: Trash,
        danger: true,
        run: () => remove(copy),
      })
    }
    if (copy.afterRunId) {
      verbs.push({
        key: "run",
        label: "Open the rollback",
        icon: ArrowRight,
        run: () => router.push(`/deploy/${projectId}/runs/${copy.afterRunId}`),
      })
    }
    return verbs
  }

  return (
    <div id="data-copies" className="scroll-mt-6">
      <Panel plain>
        <PanelHeader
          title="Data copies"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {copies.filter((copy) => copy.state === "ready").length} ready
            </span>
          }
        />
        <PanelBody flush>
          <RowList aria-label="Data copies">
            {copies.map((copy) => {
              const status = copyStatus(copy)
              return (
                <Row
                  key={copy.id}
                  leading={<ProductLogo id={copy.engine} size="sm" />}
                  title={copy.name}
                  subtitle={copyLine(copy, now)}
                  trailing={
                    <>
                      <Status tone={status.tone} label={status.label} live={copyBusy(copy.state)} />
                      <VerbActions
                        dim
                        verbs={verbsOf(copy)}
                        menuLabel={`Actions for the copy of ${copy.name}`}
                      />
                    </>
                  }
                />
              )
            })}
          </RowList>
          <FormNote className="pt-3">{NOT_POINTED}</FormNote>
        </PanelBody>
      </Panel>
      {connection && (
        <CopyConnectionDialog
          copy={connection}
          base={base}
          onOpenChange={(open) => !open && setConnection(undefined)}
        />
      )}
      <DataCopyComparisonSheet
        open={Boolean(comparing)}
        onOpenChange={(open) => !open && setComparing(undefined)}
        base={base}
        copy={comparing}
      />
      {inPlace && (
        <RestoreInPlaceDialog
          copy={inPlace}
          onOpenChange={(open) => !open && setInPlace(undefined)}
        />
      )}
      {dialog}
    </div>
  )
}

/**
 * The copy's connection URL, which carries its generated password: read by a
 * session alone and audited, so it is fetched on request — revealed, or copied
 * without ever being drawn on a screen somebody may be sharing.
 */
function CopyConnectionDialog({
  copy,
  base,
  onOpenChange,
}: {
  copy: DataCopy
  base: string
  onOpenChange: (open: boolean) => void
}) {
  const [shown, setShown] = useState<DataCopyConnection>()
  const [busy, setBusy] = useState<"reveal" | "copy">()
  const read = () => get<DataCopyConnection>(`${base}/${copy.id}/url`)

  const reveal = async () => {
    setBusy("reveal")
    try {
      setShown(await read())
    } catch (error) {
      notify.error("Could not read the copy's connection URL", error)
    } finally {
      setBusy(undefined)
    }
  }
  const copyURL = async () => {
    setBusy("copy")
    try {
      const answer = shown ?? (await read())
      await copyText(answer.url, "Connection URL copied")
    } catch (error) {
      notify.error("Could not copy the connection URL", error)
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`Connect to the copy of ${copy.name}`}
      description="The copy's connection URL, with its own generated password."
      footer={
        <Button variant="outline" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      <div className="space-y-4">
        <FormFacts>
          {copy.host && (
            <FormFact label="Host" mono>
              {copy.host}
            </FormFact>
          )}
          {copy.port ? (
            <FormFact label="Port" mono>
              {copy.port}
            </FormFact>
          ) : null}
          <FormFact label="Database" mono>
            {copy.database}
          </FormFact>
        </FormFacts>
        <Well className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 flex-1 break-all" aria-label="Connection URL">
            {shown ? shown.url : "••••••••••••••••••••••••"}
          </span>
        </Well>
        <div className="flex flex-wrap gap-2">
          {shown ? (
            <Button variant="outline" size="sm" onClick={() => setShown(undefined)}>
              <EyeOff className="size-3.5" /> Hide
            </Button>
          ) : (
            <Button variant="outline" size="sm" pending={busy === "reveal"} onClick={reveal}>
              <Eye className="size-3.5" /> Reveal
            </Button>
          )}
          <Button variant="outline" size="sm" pending={busy === "copy"} onClick={copyURL}>
            <Copy className="size-3.5" /> Copy
          </Button>
        </div>
        <FormNote>
          It answers by this name on the deployment&apos;s network
          {copy.network ? ` (${copy.network})` : ""}, to a container there or a console you open on
          it; nothing publishes it outside this server. {NOT_POINTED}
        </FormNote>
      </div>
    </Modal>
  )
}

/**
 * The copy's tables beside the live database's, with their row counts: the
 * first question after a bad release is what it changed, and a table that
 * gained or lost rows since the release went live is where to look.
 */
export function DataCopyComparisonSheet({
  open,
  onOpenChange,
  base,
  copy,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  base: string
  copy?: DataCopy
}) {
  const result = usePoll(
    (signal) => get<DataCopyComparison>(`${base}/${copy?.id}/comparison`, undefined, signal),
    0,
    [base, copy?.id],
    { enabled: open && Boolean(copy) },
  )
  const comparison = result.data
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      title={copy ? `${copy.name}: copy and live` : "Copy and live"}
      description="Each table's row count in the copy beside the live database."
      width="lg"
    >
      {result.error ? (
        <ErrorState error={result.error} />
      ) : !comparison ? (
        <LoadingRows />
      ) : (
        <div className="space-y-4">
          <FormFacts>
            <FormFact label="Database" mono>
              {comparison.database}
            </FormFact>
            {copy && <span>as of release #{copy.releaseNumber}</span>}
            <span className="font-medium text-foreground">{comparisonSummary(comparison)}</span>
          </FormFacts>
          {comparison.copyError && (
            <Notice tone="warning" title="The copy could not be read">
              {comparison.copyError}
            </Notice>
          )}
          {comparison.liveError && (
            <Notice tone="warning" title="The live database could not be read">
              {comparison.liveError}
            </Notice>
          )}
          {comparison.tables.length === 0 ? (
            <EmptyNote className="px-0 text-left">Neither side has a table to compare.</EmptyNote>
          ) : (
            <Table aria-label="Tables, copy and live">
              <TableHeader>
                <TableRow>
                  <TableHead>Table</TableHead>
                  <TableHead className="text-right">Copy</TableHead>
                  <TableHead className="text-right">Live</TableHead>
                  <TableHead className="text-right">Difference</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {comparison.tables.map((table) => {
                  const differs = tableDiffers(table)
                  return (
                    <TableRow key={`${table.schema}.${table.name}`}>
                      <TableCell className="font-mono">
                        {table.schema ? `${table.schema}.${table.name}` : table.name}
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {table.copyRows === null ? "—" : table.copyRows.toLocaleString()}
                      </TableCell>
                      <TableCell className="numeric text-right">
                        {table.liveRows === null ? "—" : table.liveRows.toLocaleString()}
                      </TableCell>
                      <TableCell
                        className={cn(
                          "text-right",
                          differs ? "font-medium text-(--git-modified)" : "text-muted-foreground",
                        )}
                      >
                        {tableDifference(table)}
                        {table.estimated ? " · estimated" : ""}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
          {comparison.truncated && (
            <FormNote>Only the first tables were compared; the database has more.</FormNote>
          )}
        </div>
      )}
    </SidePanel>
  )
}

/**
 * Restoring production in place is not this feature's to do: it hands the
 * operator to the route that already owns it, with the recorded point chosen,
 * where it is destructive and asks its own confirmation.
 */
function RestoreInPlaceDialog({
  copy,
  onOpenChange,
}: {
  copy: DataCopy
  onOpenChange: (open: boolean) => void
}) {
  const href = restoreInPlaceLink(copy.connectionId, copy.source)
  const pitr = copy.source.kind === "pitr"
  return (
    <Modal
      open
      onOpenChange={onOpenChange}
      title={`Restore ${copy.name} in place`}
      description="Restore production's database to the point this copy was made from."
      footer={
        <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {href && (
            <Button asChild>
              <Link href={href}>
                {pitr ? "Open its Backups page" : "Open the backup run"}
                <ArrowRight className="size-3.5" />
              </Link>
            </Button>
          )}
        </div>
      }
    >
      <div className="space-y-4">
        <div className="flex min-w-0 items-center gap-3">
          <ProductLogo id={copy.engine} size="sm" />
          <div className="min-w-0 space-y-0.5">
            <p className="truncate text-body font-medium">{copy.name}</p>
            <p className="truncate text-hint text-muted-foreground">
              {dataPointWords({ ...copy.source })}
            </p>
          </div>
        </div>
        <Notice tone="warning" title="This replaces production's data">
          {restoreInPlaceWords(copy.source.kind)}
        </Notice>
      </div>
    </Modal>
  )
}

/**
 * The copies a rollback asked for, on that rollback's page: what each is
 * doing, linked to the Deployments page where the copies are worked with.
 */
export function RunDataCopies({
  projectId,
  environmentId,
  runId,
  enabled,
}: {
  projectId: number
  environmentId: number
  runId: number
  enabled: boolean
}) {
  const now = useNow(30_000)
  const { copies } = useDataCopies({ projectId, environmentId, run: runId, enabled })
  const mine = copies.filter((copy) => copy.afterRunId === runId)
  if (mine.length === 0) return null
  return (
    <Panel plain aria-label="Side copies">
      <PanelHeader title="Side copy of the database" />
      <PanelBody flush>
        <RowList aria-label="Side copies of this rollback">
          {mine.map((copy) => {
            const status = copyStatus(copy)
            return (
              <Row
                key={copy.id}
                href={`/deploy/${projectId}/deployments#data-copies`}
                leading={<ProductLogo id={copy.engine} size="sm" />}
                title={copy.name}
                subtitle={copyLine(copy, now)}
                trailing={
                  <Status tone={status.tone} label={status.label} live={copyBusy(copy.state)} />
                }
              />
            )
          })}
        </RowList>
      </PanelBody>
    </Panel>
  )
}
