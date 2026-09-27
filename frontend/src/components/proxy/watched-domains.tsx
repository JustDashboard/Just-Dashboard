"use client"

import { useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { Globe, Inspect, RefreshClockwise, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { ApiError, del, get, post } from "@/lib/api"
import { calendarDate, relativeTime } from "@/lib/format"
import { parseScanTarget, targetLabel, tlsReportHref } from "@/lib/scan-target"
import type { Certificate } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FormSection, FormSections } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { ErrorState, LoadingRows } from "@/components/state"
import { VerbBar } from "@/components/verbs"
import { CertLife, ExpiryStatus } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

type Watched = {
  id: number
  domain: string
  port: number
  /** When the certificate was read; absent until the first check. */
  checkedAt?: string
  certificate?: Certificate
}

/**
 * Endpoints checked with a live handshake. They stay apart from the installed
 * certificates because a live handshake can disagree with the file on disk,
 * and that disagreement is what they are for.
 *
 * Only an administrator's request checks them — the handshake is traffic to
 * another host, which a read-only account may not send — so everyone else
 * reads what the last check found, and every row says when that was.
 */
export function WatchedDomains({ admin }: { admin: boolean }) {
  const router = useRouter()
  const [domain, setDomain] = useState("")
  const [fieldError, setFieldError] = useState<string>()
  const [adding, setAdding] = useState(false)
  const [removing, setRemoving] = useState<number>()
  // Endpoints removed since the list was fetched. Fetching it again would
  // send an administrator's handshake to every other endpoint to show one
  // fewer row, so a removal the server confirmed is taken off here instead.
  const [removed, setRemoved] = useState<number[]>([])
  const watched = usePoll(
    (signal) => get<Watched[]>("/certificates/watched", undefined, signal),
    300_000,
  )
  // An administrator's request handshakes with every endpoint, which can take
  // up to half a minute; usePoll keeps the old list on screen meanwhile and
  // does not call that loading, so the re-check is tracked here until an
  // answer or an error replaces the list it was asked over. An endpoint just
  // added rides along: its row is on screen, being checked, from the moment
  // the server has it, where it used to appear only when the whole list came
  // back and nothing said anything was happening.
  const [recheckOver, setRecheckOver] = useState<
    Pick<typeof watched, "data" | "error"> & { added?: Watched }
  >()
  const rechecking =
    recheckOver !== undefined &&
    recheckOver.data === watched.data &&
    recheckOver.error === watched.error
  // Adding re-checks after its request returns, when the list this render
  // closed over may already have been replaced; the one on screen is read.
  const onScreen = useRef(watched)
  useEffect(() => {
    onScreen.current = watched
  })
  const recheck = (added?: Watched) => {
    setRecheckOver({ data: onScreen.current.data, error: onScreen.current.error, added })
    watched.refresh()
  }
  const added = rechecking ? recheckOver.added : undefined
  // Watch stays busy until the new endpoint's first check is on screen: a
  // second submission would restart the list's request and abandon it.
  const watching = adding || added !== undefined
  const rows = watched.data
    ?.concat(added && !watched.data.some((row) => row.id === added.id) ? [added] : [])
    .filter((row) => !removed.includes(row.id))

  const addDomain = async () => {
    // Read the way the TLS report reads its field: host:port for a service
    // off 443 (a mail server on 993), a pasted URL for its host, and an IPv6
    // address with its colons intact.
    const parsed = parseScanTarget(domain)
    if (!parsed.target) {
      setFieldError(parsed.error)
      return
    }
    const label = targetLabel(parsed.target)
    setAdding(true)
    try {
      const row = await post<Watched>("/certificates/watched", {
        domain: parsed.target.host,
        port: parsed.target.port,
      })
      setDomain("")
      if (rows?.some((shown) => shown.id === row.id)) {
        notify.info(`${label} is already watched`)
      } else {
        notify.success(`Watching ${label}`)
        recheck(row)
      }
    } catch (err) {
      notify.error("Could not watch domain", err)
    } finally {
      setAdding(false)
    }
  }

  const stopWatching = async (row: Watched) => {
    const label = targetLabel({ host: row.domain, port: row.port })
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
        hint={`${rows?.length ?? 0} watched, checked while an administrator has this page open`}
        actions={
          admin &&
          rows &&
          rows.length > 0 && (
            <Button variant="outline" size="sm" onClick={() => recheck()} pending={rechecking}>
              <RefreshClockwise className="size-3.5" />
              {rechecking ? "Re-checking…" : "Re-check now"}
            </Button>
          )
        }
      >
        {admin && (
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (domain.trim() && !watching) void addDomain()
            }}
            className="min-w-0"
          >
            {/* The button sits in the field's row so an error line under the
                input does not pull it down with it. */}
            <Field label="Domain to watch" htmlFor="watch-domain" error={fieldError}>
              <div className="flex min-w-0 items-center gap-2">
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
                />
                <Button
                  type="submit"
                  size="sm"
                  disabled={!domain.trim() || watching}
                  pending={watching}
                >
                  Watch
                </Button>
              </div>
            </Field>
          </form>
        )}
        <div>
          {watched.loading || (rechecking && watched.error) ? (
            <LoadingRows rows={2} />
          ) : watched.error ? (
            <ErrorState error={watched.error} onRetry={() => recheck()} />
          ) : !rows?.length ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing watched yet. A watched domain is checked with a real handshake when an
              administrator opens this page and every five minutes while it stays open, which is
              what catches a certificate renewed on disk and never reloaded.
            </p>
          ) : (
            <ChoiceList aria-label="Watched domains" className="animate-rise">
              {rows.map((row) => (
                <ChoiceRow
                  key={row.id}
                  verb={
                    admin
                      ? `Inspect ${targetLabel({ host: row.domain, port: row.port })}`
                      : row.domain
                  }
                  disabled={!admin}
                  busy={rechecking || removing === row.id}
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
                    <p className="text-hint text-muted-foreground">
                      {removing === row.id
                        ? "Removing…"
                        : row.checkedAt
                          ? `checked ${relativeTime(row.checkedAt)}`
                          : rechecking
                            ? "checking…"
                            : "not checked yet"}
                    </p>
                    {admin && (
                      <VerbBar
                        menuLabel={`More actions for ${targetLabel({ host: row.domain, port: row.port })}`}
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
