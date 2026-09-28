"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { PM2Daemon, PM2StartRequest } from "@/lib/types"
import { Field, FieldRow, FormNote, OptionList, OptionRow, Statement } from "@/components/form"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Mode = "fork" | "cluster" | "max"

const INTERPRETERS = [
  { value: "auto", label: "Decide from the file" },
  { value: "node", label: "node" },
  { value: "bash", label: "bash" },
  { value: "python3", label: "python3" },
  { value: "php", label: "php" },
  { value: "none", label: "None — run the file itself" },
]

const ECOSYSTEM = /\.(config\.(c|m)?js|json|ya?ml)$/i

/**
 * Registering a program with PM2 from here rather than from a shell.
 *
 * Every field is one argument of `pm2 start`, and the statement under the
 * form is the command that will run — so the dialog can be learned from, and
 * so nothing is hidden about what it does. Pointing it at an ecosystem file
 * hands the file to PM2 whole, because that file already says everything the
 * other fields would.
 */
export function PM2StartDialog({
  open,
  daemons,
  onOpenChange,
  onStarted,
}: {
  open: boolean
  daemons: PM2Daemon[]
  onOpenChange: (open: boolean) => void
  onStarted: () => void
}) {
  // Kept for the tab while the dialog is open; the page forgets it on close.
  const [account, setAccount] = useSessionState(
    "processes.pm2.start.account",
    daemons[0]?.account ?? "",
  )
  const [script, setScript] = useSessionState("processes.pm2.start.script", "")
  const [name, setName] = useSessionState("processes.pm2.start.name", "")
  const [cwd, setCwd] = useSessionState("processes.pm2.start.cwd", "")
  const [interpreter, setInterpreter] = useSessionState("processes.pm2.start.interpreter", "auto")
  const [mode, setMode] = useSessionState<Mode>("processes.pm2.start.mode", "fork")
  const [instances, setInstances] = useSessionState("processes.pm2.start.instances", "2")
  const [watch, setWatch] = useSessionState("processes.pm2.start.watch", false)
  const [memory, setMemory] = useSessionState("processes.pm2.start.memory", "")
  const [args, setArgs] = useSessionState("processes.pm2.start.args", "")
  const [busy, setBusy] = useState(false)

  const chosenAccount = account || daemons[0]?.account || ""
  const ecosystem = ECOSYSTEM.test(script.trim())
  const scriptError =
    script.trim() && !script.trim().startsWith("/") ? "An absolute path on the server." : undefined
  const nameError =
    name && !/^[A-Za-z0-9._@:][A-Za-z0-9._@:-]{0,127}$/.test(name)
      ? "Letters, digits, dots, dashes and underscores."
      : undefined
  const cwdError = cwd && !cwd.startsWith("/") ? "An absolute path on the server." : undefined
  const memoryError =
    memory && !/^[0-9]{1,6}[KMG]?$/.test(memory) ? "A size such as 300M or 1G." : undefined
  const count = Number(instances)
  const instancesError =
    mode === "cluster" && (!Number.isInteger(count) || count < 2 || count > 128)
      ? "A whole number from 2 to 128."
      : undefined

  const request = useMemo<PM2StartRequest>(() => {
    const req: PM2StartRequest = { account: chosenAccount, script: script.trim() }
    if (name) req.name = name
    if (ecosystem) return req
    if (cwd) req.cwd = cwd
    if (interpreter !== "auto") req.interpreter = interpreter
    if (mode === "max") req.instances = -1
    else if (mode === "cluster") req.instances = count
    if (watch) req.watch = true
    if (memory) req.maxMemoryRestart = memory
    const argv = args.trim() ? args.trim().split(/\s+/) : []
    if (argv.length) req.args = argv
    return req
  }, [chosenAccount, script, name, ecosystem, cwd, interpreter, mode, count, watch, memory, args])

  const command = useMemo(() => {
    if (!request.script) return ""
    const parts = ["pm2", "start", request.script]
    if (ecosystem) {
      if (request.name) parts.push("--only", request.name)
      return parts.join(" ")
    }
    if (request.name) parts.push("--name", request.name)
    if (request.cwd) parts.push("--cwd", request.cwd)
    if (request.interpreter) parts.push("--interpreter", request.interpreter)
    if (request.instances === -1) parts.push("-i", "max")
    else if (request.instances) parts.push("-i", String(request.instances))
    if (request.watch) parts.push("--watch")
    if (request.maxMemoryRestart) parts.push("--max-memory-restart", request.maxMemoryRestart)
    if (request.args?.length) parts.push("--", ...request.args)
    return parts.join(" ")
  }, [request, ecosystem])

  const valid =
    Boolean(chosenAccount && request.script) &&
    !scriptError &&
    !nameError &&
    !cwdError &&
    !memoryError &&
    !instancesError

  const submit = async () => {
    setBusy(true)
    try {
      await post("/pm2/start", request)
      notify.success(`${request.name || request.script} started under PM2`)
      onStarted()
      onOpenChange(false)
      setScript("")
      setName("")
      setArgs("")
    } catch (err) {
      notify.error("Could not start the application", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Start an application"
      description="Register a script or an ecosystem file with PM2 and run it"
      size="lg"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={!valid || busy} onClick={() => void submit()}>
            {busy ? "Starting…" : "Start"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <FieldRow>
          <Field
            label="Account"
            htmlFor="pm2-account"
            hint="Whose PM2 runs it. The script runs as this user."
          >
            <Select value={chosenAccount} onValueChange={setAccount}>
              <SelectTrigger id="pm2-account" size="sm" className="w-full">
                <SelectValue placeholder="Account" />
              </SelectTrigger>
              <SelectContent>
                {daemons.map((d) => (
                  <SelectItem key={d.account} value={d.account}>
                    {d.account}
                    <span className="text-muted-foreground">{d.home}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field
            label="Name"
            htmlFor="pm2-name"
            hint={
              ecosystem
                ? "Only this application from the file; blank starts them all."
                : "How PM2 lists it. Blank uses the file name."
            }
            error={nameError}
          >
            <Input
              id="pm2-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="api"
            />
          </Field>
        </FieldRow>
        <Field
          label="Script or ecosystem file"
          htmlFor="pm2-script"
          hint="A JavaScript, shell or Python file, or an ecosystem.config.js that describes several applications."
          error={scriptError}
        >
          <Input
            id="pm2-script"
            value={script}
            onChange={(e) => setScript(e.target.value)}
            placeholder="/srv/api/dist/server.js"
            className="font-mono"
            spellCheck={false}
          />
        </Field>

        {!ecosystem && (
          <>
            <FieldRow>
              <Field
                label="Working directory"
                htmlFor="pm2-cwd"
                hint="Blank runs it where the file is."
                error={cwdError}
              >
                <Input
                  id="pm2-cwd"
                  value={cwd}
                  onChange={(e) => setCwd(e.target.value)}
                  placeholder="/srv/api"
                  className="font-mono"
                  spellCheck={false}
                />
              </Field>
              <Field
                label="Interpreter"
                htmlFor="pm2-interpreter"
                hint="PM2 picks node for .js and the shebang otherwise."
              >
                <Select value={interpreter} onValueChange={setInterpreter}>
                  <SelectTrigger id="pm2-interpreter" size="sm" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {INTERPRETERS.map((i) => (
                      <SelectItem key={i.value} value={i.value}>
                        {i.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </FieldRow>
            <FieldRow columns={3}>
              <Field
                label="Mode"
                htmlFor="pm2-mode"
                hint="Cluster shares one port across workers; Node only."
              >
                <Select value={mode} onValueChange={(v) => setMode(v as Mode)}>
                  <SelectTrigger id="pm2-mode" size="sm" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="fork">One process</SelectItem>
                    <SelectItem value="cluster">Cluster of…</SelectItem>
                    <SelectItem value="max">One worker per CPU</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Instances"
                htmlFor="pm2-instances"
                hint="Workers in the cluster."
                error={instancesError}
              >
                <Input
                  id="pm2-instances"
                  type="number"
                  min={2}
                  max={128}
                  value={instances}
                  onChange={(e) => setInstances(e.target.value)}
                  disabled={mode !== "cluster"}
                  className="font-mono"
                />
              </Field>
              <Field
                label="Memory limit"
                htmlFor="pm2-memory"
                hint="Restart when it grows past this. Blank is no limit."
                error={memoryError}
              >
                <Input
                  id="pm2-memory"
                  value={memory}
                  onChange={(e) => setMemory(e.target.value.toUpperCase())}
                  placeholder="300M"
                  className="font-mono"
                />
              </Field>
            </FieldRow>
            <Field
              label="Arguments"
              htmlFor="pm2-args"
              hint="Passed to the script, split on spaces. Quoting is not interpreted."
            >
              <Input
                id="pm2-args"
                value={args}
                onChange={(e) => setArgs(e.target.value)}
                placeholder="--port 3000"
                className="font-mono"
                spellCheck={false}
              />
            </Field>
            <OptionList>
              <OptionRow
                title="Restart when files change"
                hint="PM2 watches the working directory. Meant for development; on a server it restarts on every deploy and every log write inside the tree."
                checked={watch}
                onCheckedChange={setWatch}
              />
            </OptionList>
          </>
        )}

        <Statement
          label="Command"
          sql={command}
          placeholder="Name a script and the command PM2 will run appears here."
        />
        <FormNote>
          The application is not in the startup list until it is saved. Save it from the menu once
          it runs the way you want.
        </FormNote>
      </div>
    </Modal>
  )
}
