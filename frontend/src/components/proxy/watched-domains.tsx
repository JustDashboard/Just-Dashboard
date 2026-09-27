"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Globe, Inspect, RefreshClockwise, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
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
  const watched = usePoll(
    (signal) => get<Watched[]>("/certificates/watched", undefined, signal),
    300_000,
  )

  const addDomain = async () => {
    // Read the way the TLS report reads its field: host:port for a service
    // off 443 (a mail server on 993), a pasted URL for its host, and an IPv6
    // address with its colons intact.
    const parsed = parseScanTarget(domain)
    if (!parsed.target) {
      setFieldError(parsed.error)
      return
    }
    setAdding(true)
    try {
      await post("/certificates/watched", { domain: parsed.target.host, port: parsed.target.port })
      setDomain("")
      watched.refresh()
    } catch (err) {
      notify.error("Could not watch domain", err)
    } finally {
      setAdding(false)
    }
  }

  return (
    <FormSections>
      <FormSection
        aside
        title="Watched domains"
        hint={`${watched.data?.length ?? 0} watched, checked while an administrator has this page open`}
        actions={
          admin &&
          watched.data &&
          watched.data.length > 0 && (
            <Button variant="outline" size="sm" onClick={() => watched.refresh()}>
              <RefreshClockwise className="size-3.5" />
              Re-check now
            </Button>
          )
        }
      >
        {admin && (
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (domain.trim()) void addDomain()
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
                  disabled={!domain.trim() || adding}
                  pending={adding}
                >
                  Watch
                </Button>
              </div>
            </Field>
          </form>
        )}
        <div>
          {watched.loading ? (
            <LoadingRows rows={2} />
          ) : watched.error ? (
            <ErrorState error={watched.error} />
          ) : !watched.data?.length ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing watched yet. A watched domain is checked with a real handshake when an
              administrator opens this page and every five minutes while it stays open, which is
              what catches a certificate renewed on disk and never reloaded.
            </p>
          ) : (
            <ChoiceList aria-label="Watched domains" className="animate-rise">
              {watched.data.map((row) => (
                <ChoiceRow
                  key={row.id}
                  verb={
                    admin
                      ? `Inspect ${targetLabel({ host: row.domain, port: row.port })}`
                      : row.domain
                  }
                  disabled={!admin}
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
                    row.certificate
                      ? [
                          row.certificate.issuer,
                          // A handshake that failed carries Go's zero time.
                          new Date(row.certificate.notAfter).getUTCFullYear() > 1 &&
                            `until ${calendarDate(row.certificate.notAfter)}`,
                          row.checkedAt && `checked ${relativeTime(row.checkedAt)}`,
                        ]
                          .filter(Boolean)
                          .join(" · ")
                      : "not checked yet"
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
                  <div className="flex flex-wrap justify-end gap-2">
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
                            run: async () => {
                              await del(`/certificates/watched/${row.id}`)
                              watched.refresh()
                            },
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
