"use client"

import { useAuth } from "@/hooks/use-auth"
import { useMemo, useState } from "react"
import { post } from "@/lib/api"
import type { DNSLookup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Field } from "@/components/form"
import { Checkbox } from "@/components/ui/checkbox"
import { Switch } from "@/components/ui/switch"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const TYPES = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "PTR", "SRV"]

/** The longest a row waits before it lands, so a resolver that timed out does not hold the race for seconds. */
const REPLAY_MS = 1600

const ms = (value: number) => (value < 10 ? value.toFixed(1) : String(Math.round(value)))

/** The native policy is tested before the reader chooses direct comparison destinations. */
export function LookupRace() {
  const { can } = useAuth()
  const allowed = can("read")
  const [name, setName] = useState("")
  const [type, setType] = useState("A")
  const [compare, setCompare] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [acknowledgeDisclosure, setAcknowledgeDisclosure] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [result, setResult] = useState<DNSLookup>()

  const destinations = result?.comparisonTargets ?? []
  const ready =
    allowed && name.trim() && !busy && (!compare || (selected.length > 0 && acknowledgeDisclosure))

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setResult(
        await post<DNSLookup>("/network/dns/lookup", {
          name: name.trim(),
          type,
          mode: compare ? "compare" : "effective",
          ...(compare ? { destinations: selected, acknowledgeDisclosure } : {}),
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <form
        className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_9rem] sm:items-start"
        onSubmit={(event) => {
          event.preventDefault()
          if (ready) void submit()
        }}
      >
        <Field
          label="Name"
          htmlFor="lookup-name"
          error={error}
          hint={
            !allowed
              ? "Running DNS diagnostics requires read access."
              : compare
                ? "This name is sent directly to each selected destination, including when it is private."
                : "Uses this server's effective resolver policy, including native split-DNS routing."
          }
        >
          <InputGroup>
            <InputGroupInput
              id="lookup-name"
              value={name}
              onChange={(event) => {
                setName(event.target.value)
                setAcknowledgeDisclosure(false)
              }}
              spellCheck={false}
              autoComplete="off"
              className="font-mono"
              placeholder={type === "PTR" ? "192.0.2.10" : "example.com"}
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              <InputGroupButton type="submit" disabled={!ready} pending={busy}>
                Resolve
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
        </Field>
        <Field label="Record" htmlFor="lookup-type">
          <Select
            value={type}
            onValueChange={(value) => {
              setType(value)
              setAcknowledgeDisclosure(false)
            }}
          >
            <SelectTrigger id="lookup-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </form>
      <div className="flex min-w-0 flex-col gap-3">
        <label className="flex min-h-11 items-center gap-3 text-body">
          <Switch
            aria-label="Compare named resolvers"
            checked={compare}
            onCheckedChange={(value) => {
              setCompare(value)
              setAcknowledgeDisclosure(false)
            }}
            disabled={!allowed || busy}
          />
          Compare named resolvers
        </label>
        {compare && (
          <div className="flex min-w-0 flex-col gap-3">
            <p className="text-body text-muted-foreground">
              Direct comparison bypasses split-DNS routing, host records, resolver encryption and
              DNSSEC validation. Each selected resolver receives this name. Private LAN and VPN
              names may leave their intended scope.
            </p>
            {destinations.length === 0 ? (
              <p className="text-body text-muted-foreground">
                Resolve with the effective policy first to load named destinations.
              </p>
            ) : (
              <fieldset className="grid min-w-0 gap-x-6 sm:grid-cols-2">
                <legend className="mb-2 text-body font-medium">Destinations</legend>
                {destinations.map((destination) => (
                  <label
                    key={destination.server}
                    className="flex min-h-11 min-w-0 items-center gap-3 text-body"
                  >
                    <Checkbox
                      aria-label={`${destination.label} ${destination.server}`}
                      checked={selected.includes(destination.server)}
                      disabled={busy}
                      onCheckedChange={(checked) => {
                        setSelected((current) =>
                          checked
                            ? [...current, destination.server]
                            : current.filter((server) => server !== destination.server),
                        )
                        setAcknowledgeDisclosure(false)
                      }}
                    />
                    <span className="min-w-0 break-all">
                      {destination.label}{" "}
                      <span className="font-mono text-muted-foreground">{destination.server}</span>
                    </span>
                  </label>
                ))}
              </fieldset>
            )}
            <label className="flex min-h-11 items-center gap-3 text-body">
              <Checkbox
                aria-label="Acknowledge private-name disclosure"
                checked={acknowledgeDisclosure}
                disabled={busy || selected.length === 0}
                onCheckedChange={(value) => setAcknowledgeDisclosure(value === true)}
              />
              I understand this name will be disclosed to the selected destinations.
            </label>
          </div>
        )}
      </div>
      {result && <Race result={result} />}
    </div>
  )
}

function Race({ result }: { result: DNSLookup }) {
  const rows = useMemo(
    () =>
      [...result.results].sort(
        (a, b) => Number(Boolean(a.error)) - Number(Boolean(b.error)) || a.latencyMs - b.latencyMs,
      ),
    [result],
  )
  const answered = rows.filter((r) => !r.error)
  const slowest = Math.max(...answered.map((r) => r.latencyMs), 1)
  return (
    // Re-keyed per asking, so a second question replays the race.
    <section
      key={`${result.type}:${result.name}:${rows.map((r) => r.latencyMs).join(",")}`}
      aria-label={`Answers for ${result.name}`}
      className="min-w-0"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 border-b border-hairline pb-2">
        <p className="min-w-0 truncate text-body font-medium">
          <span className="text-muted-foreground">{result.type}</span>{" "}
          <span className="font-mono">{result.name}</span>
        </p>
        <p className="numeric text-hint text-muted-foreground">
          {answered.length} of {rows.length} answered
          {answered[0] && (
            <>
              {" · "}fastest {answered[0].label} in {ms(answered[0].latencyMs)} ms
            </>
          )}
        </p>
      </div>
      {result.route && <p className="pt-3 text-body text-muted-foreground">{result.route}</p>}
      {result.note && <p className="pt-2 text-hint text-muted-foreground">{result.note}</p>}
      {result.omittedTargets?.map((destination, index) => (
        <p key={`${destination.server}:${index}`} className="pt-2 text-hint text-muted-foreground">
          {destination.label} {destination.server}: omitted — {destination.reason}
        </p>
      ))}
      <ol className="divide-y divide-hairline">
        {rows.map((row, rank) => {
          const landed = Math.min(row.latencyMs, REPLAY_MS) * 0.8
          return (
            <li
              key={`${row.server}:${row.label}`}
              style={{ "--rise-at": `${landed}ms` } as React.CSSProperties}
              className="grid min-w-0 animate-rise gap-x-6 gap-y-1.5 py-3 motion-safe:[animation-delay:var(--rise-at)] sm:grid-cols-[minmax(0,13rem)_minmax(0,1fr)_minmax(0,17rem)] sm:items-center"
            >
              <div className="min-w-0">
                <p className="truncate text-body font-medium">{row.label}</p>
                <p className="truncate font-mono text-hint text-muted-foreground">{row.server}</p>
              </div>
              <div className="flex min-w-0 items-center gap-3">
                <div
                  role="img"
                  aria-label={
                    row.error
                      ? `${row.label} did not answer`
                      : `${row.label} answered in ${ms(row.latencyMs)} milliseconds`
                  }
                  className="h-2 min-w-0 flex-1 overflow-hidden rounded-sm bg-meter-track"
                >
                  {!row.error && (
                    <span
                      className="block h-full rounded-sm transition-[width] duration-500 ease-out starting:w-0"
                      style={{
                        width: `${Math.max((row.latencyMs / slowest) * 100, 2)}%`,
                        background:
                          rank === 0
                            ? "var(--chart-1)"
                            : "color-mix(in oklab, var(--chart-1) 55%, transparent)",
                      }}
                    />
                  )}
                </div>
                <span
                  className={cn(
                    "numeric w-16 shrink-0 text-right text-body",
                    row.error ? "text-muted-foreground" : "font-medium",
                  )}
                >
                  {row.error ? "—" : `${ms(row.latencyMs)} ms`}
                </span>
              </div>
              <div className="min-w-0 font-mono text-xs break-all">
                {row.error ? (
                  <span className="text-destructive">{row.error}</span>
                ) : row.answers.length === 0 ? (
                  <span className="text-muted-foreground">no records</span>
                ) : (
                  row.answers.map((answer) => <p key={answer}>{answer}</p>)
                )}
              </div>
            </li>
          )
        })}
      </ol>
    </section>
  )
}
