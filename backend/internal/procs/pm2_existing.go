package procs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The normal CLI connects by starting a daemon if none answers. Discovery must
// only ask an existing daemon, so this constant client uses PM2's own transport
// without loading its API (whose initialization also writes account files).
const existingPM2Client = `
const fs = require("fs")
const path = require("path")
let binary = process.argv[1]
if (!path.isAbsolute(binary)) {
  binary = process.env.PATH.split(path.delimiter).map(dir => path.join(dir, binary)).find(file => fs.existsSync(file))
}
binary = fs.realpathSync(binary)
const local = require("module").createRequire(binary)
const transport = name => {
  const bundled = path.join(path.dirname(binary), "..", "modules", name)
  return fs.existsSync(bundled) ? local(bundled) : local(name)
}
const socket = transport("pm2-axon").socket("req")
const client = new (transport("pm2-axon-rpc").Client)(socket)
const timer = setTimeout(() => { socket.close(); process.exit(2) }, 5000)
socket.on("error", () => { clearTimeout(timer); socket.close(); process.exit(2) })
socket.connect(path.join(process.env.PM2_HOME, "rpc.sock"))
client.call("getMonitorData", {}, (error, processes) => {
  clearTimeout(timer)
  socket.close()
  if (error) process.exit(2)
  process.stdout.write(JSON.stringify(processes))
})
`

// ListExisting reads each already-running default PM2 daemon under its account.
// It never starts a daemon, writes a saved list or executes an ecosystem file.
func (p *PM2) ListExisting(ctx context.Context) ([]PM2Process, error) {
	out := []PM2Process{}
	var firstErr error
	for _, home := range discoverPM2Homes() {
		info, err := os.Stat(filepath.Join(home.home, ".pm2", "rpc.sock"))
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		account, err := pm2Account(home.home)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("an existing PM2 daemon account could not be verified")
			}
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
		command, err := hostexec.CommandOnHostAsUser(readCtx, account, pm2Env(home), "node", "-e", existingPM2Client, home.bin)
		if err == nil {
			var result *CommandResult
			result, err = runPrepared(readCtx, command, 7*time.Second, "node", "existing PM2 inventory")
			if err == nil {
				var rows []PM2Process
				rows, err = parsePM2List([]byte(result.Stdout), time.Now().UnixMilli(), account.Username)
				out = append(out, rows...)
			}
		}
		cancel()
		if err != nil && firstErr == nil {
			// PM2 output may reflect account secrets, so only a fixed summary
			// leaves this adapter on failure.
			firstErr = fmt.Errorf("an existing PM2 daemon could not be read without starting it")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DaemonID != out[j].DaemonID {
			return out[i].DaemonID < out[j].DaemonID
		}
		return out[i].ID < out[j].ID
	})
	return out, firstErr
}
