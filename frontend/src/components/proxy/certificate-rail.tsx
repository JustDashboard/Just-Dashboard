"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { ShieldCheck } from "@/components/icons"
import { plural } from "@/lib/format"
import type { Certificate } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductLogo } from "@/components/product-logo"
import { LoadingRows } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { CertificateSearch } from "@/components/proxy/certificate-inventory"
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
  filters,
  hrefFor,
  className,
}: {
  certs: Certificate[] | undefined
  rows: Certificate[]
  current?: Certificate
  loading: boolean
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
  const listed = rows.some((row) => row.path === current?.path)
  const pinned = !listed ? current : undefined
  const list = useRef<HTMLOListElement>(null)
  // The certificate on screen is kept in view: opened from a deep link or by
  // Alt+↓ past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>("[aria-current='page']")
      ?.scrollIntoView({ block: "nearest" })
  }, [current?.path])
  const filtered = filters.source !== "all"

  return (
    <nav
      aria-label="Certificates"
      data-slot="certificate-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <CertificateSearch
          value={filters.query}
          onChange={filters.setQuery}
          className="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">
            {certs
              ? `${plural(rows.length, "certificate")} · ${SORT_WORDS[filters.sort]}`
              : "Reading the certificates…"}
          </span>
          {filtered && (
            <button
              type="button"
              onClick={() => filters.setSource("all")}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Certificates to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow cert={pinned} current href={hrefFor(pinned)} />}
        {pinned && rows.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {rows.map((cert) => (
          <RailRow
            key={cert.path}
            cert={cert}
            current={cert.path === current?.path}
            href={hrefFor(cert)}
          />
        ))}
        {loading && rows.length === 0 && <LoadingRows rows={6} />}
        {!loading && rows.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No certificate matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({ cert, current, href }: { cert: Certificate; current: boolean; href: string }) {
  const tone = expiryTone(cert)
  return (
    <li className="min-w-0">
      <Link
        href={href}
        data-workspace-item={cert.path}
        data-workspace-name={cert.name}
        aria-current={current ? "page" : undefined}
        aria-label={`${cert.name}, ${daysWord(cert)}`}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <ProductLogo
            id={certificateProduct(cert)}
            size="sm"
            fallback={ShieldCheck}
            className="size-7 rounded-md [&_img]:size-4"
          />
          <span
            aria-hidden
            className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
          >
            <StatusDot tone={dotTone(cert)} className="size-1.5" />
          </span>
        </span>
        <span className="min-w-0 flex-1">
          <span
            className={cn("block truncate text-body", current ? "font-semibold" : "font-medium")}
          >
            {cert.name}
          </span>
          <span className="block truncate font-mono text-micro text-muted-foreground">
            {cert.domains.join(", ") || "no names"}
          </span>
        </span>
        <span
          className={cn(
            "numeric shrink-0 font-mono text-hint",
            tone === "danger"
              ? "font-medium text-destructive"
              : tone === "warning"
                ? "text-warning"
                : "text-muted-foreground",
          )}
        >
          {daysWord(cert)}
        </span>
      </Link>
    </li>
  )
}
