"use client"

import { useEffect, useRef, useState } from "react"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, SiteCertificates, SiteRead, SiteResult, SiteSpec } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { JobIssuanceProblems } from "@/components/proxy/issuance-problems"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { saveRequest } from "@/components/proxy/site-save"

type StepState = "waiting" | "running" | "done" | "failed"
type Step = { key: string; label: string; state: StepState }

const STEP_STATUS: Record<StepState, { tone: DotTone; label: string }> = {
  waiting: { tone: "unknown", label: "waiting" },
  running: { tone: "notice", label: "running" },
  done: { tone: "running", label: "done" },
  failed: { tone: "danger", label: "failed" },
}

/**
 * Whether the file on disk answers the HTTP challenge from the dashboard's
 * webroot: saved with it, and not a redirect, whose server-level return is
 * answered before any path is looked at — unless its plain-HTTP half is the
 * redirect server a forced-HTTPS site carries, which serves the path first.
 */
function servesChallenge(spec: SiteSpec | undefined): boolean {
  if (!spec?.managedAcme) return false
  return spec.kind !== "redirect" || (spec.tls && spec.forceHttps)
}

/**
 * The certificate half of the site form: the installed certificates that
 * cover every domain, and "Get a certificate", which has certbot issue one
 * over the site's own plain-HTTP port and switches the site onto it.
 *
 * A site whose file does not serve the challenge yet is saved first — as it
 * is live, or over plain HTTP for a new one — because Let's Encrypt asks
 * nginx for the token before anything is signed. Once that save is made the
 * HTTPS switch is saved too, so the file never waits half-way; when no save
 * was needed the paths are only filled in, and Save is the operator's.
 */
export function SiteCertificate({
  spec,
  disk,
  existing,
  baseDigest,
  served,
  onUse,
  onRebased,
  onDone,
}: {
  spec: SiteSpec
  /** The file as last read, for an edit. */
  disk: SiteRead | null
  existing: boolean
  baseDigest?: string
  /** nginx reads the file a save writes: not a disabled site, nor one whose served copy is another file. */
  served: boolean
  onUse: (pair: { certPath: string; keyPath: string }) => void
  /** The HTTP save wrote the file: the form measures its draft from this version now. */
  onRebased: (read: SiteRead) => void
  /** The HTTPS save wrote the file. */
  onDone: (result: SiteResult) => void
}) {
  const domains = spec.domains
  const covering = usePoll<SiteCertificates>(
    (signal) => get("/certificates/covering", { domains: domains.join(",") }, signal),
    0,
    [domains.join(",")],
    { enabled: domains.length > 0 },
  )
  const [email, setEmail] = useState<string | null>(null)
  const [dryRun, setDryRun] = useState(true)
  const [steps, setSteps] = useState<Step[]>([])
  const [busy, setBusy] = useState(false)
  // The version of the file this run saved over plain HTTP, so a retry after
  // a failed order does not save it again — for a new site, it no longer is.
  const [saved, setSaved] = useState<SiteRead | null>(null)
  const jobs = useJobConsole()
  // Resolved when the job the console is on finishes, with whether it
  // succeeded: the steps wait on certbot, which answers over the socket.
  const settle = useRef<((ok: boolean) => void) | null>(null)
  useEffect(() => {
    if (!jobs.job || jobs.job.status === "running") return
    settle.current?.(jobs.job.status === "succeeded")
    settle.current = null
  }, [jobs.job])

  const certificates = covering.data?.certificates ?? []
  const shownEmail = email ?? covering.data?.email ?? ""
  const wildcard = domains.some((d) => d.startsWith("*."))
  const needsSave = !saved && !servesChallenge(disk?.spec)

  const mark = (key: string, state: StepState) =>
    setSteps((all) => all.map((s) => (s.key === key ? { ...s, state } : s)))

  const issue = async (dry: boolean): Promise<boolean> => {
    const job = await post<Job>("/certificates/issue", {
      domains,
      email: shownEmail,
      method: "webroot",
      webRoot: covering.data?.webRoot,
      dryRun: dry,
    })
    const done = new Promise<boolean>((resolve) => {
      settle.current = resolve
    })
    jobs.attach(job)
    return done
  }

  const run = async () => {
    const plan: Step[] = [
      ...(needsSave ? [{ key: "http", label: "Save so nginx answers the challenge" }] : []),
      ...(dryRun ? [{ key: "dry", label: "Dry run against the staging authority" }] : []),
      { key: "issue", label: `Issue the certificate for ${domains.join(", ")}` },
      saved || needsSave
        ? { key: "https", label: "Save the site with HTTPS on" }
        : { key: "https", label: "Fill in the certificate and switch HTTPS on" },
    ].map((s) => ({ ...s, state: "waiting" as const }))
    setSteps(plan)
    setBusy(true)
    let step = plan[0].key
    try {
      let file = saved
      if (needsSave) {
        // As it is live for an edit — a working certificate stays in use
        // until the new one is — and over plain HTTP for a new site.
        const live = disk?.spec
        const http: SiteSpec = {
          ...spec,
          managedAcme: true,
          tls: live?.tls ?? false,
          certPath: live?.certPath,
          keyPath: live?.keyPath,
          forceHttps: live?.forceHttps ?? spec.forceHttps,
        }
        mark("http", "running")
        await post<SiteResult>(
          "/proxy/sites/",
          saveRequest(http, { existing, reload: true, baseDigest }),
        )
        file = await get<SiteRead>(`/proxy/sites/${encodeURIComponent(spec.name)}`)
        setSaved(file)
        onRebased(file)
        mark("http", "done")
      }
      if (dryRun) {
        step = "dry"
        mark("dry", "running")
        if (!(await issue(true))) throw new Error("The dry run failed — the console says why.")
        mark("dry", "done")
      }
      step = "issue"
      mark("issue", "running")
      if (!(await issue(false))) throw new Error("certbot did not issue it — the console says why.")
      mark("issue", "done")

      step = "https"
      mark("https", "running")
      const fresh = await get<SiteCertificates>("/certificates/covering", {
        domains: domains.join(","),
      })
      const pair = fresh.certificates[0]
      if (!pair)
        throw new Error("certbot finished, but no certificate on disk covers every domain.")
      onUse({ certPath: pair.path, keyPath: pair.keyPath })
      if (file) {
        const result = await post<SiteResult>(
          "/proxy/sites/",
          saveRequest(
            { ...spec, managedAcme: true, tls: true, certPath: pair.path, keyPath: pair.keyPath },
            { existing: true, reload: true, baseDigest: file.digest },
          ),
        )
        mark("https", "done")
        onDone(result)
        return
      }
      mark("https", "done")
      covering.refresh()
    } catch (err) {
      mark(step, "failed")
      notify.error(step === "http" || step === "https" ? "Not applied" : "No certificate", err)
    } finally {
      setBusy(false)
    }
  }

  const blocked = wildcard
    ? "A wildcard is only signed against a DNS challenge; issue it on the Certificates page."
    : spec.kind === "redirect" && !servesChallenge(disk?.spec)
      ? "A redirect answers before the challenge path is read; issue its certificate on the Certificates page."
      : !served
        ? "nginx does not read this site's file, so it cannot answer the challenge; enable it first."
        : !spec.name
          ? "Name the site first."
          : undefined

  return (
    <div className="space-y-3">
      <Field label="Installed certificates" hint="Only those covering every domain are listed.">
        {covering.loading && !covering.data ? (
          <FormNote>Looking for a certificate…</FormNote>
        ) : certificates.length === 0 ? (
          <FormNote>None covers every domain yet.</FormNote>
        ) : (
          <ul className="divide-y divide-hairline">
            {certificates.map((c) => (
              <li key={c.path} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2">
                <div className="min-w-0 flex-1 basis-48">
                  <p className="truncate text-xs font-medium" title={c.path}>
                    {c.name}
                  </p>
                  <p className="text-hint break-all text-muted-foreground">
                    {c.domains.join(", ")} · {c.issuer}
                  </p>
                </div>
                <Status
                  verdict={c.daysLeft <= 7 ? "critical" : c.daysLeft <= 30 ? "warning" : "ok"}
                  label={`${c.daysLeft}d left`}
                />
                <Button
                  size="xs"
                  variant="ghost"
                  disabled={spec.certPath === c.path && spec.keyPath === c.keyPath}
                  onClick={() => onUse({ certPath: c.path, keyPath: c.keyPath })}
                >
                  Use
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Field>

      {blocked ? (
        <FormNote>{blocked}</FormNote>
      ) : (
        <div className="space-y-3">
          <Field
            label="Contact email"
            htmlFor="site-cert-email"
            hint="Where Let's Encrypt sends expiry warnings. Remembered for next time."
          >
            <Input
              id="site-cert-email"
              type="email"
              value={shownEmail}
              onChange={(e) => setEmail(e.target.value)}
              className="font-mono text-xs"
            />
          </Field>
          <OptionList>
            <OptionRow
              title="Dry run first"
              hint="Runs the whole order against the staging authority and saves nothing, so a challenge that cannot reach this host costs none of the five failures an hour the real one allows."
              checked={dryRun}
              onCheckedChange={setDryRun}
            />
          </OptionList>
          {needsSave && (
            <FormNote>
              {existing
                ? "The site is saved first, as it is now served, so nginx answers the challenge."
                : "The site is saved over plain HTTP first, so nginx answers the challenge."}
            </FormNote>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={run}
            disabled={busy || !shownEmail.trim() || !covering.data}
            pending={busy}
          >
            Get a certificate
          </Button>
        </div>
      )}

      {steps.length > 0 && (
        <ol className="divide-y divide-hairline">
          {steps.map((s) => (
            <li key={s.key} className="flex items-center justify-between gap-3 py-1.5 text-xs">
              <span className="min-w-0">{s.label}</span>
              <Status
                tone={STEP_STATUS[s.state].tone}
                label={STEP_STATUS[s.state].label}
                live={s.state === "running"}
              />
            </li>
          ))}
        </ol>
      )}
      <JobConsole job={jobs.job} lines={jobs.lines} onCancel={jobs.cancel} />
      <JobIssuanceProblems job={jobs.job} />
    </div>
  )
}
