"use client"

import { useAuth } from "@/hooks/use-auth"
import { useMemo, useState } from "react"
import { ApiError, post } from "@/lib/api"
import type { DNSLookup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Field, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Checkbox } from "@/components/ui/checkbox"
import { Segments } from "@/components/deploy/settings/segments"
import { Question } from "@/components/network/dns/lookup-question"

/** The longest a row waits before it lands, so a resolver that timed out does not hold the race for seconds. */
const REPLAY_MS = 1600

const ms = (value: number) => (value < 10 ? value.toFixed(1) : String(Math.round(value)))

type Acknowledged = {
  /** The question these were given for: another name is another disclosure. */
  question: string
  disclosure: boolean
  forwarding: { asked: boolean; acknowledged: boolean }
}

const NOTHING = { disclosure: false, forwarding: { asked: false, acknowledged: false } }

/**
 * Asks the resolver for a name, then, if the reader wants, asks chosen
 * resolvers the same thing side by side. The native policy is tested before
 * the reader chooses direct comparison destinations, and what the reader has
 * acknowledged — a private name leaving its scope — holds only for the
 * question it was acknowledged for.
 */
export function LookupRace({
  name,
  type,
  onName,
  onType,
}: {
  name: string
  type: string
  onName: (name: string) => void
  onType: (type: string) => void
}) {
  const { can } = useAuth()
  const allowed = can("read")
  const [compare, setCompare] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [transport, setTransport] = useState<"classic" | "tls">("classic")
  const [dnssec, setDNSSEC] = useState(false)
  // A private name on a chain whose forwarding this page cannot see is asked
  // only after the reader acknowledges it; the refusal says when.
  const question = `${type} ${name.trim()}`
  const [given, setGiven] = useState<Acknowledged>({ question, ...NOTHING })
  const held = given.question === question ? given : { question, ...NOTHING }
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [result, setResult] = useState<DNSLookup>()

  const destinations = result?.comparisonTargets ?? []
  const ready = Boolean(
    allowed && name.trim() && !busy && (!compare || (selected.length > 0 && held.disclosure)),
  )

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setResult(
        await post<DNSLookup>("/network/dns/lookup", {
          name: name.trim(),
          type,
          mode: compare ? "compare" : "effective",
          ...(compare
            ? {
                destinations: selected,
                acknowledgeDisclosure: held.disclosure,
                ...(transport === "tls" ? { transport } : {}),
                ...(dnssec ? { dnssec } : {}),
              }
            : held.forwarding.acknowledged
              ? { acknowledgeForwarding: true }
              : {}),
        }),
      )
    } catch (err) {
      if (err instanceof ApiError && err.code === "dns_private_name_unknown_forwarding")
        setGiven({ ...held, forwarding: { asked: true, acknowledged: false } })
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <Question
        id="lookup-name"
        name={name}
        onName={onName}
        type={type}
        onType={onType}
        verb="Resolve"
        ready={ready}
        busy={busy}
        error={error}
        info={
          compare
            ? "This name is sent directly to each selected destination, including when it is private."
            : "Uses the configured resolver chain. Native split-DNS is checked when supported; other paths are marked unknown."
        }
        onSubmit={() => void submit()}
      />
      <div className="flex min-w-0 flex-col gap-3">
        {!compare && held.forwarding.asked && (
          <label className="flex min-h-11 items-center gap-3 text-body">
            <Checkbox
              aria-label="Acknowledge unknown forwarding"
              checked={held.forwarding.acknowledged}
              disabled={busy}
              onCheckedChange={(value) =>
                setGiven({ ...held, forwarding: { asked: true, acknowledged: value === true } })
              }
            />
            Send it to the configured resolver anyway; where that resolver forwards it is not
            measured.
          </label>
        )}
        <OptionRow
          title="Compare named resolvers"
          checked={compare}
          onCheckedChange={(value) => {
            setCompare(value)
            setGiven({ question, ...NOTHING })
          }}
          disabled={!allowed || busy}
        />
        {compare && (
          <div className="flex min-w-0 animate-rise flex-col gap-4">
            <Notice title="Each selected resolver receives this name" tone="warning">
              Direct comparison bypasses split-DNS routing and host records. Private LAN and VPN
              names may leave their intended scope.
            </Notice>
            <div className="flex min-w-0 flex-wrap items-end gap-x-6 gap-y-3">
              <Field
                label="Transport"
                info="Over TLS each destination's certificate is verified for the name it publishes. A destination without one is listed as not asked."
              >
                <Segments
                  label="Transport"
                  value={transport}
                  options={[
                    { value: "classic", label: "Classic DNS" },
                    { value: "tls", label: "DNS over TLS" },
                  ]}
                  onChange={(value) => {
                    setTransport(value)
                    setGiven({ ...held, disclosure: false })
                  }}
                />
              </Field>
              <label className="flex min-h-8 items-center gap-3 text-body">
                <Checkbox
                  aria-label="Request DNSSEC records"
                  checked={dnssec}
                  disabled={busy}
                  onCheckedChange={(value) => setDNSSEC(value === true)}
                />
                Request DNSSEC records (the DO bit)
              </label>
            </div>
            {destinations.length === 0 ? (
              <p className="text-body text-muted-foreground">
                Resolve with the effective policy first to load named destinations.
              </p>
            ) : (
              <fieldset className="grid min-w-0 gap-x-6 sm:grid-cols-2 xl:grid-cols-3">
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
                        setGiven({ ...held, disclosure: false })
                      }}
                    />
                    <span className="min-w-0 break-all">
                      {destination.label}{" "}
                      <span className="font-mono text-muted-foreground">{destination.server}</span>
                      {transport === "tls" && (
                        <span className="block text-hint text-muted-foreground">
                          {destination.tlsName
                            ? `TLS ${destination.tlsName}`
                            : "no TLS identity: not asked over TLS"}
                        </span>
                      )}
                    </span>
                  </label>
                ))}
              </fieldset>
            )}
            <label className="flex min-h-11 items-center gap-3 text-body">
              <Checkbox
                aria-label="Acknowledge private-name disclosure"
                checked={held.disclosure}
                disabled={busy || selected.length === 0}
                onCheckedChange={(value) => setGiven({ ...held, disclosure: value === true })}
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

/**
 * The answers, fastest first, each resolver on one line of the same columns:
 * who answered, what it said, how long it took. A bar per row grew from row
 * to row and said less than the figure at its end; the order and the figures
 * are the race. Rows still land in the order the answers did.
 */
function Race({ result }: { result: DNSLookup }) {
  const rows = useMemo(
    () =>
      [...result.results].sort(
        (a, b) => Number(Boolean(a.error)) - Number(Boolean(b.error)) || a.latencyMs - b.latencyMs,
      ),
    [result],
  )
  const answered = rows.filter((r) => !r.error)
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
        {rows.map((row) => {
          const landed = Math.min(row.latencyMs, REPLAY_MS) * 0.8
          return (
            <li
              key={`${row.server}:${row.label}`}
              style={{ "--rise-at": `${landed}ms` } as React.CSSProperties}
              className="grid min-w-0 animate-rise grid-cols-[minmax(0,1fr)_auto] gap-x-6 gap-y-1 py-3 motion-safe:[animation-delay:var(--rise-at)] sm:grid-cols-[minmax(0,15rem)_minmax(0,1fr)_5rem] sm:items-center"
            >
              <div className="min-w-0">
                <p className="truncate text-body font-medium">{row.label}</p>
                <p className="truncate font-mono text-hint text-muted-foreground">{row.server}</p>
                {row.transport && (
                  <p className="truncate text-hint text-muted-foreground">
                    {row.transport === "tls"
                      ? `TLS ${row.tlsVersion ?? ""} · ${row.tlsName}`
                      : row.transport.toUpperCase()}
                    {row.authenticatedData !== undefined &&
                      ` · AD ${row.authenticatedData ? "set" : "clear"} · ${row.signatures ?? 0} RRSIG`}
                  </p>
                )}
              </div>
              <div className="min-w-0 font-mono text-xs break-all max-sm:order-3 max-sm:col-span-2">
                {row.error ? (
                  <span className="text-destructive">{row.error}</span>
                ) : row.answers.length === 0 ? (
                  <span className="text-muted-foreground">no records</span>
                ) : (
                  row.answers.map((answer) => <p key={answer}>{answer}</p>)
                )}
              </div>
              <span
                className={cn(
                  "numeric shrink-0 text-right text-body",
                  row.error ? "text-muted-foreground" : "font-medium",
                )}
              >
                {row.error ? (
                  <>
                    <span aria-hidden>—</span>
                    <span className="sr-only">did not answer</span>
                  </>
                ) : (
                  `${ms(row.latencyMs)} ms`
                )}
              </span>
            </li>
          )
        })}
      </ol>
    </section>
  )
}
