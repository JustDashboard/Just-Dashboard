"use client"

import Link from "next/link"
import { Archive, Check, Heart, Key, Route, ShieldCheck } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Disclosure, FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { Group } from "@/components/panel"
import { ProductLogo, variableProduct } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import type { DeploymentPreflightFinding } from "@/lib/types"
import { FindingRow, findingRemedy } from "@/components/deploy/deployment-findings"
import { variableFixLabel } from "@/components/deploy/failure-cause"
import { StepMark, type ReleaseNodeState } from "@/components/deploy/vocabulary"
import { CODE, ShellWords } from "@/components/deploy/run-evidence"
import { AutomaticDeployment } from "@/components/deploy/new-project/automatic-deployment"
import { Notice } from "@/components/state"
import type { ConfigureFlow, DraftGitPolicy } from "@/components/deploy/new-project/draft"
import { sourceWatchesGit } from "@/components/deploy/new-project/draft"
import { groupImportWarnings } from "@/lib/workload-import"
import { RecoveredWorkloadPlan } from "./recovered-workload-plan"
import { checkBudget, checkTarget, secretShape } from "@/components/deploy/new-project/plan-reading"
import type { Icon } from "@/components/icons"

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
 * It opens on the answer: what this server said about the plan, the way the
 * run page opens on how a run ended — the findings to fix and the warnings to
 * acknowledge under one line saying what stands between the plan and Deploy.
 *
 * What it reads back after that is what the rail beside it does not. The rail
 * carries the project, its source, build, container, address and the rest one
 * row each, and repeating any of those here would be the same sentence twice —
 * which is why the name, the type and the variable count left this screen.
 * What it cannot carry is everything the release will *do to this server*: the
 * data it keeps between rebuilds, the secrets it mints, the question it asks
 * the application before sending anyone to it, and what happens to the
 * previous release at the cutover. Each of those is drawn as what it is — a
 * path, a variable on the product its name says holds it, a request — and
 * each is derived from the plan rather than from the source, so an adopted
 * container and a reviewed template read the same way.
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
  suppliedVariables = [],
  findings,
  checking,
  blockers,
  warnings,
  acknowledged,
  acknowledgementsDisabled,
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
  suppliedVariables?: string[]
  /** Everything preflight reported, passes included. */
  findings: DeploymentPreflightFinding[]
  /** Preflight is in flight: it runs on arrival, not under the button. */
  checking: boolean
  blockers: DeploymentPreflightFinding[]
  warnings: DeploymentPreflightFinding[]
  acknowledged: string[]
  acknowledgementsDisabled: boolean
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
      <Verdict
        findings={findings}
        checking={checking}
        blockers={blockers}
        warnings={warnings}
        acknowledged={acknowledged}
        acknowledgementsDisabled={acknowledgementsDisabled}
        onAcknowledgedChange={onAcknowledgedChange}
        onOpenRemedy={onOpenRemedy}
        canOpenRemedy={canOpenRemedy}
        onApplyFix={onApplyFix}
        onInspectAgain={onInspectAgain}
      />

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
      {adoption && <RecoveredWorkloadPlan adoption={adoption} />}
      <FormSection title="Data it keeps">
        {compose && (
          <>
            <FormNote>
              Each service keeps the volumes and bind mounts declared in the Compose source. The
              fields below are additional overrides, so an empty list does not mean the stack has no
              persistent storage. Review the full service configuration below.
            </FormNote>
            {composeMounts.length > 0 && (
              <ul aria-label="Compose service mounts" className="min-w-0 space-y-1">
                {composeMounts.map(({ service, mount }, index) => (
                  <ReviewItem
                    key={`${service}-${index}`}
                    glyph={Archive}
                    title={<span className="font-mono">{service}</span>}
                    detail={<code className={cn("font-mono break-all", CODE.path)}>{mount}</code>}
                  />
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
          <ul aria-label="Kept between releases" className="min-w-0 space-y-1">
            {mounts.map((mount) => {
              const dependency = configuration.dependencies.find(
                (entry) => entry.kind === "storage" && entry.resourceId === mount.source,
              )
              const config = (dependency?.config ?? {}) as { purpose?: string; backup?: boolean }
              return (
                <ReviewItem
                  key={`${mount.source}:${mount.target}`}
                  glyph={Archive}
                  title={
                    <code className={cn("font-mono break-all", CODE.path)}>{mount.target}</code>
                  }
                  detail={
                    <>
                      {adoption && (
                        <span className="block font-mono break-all">{mount.source}</span>
                      )}
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
                    </>
                  }
                />
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
          <ul className="min-w-0 space-y-1">
            {generated.map((variable) => (
              <ReviewItem
                key={variable.name}
                product={variableProduct(variable.name)}
                glyph={Key}
                title={<code className={cn("font-mono", CODE.key)}>{variable.name}</code>}
                detail={`${secretShape(variable)}, made when this plan is saved`}
              />
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
          <ul className="min-w-0 space-y-1">
            {settled.map((variable) => (
              <ReviewItem
                key={variable.name}
                product={variableProduct(variable.name)}
                glyph={Key}
                title={<code className={cn("font-mono", CODE.key)}>{variable.name}</code>}
                detail={
                  <>
                    <code className={cn("font-mono break-all", CODE.string)}>{variable.value}</code>
                    {variable.domainTemplate ? " · follows the domain" : ""}
                  </>
                }
              />
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
          <ul aria-label="Readiness checks" className="min-w-0 space-y-1">
            {[...readiness, ...smoke].map((check, index) => (
              <ReviewItem
                key={`${check.phase}:${check.name}:${index}`}
                glyph={Heart}
                title={check.name}
                detail={
                  <span>
                    <ShellWords command={checkTarget(check)} />
                    {[checkBudget(check), check.phase === "smoke" && "after it is live"]
                      .filter(Boolean)
                      .map((part) => ` · ${part}`)
                      .join("")}
                  </span>
                }
              />
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
        <ul className="min-w-0">
          <ReviewItem
            glyph={Route}
            title={
              configuration.runtime.strategy === "blue_green" ? "Candidate first" : "Stop first"
            }
            detail={
              /* "Nothing is lost" only when something is kept: with no mounts
               the section above has just said the opposite. */
              adoption && configuration.runtime.strategy === "stop_first"
                ? "The first deployment of changes stops the original runtime before the replacement starts, causing an outage. Review the recovered storage and backup coverage first; state outside the declared mounts is not preserved by a rebuild."
                : configuration.runtime.strategy === "blue_green"
                  ? "The new container starts beside the running one and only takes the address once it has answered, so a release that never becomes ready changes nothing."
                  : mounts.length === 0
                    ? "The running container is stopped before the new one starts, so on every release after the first this project is unreachable for a few seconds."
                    : "The running container is stopped before the new one starts. Nothing is lost — the data above is kept — but on every release after the first this project is unreachable for a few seconds."
            }
          />
        </ul>
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

      <PassedChecks findings={findings} checking={checking} />
    </>
  )
}

/**
 * What this server said about the plan, at the head of the screen that asks
 * whether it is right: the run page's outcome, before the run.
 *
 * Its findings used to be the last section of the screen, under a page of
 * facts and over the button whose word they changed — "Deploy" became "Re-check
 * and deploy" because of something drawn below the fold. The answer is the
 * first thing now: a tile in how the check went, one line saying what stands
 * between the plan and Deploy, how many checks passed, and under it the
 * findings to fix and the warnings to acknowledge.
 */
function Verdict({
  findings,
  checking,
  blockers,
  warnings,
  acknowledged,
  acknowledgementsDisabled,
  onAcknowledgedChange,
  onOpenRemedy,
  canOpenRemedy,
  onApplyFix,
  onInspectAgain,
}: {
  findings: DeploymentPreflightFinding[]
  checking: boolean
  blockers: DeploymentPreflightFinding[]
  warnings: DeploymentPreflightFinding[]
  acknowledged: string[]
  acknowledgementsDisabled: boolean
  onAcknowledgedChange: (codes: string[]) => void
  onOpenRemedy: (finding: DeploymentPreflightFinding) => void
  canOpenRemedy: (finding: DeploymentPreflightFinding) => boolean
  onApplyFix?: (finding: DeploymentPreflightFinding) => void
  onInspectAgain?: () => void
}) {
  if (!checking && findings.length === 0) return null
  const passed = findings.filter((finding) => finding.severity === "pass").length
  // One acknowledgement covers a group, so the headline counts what is ticked.
  const warningGroups = groupImportWarnings(warnings)
  const outstanding = warningGroups.filter(
    (group) => !group.codes.every((code) => acknowledged.includes(code)),
  )
  const state: ReleaseNodeState | undefined = checking
    ? undefined
    : blockers.length > 0
      ? "failed"
      : outstanding.length > 0
        ? "warning"
        : "passed"
  const headline =
    blockers.length > 0
      ? `${blockers.length} ${blockers.length === 1 ? "thing stops" : "things stop"} this deploy`
      : outstanding.length > 0
        ? `${outstanding.length} ${outstanding.length === 1 ? "warning" : "warnings"} to acknowledge first`
        : warnings.length > 0
          ? "Ready to deploy, its warnings acknowledged"
          : "Nothing stops this deploy"
  return (
    <section aria-label="What this server said about the plan" className="min-w-0 space-y-3">
      <div className="flex min-w-0 items-center gap-3.5">
        <span
          aria-hidden
          className={cn(
            "relative flex size-10 shrink-0 items-center justify-center rounded-lg border",
            state === "failed" && "border-rule-danger bg-wash-danger text-destructive",
            state === "warning" && "border-rule-warning bg-wash-warning text-warning",
            state === "passed" && "border-rule-success bg-wash-success text-success",
            !state && "border-hairline bg-background text-muted-foreground",
          )}
        >
          <ShieldCheck className="size-5" />
          {state && (
            <span className="absolute -right-1 -bottom-1 flex size-4 items-center justify-center rounded-sm border border-hairline bg-background">
              <StepMark state={state} className="size-3 [&_svg]:size-3" />
            </span>
          )}
        </span>
        <div className="min-w-0 space-y-0.5">
          {checking ? (
            <TextShimmer className="text-title leading-tight font-semibold tracking-tight">
              Checking the plan against this server…
            </TextShimmer>
          ) : (
            <p className="text-title leading-tight font-semibold tracking-tight">{headline}</p>
          )}
          <p className="numeric text-xs text-muted-foreground">
            {checking
              ? "The port, the build, the address and what the release keeps"
              : `${passed} ${passed === 1 ? "check" : "checks"} passed against this server`}
          </p>
        </div>
      </div>

      {blockers.length > 0 && (
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
      )}
      {warnings.length > 0 && (
        <Group id="deployment-warning-acknowledgements" tone="warning" className="space-y-2">
          {warningGroups.map(({ key, finding, codes, details, services }) => (
            <div key={key} className="min-w-0 space-y-2">
              <Label
                /* An `OptionRow`'s anatomy, at an `OptionRow`'s sizes: title
                 at body, everything under it at hint. It was one 12px
                 block throughout, which is a step the ladder does not have
                 between the two (§8). */
                className="flex min-h-11 items-start gap-3 text-hint leading-relaxed"
              >
                <Checkbox
                  className="mt-0.5"
                  checked={codes.every((code) => acknowledged.includes(code))}
                  disabled={acknowledgementsDisabled}
                  onCheckedChange={(checked) =>
                    onAcknowledgedChange(
                      checked
                        ? [...new Set([...acknowledged, ...codes])]
                        : acknowledged.filter((code) => !codes.includes(code)),
                    )
                  }
                />
                <span className="min-w-0">
                  <span className="block text-body font-medium">{finding.title}</span>
                  <span className="mt-0.5 block text-muted-foreground">
                    {details.length > 1
                      ? `Applies to ${services.join(", ") || `${details.length} recovered settings`}. ${finding.means ?? ""}`
                      : finding.measured || finding.means}
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
              {details.length > 1 && (
                <Disclosure
                  summary="Affected services and evidence"
                  facts={`${details.length} findings`}
                >
                  <ul className="space-y-2 text-hint text-muted-foreground">
                    {details.map((entry) => (
                      <li key={entry.code}>{entry.measured || entry.means}</li>
                    ))}
                  </ul>
                </Disclosure>
              )}
            </div>
          ))}
        </Group>
      )}
    </section>
  )
}

/**
 * One thing the release will do to this server, on the tile of what it is —
 * the product a variable's name says holds it, a volume, a check — with what
 * it is and what it means.
 */
function ReviewItem({
  product,
  glyph,
  title,
  detail,
}: {
  product?: string
  glyph: Icon
  title: React.ReactNode
  detail?: React.ReactNode
}) {
  return (
    <li className="flex min-w-0 items-start gap-3 py-1">
      <ProductLogo id={product} size="sm" fallback={glyph} />
      <div className="min-w-0 flex-1 pt-px">
        <p className="min-w-0 text-body font-medium break-words">{title}</p>
        {detail && (
          <p className="min-w-0 text-hint leading-relaxed break-words text-muted-foreground">
            {detail}
          </p>
        )}
      </div>
    </li>
  )
}

/**
 * What preflight asked this server and got a straight answer to.
 *
 * Every pass used to be discarded — `blockingFindings` and `warningFindings`
 * are the only two filters the screen had — so a plan with nothing wrong with
 * it said nothing at all, and the reader could not tell a checked plan from an
 * unchecked one. They are a line each, no measurement and no remedy: a pass
 * has no remedy, and its measurement is on the rail or in the sections above —
 * except where the measurement is the answer, which is drawn as the command it
 * is.
 */
function PassedChecks({
  findings,
  checking,
}: {
  findings: DeploymentPreflightFinding[]
  checking: boolean
}) {
  // Said by the Address row and by the sections above respectively; a pass
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
  if (checking || passed.length === 0) return null
  return (
    <FormSection title="Checked against this server">
      <ul className="grid min-w-0 gap-x-6 gap-y-1.5 sm:grid-cols-2">
        {passed.map((finding, index) => {
          const reading = readings.has(finding.code) && finding.measured
          return (
            <li
              key={`${finding.code}:${index}`}
              className={cn("flex min-w-0 items-start gap-2", reading && "sm:col-span-2")}
            >
              <Check aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
              <span className="min-w-0">
                <span className="block text-body">{finding.title}</span>
                {reading && (
                  <ShellWords
                    command={finding.measured!}
                    className="block text-hint break-words whitespace-pre-wrap"
                  />
                )}
              </span>
            </li>
          )
        })}
      </ul>
    </FormSection>
  )
}
