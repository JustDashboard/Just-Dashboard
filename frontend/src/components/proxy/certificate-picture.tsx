"use client"

import { createRef, useRef, useState } from "react"
import Link from "next/link"
import { ArrowRight, Globe, LockClosed, ShieldCheck, ArrowLeftRight } from "@/components/icons"
import { plural } from "@/lib/format"
import type { Certificate, VHost } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { CertLife, expiryTone } from "@/components/proxy/expiry-status"
import { certificateProduct, siteProduct } from "@/components/proxy/marks"
import { sitePath } from "@/components/proxy/site-verbs"
import { originWord } from "@/components/proxy/certificate-inventory"

/** The most users drawn at the picture's end; the facts under it list every one. */
const SHOWN_USERS = 4

type User = {
  key: string
  name: string
  href: string
  kind: "site" | "stream"
  product?: string
  /** Serving now: a disabled site names the file and serves nothing. */
  live: boolean
}

/**
 * What the certificate is between: the authority that signed it, the
 * certificate with the time it has left, and the sites and streams that
 * serve it. A line carries only where both ends are up — a certificate a
 * browser accepts, a site that is on — and it is amber in the renewal window
 * and red once browsers refuse it, so the picture says before a word is read
 * whether what serves it is in trouble.
 */
export function CertificatePicture({ cert, vhosts }: { cert: Certificate; vhosts?: VHost[] }) {
  const container = useRef<HTMLDivElement>(null)
  const authorityNode = useRef<HTMLDivElement>(null)
  const certNode = useRef<HTMLDivElement>(null)
  // The far ends as plain ref objects made once: a picture draws up to four.
  const [ends] = useState(() => [
    createRef<HTMLDivElement>(),
    createRef<HTMLDivElement>(),
    createRef<HTMLDivElement>(),
    createRef<HTMLDivElement>(),
  ])

  const users: User[] = [
    ...cert.usedBy.map((name) => {
      const vhost = vhosts?.find((v) => v.name === name)
      return {
        key: `site:${name}`,
        name,
        href: sitePath(name),
        kind: "site" as const,
        product: vhost ? siteProduct(vhost) : "nginx-static",
        live: vhost ? vhost.enabled : true,
      }
    }),
    ...(cert.usedByStreams ?? []).map((name) => ({
      key: `stream:${name}`,
      name,
      href: `/proxy/streams?stream=${encodeURIComponent(name)}`,
      kind: "stream" as const,
      product: "nginx-static",
      live: true,
    })),
  ]
  const tone = expiryTone(cert)
  const lineTone = tone === "danger" ? "danger" : tone === "warning" ? "warning" : "default"
  // A certificate browsers refuse is not carried anywhere, whatever names it.
  const accepted = !cert.error && !cert.expired && !cert.staging
  const product = certificateProduct(cert)

  return (
    <section
      aria-label="Where the certificate comes from and who serves it"
      data-slot="certificate-picture"
      className="relative animate-rise py-4"
    >
      <div ref={container} className="relative min-w-0">
        <AnimatedBeam
          containerRef={container}
          fromRef={authorityNode}
          toRef={certNode}
          shape="arc"
          still={!accepted}
          tone={lineTone}
        />
        {users.slice(0, SHOWN_USERS).map((user, index) => (
          <AnimatedBeam
            key={user.key}
            containerRef={container}
            fromRef={certNode}
            toRef={ends[index]}
            shape="s"
            still={!accepted || !user.live}
            tone={lineTone}
            delay={(index + 1) * 0.35}
          />
        ))}

        <ol className="grid min-w-0 gap-y-7 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.3fr)] lg:items-center lg:gap-x-6 lg:pb-16">
          <li className="min-w-0">
            <WireNode
              nodeRef={authorityNode}
              align="center"
              mark={
                <WireMark tone="logo">
                  {product && hasProductLogo(product) ? (
                    <ProductGlyph id={product} />
                  ) : cert.selfSigned ? (
                    <LockClosed aria-hidden />
                  ) : (
                    <ShieldCheck aria-hidden />
                  )}
                </WireMark>
              }
              eyebrow="Signed by"
              title={
                <span className="block truncate" title={cert.issuer}>
                  {cert.selfSigned ? "itself" : cert.issuer || "an unknown authority"}
                </span>
              }
              hint={cert.source === "caddy" ? "Caddy renews it" : originWord(cert)}
            />
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={certNode}
              align="center"
              mark={
                <WireMark
                  tone={tone === "danger" ? "danger" : tone === "warning" ? "warning" : "success"}
                >
                  <LockClosed aria-hidden />
                </WireMark>
              }
              eyebrow={plural(cert.domains.length, "name")}
              title={
                <span
                  className="block truncate font-mono"
                  title={cert.domains.join(", ") || undefined}
                >
                  {cert.domains[0] ?? "no names"}
                  {cert.domains.length > 1 && (
                    <span className="text-muted-foreground"> +{cert.domains.length - 1}</span>
                  )}
                </span>
              }
              hint={
                <span className="flex flex-col items-center gap-1.5 max-lg:items-start">
                  <span
                    className={cn(
                      tone === "danger" && "text-destructive",
                      tone === "warning" && "text-warning",
                    )}
                  >
                    {cert.error
                      ? "could not be read"
                      : cert.staging
                        ? "a test certificate"
                        : cert.expired
                          ? "expired"
                          : `${plural(cert.daysLeft, "day")} left`}
                  </span>
                  {!cert.error && <CertLife cert={cert} className="w-24" />}
                </span>
              }
            />
          </li>
          <li className="flex min-w-0 flex-col gap-5">
            {users.length === 0 ? (
              <WireNode
                nodeRef={ends[0]}
                mark={<WirePlaceholder fallback={Globe} />}
                eyebrow="Served by"
                title={<span className="text-muted-foreground">Nothing names it</span>}
                hint="no site or stream points at this file"
              />
            ) : (
              users.slice(0, SHOWN_USERS).map((user, index) => (
                <WireNode
                  key={user.key}
                  nodeRef={ends[index]}
                  mark={
                    <Link
                      href={user.href}
                      aria-label={`Open ${user.kind} ${user.name}`}
                      className="rounded-xl focus-ring"
                    >
                      <WireMark tone={user.live ? "logo" : "neutral"}>
                        {user.kind === "stream" ? (
                          <ArrowLeftRight aria-hidden />
                        ) : user.product && hasProductLogo(user.product) ? (
                          <ProductGlyph id={user.product} />
                        ) : (
                          <Globe aria-hidden />
                        )}
                      </WireMark>
                    </Link>
                  }
                  eyebrow={index === 0 ? "Served by" : undefined}
                  title={
                    <Link
                      href={user.href}
                      className="inline-flex max-w-full items-center gap-1 rounded-sm focus-ring hover:underline"
                    >
                      <span className="truncate">{user.name}</span>
                      <ArrowRight aria-hidden className="size-3 shrink-0" />
                    </Link>
                  }
                  hint={user.kind === "stream" ? "stream" : user.live ? "site" : "site · disabled"}
                />
              ))
            )}
            {users.length > SHOWN_USERS && (
              <p className="pl-[4.375rem] text-hint text-muted-foreground">
                and {plural(users.length - SHOWN_USERS, "more")}
              </p>
            )}
          </li>
        </ol>
      </div>
    </section>
  )
}
