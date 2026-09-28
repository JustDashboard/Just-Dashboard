"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Globe, Inspect, RefreshClockwise, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, post } from "@/lib/api"
import { calendarDate } from "@/lib/format"
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

type Watched = { id: number; domain: string; port: number; certificate?: Certificate }

/**
 * Domains checked with a live handshake on a schedule. They stay apart from
 * the installed certificates because a live handshake can disagree with the
 * file on disk, and that disagreement is what they are for.
 */
export function WatchedDomains({
  admin,
  added = 0,
}: {
  admin: boolean
  /** Bumped when a domain is watched from elsewhere on the page, so the list shows it. */
  added?: number
}) {
  const router = useRouter()
  const [domain, setDomain] = useState("")
  const watched = usePoll(
    (signal) => get<Watched[]>("/certificates/watched", undefined, signal),
    300_000,
    [added],
  )

  const addDomain = async () => {
    // "host:port" for the services that answer TLS off 443: a mail server
    // on 993, a database on 5432 with TLS required.
    const [host, port] = domain.trim().split(":")
    try {
      await post("/certificates/watched", { domain: host, port: Number(port) || 443 })
      setDomain("")
      watched.refresh()
    } catch (err) {
      notify.error("Could not watch domain", err)
    }
  }

  return (
    <FormSections>
      <FormSection
        aside
        title="Watched domains"
        hint={`${watched.data?.length ?? 0} checked every five minutes`}
        actions={
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
            className="flex items-end gap-2"
          >
            <Field label="Domain to watch" htmlFor="watch-domain" className="min-w-0 flex-1">
              <Input
                id="watch-domain"
                value={domain}
                onChange={(event) => setDomain(event.target.value)}
                placeholder="example.com or mail.example.com:993"
              />
            </Field>
            <Button type="submit" size="sm" disabled={!domain.trim()}>
              Watch
            </Button>
          </form>
        )}
        <div>
          {watched.loading ? (
            <LoadingRows rows={2} />
          ) : watched.error ? (
            <ErrorState error={watched.error} />
          ) : !watched.data?.length ? (
            <p className="py-2 text-body text-muted-foreground">
              Nothing watched yet. A watched domain is checked with a real handshake every five
              minutes, which is what catches a certificate renewed on disk and never reloaded.
            </p>
          ) : (
            <ChoiceList aria-label="Watched domains" className="animate-rise">
              {watched.data.map((row) => (
                <ChoiceRow
                  key={row.id}
                  verb={admin ? `Inspect ${row.domain}` : row.domain}
                  disabled={!admin}
                  href={
                    admin
                      ? `/proxy/tls?domain=${encodeURIComponent(row.port === 443 ? row.domain : `${row.domain}:${row.port}`)}`
                      : undefined
                  }
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
                          row.certificate.notAfter &&
                            `until ${calendarDate(row.certificate.notAfter)}`,
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
                        menuLabel={`More actions for ${row.domain}`}
                        verbs={[
                          {
                            key: "scan",
                            label: "TLS report",
                            icon: Inspect,
                            inline: true,
                            run: () =>
                              router.push(
                                `/proxy/tls?domain=${encodeURIComponent(row.port === 443 ? row.domain : `${row.domain}:${row.port}`)}`,
                              ),
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
