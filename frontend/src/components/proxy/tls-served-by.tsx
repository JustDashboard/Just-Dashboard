"use client"

import { useState } from "react"
import Link from "next/link"
import { post } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Certificate, ProxyValidation, TLSOrigin } from "@/lib/types"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status, type Verdict } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

const STATE: Record<TLSOrigin["state"], { verdict: Verdict; label: string }> = {
  current: { verdict: "ok", label: "Serving the current file" },
  stale: { verdict: "warning", label: "Stale, renewed on disk" },
  other: { verdict: "warning", label: "Another certificate on this host answered" },
  foreign: { verdict: "notice", label: "Not a certificate on this host" },
  unserved: { verdict: "critical", label: "No site serves this name" },
  unknown: { verdict: "notice", label: "File not traced" },
}

const MATCH: Record<NonNullable<TLSOrigin["match"]>, string> = {
  exact: "server_name, exact",
  wildcard: "server_name, wildcard",
  regex: "server_name, regular expression",
  default: "default server for the port",
}

/**
 * The site and file behind the answer. A certificate renewed on disk is not
 * what visitors get until nginx reads it again, and this is the one place the
 * file and the handshake are side by side, so the reload is offered here and
 * the scan asked again once it is done, which is how the reader sees it took.
 */
export function TLSServedBy({ origin, onRescan }: { origin: TLSOrigin; onRescan: () => void }) {
  const { reloading, reload } = useNginxReload(onRescan)
  const state = STATE[origin.state]

  return (
    <Panel plain>
      <PanelHeader
        title="Served by"
        actions={<Status verdict={state.verdict} label={state.label} />}
      />
      <PanelBody>
        <div className="min-w-0 space-y-4">
          <p className="text-body break-words">{origin.detail}</p>
          <DetailList>
            <Detail label="Site">
              {origin.site ? (
                <Link
                  href={`/proxy/sites?site=${encodeURIComponent(origin.site)}`}
                  className="font-mono wrap-anywhere underline-offset-4 hover:underline"
                >
                  {origin.site}
                </Link>
              ) : (
                "none"
              )}
            </Detail>
            {origin.match && (
              <Detail label="Chosen by">
                {origin.match === "default" && !origin.defaultFlag
                  ? "first site loaded for the port, none is marked default_server"
                  : MATCH[origin.match]}
              </Detail>
            )}
            {origin.file && <CertificateFact label="File" cert={origin.file} />}
            {origin.served && <CertificateFact label="Answered with" cert={origin.served} />}
          </DetailList>
          {origin.state === "stale" && (
            <Button
              type="button"
              size="xs"
              onClick={() => void reload()}
              disabled={reloading}
              pending={reloading}
            >
              Reload nginx
            </Button>
          )}
          {origin.state === "unserved" && (
            <Button variant="outline" size="xs" asChild>
              <Link href="/proxy/sites">Create a site on the Sites page</Link>
            </Button>
          )}
        </div>
      </PanelBody>
    </Panel>
  )
}

/**
 * Test, then reload, then scan again. The test first, so a configuration
 * nginx would refuse is shown as its own output rather than as a failed
 * reload.
 */
export function useNginxReload(onReloaded: () => void) {
  const [reloading, setReloading] = useState(false)
  const reload = async () => {
    setReloading(true)
    try {
      const test = await post<ProxyValidation>("/proxy/test", { kind: "nginx" })
      if (!test.valid) {
        notify.error("nginx refuses its configuration", undefined, {
          description: test.output.slice(0, 400),
        })
        return
      }
      await post("/proxy/reload", { kind: "nginx" })
      notify.success("nginx reloaded", { description: "Scanning again." })
      onReloaded()
    } catch (err) {
      notify.error("Reload refused", err)
    } finally {
      setReloading(false)
    }
  }
  return { reloading, reload }
}

function CertificateFact({ label, cert }: { label: string; cert: Certificate }) {
  return (
    <Detail label={label}>
      <Link
        href={`/proxy/certificates?cert=${encodeURIComponent(cert.path)}`}
        className="font-mono break-all underline-offset-4 hover:underline"
      >
        {cert.path}
      </Link>
      {!cert.error && (
        <span className="block text-muted-foreground">
          {cert.expired ? "expired" : "expires"} {timestamp(cert.notAfter)}
        </span>
      )}
    </Detail>
  )
}
