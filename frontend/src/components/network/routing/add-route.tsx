"use client"

import { useMemo, useState } from "react"
import { post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import { noteProblem, preferredSourceProblem } from "@/components/network/draft-input"
import type {
  NetworkLink,
  NetworkRoute,
  NetworkRouteDecision,
  NetworkRouteImpact,
  NetworkRoutePlan,
  NetworkRouting,
  NetworkRulePreview,
} from "@/lib/types"
import { Modal } from "@/components/modal"
import { Disclosure, Field, FieldRow, FormSection } from "@/components/form"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Plus, Trash, Warning } from "@/components/icons"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { RouteImpactList } from "./route-impact"
import { nexthopProblem, type NexthopDraft } from "./route-reading"

const NONE = "__none"

const blankLeg = (): NexthopDraft => ({ gateway: "", device: "", weight: "" })

/**
 * A static route: where to, by which hop or hops, in which table. Before it
 * is added the server models what it would do to the addresses this host is
 * known to depend on; a route that discards traffic cannot be added without
 * that review. The apply then asks the kernel how it answers this browser,
 * its tunnels and its way out, and takes the route back if any of them moved.
 */
export function AddRoute(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onAdded: () => void
  /** A device another page handed the form, already chosen. */
  initialDevice?: string
}) {
  if (!props.open) return null
  return <RouteForm {...props} onDone={props.onAdded} />
}

/**
 * The same form on a route the dashboard made. An edit is reviewed as a plan
 * — the fields that change, the commands, whether the kernel can replace the
 * route in place or the new one is added before the old is removed, and the
 * traffic it moves — and only that reviewed plan can be applied.
 */
export function EditRoute({
  route,
  tableId,
  ...props
}: {
  route: NetworkRoute
  tableId: number
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onDone: () => void
}) {
  if (!props.open) return null
  return <RouteForm {...props} edit={{ route, tableId }} />
}

function RouteForm({
  open,
  onOpenChange,
  routing,
  links,
  onDone,
  edit,
  initialDevice,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onDone: () => void
  edit?: { route: NetworkRoute; tableId: number }
  initialDevice?: string
}) {
  const initial = edit?.route
  const [destination, setDestination] = useState(initial?.destination ?? "")
  const [type, setType] = useState(initial?.type ?? "unicast")
  const [gateway, setGateway] = useState(initial?.gateway ?? "")
  const [device, setDevice] = useState(initial?.device ?? initialDevice ?? NONE)
  const [multipath, setMultipath] = useState((initial?.nexthops.length ?? 0) > 0)
  const [legs, setLegs] = useState<NexthopDraft[]>(
    initial && initial.nexthops.length > 0
      ? initial.nexthops.map((n) => ({
          gateway: n.gateway ?? "",
          device: n.device ?? "",
          weight: n.weight && n.weight > 1 ? String(n.weight) : "",
        }))
      : [blankLeg(), blankLeg()],
  )
  const [table, setTable] = useState(String(edit?.tableId ?? 254))
  const [metric, setMetric] = useState(
    initial?.metric && !(initial.family === "inet6" && initial.metric === 1024)
      ? String(initial.metric)
      : "",
  )
  const [source, setSource] = useState(initial?.source ?? "")
  const [comment, setComment] = useState(initial?.comment ?? "")
  const [busy, setBusy] = useState(false)
  const [reviewing, setReviewing] = useState(false)
  const [error, setError] = useState<string>()
  const [review, setReview] = useState<{
    key: string
    impact: NetworkRouteImpact
    plan?: NetworkRoutePlan
  }>()

  const discards = type !== "unicast"
  const sourceError = discards ? undefined : preferredSourceProblem(source, destination, gateway)
  const commentError = noteProblem(comment)
  const legsError = !discards && multipath ? nexthopProblem(legs, destination) : undefined
  const body = useMemo(
    () => ({
      destination: destination.trim(),
      type,
      gateway: discards || multipath ? undefined : gateway.trim() || undefined,
      device: discards || multipath || device === NONE ? undefined : device,
      nexthops:
        !discards && multipath
          ? legs.map((leg) => ({
              gateway: leg.gateway.trim() || undefined,
              device: leg.device || undefined,
              weight: leg.weight ? Number(leg.weight) : undefined,
            }))
          : undefined,
      table: Number(table) || 254,
      metric: metric ? Number(metric) : undefined,
      source: discards ? undefined : source.trim() || undefined,
      comment: comment.trim() || undefined,
    }),
    [destination, type, gateway, device, multipath, legs, table, metric, source, comment, discards],
  )
  const key = JSON.stringify(body)
  const reviewed = review?.key === key ? review : undefined
  const ready =
    !sourceError &&
    !commentError &&
    !legsError &&
    destination.trim() &&
    (discards || multipath || gateway.trim() || device !== NONE)
  // A discard route and every edit apply only what was reviewed.
  const needsReview = Boolean(edit) || discards

  const check = async () => {
    if (!ready || reviewing) return
    setReviewing(true)
    setError(undefined)
    try {
      if (edit) {
        const plan = await post<NetworkRoutePlan>(
          `/network/routing/routes/${edit.route.id}/plan`,
          body,
        )
        setReview({ key, impact: plan.impact, plan })
      } else {
        const impact = await post<NetworkRouteImpact>("/network/routing/routes/preview", body)
        setReview({ key, impact })
      }
    } catch (err) {
      setReview(undefined)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setReviewing(false)
    }
  }

  const submit = async () => {
    if (!ready || busy || (needsReview && !reviewed)) return
    setBusy(true)
    setError(undefined)
    try {
      if (edit) {
        await put(`/network/routing/routes/${edit.route.id}`, body)
        notify.success("Route changed", {
          description: "The new route is made again at every boot.",
        })
      } else {
        await post("/network/routing/routes", body)
        notify.success("Route added", { description: "It is made again at every boot." })
      }
      onOpenChange(false)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const tables = routing.tables.filter((t) => t.id !== 52 && t.id !== 255 && t.id !== 253)
  const devices = links.filter((l) => l.role !== "container" && l.owner !== "kernel")
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={edit ? `Edit the route to ${edit.route.destination}` : "Add a route"}
      description={
        edit
          ? "Review the change as a plan, then apply exactly that plan"
          : "Send traffic for a network by a chosen hop, hops or device"
      }
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="outline"
            onClick={check}
            disabled={!ready || reviewing}
            pending={reviewing}
          >
            {edit ? "Review plan" : "Check effect"}
          </Button>
          <Button
            onClick={submit}
            disabled={!ready || busy || (needsReview && !reviewed)}
            pending={busy}
          >
            {edit ? "Apply plan" : "Add route"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <FieldRow>
          <Field
            label="Destination"
            htmlFor="route-dest"
            hint="An IPv4 or IPv6 network in CIDR form, or default"
          >
            <Input
              id="route-dest"
              value={destination}
              placeholder="192.168.60.0/24"
              onChange={(event) => setDestination(event.target.value)}
              className="font-mono"
            />
          </Field>
          <Field label="Kind" htmlFor="route-type">
            <Select value={type} onValueChange={setType}>
              <SelectTrigger id="route-type" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="unicast">Send it on</SelectItem>
                <SelectItem value="blackhole">Drop it silently (blackhole)</SelectItem>
                <SelectItem value="unreachable">Refuse: unreachable</SelectItem>
                <SelectItem value="prohibit">Refuse: prohibited</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        {!discards && (
          <label className="flex items-center gap-3 text-body">
            <Switch
              aria-label="Spread over several hops"
              checked={multipath}
              onCheckedChange={setMultipath}
            />
            Spread over several hops (equal-cost multipath)
          </label>
        )}
        {!discards && !multipath && (
          <FieldRow>
            <Field label="Via" htmlFor="route-via" hint="The next hop's address, if there is one">
              <Input
                id="route-via"
                value={gateway}
                placeholder="10.8.0.2"
                onChange={(event) => setGateway(event.target.value)}
                className="font-mono"
              />
            </Field>
            <Field label="Device" htmlFor="route-dev">
              <Select value={device} onValueChange={setDevice}>
                <SelectTrigger id="route-dev" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Whichever reaches the hop</SelectItem>
                  {devices.map((l) => (
                    <SelectItem key={l.name} value={l.name}>
                      {l.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
        )}
        {!discards && multipath && (
          <FormSection title="Nexthops">
            <ol className="space-y-3" aria-label="Nexthops">
              {legs.map((leg, index) => (
                <li
                  key={index}
                  className="grid gap-2 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_5rem_auto] sm:items-end"
                >
                  <Field label={`Hop ${index + 1} via`} htmlFor={`route-leg-${index}-via`}>
                    <Input
                      id={`route-leg-${index}-via`}
                      value={leg.gateway}
                      placeholder="10.8.0.2"
                      className="font-mono"
                      onChange={(event) =>
                        setLegs(
                          legs.map((l, i) =>
                            i === index ? { ...l, gateway: event.target.value } : l,
                          ),
                        )
                      }
                    />
                  </Field>
                  <Field label="Device" htmlFor={`route-leg-${index}-dev`}>
                    <Select
                      value={leg.device || NONE}
                      onValueChange={(value) =>
                        setLegs(
                          legs.map((l, i) =>
                            i === index ? { ...l, device: value === NONE ? "" : value } : l,
                          ),
                        )
                      }
                    >
                      <SelectTrigger id={`route-leg-${index}-dev`} className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value={NONE}>Any</SelectItem>
                        {devices.map((l) => (
                          <SelectItem key={l.name} value={l.name}>
                            {l.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field label="Weight" htmlFor={`route-leg-${index}-weight`}>
                    <Input
                      id={`route-leg-${index}-weight`}
                      inputMode="numeric"
                      value={leg.weight}
                      placeholder="1"
                      onChange={(event) =>
                        setLegs(
                          legs.map((l, i) =>
                            i === index ? { ...l, weight: event.target.value } : l,
                          ),
                        )
                      }
                    />
                  </Field>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Remove hop ${index + 1}`}
                    disabled={legs.length <= 2}
                    onClick={() => setLegs(legs.filter((_, i) => i !== index))}
                  >
                    <Trash aria-hidden />
                  </Button>
                </li>
              ))}
            </ol>
            <Button
              size="xs"
              variant="outline"
              className="w-fit"
              disabled={legs.length >= 16}
              onClick={() => setLegs([...legs, blankLeg()])}
            >
              <Plus aria-hidden />
              Add a hop
            </Button>
            {legsError && <p className="text-hint text-destructive">{legsError}</p>}
          </FormSection>
        )}
        {!discards && (
          <Field
            label="Preferred source"
            htmlFor="route-source"
            hint="Optional: the local address used for traffic this server sends over the route. Leave empty for the kernel to choose."
            error={sourceError}
          >
            <Input
              id="route-source"
              value={source}
              onChange={(event) => setSource(event.target.value)}
              aria-invalid={Boolean(sourceError)}
              placeholder={gateway.includes(":") ? "2001:db8::1" : "192.168.60.1"}
              className="font-mono"
            />
          </Field>
        )}
        <FieldRow>
          <Field
            label="Table"
            htmlFor="route-table"
            hint="main, or a number for a policy rule to pick"
          >
            <Input
              id="route-table"
              list="route-tables"
              inputMode="numeric"
              value={table}
              onChange={(event) => setTable(event.target.value)}
            />
            <datalist id="route-tables">
              {tables.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </datalist>
          </Field>
          <Field label="Metric" htmlFor="route-metric" hint="Lower wins between equal routes">
            <Input
              id="route-metric"
              inputMode="numeric"
              value={metric}
              placeholder="100"
              onChange={(event) => setMetric(event.target.value)}
            />
          </Field>
        </FieldRow>
        <Field
          label="Note"
          htmlFor="route-comment"
          hint="Optional: what it is for"
          error={commentError}
        >
          <Input
            id="route-comment"
            value={comment}
            aria-invalid={Boolean(commentError)}
            placeholder="office LAN through wg0"
            onChange={(event) => setComment(event.target.value)}
          />
        </Field>
        {routing.clientPath.device && (
          <Notice title="Checked against your connection">
            Your browser is answered through{" "}
            <span className="font-mono">{routing.clientPath.device}</span>
            {routing.clientPath.gateway && (
              <>
                {" "}
                via <span className="font-mono">{routing.clientPath.gateway}</span>
              </>
            )}
            . A route that would move that reply, a tunnel endpoint or this server&rsquo;s way out
            is taken back the moment it is applied.
          </Notice>
        )}
        {needsReview && !reviewed && ready && (
          <p className="text-hint text-muted-foreground">
            {edit
              ? "Review the plan to see exactly what changes before it can be applied."
              : "A route that discards traffic is added only after its effect has been checked."}
          </p>
        )}
        {reviewed?.plan && <PlanSummary plan={reviewed.plan} />}
        {reviewed && (
          <RouteImpactList
            impact={reviewed.impact}
            title={edit ? "What the edit would move" : "What the route would change"}
          />
        )}
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}

function PlanSummary({ plan }: { plan: NetworkRoutePlan }) {
  return (
    <section aria-label="Plan" className="space-y-2 rounded-lg border border-hairline p-3">
      <p className="flex flex-wrap items-center gap-2 text-body font-medium">
        Plan
        <Tag>{plan.replace ? "replaced in place" : "added, then the old one removed"}</Tag>
      </p>
      <ul className="space-y-1 text-xs">
        {plan.changes.map((change) => (
          <li key={change}>{change}</li>
        ))}
      </ul>
      <ol className="space-y-1">
        {plan.commands.map((command) => (
          <li key={command}>
            <code className="font-mono text-hint break-all text-muted-foreground">{command}</code>
          </li>
        ))}
      </ol>
    </section>
  )
}

/**
 * A policy rule: which traffic, and what to do with it. Its priority is
 * picked from the dashboard's own range unless one is given, so it never
 * lands between the rules the system and Tailscale wrote; a goto continues
 * only at a later rule made here. Before it is added, the preview places it
 * among the existing rules, names rules that would shadow it or that it
 * would shadow, models what it moves, and can answer a named packet with
 * the kernel's present answer beside the model's prediction.
 */
export function AddRule({
  open,
  onOpenChange,
  routing,
  links,
  onAdded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onAdded: () => void
}) {
  const [family, setFamily] = useState("inet")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [iif, setIif] = useState(NONE)
  const [oif, setOif] = useState(NONE)
  const [fwmark, setFwmark] = useState("")
  const [uidRange, setUidRange] = useState("")
  const [tos, setTos] = useState("")
  const [action, setAction] = useState("lookup")
  const [table, setTable] = useState("")
  const [goto, setGoto] = useState(NONE)
  const [priority, setPriority] = useState("")
  const [comment, setComment] = useState("")
  const [probe, setProbe] = useState({
    target: "",
    source: "",
    uid: "",
    mark: "",
    tos: "",
    iif: "",
  })
  const [busy, setBusy] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [error, setError] = useState<string>()
  const [preview, setPreview] = useState<{ key: string; result: NetworkRulePreview }>()

  const vrf = action === "vrf"
  const commentError = noteProblem(comment)
  const selects =
    from.trim() ||
    to.trim() ||
    iif !== NONE ||
    oif !== NONE ||
    fwmark.trim() ||
    uidRange.trim() ||
    tos.trim() ||
    vrf
  const gotoTargets = routing.rules.filter(
    (rule) =>
      rule.managed && rule.family === family && (!priority || rule.priority > Number(priority)),
  )
  const body = {
    family,
    from: from.trim() || undefined,
    to: to.trim() || undefined,
    iif: iif === NONE ? undefined : iif,
    oif: oif === NONE ? undefined : oif,
    fwmark: fwmark.trim() || undefined,
    uidRange: uidRange.trim() || undefined,
    tos: tos.trim() || undefined,
    l3mdev: vrf || undefined,
    action: vrf ? "lookup" : action,
    table: action === "lookup" ? Number(table) : undefined,
    goto: action === "goto" && goto !== NONE ? Number(goto) : undefined,
    priority: priority ? Number(priority) : undefined,
    comment: comment.trim() || undefined,
  }
  const key = JSON.stringify(body)
  const ready =
    !commentError &&
    selects &&
    (action !== "lookup" || table.trim()) &&
    (action !== "goto" || goto !== NONE)
  const probeBody = probe.target.trim()
    ? {
        target: probe.target.trim(),
        source: probe.source.trim() || undefined,
        uid: probe.uid.trim() ? Number(probe.uid) : undefined,
        mark: probe.mark.trim() || undefined,
        tos: probe.tos.trim() || undefined,
        iif: probe.iif.trim() || undefined,
      }
    : undefined

  const runPreview = async () => {
    if (!ready || previewing) return
    setPreviewing(true)
    setError(undefined)
    try {
      const result = await post<NetworkRulePreview>("/network/routing/rules/preview", {
        ...body,
        probe: probeBody,
      })
      setPreview({ key, result })
    } catch (err) {
      setPreview(undefined)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setPreviewing(false)
    }
  }
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/routing/rules", body)
      notify.success("Rule added", { description: "It is made again at every boot." })
      onOpenChange(false)
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const devices = links.filter((l) => l.role !== "container" && l.owner !== "kernel")
  const current = preview?.key === key ? preview.result : undefined
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add a policy rule"
      description="Send a kind of traffic to its own routing table"
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="outline"
            onClick={runPreview}
            disabled={!ready || previewing}
            pending={previewing}
          >
            Preview effect
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            Add rule
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Field label="Address family" htmlFor="rule-family">
          <Select value={family} onValueChange={setFamily}>
            <SelectTrigger id="rule-family">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="inet">IPv4</SelectItem>
              <SelectItem value="inet6">IPv6</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <FieldRow>
          <Field label="From" htmlFor="rule-from" hint="Traffic from this network">
            <Input
              id="rule-from"
              value={from}
              placeholder={family === "inet6" ? "2001:db8::/64" : "10.8.0.0/24"}
              onChange={(event) => setFrom(event.target.value)}
              className="font-mono"
            />
          </Field>
          <Field label="To" htmlFor="rule-to" hint="Traffic to this network">
            <Input
              id="rule-to"
              value={to}
              placeholder="any"
              onChange={(event) => setTo(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <Field
          label="Leaving on"
          htmlFor="rule-oif"
          hint="Optional: the outgoing device for locally generated traffic"
        >
          <Select value={oif} onValueChange={setOif}>
            <SelectTrigger id="rule-oif" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>Any device</SelectItem>
              {devices.map((link) => (
                <SelectItem key={link.name} value={link.name}>
                  {link.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <FieldRow>
          <Field label="Arriving on" htmlFor="rule-iif">
            <Select value={iif} onValueChange={setIif}>
              <SelectTrigger id="rule-iif" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Any device</SelectItem>
                {devices.map((l) => (
                  <SelectItem key={l.name} value={l.name}>
                    {l.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Firewall mark" htmlFor="rule-fwmark" hint="Optional, such as 0x10/0xff">
            <Input
              id="rule-fwmark"
              value={fwmark}
              placeholder="0x10"
              onChange={(event) => setFwmark(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field
            label="Socket owner (UID)"
            htmlFor="rule-uid"
            hint="Optional: a UID or a range, for traffic this server sends"
          >
            <Input
              id="rule-uid"
              value={uidRange}
              placeholder="1000-1999"
              onChange={(event) => setUidRange(event.target.value)}
              className="font-mono"
            />
          </Field>
          <Field
            label="TOS"
            htmlFor="rule-tos"
            hint={
              family === "inet6"
                ? "Optional: a DS field such as EF or 0xb8"
                : "Optional: 0x04 to 0x1c in steps of 4"
            }
          >
            <Input
              id="rule-tos"
              value={tos}
              placeholder={family === "inet6" ? "EF" : "0x10"}
              onChange={(event) => setTos(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Then" htmlFor="rule-action">
            <Select value={action} onValueChange={setAction}>
              <SelectTrigger id="rule-action" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="lookup">Look it up in a table</SelectItem>
                <SelectItem value="vrf">Look it up in its VRF&rsquo;s table</SelectItem>
                <SelectItem value="goto">Continue at a later rule (goto)</SelectItem>
                <SelectItem value="blackhole">Drop it silently</SelectItem>
                <SelectItem value="unreachable">Refuse: unreachable</SelectItem>
                <SelectItem value="prohibit">Refuse: prohibited</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {action === "lookup" ? (
            <Field label="Table" htmlFor="rule-table" hint="The number of the table to use">
              <Input
                id="rule-table"
                inputMode="numeric"
                value={table}
                placeholder="100"
                list="rule-tables"
                onChange={(event) => setTable(event.target.value)}
              />
              <datalist id="rule-tables">
                {(routing.vrfs ?? []).map((v) => (
                  <option key={v.name} value={v.table}>
                    VRF {v.name}
                  </option>
                ))}
              </datalist>
            </Field>
          ) : action === "goto" ? (
            <Field
              label="Continue at"
              htmlFor="rule-goto"
              hint="A later rule made here; foreign rules are never jumped over"
            >
              <Select value={goto} onValueChange={setGoto}>
                <SelectTrigger id="rule-goto" className="w-full">
                  <SelectValue placeholder="Choose a rule" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Choose a rule</SelectItem>
                  {gotoTargets.map((rule) => (
                    <SelectItem key={rule.priority} value={String(rule.priority)}>
                      {rule.priority}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          ) : (
            <div />
          )}
        </FieldRow>
        <Field
          label="Priority"
          htmlFor="rule-priority"
          hint={`Optional: ${routing.rulePriorities.min}–${routing.rulePriorities.max}; the first free one otherwise`}
        >
          <Input
            id="rule-priority"
            inputMode="numeric"
            value={priority}
            onChange={(event) => setPriority(event.target.value)}
          />
        </Field>
        <Field
          label="Note"
          htmlFor="rule-comment"
          hint="Optional: what this rule is for"
          error={commentError}
        >
          <Input
            id="rule-comment"
            value={comment}
            aria-invalid={Boolean(commentError)}
            onChange={(event) => setComment(event.target.value)}
          />
        </Field>
        <Disclosure summary="Test a packet in the preview" quiet>
          <div className="grid gap-3 sm:grid-cols-3">
            {(
              [
                ["target", "Destination", "198.51.100.7"],
                ["source", "Source", "10.8.0.2"],
                ["iif", "Arriving on", "wg0"],
                ["uid", "UID", "1000"],
                ["mark", "Mark", "0x10"],
                ["tos", "TOS", "0x10"],
              ] as const
            ).map(([field, label, placeholder]) => (
              <Field key={field} label={label} htmlFor={`rule-probe-${field}`}>
                <Input
                  id={`rule-probe-${field}`}
                  value={probe[field]}
                  placeholder={placeholder}
                  className="font-mono"
                  onChange={(event) => setProbe({ ...probe, [field]: event.target.value })}
                />
              </Field>
            ))}
          </div>
        </Disclosure>
        {!selects && (
          <Notice title="A rule needs something to match">
            A rule for all traffic would send this browser&rsquo;s replies through the table it
            names too; give it a source, a destination, a device, a mark, a UID, a TOS or a VRF.
          </Notice>
        )}
        {current && <RulePreviewSummary preview={current} />}
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}

const AGREEMENT: Record<string, string> = {
  agrees: "The model's answer for now matches the kernel's.",
  disagrees:
    "The model's answer for now differs from the kernel's; its prediction is not reliable.",
  model_unknown: "The model cannot decide this packet now; only the kernel's answer stands.",
  kernel_unavailable: "The kernel could not be asked about this packet.",
}

function describeDecision(d: NetworkRouteDecision) {
  if (d.status === "route" && d.route)
    return `${d.route.destination} via ${d.route.gateway ?? d.route.device ?? "nexthops"} (table ${d.tableName || d.table}, rule ${d.rulePriority})`
  if (d.status === "discard") return d.reason ?? "discarded"
  if (d.status === "local") return "this host itself"
  if (d.status === "unknown")
    return `unknown — ${d.reason ?? "a selector the model cannot evaluate"}${
      d.alternatives?.length
        ? ` (${d.alternatives.map((a) => `${a.assumption}: ${a.decision.status}`).join("; ")})`
        : ""
    }`
  return d.reason ?? d.status
}

function RulePreviewSummary({ preview }: { preview: NetworkRulePreview }) {
  return (
    <section aria-label="Rule preview" className="space-y-3">
      <div className="space-y-1 rounded-lg border border-hairline p-3 text-xs">
        <p className="text-body font-medium">
          Priority <span className="numeric font-mono">{preview.rule.priority}</span>
        </p>
        <p className="text-muted-foreground">
          {preview.after ? `After rule ${preview.after.priority}` : "First"}
          {preview.before ? `, before rule ${preview.before.priority}` : ", last"}.
        </p>
        {preview.shadowedBy.map((relation) => (
          <p key={`by-${relation.priority}`} className="text-warning">
            Shadowed: {relation.reason}.
          </p>
        ))}
        {preview.shadows.map((relation) => (
          <p key={`over-${relation.priority}`}>Shadows: {relation.reason}.</p>
        ))}
      </div>
      {preview.refusal && (
        <Notice tone="danger" icon={Warning} title="The server would refuse this rule">
          {preview.refusal}
        </Notice>
      )}
      {preview.probe && (
        <div className="space-y-1 rounded-lg border border-hairline p-3 text-xs">
          <p className="text-body font-medium">Your packet</p>
          <p>
            <span className="text-muted-foreground">Kernel now: </span>
            {preview.probe.kernel
              ? `${preview.probe.kernel.device ?? "local"}${preview.probe.kernel.gateway ? ` via ${preview.probe.kernel.gateway}` : ""} (table ${preview.probe.kernel.table ?? "main"})`
              : preview.probe.kernelError}
          </p>
          <p>
            <span className="text-muted-foreground">Model now: </span>
            {describeDecision(preview.probe.now)}
          </p>
          <p>
            <span className="text-muted-foreground">Model with this rule: </span>
            {describeDecision(preview.probe.with)}
          </p>
          <p className="text-muted-foreground">{AGREEMENT[preview.probe.agreement]}</p>
        </div>
      )}
      <RouteImpactList impact={preview.impact} title="What the rule would move" />
    </section>
  )
}
