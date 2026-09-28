"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Globe, Inspect, RefreshClockwise, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, post, put } from "@/lib/api"
import { calendarDate, duration, plural, relativeTime } from "@/lib/format"
import { parseScanTarget, scanSuggestions, targetLabel, tlsReportHref } from "@/lib/scan-target"
import type { Certificate, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormSection, FormSections } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { Sparkline } from "@/components/metrics/sparkline"
import { ErrorState, LoadingRows } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Watched = {
  id: number
  domain: string
  port: number
  /** The address the name is reached at instead of where DNS points. */
  ip?: string
  /** When the certificate was read; absent until the first check. */
  checkedAt?: string
  certificate?: Certificate
}

type WatchedCheck = { checkedAt: string; daysLeft?: number; fingerprint?: string; error?: string }

/** The schedule's choices, in seconds; the server takes 60 to 86400. */
const INTERVALS = [
  { seconds: 60, label: "every minute" },
  { seconds: 300, label: "every 5 minutes" },
  { seconds: 900, label: "every 15 minutes" },
  { seconds: 3600, label: "every hour" },
  { seconds: 21600, label: "every 6 hours" },
  { seconds: 86400, label: "every day" },
]

function intervalLabel(seconds: number) {
  return (
    INTERVALS.find((choice) => choice.seconds === seconds)?.label ?? `every ${duration(seconds)}`
  )
}

function endpointLabel(row: Pick<Watched, "domain" | "port" | "ip">) {
  const label = targetLabel({ host: row.domain, port: row.port })
  return row.ip ? `${label} via ${row.ip}` : label
}

/**
 * Endpoints checked with a live handshake. They stay apart from the installed
 * certificates because a live handshake can disagree with the file on disk,
 * and that disagreement is what they are for.
 *
 * The server checks them on its own schedule, whoever has the page open or
 * nobody, and every row says when its answer was read. Reading the list sends
 * nothing; an administrator can ask for a check now.
 */
export function WatchedDomains({ admin }: { admin: boolean }) {
  const router = useRouter()
  const [domain, setDomain] = useState("")
  const [ip, setIp] = useState("")
  const [fieldError, setFieldError] = useState<string>()
  const [adding, setAdding] = useState(false)
  const [checking, setChecking] = useState(false)
  const [watchingSites, setWatchingSites] = useState(false)
  const [removing, setRemoving] = useState<number>()
  // Endpoints removed since the list was fetched, kept off the screen until
  // the next read confirms it.
  const [removed, setRemoved] = useState<number[]>([])
  const watched = usePoll(
    (signal) => get<Watched[]>("/certificates/watched", undefined, signal),
    60_000,
  )
  const settings = usePoll(
    (signal) => get<{ intervalSeconds: number }>("/certificates/watch-schedule", undefined, signal),
    0,
  )
  const interval = settings.data?.intervalSeconds
  const rows = watched.data?.filter((row) => !removed.includes(row.id))

  const addDomain = async () => {
    // Read the way the TLS report reads its field: host:port for a service
    // off 443 (a mail server on 993), a pasted URL for its host, and an IPv6
    // address with its colons intact.
    const parsed = parseScanTarget(domain)
    if (!parsed.target) {
      setFieldError(parsed.error)
      return
    }
    const label = endpointLabel({
      domain: parsed.target.host,
      port: parsed.target.port,
      ip: ip.trim(),
    })
    setAdding(true)
    try {
      const row = await post<Watched>("/certificates/watched", {
        domain: parsed.target.host,
        port: parsed.target.port,
        ip: ip.trim() || undefined,
      })
      setDomain("")
      setIp("")
      if (rows?.some((shown) => shown.id === row.id)) {
        notify.info(`${label} is already watched`)
      } else {
        notify.success(`Watching ${label}`)
        watched.refresh()
      }
    } catch (err) {
      notify.error("Could not watch domain", err)
    } finally {
      setAdding(false)
    }
  }

  const checkNow = async () => {
    setChecking(true)
    try {
      await post("/certificates/watched/check")
      watched.refresh()
    } catch (err) {
      notify.error("Could not check the watched domains", err)
    } finally {
      setChecking(false)
    }
  }

  // Every name a TLS site of this server answers, on the ports it serves TLS
  // on, as the TLS report's field offers them.
  const watchSites = async () => {
    setWatchingSites(true)
    try {
      const sites = await get<VHost[]>("/proxy/vhosts")
      const targets = scanSuggestions({ recent: [], sites: sites.filter((site) => site.tls) })
        .map((suggestion) => parseScanTarget(suggestion.value).target)
        .filter((target) => target !== undefined)
        .filter(
          (target) =>
            !rows?.some((row) => !row.ip && row.domain === target.host && row.port === target.port),
        )
      if (targets.length === 0) {
        notify.info("Every site domain is already watched")
        return
      }
      const results = await Promise.allSettled(
        targets.map((target) =>
          post("/certificates/watched", { domain: target.host, port: target.port }),
        ),
      )
      const failed = results.filter((result) => result.status === "rejected").length
      if (failed > 0)
        notify.error(`${failed} of ${targets.length} site domains could not be watched`)
      else notify.success(`Watching ${plural(targets.length, "site domain")}`)
      watched.refresh()
    } catch (err) {
      notify.error("Could not read this server's sites", err)
    } finally {
      setWatchingSites(false)
    }
  }

  const changeInterval = async (seconds: number) => {
    try {
      await put("/certificates/watch-schedule", { intervalSeconds: seconds })
      settings.refresh()
      notify.success(`Watched domains are checked ${intervalLabel(seconds)}`)
    } catch (err) {
      notify.error("Could not change the schedule", err)
    }
  }

  const stopWatching = async (row: Watched) => {
    const label = endpointLabel(row)
    setRemoving(row.id)
    try {
      await del(`/certificates/watched/${row.id}`)
      setRemoved((ids) => [...ids, row.id])
      notify.success(`${label} is no longer watched`)
    } catch (err) {
      // Already gone — removed from another tab or by another administrator.
      if (err instanceof ApiError && err.status === 404) {
        setRemoved((ids) => [...ids, row.id])
        notify.info(`${label} was already removed`)
      } else notify.error(`Could not stop watching ${label}`, err)
    } finally {
      setRemoving(undefined)
    }
  }

  return (
    <FormSections>
      <FormSection
        aside
        title="Watched domains"
        hint={
          interval === undefined
            ? `${rows?.length ?? 0} watched`
            : `${rows?.length ?? 0} watched, checked by the server ${intervalLabel(interval)}`
        }
        actions={
          admin && (
            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void watchSites()}
                pending={watchingSites}
                disabled={watchingSites || !rows}
              >
                <Globe className="size-3.5" />
                Watch every site domain
              </Button>
              {rows && rows.length > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void checkNow()}
                  pending={checking}
                  disabled={checking}
                >
                  <RefreshClockwise className="size-3.5" />
                  {checking ? "Re-checking…" : "Re-check now"}
                </Button>
              )}
            </div>
          )
        }
      >
        {admin && (
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (domain.trim() && !adding) void addDomain()
            }}
            className="min-w-0"
          >
            {/* The button sits in the field's row so an error line under the
                input does not pull it down with it. */}
            <Field
              label="Domain to watch"
              htmlFor="watch-domain"
              hint="A name, host:port or URL, and optionally the IP address to reach it at"
              info="With an address, the handshake goes to that server and still asks for the name, so an origin behind a CDN or one server of several behind one name is checked on its own."
              error={fieldError && <span className="wrap-anywhere">{fieldError}</span>}
            >
              <div className="flex min-w-0 flex-wrap items-center gap-2 sm:flex-nowrap">
                <Input
                  id="watch-domain"
                  value={domain}
                  onChange={(event) => {
                    setDomain(event.target.value)
                    setFieldError(undefined)
                  }}
                  placeholder="example.com or mail.example.com:993"
                  aria-invalid={fieldError ? true : undefined}
                  spellCheck={false}
                  autoCapitalize="none"
                  autoCorrect="off"
                  inputMode="url"
                  className="min-w-0 flex-1"
                />
                <Input
                  id="watch-ip"
                  aria-label="Reach it at this IP address (optional)"
                  value={ip}
                  onChange={(event) => setIp(event.target.value)}
                  placeholder="IP address (optional)"
                  spellCheck={false}
                  autoCapitalize="none"
                  autoCorrect="off"
                  className="min-w-0 font-mono sm:w-44"
                />
                <Button
                  type="submit"
                  size="sm"
                  disabled={!domain.trim() || adding}
                  pending={adding}
                >
                  Watch
                </Button>
              </div>
            </Field>
            {interval !== undefined && (
              <Field label="Check" htmlFor="watch-interval" className="pt-3">
                <Select
                  value={String(interval)}
                  onValueChange={(value) => void changeInterval(Number(value))}
                >
                  <SelectTrigger id="watch-interval" className="w-full sm:w-56">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {INTERVALS.some((choice) => choice.seconds === interval) ? null : (
                      <SelectItem value={String(interval)}>{intervalLabel(interval)}</SelectItem>
                    )}
                    {INTERVALS.map((choice) => (
                      <SelectItem key={choice.seconds} value={String(choice.seconds)}>
                        {choice.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </form>
        )}
        <div>
          {watched.loading ? (
            <LoadingRows rows={2} />
          ) : watched.error ? (
            <ErrorState error={watched.error} onRetry={watched.refresh} />
          ) : !rows?.length ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing watched yet. The server checks a watched domain with a real handshake on its
              own schedule, which is what catches a certificate renewed on disk and never reloaded.
            </p>
          ) : (
            <ChoiceList aria-label="Watched domains" className="animate-rise">
              {rows.map((row) => (
                <ChoiceRow
                  key={row.id}
                  verb={admin ? `Inspect ${endpointLabel(row)}` : endpointLabel(row)}
                  disabled={!admin}
                  busy={removing === row.id}
                  href={admin ? tlsReportHref({ host: row.domain, port: row.port }) : undefined}
                  leading={
                    <ProductLogo
                      id={certificateProduct(row.certificate)}
                      size="sm"
                      fallback={Globe}
                    />
                  }
                  title={
                    <>
                      {row.domain}
                      {row.port !== 443 && (
                        <span className="numeric ml-1.5 font-mono text-hint text-muted-foreground">
                          :{row.port}
                        </span>
                      )}
                      {row.ip && (
                        <span className="ml-1.5 font-mono text-hint text-muted-foreground">
                          via {row.ip}
                        </span>
                      )}
                    </>
                  }
                  description={
                    row.certificate &&
                    [
                      row.certificate.issuer,
                      // A handshake that failed carries Go's zero time.
                      new Date(row.certificate.notAfter).getUTCFullYear() > 1 &&
                        `until ${calendarDate(row.certificate.notAfter)}`,
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  }
                  trailing={<ExpiryStatus cert={row.certificate} />}
                  className="gap-3 p-4"
                >
                  {row.certificate?.error && (
                    <p className="text-hint break-all text-destructive">{row.certificate.error}</p>
                  )}
                  {row.certificate && !row.certificate.error && (
                    <CertLife cert={row.certificate} className="w-full" />
                  )}
                  {/* How old the answer is gets a line of its own: at the end of
                      the description it was the part a phone cut off, and for
                      a read-only account it is the one new fact. */}
                  <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
                    <div className="flex min-w-0 items-center gap-3">
                      <p className="text-hint text-muted-foreground">
                        {removing === row.id
                          ? "Removing…"
                          : row.checkedAt
                            ? `checked ${relativeTime(row.checkedAt)}`
                            : "waiting for its first check"}
                      </p>
                      <CheckTrend id={row.id} checkedAt={row.checkedAt} />
                    </div>
                    {admin && (
                      <VerbBar
                        menuLabel={`More actions for ${endpointLabel(row)}`}
                        verbs={[
                          {
                            key: "scan",
                            label: "TLS report",
                            icon: Inspect,
                            inline: true,
                            run: () =>
                              router.push(tlsReportHref({ host: row.domain, port: row.port })),
                          },
                          {
                            key: "remove",
                            label: "Stop watching",
                            icon: Trash,
                            danger: true,
                            disabled: removing !== undefined,
                            run: () => void stopWatching(row),
                          },
                        ]}
                      />
                    )}
                  </div>
                </ChoiceRow>
              ))}
            </ChoiceList>
          )}
        </div>
      </FormSection>
    </FormSections>
  )
}

/**
 * How an endpoint's days left moved over its recent checks, and how many of
 * them failed. A renewal shows as a jump up; a line that only falls is a
 * certificate nothing is renewing.
 */
function CheckTrend({ id, checkedAt }: { id: number; checkedAt?: string }) {
  const history = usePoll(
    (signal) => get<WatchedCheck[]>(`/certificates/watched/${id}/history`, undefined, signal),
    0,
    [id, checkedAt],
    { enabled: checkedAt !== undefined },
  )
  const checks = history.data ?? []
  const days = checks.flatMap((check) => (check.daysLeft === undefined ? [] : [check.daysLeft]))
  const failed = checks.filter((check) => check.error).length
  if (checks.length < 2) return null
  return (
    <span className="flex items-center gap-2 text-hint text-muted-foreground">
      <Sparkline values={days} label="Days left over the recent checks" />
      {failed > 0 && <span className="numeric">{plural(failed, "failed check")}</span>}
    </span>
  )
}
