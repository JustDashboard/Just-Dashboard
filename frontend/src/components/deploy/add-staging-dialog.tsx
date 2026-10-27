"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import { Check, Minus } from "@/components/icons"
import { ApiError, post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DeploymentStagingCopy } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useProject } from "@/components/deploy/project-context"
import { deploymentURL, hostOf } from "@/components/deploy/vocabulary"

const REMOTE_GIT = new Set(["git_url", "connected_repository"])

/**
 * Add the deployment's staging environment.
 *
 * Staging is production copied once and then left to diverge: the dialog
 * asks only what the copy cannot decide for itself — which branch staging
 * builds, the address it answers at, and whether production's secrets come
 * too — and says, before anything is written, what crosses and what does not.
 * That list is the decision, so it is the body; the fields are three.
 *
 * Secrets stay behind unless asked for, because staging runs code production
 * has not shipped. Database links stay behind always: a staging release task
 * on production's database is exactly the migration staging exists to try
 * first. After the copy the dialog reads back what was left behind by name,
 * since those are what staging needs before its first deploy can succeed.
 */
export function AddStagingDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const router = useRouter()
  const project = useProject()
  const source = project.configuration?.source
  const remote = Boolean(source && REMOTE_GIT.has(source.mode))
  const productionHost = hostOf(deploymentURL(project.detail.deployment.endpoint))
  const suggestion = productionHost ? `staging.${productionHost.replace(/^www\./, "")}` : ""
  const [branch, setBranch] = useState("staging")
  const [domain, setDomain] = useState(suggestion)
  const [secrets, setSecrets] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ field: "branch" | "domain"; message: string }>()
  const [result, setResult] = useState<DeploymentStagingCopy>()

  // Radix keeps the dialog mounted across close and open, so the form is
  // reset on the way in rather than relying on a remount.
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setBranch("staging")
      setDomain(suggestion)
      setSecrets(false)
      setError(undefined)
      setResult(undefined)
    }
  }

  const create = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const copied = await post<DeploymentStagingCopy>(
        `/deploy/${project.projectId}/environments`,
        {
          kind: "staging",
          ref: remote ? branch.trim() : undefined,
          domain: domain.trim() || undefined,
          copySecrets: secrets,
        },
      )
      setResult(copied)
      project.refreshEnvironments()
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "ref_not_found") {
        setError({ field: "branch", message: `The repository has no branch called ${branch}.` })
      } else if (caught instanceof ApiError && caught.code === "domain_taken") {
        setError({ field: "domain", message: "Another environment already answers at that name." })
      } else if (caught instanceof ApiError && caught.code === "invalid_domain") {
        setError({ field: "domain", message: "Enter a hostname such as staging.example.com." })
      } else {
        notify.error("Could not add staging", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  const open_ = (path: string) => {
    project.selectEnvironment("staging")
    onOpenChange(false)
    router.push(`/deploy/${project.projectId}${path}`)
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title={result ? "Staging is ready" : "Add a staging environment"}
      description="Copy production's configuration into a staging environment of its own."
      size="md"
      footer={
        result ? (
          <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
            {result.skipped.some((item) => item.kind === "variable") && (
              <Button variant="outline" onClick={() => open_("/settings/variables")}>
                Set staging&apos;s variables
              </Button>
            )}
            <Button onClick={() => open_("")}>Open staging</Button>
          </div>
        ) : (
          <div className="flex w-full flex-wrap items-center justify-end gap-2 max-sm:[&>*]:flex-1">
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              onClick={() => void create()}
              pending={busy}
              disabled={busy || (remote && !branch.trim())}
            >
              Add staging
            </Button>
          </div>
        )
      }
    >
      {result ? (
        <StagingResult result={result} />
      ) : (
        <form
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault()
            void create()
          }}
        >
          {remote && (
            <Field
              label="Branch"
              htmlFor="staging-branch"
              hint="Staging builds this branch, and deploys it on every push."
              error={error?.field === "branch" ? error.message : undefined}
            >
              <Input
                id="staging-branch"
                value={branch}
                onChange={(event) => setBranch(event.target.value)}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
          )}
          <Field
            label="Domain"
            htmlFor="staging-domain"
            hint="Optional. Staging answers here over HTTPS; production's domains stay production's."
            error={error?.field === "domain" ? error.message : undefined}
          >
            <Input
              id="staging-domain"
              value={domain}
              onChange={(event) => setDomain(event.target.value)}
              placeholder="staging.example.com"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>

          <section aria-label="What staging gets" className="space-y-2">
            <p className="eyebrow">From production</p>
            <ul className="space-y-1.5 text-body">
              <CopyLine copied>Build, runtime, health checks and release tasks</CopyLine>
              <CopyLine copied>Plain variables and shared-variable references</CopyLine>
              <CopyLine copied={secrets}>
                {secrets ? "Secrets, as production has them" : "Secrets: staging sets its own"}
              </CopyLine>
              <CopyLine>Database links: link a database of staging&apos;s own</CopyLine>
              <CopyLine>Volumes: staging starts with new, empty ones</CopyLine>
            </ul>
          </section>

          <OptionList>
            <OptionRow
              title="Also copy production's secrets"
              hint="Staging runs code production has not shipped. With production's keys it can charge real cards and mail real customers."
              tone={secrets ? "warning" : "default"}
              checked={secrets}
              onCheckedChange={setSecrets}
            />
          </OptionList>
        </form>
      )}
    </Modal>
  )
}

function CopyLine({ copied = false, children }: { copied?: boolean; children: React.ReactNode }) {
  const Mark = copied ? Check : Minus
  return (
    <li className="flex min-w-0 items-start gap-2">
      <Mark
        aria-hidden
        className={
          copied
            ? "mt-0.5 size-4 shrink-0 text-success"
            : "mt-0.5 size-4 shrink-0 text-muted-foreground"
        }
      />
      <span className="sr-only">{copied ? "Copied: " : "Not copied: "}</span>
      <span className={copied ? undefined : "text-muted-foreground"}>{children}</span>
    </li>
  )
}

/** What the copy did, by name, so what staging still needs is the next thing read. */
function StagingResult({ result }: { result: DeploymentStagingCopy }) {
  const variables = result.skipped.filter((item) => item.kind === "variable")
  const links = result.skipped.filter((item) => item.kind !== "variable")
  return (
    <div className="space-y-5">
      <p className="text-body">
        {result.copied.length === 0
          ? "No variable was copied."
          : `${plural(result.copied.length, "variable")} copied from production.`}
      </p>
      {variables.length > 0 && (
        <Notice tone="warning" title="Set these before staging's first deploy">
          <ul className="mt-1 space-y-1">
            {variables.map((item) => (
              <li key={item.name} className="flex min-w-0 flex-wrap gap-x-2">
                <span className="font-mono text-foreground">{item.name}</span>
                <span className="text-muted-foreground">{item.reason}</span>
              </li>
            ))}
          </ul>
        </Notice>
      )}
      {links.length > 0 && (
        <section aria-label="Not linked" className="space-y-1.5">
          <p className="eyebrow">Not linked</p>
          <ul className="space-y-1 text-body">
            {links.map((item) => (
              <li key={item.name} className="flex min-w-0 flex-wrap gap-x-2">
                <span className="font-mono">{item.name}</span>
                <span className="text-muted-foreground">{item.reason}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {result.adjusted.length > 0 && (
        <section aria-label="Changed for staging" className="space-y-1.5">
          <p className="eyebrow">Changed for staging</p>
          <ul className="space-y-1 text-body text-muted-foreground">
            {result.adjusted.map((line) => (
              <li key={line}>{line}</li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
