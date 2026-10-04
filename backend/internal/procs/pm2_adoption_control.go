package procs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The configuration comparison and mutation happen in one connected client.
// Configuration is stdin, never command-line text, and the method vocabulary is
// closed. Calling the transport directly cannot boot a replacement PM2 daemon.
const existingPM2ControlClient = `
const fs = require("fs")
const path = require("path")
const request = JSON.parse(fs.readFileSync(0, "utf8"))
if (!["stop", "start"].includes(request.action)) process.exit(2)
let binary = process.argv[1]
if (!path.isAbsolute(binary)) binary = process.env.PATH.split(path.delimiter).map(dir => path.join(dir, binary)).find(file => fs.existsSync(file))
binary = fs.realpathSync(binary)
const local = require("module").createRequire(binary)
const transport = name => {
  const bundled = path.join(path.dirname(binary), "..", "modules", name)
  return fs.existsSync(bundled) ? local(bundled) : local(name)
}
const socket = transport("pm2-axon").socket("req")
const client = new (transport("pm2-axon-rpc").Client)(socket)
const close = code => { socket.close(); process.exit(code) }
const timer = setTimeout(() => close(2), 65000)
socket.on("error", () => close(2))
socket.connect(path.join(process.env.PM2_HOME, "rpc.sock"))
const volatile = new Set(["status","pm_uptime","restart_time","unstable_restarts","exit_code","prev_restart_delay","restart_task","vizion_running","axm_actions","axm_monitor","axm_options","axm_dynamic","versioning","node_version","km_link"])
const stable = value => {
  const result = {}
  for (const key of Object.keys(value).sort()) if (!volatile.has(key)) result[key] = value[key]
  return result
}
const canonical = value => {
  if (Array.isArray(value)) return value.map(canonical)
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map(key => [key,canonical(value[key])]))
  return value
}
client.call("getMonitorData", {}, (error, rows) => {
  if (error || !Array.isArray(rows)) return close(2)
  const selected = rows.filter(row => row.name === request.name && (row.namespace || row.pm2_env.namespace || "") === request.namespace)
  if (selected.length !== request.configuration.length) return close(3)
  for (const expected of request.configuration) {
    const row = selected.find(row => row.pm_id === expected.pm_id)
    if (!row || JSON.stringify(canonical(stable({...row.pm2_env, pm_id:row.pm_id}))) !== JSON.stringify(canonical(expected))) return close(3)
  }
  let index = 0
  const next = () => {
    if (index >= request.configuration.length) { clearTimeout(timer); socket.close(); process.stdout.write("{}"); return }
    const expected = request.configuration[index++]
    const row = selected.find(row => row.pm_id === expected.pm_id)
    if (request.action === "start" && ["online","launching"].includes(row.pm2_env.status)) return next()
    if (request.action === "stop" && row.pm2_env.status === "stopped") return next()
    client.call(request.action === "stop" ? "stopProcessId" : "startProcessId", expected.pm_id, error => error ? close(2) : next())
  }
  next()
})
`

func (p *PM2) ControlCaptured(ctx context.Context, capture *HostWorkloadCapture, namespace, action string) error {
	if capture == nil || capture.Manager != "pm2" || (action != "stop" && action != "start") {
		return fmt.Errorf("unsupported original PM2 action")
	}
	home, err := p.homeFor(capture.Account)
	if err != nil {
		return fmt.Errorf("the original PM2 account is unavailable")
	}
	account, err := pm2Account(home.home)
	if err != nil {
		return fmt.Errorf("the original PM2 account could not be verified")
	}
	var configuration []map[string]json.RawMessage
	if json.Unmarshal(capture.OriginalConfig, &configuration) != nil || len(configuration) == 0 || len(configuration) > 128 {
		return fmt.Errorf("the original PM2 configuration is unavailable")
	}
	request, _ := json.Marshal(struct {
		Action        string                       `json:"action"`
		Name          string                       `json:"name"`
		Namespace     string                       `json:"namespace"`
		Configuration []map[string]json.RawMessage `json:"configuration"`
	}{action, capture.Name, namespace, configuration})
	commandCtx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	command, err := hostexec.CommandOnHostAsUser(commandCtx, account, pm2Env(home), "node", "-e", existingPM2ControlClient, home.bin)
	if err != nil {
		return fmt.Errorf("the original PM2 account could not be verified")
	}
	command.Stdin = bytes.NewReader(request)
	result, err := runPrepared(commandCtx, command, 70*time.Second, "node", "original PM2 lifecycle")
	if err != nil {
		if result != nil && result.ExitCode == 3 {
			return ErrHostWorkloadChanged
		}
		return fmt.Errorf("the original PM2 lifecycle action could not be completed")
	}
	p.invalidate()
	return nil
}
