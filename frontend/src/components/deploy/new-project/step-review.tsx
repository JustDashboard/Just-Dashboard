"use client"

import Link from "next/link"
import { FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { Group } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import type { DeploymentConfiguration, DeploymentPreflightFinding } from "@/lib/types"
import { FindingRow, findingRemedy } from "@/components/deploy/deployment-findings"
import { WORKLOAD_LABELS } from "@/components/deploy/vocabulary"
import { AutomaticDeployment } from "@/components/deploy/new-project/automatic-deployment"
import type { ConfigureFlow, DraftGitPolicy } from "@/components/deploy/new-project/draft"

/**
 * Step four: **is this right.**
 *
 * The step that did not exist. Preflight is a step of the draft's own state
 * machine on the server, and it used to run inside the press of Deploy — so
 * its findings appeared at the foot of a screen the reader had already
 * finished reading, under a button they had already pressed, and the button's
 * word changed under their finger from "Deploy" to "Re-check and deploy".
 * Given a screen of its own it is what it always was: the last look before
 * the thing is made.
 *
 * What it reads back is what the plan drawing beside it does not. The drawing
 * carries the four things a request passes through — source, build, container,
 * name — with the port, the strategy, the limits and the hostname on them, and
 * repeating any of those here would be the same sentence twice. What it cannot
 * carry is everything the release will *do to this server*: the data it keeps
 * between rebuilds, the secrets it mints, the question it asks the application
 * before sending anyone to it, and what happens to the previous release at the
 * cutover. Those are the four sections below, and each of them is derived from
 * the plan rather than from the source, so an adopted container and a reviewed
 * template read the same way.
 *
 * Before this, none of that was here. A template with no blocking finding drew
 * one section of four facts against a four-node drawing half a screen taller
 * than it, and the emptiest screen in the sequence was the one under the
 * irreversible button.
 */
export function StepReview({
  flow,
  branch,
  gitPolicy,
  onGitPolicyChange,
  variableCount,
  suppliedVariables = [],
  findings,
  checking,
  blockers,
  warnings,
  acknowledged,
  onAcknowledgedChange,
  onOpenRemedy,
  canOpenRemedy,
}: {
  flow: ConfigureFlow
  branch?: string
  gitPolicy: DraftGitPolicy
  onGitPolicyChange: (policy: DraftGitPolicy) => void
  /** Rows with a key, which is what actually reaches the project. */
  variableCount: number
  suppliedVariables?: string[]
  /** Everything preflight reported, passes included. */
  findings: DeploymentPreflightFinding[]
  /** Preflight is in flight: it runs on arrival, not under the button. */
  checking: boolean
  blockers: DeploymentPreflightFinding[]
  warnings: DeploymentPreflightFinding[]
  acknowledged: string[]
  onAcknowledgedChange: (codes: string[]) => void
  onOpenRemedy: (finding: DeploymentPreflightFinding) => void
  canOpenRemedy: (finding: DeploymentPreflightFinding) => boolean
}) {
  const isGitSource = flow.source.kind === "git" || flow.source.kind === "local"
  const configuration = flow.configuration
  const declared = configuration.variables
  const generated = declared.filter(
    (variable) => (variable.generate ?? 0) > 0 && !suppliedVariables.includes(variable.name),
  )
  const mounts = configuration.runtime.mounts ?? []
  const readiness = configuration.checks.filter((check) => check.phase === "readiness")
  const smoke = configuration.checks.filter((check) => check.phase === "smoke")
  const gated = flow.profile === "web" || flow.profile === "static"

  return (
    <>
      <FormSection title="Project">
        <FormFacts>
          <FormFact label="Name" mono>
            {flow.name || "Unnamed"}
          </FormFact>
          <FormFact label="Type">{WORKLOAD_LABELS[flow.profile] ?? flow.profile}</FormFact>
          {/* What actually reaches the container, which is not what the
              environment editor holds: a reviewed template declares its own
              variables and mints its own secrets, so a plan carrying five of
              them read "1 value" — the count of the rows somebody typed. */}
          <FormFact label="Variables">{variablesFact(declared.length, variableCount)}</FormFact>
          {/* No "Release" fact: the drawing beside this already says
              "stop first", and what the strategy *costs* is the only part of
              it anybody is deciding — which is its own section below. */}
        </FormFacts>
      </FormSection>

      <FormSection title="Data it keeps">
        {mounts.length === 0 ? (
          <FormNote>
            Nothing survives a rebuild. Every release starts from the image, so anything the
            application writes is gone when the next one replaces it.
          </FormNote>
        ) : (
          <ul className="min-w-0 space-y-1.5 text-hint">
            {mounts.map((mount) => {
              const dependency = configuration.dependencies.find(
                (entry) => entry.kind === "storage" && entry.resourceId === mount.source,
              )
              const config = (dependency?.config ?? {}) as { purpose?: string; backup?: boolean }
              return (
                <li key={`${mount.source}:${mount.target}`} className="min-w-0">
                  <span className="block font-mono break-all text-foreground">{mount.target}</span>
                  <span className="block text-muted-foreground">
                    {[
                      config.purpose,
                      mount.ownership === "managed"
                        ? "kept in a managed volume"
                        : "an existing path on this host",
                      mount.readOnly && "read-only",
                      config.backup && "included in backups",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </FormSection>

      {generated.length > 0 && (
        <FormSection title="Secrets made on this server">
          <ul className="min-w-0 space-y-1 text-hint">
            {generated.map((variable) => (
              <li key={variable.name} className="min-w-0">
                <span className="font-mono text-foreground">{variable.name}</span>
                <span className="text-muted-foreground">
                  {" — "}
                  {variable.generate} characters, made when this plan is saved
                </span>
              </li>
            ))}
          </ul>
          {/* Said once, here: a generated value is the one thing on the plan
              that cannot be recovered by reading the source it came from. */}
          <FormNote>
            Each is minted from this server&apos;s own randomness, stored sealed, and never shipped
            with the source. Back them up with the data they protect.
          </FormNote>
        </FormSection>
      )}

      <FormSection title="Before it takes traffic">
        {readiness.length === 0 ? (
          <FormNote tone={gated ? "warning" : "default"}>
            {gated
              ? "No readiness check. The release goes live without asking the application whether it started, so a container that crashes on boot still takes the address."
              : "No readiness check. This workload answers no requests, so the release is complete once the container is running."}
          </FormNote>
        ) : (
          <ul className="min-w-0 space-y-1 text-hint">
            {[...readiness, ...smoke].map((check, index) => (
              <li key={`${check.phase}:${check.name}:${index}`} className="min-w-0">
                <span className="text-foreground">{check.name}</span>
                <span className="text-muted-foreground">
                  {" — "}
                  {[
                    checkTarget(check),
                    checkBudget(check),
                    check.phase === "smoke" && "after it is live",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </li>
            ))}
          </ul>
        )}
      </FormSection>

      <FormSection title="At the cutover">
        <FormNote>
          {/* "Nothing is lost" only when something is kept: with no mounts the
              section above has just said the opposite. */}
          {configuration.runtime.strategy === "blue_green"
            ? "The new container starts beside the running one and only takes the address once it has answered, so a release that never becomes ready changes nothing."
            : mounts.length === 0
              ? "The running container is stopped before the new one starts, so on every release after the first this project is unreachable for a few seconds."
              : "The running container is stopped before the new one starts. Nothing is lost — the data above is kept — but on every release after the first this project is unreachable for a few seconds."}
        </FormNote>
      </FormSection>

      {isGitSource && (
        <AutomaticDeployment branch={branch} policy={gitPolicy} onChange={onGitPolicyChange} />
      )}

      {(blockers.length > 0 || warnings.length > 0) && (
        <FormSection title="Findings">
          <div className="space-y-2">
            {blockers.map((finding, index) => (
              <FindingRow
                key={`${finding.code}:${finding.fieldId ?? index}`}
                finding={finding}
                index={index}
                onOpenRemedy={onOpenRemedy}
                canOpenRemedy={canOpenRemedy(finding)}
              />
            ))}
          </div>
          {warnings.length > 0 && (
            <Group tone="warning" className="space-y-2">
              {warnings.map((finding, index) => (
                <Label
                  key={`${finding.code}:${finding.fieldId ?? index}`}
                  /* An `OptionRow`'s anatomy, at an `OptionRow`'s sizes: title
                     at body, everything under it at hint. It was one 12px
                     block throughout, which is a step the ladder does not have
                     between the two (§8). */
                  className="flex min-h-11 items-start gap-3 text-hint leading-relaxed"
                >
                  <Checkbox
                    className="mt-0.5"
                    checked={acknowledged.includes(finding.code)}
                    onCheckedChange={(checked) =>
                      onAcknowledgedChange(
                        checked
                          ? [...new Set([...acknowledged, finding.code])]
                          : acknowledged.filter((code) => code !== finding.code),
                      )
                    }
                  />
                  <span className="min-w-0">
                    <span className="block text-body font-medium">{finding.title}</span>
                    <span className="mt-0.5 block text-muted-foreground">
                      {finding.measured || finding.means}
                    </span>
                    {/* A warning is a thing to accept *or* fix, and this said
                        only what it was: the remedy and the owning feature's
                        page were dropped, so "link a backup job" arrived with
                        nowhere to do it. */}
                    {findingRemedy(finding) && (
                      <span className="mt-1 block font-normal">
                        <b className="font-medium">Next:</b> {findingRemedy(finding)}
                        {finding.deepLink && (
                          <>
                            {" · "}
                            <Link href={finding.deepLink} className="underline underline-offset-2">
                              Open owning page
                            </Link>
                          </>
                        )}
                      </span>
                    )}
                  </span>
                </Label>
              ))}
            </Group>
          )}
        </FormSection>
      )}

      <PassedChecks findings={findings} checking={checking} />
    </>
  )
}

/**
 * What preflight asked this server and got a straight answer to.
 *
 * Every pass used to be discarded — `blockingFindings` and `warningFindings`
 * are the only two filters the screen had — so a plan with nothing wrong with
 * it said nothing at all, and the reader could not tell a checked plan from an
 * unchecked one. They are a line each, no measurement and no remedy: a pass
 * has no remedy, and its measurement is on the drawing or in the sections
 * above.
 */
function PassedChecks({
  findings,
  checking,
}: {
  findings: DeploymentPreflightFinding[]
  checking: boolean
}) {
  // Said by the Address node and by the sections above respectively; a pass
  // whose whole content is already on screen is noise at the foot of it.
  const covered = new Set(["dns_verified", "domain_ownership", "detection_selected"])
  const passed = findings.filter(
    (finding) => finding.severity === "pass" && !covered.has(finding.code),
  )
  if (checking) {
    return (
      <FormSection title="Checked against this server">
        <FormNote>Asking this server about the plan…</FormNote>
      </FormSection>
    )
  }
  if (passed.length === 0) return null
  return (
    <FormSection title="Checked against this server">
      <ul className="min-w-0 space-y-1">
        {passed.map((finding, index) => (
          <li key={`${finding.code}:${index}`} className="min-w-0">
            <Status verdict="ok" label={finding.title} className="font-normal" />
          </li>
        ))}
      </ul>
    </FormSection>
  )
}

/**
 * "3 declared · 2 typed", dropping whichever is zero.
 *
 * Two different things reach the container and the screen counted only one of
 * them: a reviewed template declares its own variables and mints its own
 * secrets, so a plan carrying five read "1 value" — the rows somebody had
 * typed into the environment editor. A Git repository is the other way round
 * and declares none.
 */
function variablesFact(declared: number, typed: number) {
  const parts = [declared > 0 && `${declared} declared`, typed > 0 && `${typed} typed`].filter(
    Boolean,
  )
  return parts.length === 0 ? "None" : parts.join(" · ")
}

/** What a check actually asks — a path, a command, or a port. */
function checkTarget(check: DeploymentConfiguration["checks"][number]) {
  const config = (check.config ?? {}) as { path?: string; command?: string[]; port?: number }
  if (config.path) return `GET ${config.path}`
  if (config.command?.length) return config.command.join(" ")
  if (config.port) return `a connection on ${config.port}`
  return check.kind
}

/** How long the release will keep asking before it gives up. */
function checkBudget(check: DeploymentConfiguration["checks"][number]) {
  const config = (check.config ?? {}) as { attempts?: number; intervalSeconds?: number }
  if (!config.attempts) return undefined
  const seconds = config.attempts * (config.intervalSeconds ?? 1)
  return `up to ${seconds < 120 ? `${seconds}s` : `${Math.round(seconds / 60)} min`} to answer`
}
