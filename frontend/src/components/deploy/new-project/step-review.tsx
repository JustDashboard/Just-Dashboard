"use client"

import Link from "next/link"
import { FormFact, FormFacts, FormSection } from "@/components/form"
import { Group } from "@/components/panel"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import type { DeploymentPreflightFinding } from "@/lib/types"
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
 * What it reads back is what the plan drawing beside it does not — the name,
 * the kind, how many variables travel with it, and what happens on the next
 * push. The four nodes of the drawing are pressable and each one goes to the
 * step that decides it, so "that is wrong" costs one press from here.
 */
export function StepReview({
  flow,
  branch,
  gitPolicy,
  onGitPolicyChange,
  variableCount,
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
  blockers: DeploymentPreflightFinding[]
  warnings: DeploymentPreflightFinding[]
  acknowledged: string[]
  onAcknowledgedChange: (codes: string[]) => void
  onOpenRemedy: (finding: DeploymentPreflightFinding) => void
  canOpenRemedy: (finding: DeploymentPreflightFinding) => boolean
}) {
  const isGitSource = flow.source.kind === "git" || flow.source.kind === "local"
  const configuration = flow.configuration

  return (
    <>
      <FormSection title="Project">
        <FormFacts>
          <FormFact label="Name" mono>
            {flow.name || "Unnamed"}
          </FormFact>
          <FormFact label="Type">{WORKLOAD_LABELS[flow.profile] ?? flow.profile}</FormFact>
          <FormFact label="Variables">
            {variableCount === 0
              ? "None"
              : `${variableCount} ${variableCount === 1 ? "value" : "values"}`}
          </FormFact>
          <FormFact label="Release">
            {configuration.runtime.strategy === "blue_green" ? "Candidate first" : "Stop first"}
          </FormFact>
        </FormFacts>
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
    </>
  )
}
