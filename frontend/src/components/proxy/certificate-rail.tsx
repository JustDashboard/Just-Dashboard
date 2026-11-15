"use client"

import { ShieldCheck } from "@/components/icons"
import { plural } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { ProductLogo } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { Rail, RailFigure, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import { expiryTone } from "@/components/proxy/expiry-status"
import { certificateProduct } from "@/components/proxy/marks"

const SORT_WORDS = { urgency: "most urgent first", expiry: "soonest to expire", name: "by name" }

/** A certificate's days, as its row's reading: the word where days say nothing. */
function daysWord(cert: Certificate): string {
  if (cert.error) return "unreadable"
  if (cert.staging) return "test"
  if (cert.expired) return "expired"
  return `${cert.daysLeft}d`
}

function dotTone(cert: Certificate): DotTone {
  const tone = expiryTone(cert)
  return tone === "danger" ? "danger" : tone === "warning" ? "warning" : "running"
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
      filtered={filters.source !== "all"}
      onClearFilters={() => filters.setSource("all")}
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
  const tone = expiryTone(cert)
  return (
    <RailRow
      item={cert.path}
      name={cert.name}
      label={`${cert.name}, ${daysWord(cert)}`}
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
      dot={<StatusDot tone={dotTone(cert)} className="size-1.5" />}
      end={
        <RailFigure ink={tone === "danger" ? "font-medium text-destructive" : railInk(tone)}>
          {daysWord(cert)}
        </RailFigure>
      }
    >
      <RailName current={current}>{cert.name}</RailName>
      <RailLine className="font-mono">{cert.domains.join(", ") || "no names"}</RailLine>
    </RailRow>
  )
}
