"use client"

import { Field, FormNote, FormSection, OptionRow } from "@/components/form"
import { Textarea } from "@/components/ui/textarea"
import { useSessionState } from "@/lib/view-state"
import type { DraftGitPolicy } from "@/components/deploy/new-project/draft"

function linesOf(text: string) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

/**
 * Whether new commits deploy themselves, decided while the project is being
 * created.
 *
 * A Git deployment polls its branch from the moment it exists and the server's
 * default is to deploy every push. That default is right for most things and
 * wrong for the one that matters: until this control existed, "manual only"
 * was a setting on the Automation page, reachable only *after* a production
 * service had already released a commit nobody meant to ship.
 *
 * The watch paths are here for the same reason. A monorepo whose deployment
 * rebuilds on every commit to every unrelated package is not a problem anyone
 * discovers on day one; it is a problem they discover on day thirty, having
 * already paid for it twenty times.
 */
export function AutomaticDeployment({
  branch,
  policy,
  onChange,
}: {
  branch?: string
  policy: DraftGitPolicy
  onChange: (policy: DraftGitPolicy) => void
}) {
  // The text is what is edited and the lines are what is committed, kept
  // apart the way the Automation page keeps them: a field that re-renders
  // itself from `filter(Boolean)` refuses the blank line you have to type to
  // reach the second pattern.
  const [patterns, setPatterns] = useSessionState(
    "deploy.new.configure.gitPolicy.include",
    policy.watchInclude.join("\n"),
  )
  return (
    <FormSection title="Automatic deployment">
      <OptionRow
        title="Deploy new commits automatically"
        hint={
          branch
            ? `Every push to ${branch} starts a release once the first deployment succeeds.`
            : "Every push to the selected branch starts a release once the first deployment succeeds."
        }
        checked={policy.automatic}
        onCheckedChange={(automatic) => onChange({ ...policy, automatic })}
      >
        <Field
          label="Only when these paths change"
          htmlFor="watch-include"
          hint="One path pattern per line, relative to the repository. Leave empty to watch everything."
        >
          <Textarea
            id="watch-include"
            rows={2}
            value={patterns}
            placeholder={"apps/web/**\npackages/ui/**"}
            className="font-mono text-xs"
            onChange={(event) => {
              setPatterns(event.target.value)
              onChange({ ...policy, watchInclude: linesOf(event.target.value) })
            }}
          />
        </Field>
      </OptionRow>
      <OptionRow
        title="Report each release on the commit"
        hint="Posts a pending, success or failure status to GitHub through the dashboard's own credential."
        checked={policy.commitStatuses}
        onCheckedChange={(commitStatuses) => onChange({ ...policy, commitStatuses })}
      />
      <FormNote>
        Both can be changed later under Settings → Automation. Manual deployments always work.
      </FormNote>
    </FormSection>
  )
}
