"use client"

import { useState } from "react"
import { ArrowDown, ArrowUp, Plus, Trash } from "@/components/icons"
import { Field } from "@/components/form"
import { Group, Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useSessionState } from "@/lib/view-state"
import type { DeploymentComposeDocument, DeploymentDraftSource } from "@/lib/types"
import { deploymentName } from "@/components/deploy/vocabulary"
import { validateSource, type WizardErrors } from "@/components/deploy/deployment-defaults"
import {
  inspectAndPrepare,
  repositoryName,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"

type FilesMode = "compose_paste" | "compose_upload" | "compose_git" | "compose_local"

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
  const [documents, setDocuments] = useSessionState<DeploymentComposeDocument[]>(
    "deploy.new.compose.documents",
    [{ path: "compose.yml", content: "", order: 0 }],
  )
  const [selectors, setSelectors] = useSessionState<string[]>("deploy.new.compose.selectors", [])
  const [gitUrl, setGitUrl] = useSessionState("deploy.new.compose.url", "")
  const [gitRef, setGitRef] = useSessionState("deploy.new.compose.ref", "main")
  const [credentialId, setCredentialId] = useSessionState("deploy.new.compose.credential", 0)
  const [localPath, setLocalPath] = useSessionState("deploy.new.compose.path", "")
  const [subdirectory, setSubdirectory] = useSessionState("deploy.new.compose.subdirectory", "")
  const [errors, setErrors] = useState<WizardErrors>({})
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<Error>()

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
      onInspected(
        await inspectAndPrepare(deploymentName(name), "compose", source, {
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
    <div className="w-full max-w-3xl min-w-0 space-y-4">
      {failure && <ErrorState error={failure} />}
      <Panel plain>
        <PanelHeader title="Compose stack" />
        <PanelBody className="space-y-4">
          <Field label="Where are the files" htmlFor="compose-mode">
            <Select value={mode} onValueChange={(value) => setMode(value as FilesMode)}>
              <SelectTrigger id="compose-mode" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="compose_paste">Paste</SelectItem>
                <SelectItem value="compose_upload">Upload</SelectItem>
                <SelectItem value="compose_git">In a Git repository</SelectItem>
                <SelectItem value="compose_local">On this server</SelectItem>
              </SelectContent>
            </Select>
          </Field>

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
        </PanelBody>
        <PanelFooter className="justify-end">
          <Button className="h-11 sm:h-9" pending={busy} onClick={() => void inspect()}>
            Inspect
          </Button>
        </PanelFooter>
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
