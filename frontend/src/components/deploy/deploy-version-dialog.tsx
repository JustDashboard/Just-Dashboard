"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type {
  DeploymentCheckResult,
  DeploymentCommit,
  DeploymentEngineRun,
  DeploymentRelease,
} from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FormFact, FormFacts } from "@/components/form"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { SourceBranch, SourceCommit } from "@/components/git/glyphs"
import { BranchChip, ShortSha } from "@/components/git/marks"
import { InitialsMark } from "@/components/account/user-avatar"
import { useProject } from "@/components/deploy/project-context"
import { runCommit } from "@/components/deploy/vocabulary"
import { DeployCheckList } from "@/components/deploy/deploy-check"
import {
  attentionFindings,
  confirmationSignature,
  needsConfirmation,
  stopsDeployment,
} from "@/components/deploy/deploy-check-state"

/** A full Git object id; anything else is a name the remote is asked about. */
const OBJECT_ID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/i

/** What looks like a shortened commit id, which the remote cannot be asked about by name. */
const PARTIAL_ID = /^[0-9a-f]{7,39}$/i

/** How many commits the dialog offers from the project's own history. */
const PICKS = 5

type Version = { commit: DeploymentCommit; run: DeploymentEngineRun; release?: DeploymentRelease }

/**
 * Deploy a version other than the branch's tip: a tag for a release, an
 * older commit to bisect a regression, another branch to try it in
 * production for an hour.
 *
 * The configured branch is untouched. Automatic deployments keep following
 * it, and the next push builds as before; this is one run of one version,
 * with the saved build settings and variables.
 *
 * It opens on what it is choosing between: the branch the project follows
 * and what is live, then the commits this environment has already deployed —
 * read from its own runs that succeeded, which record the commit each one
 * built, rather than from the remote — as rows you take, the branch first.
 * Taking one fills the field; the field says how it will read what is in it,
 * because a full commit id is deployed exactly and anything else is a name
 * the remote is asked about, and that difference used to be a paragraph under
 * the field. The command names the version it will build, the way the field
 * reads it.
 */
export function DeployVersionDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const router = useRouter()
  const project = useProject()
  const { deployment } = project.detail
  const branch = deployment.sourceRef
  const [value, setValue] = useState("")
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  // What the advisory check found for the version in the field, kept against
  // the request it was about: another version is another commit to check.
  const [checked, setChecked] = useState<{ request: string; result: DeploymentCheckResult }>()
  const [confirmed, setConfirmed] = useSessionState(
    `deploy.${project.projectId}.check.confirmed`,
    "",
  )
  const trimmed = value.trim()
  const exact = OBJECT_ID.test(trimmed)
  const partial = !exact && PARTIAL_ID.test(trimmed)
  const live = runCommit(project.liveRun)?.sha ?? project.liveRelease?.sourceRevision

  // Newest first and once per commit: a commit redeployed three times is one
  // version, and the run that built it last is the one worth naming. Only
  // this environment's runs that went live: a preview's pull request, or a
  // build that failed, was never deployed here.
  const picks = useMemo(() => {
    const seen = new Set<string>()
    const found: Version[] = []
    for (const run of project.runs) {
      if (run.environmentId !== project.environmentId || run.state !== "succeeded") continue
      const commit = runCommit(run)
      if (!commit || seen.has(commit.sha)) continue
      seen.add(commit.sha)
      found.push({
        commit,
        run,
        release: project.releases.find((release) => release.id === run.releaseId),
      })
      if (found.length === PICKS) break
    }
    return found
  }, [project.runs, project.releases, project.environmentId])

  const close = () => {
    onOpenChange(false)
    setValue("")
    setError(undefined)
    setChecked(undefined)
  }

  const version = exact ? { sourceRevision: trimmed.toLowerCase() } : { ref: trimmed }
  const request = JSON.stringify(version)
  const check = checked?.request === request ? checked.result : undefined
  const findings = check ? attentionFindings(check.findings) : []
  const stops = findings.some(stopsDeployment)

  const deploy = async () => {
    if (!trimmed) {
      setError("Enter a branch, a tag or a commit id.")
      return
    }
    setBusy(true)
    setError(undefined)
    try {
      // The version is checked before it is built, the way the header's
      // Deploy is: a first press that finds something asks, a second press
      // on the same version confirms it.
      if (!check) {
        const result = await post<DeploymentCheckResult>(
          `/deploy/${project.projectId}/environments/${project.environmentId}/check`,
          version,
        ).catch((caught: unknown) => {
          // A name the remote does not have is the answer; any other failure
          // leaves the deployment to check itself before building.
          if (caught instanceof ApiError && caught.code === "ref_not_found") throw caught
          return undefined
        })
        if (Array.isArray(result?.findings) && needsConfirmation(result, confirmed)) {
          setChecked({ request, result })
          return
        }
      } else {
        setConfirmed(confirmationSignature(check.findings))
      }
      const run = await post<DeploymentEngineRun>(
        `/deploy/${project.projectId}/environments/${project.environmentId}/runs`,
        { operation: "deploy", ...version },
      )
      close()
      router.push(`/deploy/${project.projectId}/runs/${run.id}`)
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === "ref_not_found") {
        setError("The remote has no branch or tag with that name.")
      } else if (caught instanceof ApiError && caught.code === "ref_not_applicable") {
        setError("Only a Git project can deploy a specific version.")
      } else {
        notify.error("Could not start deployment", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  const pick = (next: string) => {
    setValue(next)
    setError(undefined)
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && (next ? onOpenChange(true) : close())}
      title="Deploy a specific version"
      description="Build a branch, a tag or a commit instead of the configured branch."
      size="md"
      footer={
        <>
          <Button variant="outline" onClick={close} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => void deploy()}
            pending={busy}
            disabled={stops}
            className="max-w-64"
          >
            <span className="truncate">
              {trimmed ? `Deploy ${exact ? trimmed.slice(0, 7) : trimmed}` : "Deploy"}
              {check && findings.length > 0 && !stops && " anyway"}
            </span>
          </Button>
        </>
      }
    >
      <form
        className="space-y-5"
        onSubmit={(event) => {
          event.preventDefault()
          void deploy()
        }}
      >
        <FormFacts>
          {branch && (
            <FormFact label="Follows">
              <BranchChip branch={branch} className="max-w-48" />
            </FormFact>
          )}
          {project.liveRelease && (
            <FormFact label="Live">
              <span className="inline-flex items-center gap-1.5">
                <ShortSha sha={live} />
                <span className="numeric">#{project.liveRelease.number}</span>
              </span>
            </FormFact>
          )}
        </FormFacts>

        <Field
          label="Branch, tag or commit"
          htmlFor="deploy-version"
          hint={
            partial
              ? "A commit is deployed by its full 40-character id; this is asked about as a name."
              : branch
                ? `Builds that version once. Automatic deployments keep following ${branch}.`
                : "Builds that version once; the configured branch is not changed."
          }
          error={error}
        >
          <InputGroup>
            {/* How the value will be read, said as it is typed: a full id is
                that exact commit, anything else is looked up on the remote
                with no guess between a tag and a branch. */}
            <InputGroupAddon align="inline-start">
              <InputGroupText className={cn(exact && "text-foreground")}>
                {exact ? (
                  <SourceCommit aria-hidden className="size-3.5" />
                ) : (
                  <SourceBranch aria-hidden className="size-3.5" />
                )}
                {exact ? "commit" : "name"}
              </InputGroupText>
            </InputGroupAddon>
            <InputGroupInput
              id="deploy-version"
              value={value}
              onChange={(event) => pick(event.target.value)}
              placeholder="v1.4.2"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={Boolean(error)}
            />
          </InputGroup>
        </Field>

        {check && findings.length > 0 && (
          <section aria-labelledby="deploy-version-check" className="space-y-2">
            <p id="deploy-version-check" className="eyebrow">
              Ready to deploy?
            </p>
            <p className="text-hint text-muted-foreground">
              {stops
                ? "This version would stop before it builds until these are fixed."
                : "This version can be deployed; confirm these first."}
            </p>
            <DeployCheckList projectId={project.projectId} findings={findings} />
          </section>
        )}

        {(branch || picks.length > 0) && (
          <section aria-labelledby="deploy-version-picks" className="space-y-2">
            <p id="deploy-version-picks" className="eyebrow">
              Deployed before
            </p>
            <ChoiceList>
              {branch && (
                <ChoiceRow
                  verb={trimmed === branch ? `Use ${branch}, selected` : `Use ${branch}`}
                  onSelect={() => pick(branch)}
                  className={cn(trimmed === branch && "bg-accent")}
                  leading={<ProductLogo size="sm" fallback={SourceBranch} />}
                  title={<span className="font-mono">{branch}</span>}
                  description="The branch it follows · builds its newest commit"
                />
              )}
              {picks.map(({ commit, run, release }) => {
                // The row knows its release; a run whose release is not in
                // the page read here is matched by the run that recorded it,
                // then by its commit.
                const isLive = release
                  ? release.id === deployment.liveReleaseId
                  : run.id === project.liveRun?.id ||
                    Boolean(live && commit.sha.startsWith(live.slice(0, 12)))
                const verb = `Use ${commit.sha.slice(0, 7)}`
                return (
                  <ChoiceRow
                    key={commit.sha}
                    verb={trimmed === commit.sha ? `${verb}, selected` : verb}
                    onSelect={() => pick(commit.sha)}
                    className={cn(trimmed === commit.sha && "bg-accent")}
                    leading={
                      commit.author ? (
                        <InitialsMark name={commit.author} size="sm" className="size-8" />
                      ) : (
                        <ProductLogo size="sm" fallback={SourceCommit} />
                      )
                    }
                    title={commit.subject ?? commit.sha.slice(0, 7)}
                    description={
                      <span className="flex min-w-0 items-center gap-1.5">
                        <ShortSha sha={commit.sha} />
                        {commit.author && <span className="truncate">{commit.author}</span>}
                        <span className="shrink-0">
                          · {relativeTime(commit.authoredAt ?? run.requestedAt)}
                        </span>
                        {release && (
                          <span className="numeric shrink-0">· Release #{release.number}</span>
                        )}
                      </span>
                    }
                    trailing={isLive ? <Status tone="running" label="Live" /> : undefined}
                  />
                )
              })}
            </ChoiceList>
          </section>
        )}
      </form>
    </Modal>
  )
}
