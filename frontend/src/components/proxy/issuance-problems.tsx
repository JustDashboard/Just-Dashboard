"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { get } from "@/lib/api"
import type { IssuanceDiagnosis, IssuanceProblem, Job } from "@/lib/types"

/**
 * Where a certbot run failed: each domain's problem at the stage of
 * validation it failed — resolving the name, reaching port 80, serving the
 * challenge file, the DNS record, the authority's limits — with that stage's
 * owner, what to do, and the page that holds the owner's evidence.
 */
export function IssuanceProblems({ problems }: { problems: IssuanceProblem[] }) {
  if (problems.length === 0) return null
  return (
    <section aria-label="Where it failed" className="min-w-0 space-y-2">
      <h3 className="text-body font-medium">Where it failed</h3>
      <ul className="divide-y divide-hairline">
        {problems.map((p, i) => (
          <li key={`${p.domain ?? ""}:${i}`} className="min-w-0 space-y-1 py-2">
            <p className="text-body">
              {p.domain && <span className="font-mono">{p.domain} · </span>}
              <span className="font-medium">{p.stageTitle}</span>
              {" — "}
              {p.owner}
            </p>
            <p className="text-body break-words">{p.action}</p>
            <p className="font-mono text-hint break-all text-muted-foreground">
              {p.address &&
                (p.here === undefined
                  ? `${p.address}: `
                  : `${p.address} (${p.here ? "this host" : "not this host"}): `)}
              {p.detail.replace(/^\S+: (?=Fetching|Invalid response)/, "")}
            </p>
            {p.links.length > 0 && (
              <p className="flex flex-wrap gap-x-3 gap-y-1 text-hint">
                {p.links.map((link) => (
                  <Link key={link.href} href={link.href} className="underline underline-offset-4">
                    {link.label}
                  </Link>
                ))}
              </p>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The problems of a finished certbot job, read from its own output once it has failed. */
export function JobIssuanceProblems({ job }: { job: Job | null }) {
  const [read, setRead] = useState<{ id: string; problems: IssuanceProblem[] } | null>(null)
  const failed = job && job.status === "failed" && job.kind.startsWith("certbot.") ? job.id : null
  useEffect(() => {
    if (!failed) return
    const abort = new AbortController()
    get<IssuanceDiagnosis>(`/certificates/jobs/${failed}/diagnosis`, undefined, abort.signal)
      .then((d) => setRead({ id: failed, problems: d.problems }))
      .catch(() => {})
    return () => abort.abort()
  }, [failed])
  if (!failed || read?.id !== failed) return null
  return <IssuanceProblems problems={read.problems} />
}
