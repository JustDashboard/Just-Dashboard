"use client"

import { useState } from "react"
import {
  ArrowDown,
  ArrowUp,
  CloudUpload,
  FileText,
  GitBranch,
  Plus,
  Servers,
  Trash,
} from "@/components/icons"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import { Field } from "@/components/form"
import { FlowActions, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Group, Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import type { DeploymentComposeDocument, DeploymentDraftSource } from "@/lib/types"
import { deploymentName } from "@/components/deploy/vocabulary"
import { useSourceInspection } from "./use-source-inspection"
import { validateSource, type WizardErrors } from "@/components/deploy/deployment-defaults"
import {
  inspectAndPrepare,
  repositoryName,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"

type FilesMode = "compose_paste" | "compose_upload" | "compose_git" | "compose_local"

/**
 * Where a stack's files can live. Four kinds, so four cards (§16) rather than
 * a select: a closed dropdown said "Paste" and hid the three answers a reader
 * with the files in a repository was looking for.
 */
const MODES: {
  key: FilesMode
  label: string
  hint: string
  icon: React.ComponentType<{ className?: string }>
}[] = [
  { key: "compose_paste", label: "Paste", hint: "Type or paste the YAML here", icon: FileText },
  { key: "compose_upload", label: "Upload", hint: "Files from this computer", icon: CloudUpload },
  {
    key: "compose_git",
    label: "In a Git repository",
    hint: "Cloned from a URL and branch",
    icon: GitBranch,
  },
  {
    key: "compose_local",
    label: "On this server",
    hint: "A directory already on the host",
    icon: Servers,
  },
]

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * A Compose stack, from wherever its files live. Detection's read of it —
 * the services, what it could not carry over, the effective plan — shows up
 * on Configure's source row once inspected, the same as a framework tag.
 */
export function SourceCompose({ onInspected }: { onInspected: (flow: ConfigureFlow) => void }) {
  const [mode, setMode] = useSessionState<FilesMode>("deploy.new.compose.mode", "compose_paste")
  const [documents, setDocuments] = useMemoryState<DeploymentComposeDocument[]>(
    "deploy.new.compose.documents",
    [{ path: "compose.yml", content: "", order: 0 }],
  )
  const [selectors, setSelectors] = useSessionState<string[]>("deploy.new.compose.selectors", [])
  const [gitUrl, setGitUrl] = useMemoryState("deploy.new.compose.url", "")
  const [gitRef, setGitRef] = useSessionState("deploy.new.compose.ref", "main")
  const [credentialId, setCredentialId] = useSessionState("deploy.new.compose.credential", 0)
  const [localPath, setLocalPath] = useSessionState("deploy.new.compose.path", "")
  const [subdirectory, setSubdirectory] = useSessionState("deploy.new.compose.subdirectory", "")
  const [errors, setErrors] = useState<WizardErrors>({})
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()
  const inspection = useSourceInspection(onInspected)

  const buildSource = (): DeploymentDraftSource => {
    // Omitted rather than sent empty: the server finds docker-compose.yml or
    // compose.yaml on its own, and a seeded default made that path
    // unreachable from here.
    const selectorFiles =
      selectors.length > 0
        ? selectors.map((path, order) => ({ path, content: "", order }))
        : undefined
    if (mode === "compose_git")
      return {
        kind: "compose",
        mode,
        url: gitUrl.trim(),
        ref: gitRef.trim() || "main",
        credentialId: credentialId || undefined,
        composeFiles: selectorFiles,
      }
    if (mode === "compose_local")
      return {
        kind: "compose",
        mode,
        localPath: localPath.trim(),
        subdirectory: subdirectory.trim() || undefined,
        composeFiles: selectorFiles,
      }
    return { kind: "compose", mode, composeFiles: documents }
  }

  const name =
    mode === "compose_git"
      ? repositoryName(gitUrl)
      : mode === "compose_local"
        ? repositoryName(localPath)
        : "compose"

  const inspect = async () => {
    if (busy) return
    const source = buildSource()
    const validation = validateSource(source)
    if (Object.keys(validation).length) {
      setErrors(validation)
      return
    }
    setErrors({})
    setBusy(true)
    setFailure(undefined)
    try {
      await inspection.inspect(() =>
        inspectAndPrepare(deploymentName(name), "compose", source, {
          sourceLabel: "Compose stack",
        }),
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    // The same two columns as every other source: what is being decided, and
    // beside it what it is decided from.
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
      {/* The one surface with depth on this tab (§16): which files the stack is
          made of, and where they live, is the whole decision here. The tab drew
          it as a `Panel plain` whose foot looked like every other panel foot in
          the product, so nothing said which thing on the screen was being
          decided. */}
      <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
        <FlowPanelHeader title="Compose stack" />
        <FlowPanelBody className="space-y-4 xl:min-h-0 xl:flex-1 xl:overflow-y-auto">
          {failure && <ErrorState error={failure} />}

          {(mode === "compose_paste" || mode === "compose_upload") && (
            <ComposeFilesEditor
              documents={documents}
              onChange={setDocuments}
              upload={mode === "compose_upload"}
              error={errors.compose}
            />
          )}

          {mode === "compose_git" && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                label="Git URL"
                htmlFor="compose-git-url"
                className="sm:col-span-2"
                error={errors.url}
              >
                <Input
                  id="compose-git-url"
                  value={gitUrl}
                  onChange={(event) => setGitUrl(event.target.value)}
                  placeholder="https://github.com/owner/repository.git"
                  className="font-mono"
                />
              </Field>
              <Field label="Branch or tag" htmlFor="compose-git-ref">
                <Input
                  id="compose-git-ref"
                  value={gitRef}
                  onChange={(event) => setGitRef(event.target.value)}
                  className="font-mono"
                />
              </Field>
              <Field
                label="Credential id"
                htmlFor="compose-git-credential"
                hint="Leave 0 for a public source."
              >
                <Input
                  id="compose-git-credential"
                  type="number"
                  min={0}
                  value={credentialId || ""}
                  onChange={(event) => setCredentialId(Number(event.target.value) || 0)}
                  className="font-mono"
                />
              </Field>
            </div>
          )}

          {mode === "compose_local" && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field
                label="Path on this server"
                htmlFor="compose-local-path"
                className="sm:col-span-2"
                error={errors.localPath}
                hint="Must be inside a configured deployment root."
              >
                <Input
                  id="compose-local-path"
                  value={localPath}
                  onChange={(event) => setLocalPath(event.target.value)}
                  placeholder="/srv/app"
                  className="font-mono"
                />
              </Field>
              <Field
                label="Subdirectory"
                htmlFor="compose-local-subdirectory"
                hint="Optional relative application root."
              >
                <Input
                  id="compose-local-subdirectory"
                  value={subdirectory}
                  onChange={(event) => setSubdirectory(event.target.value)}
                  className="font-mono"
                />
              </Field>
            </div>
          )}

          {(mode === "compose_git" || mode === "compose_local") && (
            <ComposeSelectorEditor
              paths={selectors}
              onChange={setSelectors}
              error={errors.compose}
            />
          )}
        </FlowPanelBody>
        {/* Inspect is what advances the tab, so it is the command in the foot of
            the focused surface rather than one more button in a panel footer.
            The two "Add file" buttons stay `outline`: one brand face per screen,
            and neither of them leaves this step. */}
        <FlowActions>
          <Button className="h-11 sm:h-9" pending={busy} onClick={() => void inspect()}>
            Inspect
          </Button>
        </FlowActions>
      </FlowPanel>

      <Panel plain className="order-first min-w-0 xl:order-last xl:min-h-0 xl:overflow-y-auto">
        <PanelHeader title="Where the files are" />
        <PanelBody>
          <div role="group" aria-label="Where the files are" className="grid gap-2">
            {MODES.map((option) => (
              <ChoiceCard
                key={option.key}
                selected={mode === option.key}
                onClick={() => setMode(option.key)}
                className="min-h-0 flex-row items-center gap-3"
              >
                <option.icon
                  aria-hidden
                  className={
                    mode === option.key
                      ? "size-4 shrink-0 text-brand"
                      : "size-4 shrink-0 text-muted-foreground"
                  }
                />
                <span className="flex min-w-0 flex-col">
                  <ChoiceCardTitle>{option.label}</ChoiceCardTitle>
                  <ChoiceCardHint>{option.hint}</ChoiceCardHint>
                </span>
              </ChoiceCard>
            ))}
          </div>
        </PanelBody>
      </Panel>
    </div>
  )
}

/** The Compose documents pasted or uploaded directly: path, then content. */
function ComposeFilesEditor({
  documents,
  onChange,
  upload,
  error,
}: {
  documents: DeploymentComposeDocument[]
  onChange: (documents: DeploymentComposeDocument[]) => void
  upload: boolean
  error?: string
}) {
  const update = (index: number, field: "path" | "content", value: string) =>
    onChange(
      documents.map((document, i) => (i === index ? { ...document, [field]: value } : document)),
    )
  const remove = (index: number) =>
    onChange(
      documents.filter((_, i) => i !== index).map((document, order) => ({ ...document, order })),
    )
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <p className="eyebrow">Compose files</p>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...documents,
              { path: `compose.${documents.length + 1}.yml`, content: "", order: documents.length },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add file
        </Button>
      </div>
      {upload && (
        <Input
          type="file"
          accept=".yml,.yaml,text/yaml"
          multiple
          aria-label="Upload Compose files"
          onChange={async (event) => {
            const files = Array.from(event.target.files ?? [])
            onChange(
              await Promise.all(
                files.map(async (file, order) => ({
                  path: file.name,
                  content: await file.text(),
                  order,
                })),
              ),
            )
          }}
        />
      )}
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      {documents.map((document, index) => (
        <Group key={`${document.order}-${index}`}>
          <div className="mb-2 flex gap-2">
            <Input
              value={document.path}
              onChange={(event) => update(index, "path", event.target.value)}
              aria-label={`Compose file ${index + 1} path`}
              className="h-9 font-mono text-xs"
            />
            <IconAction
              label={`Remove ${document.path}`}
              disabled={documents.length === 1}
              onClick={() => remove(index)}
            >
              <Trash />
            </IconAction>
          </div>
          <Textarea
            value={document.content}
            onChange={(event) => update(index, "content", event.target.value)}
            aria-label={`${document.path} content`}
            rows={10}
            className="min-h-48 resize-y font-mono text-xs"
            placeholder={"services:\n  web:\n    image: nginx:alpine"}
          />
        </Group>
      ))}
    </div>
  )
}

/** Which files, and in which order, when the files live in Git or on disk. */
function ComposeSelectorEditor({
  paths,
  onChange,
  error,
}: {
  paths: string[]
  onChange: (paths: string[]) => void
  error?: string
}) {
  const move = (index: number, offset: number) => {
    const target = index + offset
    if (target < 0 || target >= paths.length) return
    const next = [...paths]
    ;[next[index], next[target]] = [next[target], next[index]]
    onChange(next)
  }
  return (
    <div className="space-y-2 rounded-xl border border-hairline p-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <p className="text-body font-medium">Compose file order</p>
          <p className="text-hint text-muted-foreground">
            {paths.length === 0
              ? "Leave empty and the server looks for docker-compose.yml or compose.yaml on its own. Add a file to name a different path."
              : "Paths are relative to the selected source. Later files override earlier ones."}
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...paths, `compose.${paths.length + 1}.yml`])}
        >
          <Plus className="size-3.5" /> Add file
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      {paths.map((path, index) => (
        <div key={index} className="flex min-w-0 items-center gap-1.5">
          <span className="numeric w-5 shrink-0 text-center text-hint text-muted-foreground">
            {index + 1}
          </span>
          <Input
            value={path}
            onChange={(event) =>
              onChange(paths.map((item, i) => (i === index ? event.target.value : item)))
            }
            aria-label={`Compose file ${index + 1} path`}
            className="min-w-0 font-mono text-xs"
          />
          <IconAction
            label={`Move ${path} earlier`}
            disabled={index === 0}
            onClick={() => move(index, -1)}
          >
            <ArrowUp />
          </IconAction>
          <IconAction
            label={`Move ${path} later`}
            disabled={index === paths.length - 1}
            onClick={() => move(index, 1)}
          >
            <ArrowDown />
          </IconAction>
          <IconAction
            label={`Remove ${path}`}
            onClick={() => onChange(paths.filter((_, i) => i !== index))}
          >
            <Trash />
          </IconAction>
        </div>
      ))}
    </div>
  )
}
