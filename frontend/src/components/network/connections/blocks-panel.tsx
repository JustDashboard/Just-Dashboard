"use client"

import Link from "next/link"
import { del } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { blockStanding, type AddressBlock } from "@/lib/network-traffic"
import { useAuth } from "@/hooks/use-auth"
import { useNow } from "@/components/deploy/vocabulary"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { ErrorState } from "@/components/state"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { addressHref } from "@/components/network/connections/model"

/**
 * The blocks made from the connection table: active first, each with why,
 * until when, which incident it belongs to and whether the firewall still
 * lists its rule — a rule removed outside the dashboard reads as gone rather
 * than as protection. Lifting one removes exactly its rule, so it takes the
 * destructive capability like deleting a rule does. Each is two lines whatever
 * it carries, and its address opens that address's page.
 */
export function BlocksPanel({
  blocks,
  error,
  lastSuccess,
  refresh,
}: {
  blocks: AddressBlock[] | undefined
  error?: Error
  lastSuccess?: number
  refresh: () => void
}) {
  const { can } = useAuth()
  const now = useNow(30_000)
  const { confirm, dialog } = useConfirm()
  const active = (blocks ?? []).filter((b) => b.state === "active")
  if (!blocks && !error) return null
  if (blocks && blocks.length === 0) return null
  const lift = (b: AddressBlock) =>
    confirm({
      title: `Lift the block on ${b.address}`,
      confirmLabel: "Lift",
      description: (
        <p>
          Its deny rule is removed and {b.address} can reach this server again. The record of the
          block stays.
        </p>
      ),
      action: async () => {
        await del(`/firewall/blocks/${encodeURIComponent(b.id)}`)
        notify.success(`${b.address} is no longer blocked`)
        refresh()
      },
    })
  return (
    <Panel plain aria-label="Blocks from this page">
      <PanelHeader
        title="Blocks"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(active.length, "active block")}
          </span>
        }
      />
      <PanelBody flush>
        {blocks && (
          <NetworkReadWarning
            error={error}
            refresh={refresh}
            lastSuccess={lastSuccess}
            reading="blocks"
          />
        )}
        {!blocks && error ? (
          <ErrorState error={error} onRetry={refresh} />
        ) : (
          <RowList>
            {(blocks ?? []).slice(0, 20).map((b) => (
              <Row
                key={b.id}
                title={
                  <Link
                    href={addressHref(b.address)}
                    aria-label={`Open ${b.address}`}
                    className="rounded-sm font-mono focus-ring hover:underline"
                  >
                    {b.address}
                  </Link>
                }
                subtitle={
                  <>
                    {b.state === "active" && b.rulePresent === false && (
                      <span className="text-warning">its rule is no longer in the firewall · </span>
                    )}
                    {b.endError && (
                      <span className="text-warning">could not be lifted yet: {b.endError} · </span>
                    )}
                    {/* The link first: the line is cut at its end on a phone. */}
                    {b.incidentRunId && (
                      <>
                        <Link
                          href={`/network/runs?run=${encodeURIComponent(b.incidentRunId)}`}
                          className="rounded-sm underline underline-offset-4 focus-ring"
                        >
                          incident
                        </Link>
                        {" · "}
                      </>
                    )}
                    {b.reason} · {b.createdBy ? `by ${b.createdBy} ` : ""}
                    {relativeTime(b.createdAt)}
                  </>
                }
                trailing={
                  <span className="flex items-center gap-3">
                    <Status
                      tone={
                        b.state === "active"
                          ? b.endError || b.rulePresent === false
                            ? "warning"
                            : "danger"
                          : "stopped"
                      }
                      label={blockStanding(b, now)}
                    />
                    {b.state === "active" && can("destructive") && (
                      <Button
                        size="xs"
                        variant="outline"
                        aria-label={`Lift the block on ${b.address}`}
                        onClick={() => lift(b)}
                      >
                        Lift
                      </Button>
                    )}
                  </span>
                }
              />
            ))}
          </RowList>
        )}
      </PanelBody>
      {dialog}
    </Panel>
  )
}
