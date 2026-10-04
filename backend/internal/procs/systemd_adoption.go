package procs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/shirou/gopsutil/v4/process"
)

// CaptureExisting retains the unit and every drop-in privately. Effective
// environment comes from the running process, because EnvironmentFile parsing
// and specifier expansion are systemd's responsibility, not a dotenv parser's.
func (s *Systemd) CaptureExisting(ctx context.Context, name string) (*HostWorkloadCapture, error) {
	unit, props, err := s.Show(ctx, name)
	if err != nil || unit.LoadState != "loaded" || !strings.HasSuffix(unit.Name, ".service") {
		return nil, fmt.Errorf("the original systemd service is unavailable")
	}
	out := systemdCaptureProperties(unit, props)
	out.UID, out.GID, err = ResolveHostAccount(out.Account)
	if err != nil {
		out.Blockers = append(out.Blockers, "The original systemd account cannot be verified.")
	}
	files := map[string]string{}
	paths := []string{unit.Fragment}
	if dropins := props["DropInPaths"]; dropins != "" {
		// systemctl escapes whitespace in path properties; unresolved path
		// syntax must block capture rather than silently skip a drop-in.
		paths = append(paths, strings.Fields(dropins)...)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00\n\r") {
			return nil, fmt.Errorf("a systemd configuration path cannot be captured safely")
		}
		content, err := readCaptureFile(hostexec.HostPath(path))
		if err != nil {
			return nil, fmt.Errorf("the original systemd unit or drop-in could not be read")
		}
		files[path] = string(content)
	}
	private, _ := json.Marshal(struct {
		Properties map[string]string
		Files      map[string]string
	}{stableSystemdProperties(props), files})
	out.OriginalConfig = private
	out.ConfigurationDigest = captureDigest(private)
	if unit.MainPID > 1 {
		p, err := process.NewProcessWithContext(ctx, int32(unit.MainPID))
		if err != nil {
			return nil, ErrHostWorkloadChanged
		}
		created, err := p.CreateTimeWithContext(ctx)
		if err != nil {
			return nil, ErrHostWorkloadChanged
		}
		live, err := CaptureExistingProcess(ctx, int32(unit.MainPID), created)
		if err != nil {
			return nil, err
		}
		// The manager, not the PID, owns restart. Keep its unit identity while
		// carrying the private argv/environment into the recovered source plan.
		out.Account, out.UID, out.GID = live.Account, live.UID, live.GID
		out.SourceDirectory, out.SourcePath = live.SourceDirectory, live.SourcePath
		out.Command, out.Environment = live.Command, live.Environment
		out.EnvironmentNames = live.EnvironmentNames
		out.Processes = live.Processes
		out.Processes[0].State = unit.ActiveState
		out.Processes[0].LogSources = []string{"journal:" + unit.Name}
		current, _, err := s.Show(ctx, name)
		if err != nil || current.MainPID != unit.MainPID || current.LoadState != "loaded" {
			return nil, ErrHostWorkloadChanged
		}
	} else {
		out.Processes = []HostProcessCapture{{State: unit.ActiveState, LogSources: []string{"journal:" + unit.Name}}}
		if props["Environment"] != "" || props["EnvironmentFiles"] != "" {
			out.Blockers = append(out.Blockers, "This service is not running. Its expanded environment cannot be recovered from a live process; provide and review its environment before migration.")
		}
		out.Blockers = append(out.Blockers, "The stopped service has no verified live command. Start it under systemd or provide a reviewed container command before migration.")
	}
	if strings.TrimSpace(props["EnvironmentFiles"]) != "" {
		out.Blockers = append(out.Blockers, "Systemd environment files can change between starts. Their content and expansion require an explicit managed variable plan before migration.")
	}
	if props["User"] != "" && props["User"] != out.Account {
		out.Warnings = append(out.Warnings, "The effective runtime account differs from the unit's User setting. Review permission and namespace handling.")
	}
	out.Blockers = uniqueCaptureStrings(out.Blockers)
	if unit.MainPID > 1 {
		out.SourceFiles, err = CaptureHostSourceFiles([]string{out.SourcePath})
		if err != nil {
			out.Blockers = append(out.Blockers, "The original service executable cannot be verified for safe restoration.")
		}
	}
	return out, nil
}

func systemdCaptureProperties(unit *Unit, props map[string]string) *HostWorkloadCapture {
	account := props["User"]
	if account == "" {
		account = "root"
	}
	out := &HostWorkloadCapture{Manager: "systemd", ResourceID: unit.Name, Name: unit.Name, Account: account,
		SourceDirectory: props["WorkingDirectory"], SourcePath: unit.Fragment, Processes: []HostProcessCapture{},
		Environment: map[string]string{}, EnvironmentNames: []string{}, Blockers: []string{}, Warnings: []string{}}
	if props["NeedDaemonReload"] == "yes" {
		out.Blockers = append(out.Blockers, "The systemd unit files differ from the loaded manager configuration. Reload and review the unit before migration.")
	}
	if props["Type"] != "simple" && props["Type"] != "exec" {
		out.Blockers = append(out.Blockers, "This systemd service uses "+props["Type"]+" lifecycle semantics, which require an equivalent container plan.")
	}
	if props["RemainAfterExit"] == "yes" {
		out.Blockers = append(out.Blockers, "RemainAfterExit needs an explicit lifecycle plan before migration.")
	}
	for _, field := range []string{"ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost", "RootDirectory", "RootImage", "LoadCredential", "LoadCredentialEncrypted", "SetCredential", "SetCredentialEncrypted", "RuntimeDirectory", "StateDirectory", "CacheDirectory", "LogsDirectory", "ConfigurationDirectory", "BindPaths", "BindReadOnlyPaths", "ReadWritePaths", "ReadOnlyPaths", "InaccessiblePaths", "DeviceAllow", "SupplementaryGroups", "Sockets", "TriggeredBy"} {
		if props[field] != "" && props[field] != "[]" {
			out.Blockers = append(out.Blockers, "The systemd setting "+field+" needs an equivalent managed runtime configuration before migration.")
		}
	}
	for _, field := range []string{"DynamicUser", "PrivateNetwork", "PrivateUsers", "PrivateTmp", "PrivateDevices", "NoNewPrivileges", "ProtectKernelTunables", "ProtectKernelModules", "ProtectControlGroups", "RestrictRealtime", "RestrictSUIDSGID", "MemoryDenyWriteExecute"} {
		if props[field] == "yes" {
			out.Blockers = append(out.Blockers, "The systemd isolation setting "+field+" must be preserved explicitly before container migration.")
		}
	}
	for _, field := range []string{"ProtectSystem", "ProtectHome", "RestrictAddressFamilies", "SystemCallFilter", "IPAddressAllow", "IPAddressDeny", "AmbientCapabilities", "CapabilityBoundingSet"} {
		value := props[field]
		if value != "" && value != "no" && value != "false" && !(field == "CapabilityBoundingSet" && value == systemdAllCapabilities) {
			out.Blockers = append(out.Blockers, "The systemd security setting "+field+" needs an equivalent managed runtime policy before migration.")
		}
	}
	out.Warnings = append(out.Warnings, "The original unit, drop-ins, account and journal are retained as the baseline. Migration requires reviewing source, operating-system dependencies, persistence and external unit dependencies.")
	return out
}

// The full default set is not portable across kernel/systemd versions; an
// unknown capability set is deliberately a decision instead of guessed parity.
const systemdAllCapabilities = "cap_chown cap_dac_override cap_dac_read_search cap_fowner cap_fsetid cap_kill cap_setgid cap_setuid cap_setpcap cap_linux_immutable cap_net_bind_service cap_net_broadcast cap_net_admin cap_net_raw cap_ipc_lock cap_ipc_owner cap_sys_module cap_sys_rawio cap_sys_chroot cap_sys_ptrace cap_sys_pacct cap_sys_admin cap_sys_boot cap_sys_nice cap_sys_resource cap_sys_time cap_sys_tty_config cap_mknod cap_lease cap_audit_write cap_audit_control cap_setfcap cap_mac_override cap_mac_admin cap_syslog cap_wake_alarm cap_block_suspend cap_audit_read cap_perfmon cap_bpf cap_checkpoint_restore"

func stableSystemdProperties(props map[string]string) map[string]string {
	out := map[string]string{}
	for _, field := range []string{"Id", "FragmentPath", "DropInPaths", "Type", "User", "Group", "WorkingDirectory", "ExecStart", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost", "Environment", "EnvironmentFiles", "PassEnvironment", "UnsetEnvironment", "Restart", "RestartUSec", "KillMode", "KillSignal", "TimeoutStopUSec", "RootDirectory", "RootImage", "DynamicUser", "PrivateNetwork", "PrivateUsers", "PrivateTmp", "PrivateDevices", "NoNewPrivileges", "ProtectSystem", "ProtectHome", "LoadCredential", "LoadCredentialEncrypted", "SetCredential", "SetCredentialEncrypted", "RuntimeDirectory", "StateDirectory", "CacheDirectory", "LogsDirectory", "ConfigurationDirectory", "BindPaths", "BindReadOnlyPaths", "ReadWritePaths", "ReadOnlyPaths", "InaccessiblePaths", "DeviceAllow", "SupplementaryGroups", "Sockets", "TriggeredBy", "NeedDaemonReload"} {
		value := props[field]
		if strings.HasPrefix(field, "Exec") {
			value = stableSystemdExec(value)
		}
		out[field] = value
	}
	return out
}

// `systemctl show ExecStart` appends timestamps/PID/exit observations. Those
// change on stop/start and do not describe the immutable startup command.
func stableSystemdExec(value string) string {
	parts := strings.Split(value, " ; ")
	kept := parts[:0]
	for _, part := range parts {
		trimmed := strings.TrimSpace(strings.TrimRight(part, "}"))
		if strings.HasPrefix(trimmed, "start_time=") || strings.HasPrefix(trimmed, "stop_time=") || strings.HasPrefix(trimmed, "pid=") || strings.HasPrefix(trimmed, "code=") || strings.HasPrefix(trimmed, "status=") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, " ; ")
}

// ResolveHostAccount is used only for a manager-selected trusted account.
func ResolveHostAccount(name string) (uint32, uint32, error) {
	raw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 || fields[0] != name {
			continue
		}
		uid, e1 := strconv.ParseUint(fields[2], 10, 32)
		gid, e2 := strconv.ParseUint(fields[3], 10, 32)
		if e1 == nil && e2 == nil {
			return uint32(uid), uint32(gid), nil
		}
	}
	return 0, 0, fmt.Errorf("the runtime account could not be verified")
}
