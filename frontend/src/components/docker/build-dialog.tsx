"use client"

import { useEffect, useState } from "react"
import { Wrench } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get } from "@/lib/api"
import type { GitRepo } from "@/lib/types"
import { RunConsole, useRunConsole } from "@/components/docker/run-console"
import { Hint } from "@/components/docker/explain"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"

import { Modal } from "@/components/modal"

/**
 * Building an image from a directory on this server.
 *
 * This is where the git panel and Docker stop being two products. A repository
 * the dashboard already clones and pulls is a build context; without this, the
 * workflow is "pull it here, then open a shell and type docker build" — two
 * tools for one thought, and the reason people keep a terminal open next to
 * the dashboard.
 *
 * The repository list is offered as suggestions rather than as the only
 * choice: plenty of build contexts are directories that are not checkouts, and
 * the server checks the path against its configured roots either way.
 */
export function BuildDialog({
  open,
  onOpenChange,
  onBuilt,
  initialDir,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onBuilt?: () => void
  initialDir?: string
}) {
  const [dir, setDir] = useState(initialDir ?? "")
  const [tag, setTag] = useState("")
  const [dockerfile, setDockerfile] = useState("")
  const [noCache, setNoCache] = useState(false)
  const [pull, setPull] = useState(true)
  const [repos, setRepos] = useState<GitRepo[]>([])
  const runner = useRunConsole()

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, controller.signal)
      .then((res) => setRepos(res.repos ?? []))
      .catch(() => undefined)
    return () => controller.abort()
  }, [open])

  const build = async () => {
    const code = await runner.run("/docker/images/build", {
      dir,
      tag,
      dockerfile: dockerfile.trim() || undefined,
      noCache: noCache ? "true" : undefined,
      pull: pull ? "true" : undefined,
    })
    if (code === 0) {
      notify.success(`${tag} built`)
      onBuilt?.()
    } else {
      notify.error("The build failed", "The output above says where.")
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        // A build takes minutes and its output is only here; closing the
        // dialog mid-run would abandon it with nowhere to read the result.
        if (runner.running || next) return
        runner.reset()
        onOpenChange(false)
      }}
      size="lg"
      title="Build an image"
      description="Runs a build from a directory on this server, with the same builder the command line
            uses. Nothing is started — you get an image you can then run."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={runner.running}>
            Close
          </Button>
          <Button
            onClick={build}
            disabled={runner.running || !dir.trim() || !tag.trim()}
            pending={runner.running}
          >
            <Wrench className="size-4" />
            Build
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="build-dir" className="text-xs">
            Directory
          </Label>
          <Input
            id="build-dir"
            value={dir}
            spellCheck={false}
            list="build-repos"
            className="font-mono text-xs"
            placeholder="/srv/my-app"
            onChange={(e) => setDir(e.target.value)}
          />
          <datalist id="build-repos">
            {repos.map((r) => (
              <option key={r.path} value={r.path}>
                {r.name} · {r.branch}
              </option>
            ))}
          </datalist>
          <Hint>
            The repositories the git panel knows about are suggested here. The build sees this whole
            directory, so a large one with node_modules in it is a slow build — a .dockerignore is
            what fixes that.
          </Hint>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="build-tag" className="text-xs">
              Name the result
            </Label>
            <Input
              id="build-tag"
              value={tag}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder="my-app:latest"
              onChange={(e) => setTag(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="build-file" className="text-xs">
              Dockerfile
            </Label>
            <Input
              id="build-file"
              value={dockerfile}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder="Dockerfile"
              onChange={(e) => setDockerfile(e.target.value)}
            />
          </div>
        </div>

        <div className="flex flex-wrap gap-4">
          <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
            <Switch checked={pull} onCheckedChange={setPull} aria-label="Pull base images" />
            Fetch newer base images first
          </label>
          <label className="flex cursor-pointer items-center gap-2 text-xs text-muted-foreground">
            <Switch checked={noCache} onCheckedChange={setNoCache} aria-label="Ignore the cache" />
            Ignore the build cache
          </label>
        </div>
      </div>

      <RunConsole
        lines={runner.lines}
        state={runner.state}
        exitCode={runner.exitCode}
        title={`build · ${tag || "image"}`}
        onDismiss={runner.reset}
      />
    </Modal>
  )
}
