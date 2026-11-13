"use client"

import { useEffect, useState } from "react"
import { del, get, put } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { NetworkLink } from "@/lib/types"
import { quotaReading, type InterfaceQuota } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Gauge, Pencil, Plus, Trash } from "@/components/icons"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Meter } from "@/components/meter"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { EmptyState, ErrorState } from "@/components/state"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { linkGlyph, linkProduct } from "@/components/network/marks"
import { parseBudget } from "@/components/network/vpn/record-logic"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { VerbMenu, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { useViewState } from "@/lib/view-state"
import { budgetWords } from "./words"

/**
 * Transfer budgets per device: a threshold the recorder's exact interval
 * counters are measured against, per UTC day, week or month. Like a WireGuard
 * peer's budget it alerts and never limits — a background cut could take the
 * operator's own way in with it. Each says what its figure is made of: the
 * bytes the counters measured, what older rows add as an estimate from their
 * mean rate, and how much of the period was recorded at all, because a
 * dashboard that was stopped counted nothing while it was.
 *
 * A budget is three lines whatever it has to say — its sentence, its band
 * with the mark at 80%, what the figure is made of — so one that is not
 * measured is the same height as one that is, with its band empty. Setting
 * one was a form under the list; it is the section's verb now, in a sheet,
 * and a budget's own menu opens the same sheet on that device.
 */
export function TrafficBudgets({ links }: { links: NetworkLink[] | undefined }) {
  const { can } = useAuth()
  const admin = can("system.admin")
  // Clearing takes the destructive budget on the server; setting does not.
  const mayClear = admin && can("destructive")
  const budgets = usePoll<InterfaceQuota[]>(
    (signal) => get("/network/traffic/quotas", undefined, signal),
    60_000,
  )
  const [editing, setEditing] = useState<{ budget?: InterfaceQuota }>()
  const [remembered, remember] = useViewState("network.traffic.budgets", 1)
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
  const list = budgets.data
  const count = list?.length
  useEffect(() => {
    if (count !== undefined && count !== remembered) remember(count)
  }, [count, remembered, remember])
  return (
    <Panel plain aria-label="Transfer budgets">
      <PanelHeader
        title="Transfer budgets"
        actions={
          <>
            {list && (
              <span className="numeric pr-1.5 text-hint text-muted-foreground">
                {plural(list.length, "budget")} · alerts, never limits
              </span>
            )}
            {admin && (
              <Button
                size="xs"
                variant="outline"
                aria-label="Set a transfer budget"
                onClick={() => setEditing({})}
              >
                <Plus />
                Budget
              </Button>
            )}
          </>
        }
      />
      <PanelBody flush className="flex flex-col">
        {list && (
          <NetworkReadWarning
            error={budgets.error}
            refresh={budgets.refresh}
            lastSuccess={budgets.lastSuccess}
            reading="transfer budgets"
          />
        )}
        {!list ? (
          budgets.error ? (
            <ErrorState error={budgets.error} onRetry={budgets.refresh} retryable />
          ) : (
            <ul aria-hidden className="divide-y divide-hairline">
              {Array.from({ length: Math.max(1, remembered) }, (_, i) => (
                <li key={i} className={ROW}>
                  <div className="flex h-9 items-center gap-3">
                    <Skeleton className="size-8 rounded-lg" />
                    <Skeleton className="h-3.5 w-48" />
                  </div>
                  <Skeleton className="h-1.5 w-full rounded-full" />
                  <Skeleton className="my-0.5 h-3 w-64" />
                </li>
              ))}
            </ul>
          )
        ) : list.length === 0 ? (
          <EmptyState
            className="mt-2"
            icon={Gauge}
            title="No device has a transfer budget"
            description="A budget alerts when a device passes a figure in a day, a week or a month. It never limits the device."
            action={
              admin ? (
                <Button size="sm" onClick={() => setEditing({})}>
                  Set a budget
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ul className="divide-y divide-hairline">
            {list.map((q) => (
              <BudgetRow
                key={q.iface}
                budget={q}
                link={links?.find((l) => l.name === q.iface)}
                verbs={[
                  ...(admin
                    ? [
                        {
                          key: "change",
                          label: "Change",
                          icon: Pencil,
                          run: () => setEditing({ budget: q }),
                        },
                      ]
                    : []),
                  ...(mayClear
                    ? [
                        {
                          key: "clear",
                          label: "Clear",
                          icon: Trash,
                          danger: true,
                          run: () => clear(q),
                        },
                      ]
                    : []),
                ]}
              />
            ))}
          </ul>
        )}
      </PanelBody>
      {editing && (
        <BudgetSheet
          links={links}
          budgets={list ?? []}
          budget={editing.budget}
          onClose={() => setEditing(undefined)}
          onSaved={budgets.refresh}
        />
      )}
      {dialog}
    </Panel>
  )
}

/** One height in every state: a budget's three lines, and the ghost that waits for them. */
const ROW = "flex min-w-0 flex-col gap-2.5 py-4"

function BudgetRow({
  budget: q,
  link,
  verbs,
}: {
  budget: InterfaceQuota
  link: NetworkLink | undefined
  verbs: Verb[]
}) {
  const reading = quotaReading(q)
  const words = budgetWords(q)
  return (
    <li data-slot="budget-row" aria-label={`${q.iface} budget`} className={ROW}>
      <div className="flex h-9 min-w-0 items-center gap-3">
        <ProductLogo
          id={link ? linkProduct(link) : undefined}
          size="sm"
          fallback={linkGlyph(link?.kind ?? "")}
        />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-body leading-tight font-medium">
            {q.iface}
          </span>
          <span className="numeric block truncate text-hint text-muted-foreground">
            {words.reading}
          </span>
        </span>
        <Status tone={reading.tone} label={reading.label} className="shrink-0" />
        {verbs.length > 0 && <VerbMenu verbs={verbs} label={`Budget on ${q.iface}`} />}
      </div>
      <Meter
        value={reading.percent}
        tone={q.state === "exceeded" ? "danger" : q.state === "warning" ? "warning" : "default"}
        mark={80}
        label={`${q.iface} budget used`}
      />
      <p className="truncate text-hint text-muted-foreground" title={words.hint}>
        {words.hint}
      </p>
    </li>
  )
}

const PERIODS = (["day", "week", "month"] as const).map((value) => ({ value, label: value }))
const DIRECTIONS: { value: InterfaceQuota["direction"]; label: string }[] = [
  { value: "both", label: "both" },
  { value: "rx", label: "in" },
  { value: "tx", label: "out" },
]

/**
 * A budget for one device. Opened from the section it sets a new one on a
 * device chosen here; opened from a budget it changes that one, the device
 * fixed. Either way the route replaces what the device had, and the sheet
 * says so before it is pressed.
 */
function BudgetSheet({
  links,
  budgets,
  budget,
  onClose,
  onSaved,
}: {
  links: NetworkLink[] | undefined
  budgets: InterfaceQuota[]
  budget?: InterfaceQuota
  onClose: () => void
  onSaved: () => void
}) {
  const devices = (links ?? []).filter((l) => l.role !== "container" && l.kind !== "loopback")
  const [device, setDevice] = useState(budget?.iface ?? "")
  const [period, setPeriod] = useState<InterfaceQuota["period"]>(budget?.period ?? "month")
  const [direction, setDirection] = useState<InterfaceQuota["direction"]>(
    budget?.direction ?? "both",
  )
  const [limit, setLimit] = useState(budget ? bytes(budget.limitBytes) : "")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const chosen = device || devices.find((l) => l.role === "uplink")?.name || devices[0]?.name || ""
  const parsed = parseBudget(limit)
  const problem = limit.trim() && !parsed ? "Use a figure in MB, GB or TB, at least 1 MB." : ""
  const replaces = !budget && budgets.some((b) => b.iface === chosen)
  const save = async () => {
    if (!parsed || !chosen || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await put(`/network/traffic/quotas/${encodeURIComponent(chosen)}`, {
        period,
        direction,
        limitBytes: parsed,
      })
      notify.success(`${chosen}'s budget is ${bytes(parsed)} a ${period}`)
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="sm"
      title={budget ? <span className="font-mono">{budget.iface}</span> : "Transfer budget"}
      description={budget ? `The transfer budget on ${budget.iface}` : "Set a transfer budget"}
      initialFocus={budget ? "body" : "first"}
      footer={
        <>
          {replaces && (
            <FormNote className="mr-auto">
              Replaces the budget <span className="font-mono">{chosen}</span> has.
            </FormNote>
          )}
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="traffic-budget"
            disabled={!parsed || !chosen || busy}
            pending={busy}
          >
            Set budget
          </Button>
        </>
      }
    >
      <form
        id="traffic-budget"
        className="flex flex-col gap-5"
        aria-label="Set a transfer budget"
        onSubmit={(event) => {
          event.preventDefault()
          void save()
        }}
      >
        {budget ? (
          <FormFacts>
            <FormFact label="Device" mono>
              {budget.iface}
            </FormFact>
            <FormFact label="Budget now" mono>
              {bytes(budget.limitBytes)} a {budget.period}
            </FormFact>
          </FormFacts>
        ) : (
          <Field label="Device" htmlFor="budget-device">
            <Select value={chosen} onValueChange={setDevice}>
              <SelectTrigger id="budget-device" className="w-full font-mono">
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
        )}
        <Field
          label="Per"
          info="Periods are UTC days, weeks and months. The figure starts again when one ends."
        >
          <Segments
            label="Budget period"
            value={period}
            options={PERIODS}
            onChange={setPeriod}
            fill
          />
        </Field>
        <Field label="Counts" info="Which direction is measured against the budget.">
          <Segments
            label="Budget direction"
            value={direction}
            options={DIRECTIONS}
            onChange={setDirection}
            fill
          />
        </Field>
        <Field
          label="Budget"
          htmlFor="budget-limit"
          error={problem || error}
          info="A figure in MB, GB or TB, at least 1 MB. Passing it raises an alert; the device is never limited."
        >
          <Input
            id="budget-limit"
            value={limit}
            placeholder="500 GB"
            autoComplete="off"
            onChange={(event) => setLimit(event.target.value)}
            aria-invalid={Boolean(problem)}
            className="font-mono"
          />
        </Field>
      </form>
    </SidePanel>
  )
}
