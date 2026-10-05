package procs

import "strings"

func systemdMigrationDirectiveBlockers(files map[string]string) []string {
	allowed := map[string]map[string]bool{
		"Unit": {"Description": true, "Documentation": true},
		"Service": {"Type": true, "User": true, "Group": true, "WorkingDirectory": true, "ExecStart": true, "Environment": true,
			"Restart": true, "KillMode": true, "KillSignal": true, "TimeoutStopSec": true},
		"Install": {"WantedBy": true, "RequiredBy": true, "Alias": true, "Also": true, "DefaultInstance": true},
	}
	known := map[string]bool{}
	for _, field := range []string{"MemoryMax", "MemoryHigh", "MemoryLow", "MemoryMin", "MemorySwapMax", "CPUQuota", "CPUQuotaPeriodSec", "CPUWeight", "AllowedCPUs", "TasksMax", "IOWeight", "IOReadBandwidthMax", "IOWriteBandwidthMax", "BlockIOWeight", "LimitNOFILE", "LimitNPROC", "LimitCORE", "LimitAS", "LimitDATA", "LimitSTACK", "LimitMEMLOCK", "LimitRSS", "LimitFSIZE", "LimitCPU", "UMask", "Nice", "OOMScoreAdjust", "OOMPolicy", "CPUSchedulingPolicy", "CPUSchedulingPriority", "CPUSchedulingResetOnFork", "CPUAffinity", "NUMAPolicy", "NUMAMask", "IOSchedulingClass", "IOSchedulingPriority", "RestartSec", "WatchdogSec", "ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "EnvironmentFile", "PAMName", "SELinuxContext", "AppArmorProfile", "ProtectHostname", "NoNewPrivileges", "PrivateTmp", "RuntimeDirectory", "StateDirectory", "CacheDirectory", "LogsDirectory", "ConfigurationDirectory", "After", "Before", "Wants", "Requires", "BindsTo", "PartOf", "Upholds", "OnFailure", "OnSuccess"} {
		known[field] = true
	}
	blockers := []string{}
	for _, content := range files {
		section, continued := "", false
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if continued {
				continued = strings.HasSuffix(line, "\\")
				continue
			}
			if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
				section = strings.TrimSpace(line[1 : len(line)-1])
				if allowed[section] == nil {
					blockers = append(blockers, "An unrecognized systemd unit section requires an explicit managed configuration review before migration.")
				}
				continue
			}
			field, _, assignment := strings.Cut(line, "=")
			field = strings.TrimSpace(field)
			continued = strings.HasSuffix(line, "\\")
			if !assignment || !allowed[section][field] {
				message := "A systemd unit directive is outside the supported migration set. Review its resource limits, scheduling, security, dependencies and lifecycle behavior before container migration."
				if known[field] {
					message = "The systemd directive " + field + " has no equivalent in this recovered runtime. Review a managed resource, scheduling, dependency or lifecycle plan before migration."
				}
				blockers = append(blockers, message)
			}
		}
	}
	return uniqueCaptureStrings(blockers)
}
