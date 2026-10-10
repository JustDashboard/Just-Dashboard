"use client"

import { useState } from "react"
import { del, get, put } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { NetworkLink } from "@/lib/types"
import { quotaReading, type InterfaceQuota } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Meter } from "@/components/meter"
import { Status } from "@/components/status-dot"
import { EmptyNote, ErrorState } from "@/components/state"
import { Field } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { parseBudget } from "@/components/network/vpn/record-logic"
import { useConfirm } from "@/components/confirm-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const DIRECTION_WORD: Record<InterfaceQuota["direction"], string> = {
  both: "in and out",
  rx: "in",
  tx: "out",
}

/**
 * Transfer budgets per device: a threshold the recorder's exact interval
 * counters are measured against, per UTC day, week or month. Like a WireGuard
 * peer's budget it alerts and never limits — a background cut could take the
 * operator's own way in with it. Each says what its figure is made of: the
 * bytes the counters measured, what older rows add as an estimate from their
 * mean rate, and how much of the period was recorded at all, because a
 * dashboard that was stopped counted nothing while it was.
 */
export function TrafficBudgets({ links }: { links: NetworkLink[] | undefined }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  const budgets = usePoll<InterfaceQuota[]>(
    (signal) => get("/network/traffic/quotas", undefined, signal),
    60_000,
  )
  const { confirm, dialog } = useConfirm()
  const clear = (q: InterfaceQuota) =>
    confirm({
      title: `Clear the budget on ${q.iface}`,
      confirmLabel: "Clear",
      description: <p>Its recorded traffic stays; only the threshold and its alert go.</p>,
      action: async () => {
        await del(`/network/traffic/quotas/${encodeURIComponent(q.iface)}`)
        notify.success(`${q.iface}'s budget cleared`)
        budgets.refresh()
      },
    })
  const list = budgets.data ?? []
  return (
    <Panel plain aria-label="Transfer budgets">
      <PanelHeader
        title="Transfer budgets"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(list.length, "budget")} · alerts, never limits
          </span>
        }
      />
      <PanelBody className="flex flex-col gap-4">
        {budgets.data && (
          <NetworkReadWarning
            error={budgets.error}
            refresh={budgets.refresh}
            lastSuccess={budgets.lastSuccess}
            reading="transfer budgets"
          />
        )}
        {budgets.error && !budgets.data ? (
          <ErrorState error={budgets.error} onRetry={budgets.refresh} />
        ) : list.length === 0 ? (
          <EmptyNote>No device has a transfer budget.</EmptyNote>
        ) : (
          <RowList>
            {list.map((q) => {
              const reading = quotaReading(q)
              return (
                <Row
                  key={q.iface}
                  title={
                    <span className="font-mono" aria-label={`${q.iface} budget`}>
                      {q.iface}
                    </span>
                  }
                  subtitle={
                    <span className="flex min-w-0 flex-col gap-1.5">
                      <span className="numeric">
                        {q.state === "unmeasured"
                          ? "Not measured: the metrics retention keeps nothing"
                          : `${bytes(q.usedBytes + q.estimatedBytes)} of ${bytes(q.limitBytes)} ${DIRECTION_WORD[q.direction]} this ${q.period}`}
                        {q.estimatedBytes > 0 &&
                          ` (${bytes(q.estimatedBytes)} estimated from older rows)`}
                      </span>
                      {q.state !== "unmeasured" && (
                        <Meter
                          value={reading.percent}
                          tone={
                            q.state === "exceeded"
                              ? "danger"
                              : q.state === "warning"
                                ? "warning"
                                : "default"
                          }
                          mark={80}
                          label={`${q.iface} budget used`}
                        />
                      )}
                      <span className="text-hint text-muted-foreground">
                        {Math.round(q.coverage * 100)}% of the {q.period} so far was recorded
                        {q.retentionShort &&
                          "; the retention keeps less than the period, so its start is gone"}
                      </span>
                    </span>
                  }
                  trailing={
                    <span className="flex items-center gap-3">
                      <Status tone={reading.tone} label={reading.label} />
                      {admin && (
                        <Button
                          size="xs"
                          variant="ghost"
                          aria-label={`Clear the budget on ${q.iface}`}
                          onClick={() => clear(q)}
                        >
                          Clear
                        </Button>
                      )}
                    </span>
                  }
                />
              )
            })}
          </RowList>
        )}
        {admin && <BudgetForm links={links} onSaved={budgets.refresh} />}
      </PanelBody>
      {dialog}
    </Panel>
  )
}

function BudgetForm({ links, onSaved }: { links: NetworkLink[] | undefined; onSaved: () => void }) {
  const devices = (links ?? []).filter((l) => l.role !== "container" && l.kind !== "loopback")
  const [device, setDevice] = useState("")
  const [period, setPeriod] = useState<InterfaceQuota["period"]>("month")
  const [direction, setDirection] = useState<InterfaceQuota["direction"]>("both")
  const [limit, setLimit] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const chosen = device || devices.find((l) => l.role === "uplink")?.name || devices[0]?.name || ""
  const parsed = parseBudget(limit)
  const problem = limit.trim() && !parsed ? "Use a figure in MB, GB or TB, at least 1 MB." : ""
  const save = async () => {
    if (!parsed || !chosen) return
    setBusy(true)
    setError(undefined)
    try {
      await put(`/network/traffic/quotas/${encodeURIComponent(chosen)}`, {
        period,
        direction,
        limitBytes: parsed,
      })
      notify.success(`${chosen}'s budget is ${bytes(parsed)} a ${period}`)
      setLimit("")
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className="flex flex-wrap items-end gap-3"
      aria-label="Set a transfer budget"
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <Field label="Device" htmlFor="budget-device">
        <Select value={chosen} onValueChange={setDevice}>
          <SelectTrigger id="budget-device" className="w-40 font-mono" size="sm">
            <SelectValue placeholder="Device" />
          </SelectTrigger>
          <SelectContent>
            {devices.map((l) => (
              <SelectItem key={l.name} value={l.name} className="font-mono">
                {l.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field label="Per">
        <ToggleGroup
          type="single"
          value={period}
          onValueChange={(next) => next && setPeriod(next as InterfaceQuota["period"])}
          variant="outline"
          size="sm"
          aria-label="Budget period"
        >
          {(["day", "week", "month"] as const).map((p) => (
            <ToggleGroupItem key={p} value={p} className="px-2.5 text-hint">
              {p}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </Field>
      <Field label="Counts">
        <ToggleGroup
          type="single"
          value={direction}
          onValueChange={(next) => next && setDirection(next as InterfaceQuota["direction"])}
          variant="outline"
          size="sm"
          aria-label="Budget direction"
        >
          <ToggleGroupItem value="both" className="px-2.5 text-hint">
            both
          </ToggleGroupItem>
          <ToggleGroupItem value="rx" className="px-2.5 text-hint">
            in
          </ToggleGroupItem>
          <ToggleGroupItem value="tx" className="px-2.5 text-hint">
            out
          </ToggleGroupItem>
        </ToggleGroup>
      </Field>
      <Field label="Budget" htmlFor="budget-limit" error={problem}>
        <Input
          id="budget-limit"
          value={limit}
          placeholder="500 GB"
          onChange={(event) => setLimit(event.target.value)}
          aria-invalid={Boolean(problem)}
          className="w-32 font-mono"
        />
      </Field>
      <Button type="submit" size="sm" disabled={!parsed || !chosen || busy} pending={busy}>
        Set budget
      </Button>
      {error && (
        <p role="alert" className="w-full text-hint text-destructive">
          {error}
        </p>
      )}
    </form>
  )
}
