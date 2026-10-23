"use client"

import { useState } from "react"
import Link from "next/link"
import { ArrowRight, ChartActivity, Download, Globe, ShieldCheck } from "@/components/icons"
import { post, get } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, UpdateReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { OptionRow } from "@/components/form"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { ProductLogo } from "@/components/product-logo"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { CERTBOT_PACKAGES } from "@/components/proxy/engine"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/** What the page draws once nginx answers, said before it does. */
const GAINS = [
  {
    icon: Globe,
    title: "Sites",
    text: "Put a domain in front of any app, container or folder on this machine.",
  },
  {
    icon: ShieldCheck,
    title: "Certificates",
    text: "Issue and renew TLS from Let's Encrypt, every expiry on one runway.",
  },
  {
    icon: ChartActivity,
    title: "Traffic",
    text: "Requests, server errors and nginx's own log, as they happen.",
  },
]

/**
 * The overview of a host with no reverse proxy: one question — install
 * nginx? — at the page's reading size, with the brand command that does it,
 * the exact package-manager line it runs, and what the page becomes after.
 *
 * It was two `xs` outline buttons in a row of grey text above an empty
 * Certificates block, an empty Routes list and a Recent changes rail, so the
 * one thing a reader on this host could do was the smallest thing on it.
 * certbot rides along by default because a proxy without certificates is the
 * next thing the page would ask for; the switch takes it out.
 *
 * Installing runs through the Packages page's own route as one job, streamed
 * under the command; when it lands the status is read again and the page
 * draws the engine in this one's place.
 */
export function EngineSetup({
  status,
  admin,
  onInstalled,
}: {
  status: ProxyStatus
  /** May install packages (system.admin). */
  admin: boolean
  /** Reads the status and the engine again once the install lands. */
  onInstalled: () => void
}) {
  const updates = usePoll(
    (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
    0,
    [],
    { enabled: admin },
  )
  const [withCertbot, setWithCertbot] = useState(true)
  const [starting, setStarting] = useState(false)
  const console_ = useJobConsole({
    onSuccess: () => {
      notify.success("nginx is installed")
      onInstalled()
    },
  })

  const report = updates.error ? undefined : updates.data
  const manager = report?.available ? report.manager : undefined
  const certbotPackages = manager && !status.certbot ? CERTBOT_PACKAGES[manager] : undefined
  const packages = ["nginx", ...(withCertbot && certbotPackages ? certbotPackages : [])]
  const running = console_.job?.status === "running"
  const busy = starting || running

  const install = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/packages/install", { packages }))
    } catch (err) {
      notify.error("Could not start installing nginx", err)
    } finally {
      setStarting(false)
    }
  }

  return (
    <section
      aria-labelledby="engine-setup-title"
      data-slot="engine-setup"
      className="grid animate-rise gap-x-14 gap-y-10 pt-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]"
    >
      <div className="min-w-0 space-y-7">
        <div className="space-y-4">
          <ProductLogo id="nginx" className="size-12 rounded-xl" />
          <div className="space-y-2">
            <h2
              id="engine-setup-title"
              className="text-2xl leading-tight font-semibold tracking-tight text-balance"
            >
              Put nginx in front of this server
            </h2>
            <p className="max-w-xl text-sm leading-relaxed text-pretty text-muted-foreground">
              nginx answers on ports 80 and 443 and hands each domain to the app behind it. Once it
              is running, this page draws every route, its certificate and its traffic.
            </p>
          </div>
        </div>

        {!admin ? (
          <p className="text-body text-muted-foreground">
            An administrator can install <span className="font-mono text-foreground">nginx</span>{" "}
            from here.
          </p>
        ) : updates.error ? (
          <ErrorState error={updates.error} onRetry={updates.refresh} />
        ) : report && !manager ? (
          <p className="max-w-xl text-body leading-relaxed text-muted-foreground">
            The dashboard found no package manager it can drive on this host. Install nginx from a
            shell, then press Refresh.
          </p>
        ) : (
          <div className="max-w-xl space-y-5">
            {certbotPackages && (
              <OptionRow
                title="Also install certbot"
                hint={
                  <>
                    Free certificates from Let&rsquo;s Encrypt for every site, renewed on a timer.
                    Adds <span className="font-mono">{certbotPackages.join(" ")}</span>.
                  </>
                }
                checked={withCertbot}
                onCheckedChange={setWithCertbot}
                disabled={busy}
              />
            )}
            <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
              <Button
                size="lg"
                onClick={() => void install()}
                pending={busy || updates.loading}
                disabled={busy || updates.loading}
              >
                <Download />
                {running
                  ? "Installing…"
                  : packages.length > 1
                    ? "Install nginx and certbot"
                    : "Install nginx"}
              </Button>
              {manager && (
                <code className="min-w-0 font-mono text-hint break-all text-muted-foreground">
                  {manager} install {packages.join(" ")}
                </code>
              )}
            </div>
            {status.ingressState === "provisionable" && (
              <p className="text-body text-muted-foreground">
                Or let a deployment bring its own: a Caddy ingress starts with the first project.{" "}
                <Link
                  href="/deploy/new"
                  className="inline-flex items-center gap-1 rounded-sm font-medium text-foreground focus-ring hover:underline"
                >
                  Deploy a project <ArrowRight className="size-3" />
                </Link>
              </p>
            )}
          </div>
        )}

        <JobConsole
          job={console_.job}
          lines={console_.lines}
          onDismiss={console_.dismiss}
          onCancel={console_.cancel}
        />
      </div>

      <ul
        aria-label="What the page shows once nginx runs"
        className="min-w-0 divide-y divide-hairline self-start border-y border-hairline"
      >
        {GAINS.map(({ icon: Icon, title, text }) => (
          <li key={title} className="flex min-w-0 gap-3.5 py-4">
            <Icon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <div className="min-w-0 space-y-1">
              <p className="text-body font-medium">{title}</p>
              <p className="text-xs leading-relaxed text-pretty text-muted-foreground">{text}</p>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}
