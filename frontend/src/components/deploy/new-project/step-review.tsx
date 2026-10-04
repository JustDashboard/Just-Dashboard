"use client"

import Link from "next/link"
import { Disclosure, FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { Group } from "@/components/panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import type { DeploymentConfiguration, DeploymentPreflightFinding } from "@/lib/types"
import { FindingRow, findingRemedy } from "@/components/deploy/deployment-findings"
import { variableFixLabel } from "@/components/deploy/failure-cause"
import { WORKLOAD_LABELS } from "@/components/deploy/vocabulary"
import { AutomaticDeployment } from "@/components/deploy/new-project/automatic-deployment"
import { Notice } from "@/components/state"
import type { ConfigureFlow, DraftGitPolicy } from "@/components/deploy/new-project/draft"
import { sourceWatchesGit } from "@/components/deploy/new-project/draft"

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
  onApplyFix,
  onInspectAgain,
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
  /**
   * Applies a finding's computed plan change to the plan this screen holds,
   * which the check then answers again; how it reads comes from
   * `variableFixLabel`.
   */
  onApplyFix?: (finding: DeploymentPreflightFinding) => void
  /**
   * Reads the source again at the branch's newer commit, keeping the chosen
   * candidate — `source_moved`'s remedy, since accepting the warning deploys
   * the commit this screen checked instead.
   */
  onInspectAgain?: () => void
}) {
  const isGitSource = sourceWatchesGit(flow.source)
  const configuration = flow.configuration
  const adoption = flow.draft.data.adoption
  const declared = configuration.variables
  const generated = declared.filter(
    (variable) => (variable.generate ?? 0) > 0 && !suppliedVariables.includes(variable.name),
  )
  // Plain values the plan carries itself — an address that follows the
  // domain, a documented default — read back so none of them is a surprise.
  const settled = declared.filter(
    (variable) =>
      variable.sensitivity === "plain" &&
      variable.value &&
      !suppliedVariables.includes(variable.name),
  )
  const mounts = configuration.runtime.mounts ?? []
  const compose = configuration.build.method === "compose"
  const composeMounts = (flow.detection?.compose?.services ?? []).flatMap((service) =>
    (service.mounts ?? []).map((mount) => ({ service: service.name, mount })),
  )
  const readiness = configuration.checks.filter((check) => check.phase === "readiness")
  const smoke = configuration.checks.filter((check) => check.phase === "smoke")
  const gated = flow.profile === "web" || flow.profile === "static"

  return (
    <>
      {adoption && (
        <FormSection title="Existing live deployment">
          <FormFacts>
            <FormFact label="Original name" mono>
              {adoption.name}
            </FormFact>
            <FormFact label="Manager">{adoption.manager}</FormFact>
            <FormFact label="Services">
              {adoption.runningCount} of {adoption.serviceCount} running
            </FormFact>
            <FormFact label="Private inputs">
              {flow.draft.environmentKeys?.length ?? 0} saved on the server
            </FormFact>
            {adoption.kind === "stack" && adoption.scope && (
              <FormFact label="Recovery scope">
                {adoption.scope === "existing_services"
                  ? "Existing containers only · running and stopped"
                  : "Every declared service"}
              </FormFact>
            )}
          </FormFacts>
          <Notice title="Adoption keeps this application running">
            The current services become the live baseline. No deployment run starts, and stopped
            services stay stopped. Deploy changes uses the settings below; Redeploy live release
            restores the original baseline.
          </Notice>
          {(adoption.excludedServices?.length ?? 0) > 0 && (
            <Notice title="Services excluded from this deployment" tone="warning">
              These declared services have no existing container. The recovered recipe will not
              create them on Deploy changes; this does not remove any existing container.
              <ul aria-label="Excluded Compose services" className="mt-2 space-y-1 font-mono">
                {adoption.excludedServices!.map((service) => (
                  <li key={service}>{service}</li>
                ))}
              </ul>
            </Notice>
          )}
          {(adoption.blockers?.length ?? 0) > 0 && (
            <Notice title="Resolve migration blockers before adoption" tone="danger">
              <ul className="space-y-1">
                {adoption.blockers.map((blocker, index) => (
                  <li key={index}>{blocker}</li>
                ))}
              </ul>
            </Notice>
          )}
          {(adoption.warnings ?? [])
            .filter(
              (warning) =>
                !findings.some(
                  (finding) =>
                    finding.title === warning ||
                    finding.means === warning ||
                    finding.measured === warning,
                ),
            )
            .map((warning, index) => (
              <FormNote key={index} tone="warning">
                {warning}
              </FormNote>
            ))}
        </FormSection>
      )}
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
        {compose && (
          <>
            <FormNote>
              Each service keeps the volumes and bind mounts declared in the Compose source. The
              fields below are additional overrides, so an empty list does not mean the stack has no
              persistent storage. Review the full service configuration below.
            </FormNote>
            {composeMounts.length > 0 && (
              <ul aria-label="Compose service mounts" className="min-w-0 space-y-1.5 text-hint">
                {composeMounts.map(({ service, mount }, index) => (
                  <li key={`${service}-${index}`} className="min-w-0 break-all">
                    <span className="font-mono text-foreground">{service}</span>{" "}
                    <span className="font-mono text-muted-foreground">{mount}</span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
        {mounts.length === 0 ? (
          !compose && (
            <FormNote>
              Nothing survives a rebuild. Every release starts from the image, so anything the
              application writes is gone when the next one replaces it.
            </FormNote>
          )
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
                  {adoption && <span className="block font-mono break-all">{mount.source}</span>}
                  <span className="block text-muted-foreground">
                    {[
                      config.purpose,
                      mount.ownership === "managed"
                        ? "kept in a managed volume"
                        : dependency?.resourceKind === "volume"
                          ? "an existing named volume"
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

      {compose && (flow.source.composeFiles?.length ?? 0) > 0 && (
        <Disclosure summary="Compose service configuration">
          <FormNote>
            These saved files carry per-service images, limits, ports, commands and storage. Private
            inputs stay on the server behind their variable references. After adoption, edit these
            files in Settings → General; saving changes leaves the live baseline running until you
            choose Deploy changes.
          </FormNote>
          {flow.source.composeFiles!.map((document, index) => (
            <Textarea
              key={`${document.path}-${index}`}
              aria-label={`Reviewed ${document.path}`}
              readOnly
              value={document.content || "This file is read from the source directory."}
              rows={10}
              className="font-mono sm:text-xs"
            />
          ))}
        </Disclosure>
      )}

      {generated.length > 0 && (
        <FormSection title="Secrets made on this server">
          <ul className="min-w-0 space-y-1 text-hint">
            {generated.map((variable) => (
              <li key={variable.name} className="min-w-0">
                <span className="font-mono text-foreground">{variable.name}</span>
                <span className="text-muted-foreground">
                  {" — "}
                  {secretShape(variable)}, made when this plan is saved
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

      {settled.length > 0 && (
        <FormSection title="Values set in the plan">
          <ul className="min-w-0 space-y-1 text-hint">
            {settled.map((variable) => (
              <li key={variable.name} className="min-w-0 break-all">
                <span className="font-mono text-foreground">{variable.name}</span>
                <span className="text-muted-foreground">
                  {" — "}
                  <span className="font-mono">{variable.value}</span>
                  {variable.domainTemplate ? ", follows the domain" : ""}
                </span>
              </li>
            ))}
          </ul>
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

      <FormSection title={adoption ? "When you deploy changes" : "At the cutover"}>
        {adoption && (
          <FormNote>
            Adoption does not perform this cutover. Deploy changes or an enabled automatic
            deployment does.
          </FormNote>
        )}
        <FormNote>
          {/* "Nothing is lost" only when something is kept: with no mounts the
              section above has just said the opposite. */}
          {adoption && configuration.runtime.strategy === "stop_first"
            ? "The first deployment of changes stops the original runtime before the replacement starts, causing an outage. Review the recovered storage and backup coverage first; state outside the declared mounts is not preserved by a rebuild."
            : configuration.runtime.strategy === "blue_green"
              ? "The new container starts beside the running one and only takes the address once it has answered, so a release that never becomes ready changes nothing."
              : mounts.length === 0
                ? "The running container is stopped before the new one starts, so on every release after the first this project is unreachable for a few seconds."
                : "The running container is stopped before the new one starts. Nothing is lost — the data above is kept — but on every release after the first this project is unreachable for a few seconds."}
        </FormNote>
      </FormSection>

      {isGitSource && (
        <AutomaticDeployment
          branch={branch}
          policy={gitPolicy}
          onChange={onGitPolicyChange}
          storageKey={
            adoption ? `deploy.new.configure.adoption.${flow.draft.id}.gitPolicy` : undefined
          }
        />
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
                fixAction={
                  finding.fix && onApplyFix
                    ? { label: variableFixLabel(finding.fix), onApply: () => onApplyFix(finding) }
                    : undefined
                }
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
                    {finding.fix && onApplyFix && (
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="mt-1.5"
                        onClick={(event) => {
                          event.preventDefault()
                          event.stopPropagation()
                          onApplyFix(finding)
                        }}
                      >
                        {variableFixLabel(finding.fix)}
                      </Button>
                    )}
                    {finding.code === "source_moved" && onInspectAgain && (
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="mt-1.5"
                        onClick={(event) => {
                          event.preventDefault()
                          event.stopPropagation()
                          onInspectAgain()
                        }}
                      >
                        Inspect again
                      </Button>
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
  // A pass whose measurement is the answer, not a restatement of its title:
  // which lockfile was chosen and why, and the commands the build will run.
  const readings = new Set([
    "build_commands",
    "package_manager_resolved",
    "package_manager_version",
  ])
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
            {readings.has(finding.code) && finding.measured && (
              <p className="pl-3 font-mono text-hint break-words text-muted-foreground">
                {finding.measured}
              </p>
            )}
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

/** A generated secret's shape, in the words its framework's own generator would use. */
function secretShape(variable: DeploymentConfiguration["variables"][number]) {
  switch (variable.generateFormat) {
    case "hex":
      return `${variable.generate} hex characters`
    case "base64":
      return `${variable.generate} random bytes, base64`
    case "laravel":
      return `a Laravel base64: key over ${variable.generate} random bytes`
    case "keylist":
      return `four keys of ${variable.generate} random bytes each`
  }
  return `${variable.generate} characters`
}

/** What a check actually asks — a path, a command, or a port. */
function checkTarget(check: DeploymentConfiguration["checks"][number]) {
  const config = (check.config ?? {}) as {
    path?: string
    command?: string[]
    port?: number
    acceptAnyAnswer?: boolean
  }
  if (config.path) return `GET ${config.path}${config.acceptAnyAnswer ? " (any answer)" : ""}`
  if (check.kind === "docker_health") return "the image's HEALTHCHECK"
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
