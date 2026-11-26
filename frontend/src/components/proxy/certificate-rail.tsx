"use client"

import { ShieldCheck } from "@/components/icons"
import { plural } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { ProductLogo } from "@/components/product-logo"
import { certificateTone, certificateWords } from "@/lib/certificates"
import { StatusDot } from "@/components/status-dot"
import { VERDICT_TONE } from "@/components/tone"
import { Rail, RailFigure, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { certificateProduct } from "@/components/proxy/marks"

const SORT_WORDS = { urgency: "most urgent first", expiry: "soonest to expire", name: "by name" }

/**
 * A certificate's time left as its row's reading, in the words every
 * certificate reading shares (lib/certificates): "29d", "10h" in its last
 * day, and the state where days say nothing ("Expired", "Test certificate").
 */
function railWord(cert: Certificate): string {
  // The figure column is a few characters wide: a test certificate is
  // "Test" there, and the row's name says the rest.
  if (cert.staging && !cert.error) return "Test"
  return certificateWords(cert, { form: "short" }).replace(/ left$/, "")
}

/**
 * The list, folded into a column beside the certificate it opened: the same
 * search, source and order, so the next certificate to read is one press
 * away rather than a trip back to the list. Each row is the certificate as
 * its issuer's mark with its state in the corner, its name over the names it
 * covers, and its days; the one on screen is filled. A certificate the
 * filter leaves out is pinned at the top while it is the one on screen.
 */
export function CertificateRail({
  certs,
  rows,
  current,
  loading,
  error,
  onRetry,
  filters,
  hrefFor,
  className,
}: {
  certs: Certificate[] | undefined
  rows: Certificate[]
  current?: Certificate
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  filters: {
    query: string
    setQuery: (value: string) => void
    source: string
    setSource: (value: "all") => void
    state: string
    setState: (value: "all") => void
    sort: keyof typeof SORT_WORDS
  }
  hrefFor: (cert: Certificate) => string
  className?: string
}) {
  return (
    <Rail
      slot="certificate-rail"
      label="Certificates"
      search={{
        placeholder: "Name, a host it covers, or issuer",
        query: filters.query,
        onQuery: filters.setQuery,
      }}
      caption={
        certs
          ? `${plural(rows.length, "certificate")} · ${SORT_WORDS[filters.sort]}`
          : error
            ? undefined
            : "Reading the certificates…"
      }
      rows={rows}
      keyOf={(cert) => cert.path}
      current={(cert) => cert.path === current?.path}
      pinned={current}
      row={(cert, on) => <CertificateRow cert={cert} current={on} href={hrefFor(cert)} />}
      loading={loading}
      error={certs ? undefined : error}
      onRetry={onRetry}
      empty="No certificate matches."
      filtered={filters.source !== "all" || filters.state !== "all"}
      onClearFilters={() => {
        filters.setSource("all")
        filters.setState("all")
      }}
      className={className}
    />
  )
}

function CertificateRow({
  cert,
  current,
  href,
}: {
  cert: Certificate
  current: boolean
  href: string
}) {
  const verdict = certificateTone(cert)
  const word = railWord(cert)
  return (
    <RailRow
      item={cert.path}
      name={cert.name}
      label={`${cert.name}, ${certificateWords(cert, { form: "long" })}`}
      current={current}
      href={href}
      mark={
        <ProductLogo
          id={certificateProduct(cert)}
          size="sm"
          fallback={ShieldCheck}
          className="size-7 rounded-md [&_img]:size-4"
        />
      }
      dot={<StatusDot tone={VERDICT_TONE[verdict]} className="size-1.5" />}
      end={
        <RailFigure
          ink={
            verdict === "critical"
              ? "font-medium text-destructive"
              : railInk(verdict === "warning" ? "warning" : undefined)
          }
        >
          {word}
        </RailFigure>
      }
    >
      <RailName current={current}>{cert.name}</RailName>
      <RailLine className="font-mono">{cert.domains.join(", ") || "no names"}</RailLine>
    </RailRow>
  )
}
