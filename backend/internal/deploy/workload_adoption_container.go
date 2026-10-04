package deploy

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/api/types/mount"
)

// The original Engine configuration is authoritative for created containers.
// Every non-default HostConfig field must either have an exact Compose mapping
// below or become a blocker. Silently accepting an omitted option is unsafe.
func (r *dockerRecovery) recoverService(name string, service map[string]any, capture *dockerx.AdoptionContainer, stack bool) {
	insp, config, host := capture.Inspection, capture.Inspection.Config, capture.Inspection.HostConfig
	for field := range service {
		if field != "depends_on" && field != "profiles" && field != "build" && !strings.HasPrefix(field, "x-") {
			delete(service, field)
		}
	}
	service["command"], service["entrypoint"] = append([]string{}, config.Cmd...), append([]string{}, config.Entrypoint...)
	for field, value := range map[string]string{"hostname": config.Hostname, "domainname": config.Domainname, "user": config.User, "working_dir": config.WorkingDir, "stop_signal": config.StopSignal} {
		if value != "" {
			service[field] = value
		}
	}
	service["tty"], service["stdin_open"] = config.Tty, config.OpenStdin
	if host.Init != nil {
		service["init"] = *host.Init
	}
	if capture.Image.OS != "" && capture.Image.Architecture != "" {
		service["platform"] = capture.Image.OS + "/" + capture.Image.Architecture
	}
	if config.StopTimeout != nil {
		if *config.StopTimeout < 0 {
			r.issue("stop_timeout_unbounded", "An infinite stop timeout cannot be safely enforced by deployment recovery.", name, "stopTimeout", true)
		} else {
			service["stop_grace_period"] = strconv.Itoa(*config.StopTimeout) + "s"
		}
	}
	if config.StdinOnce || config.ArgsEscaped {
		r.issue("container_config_unsupported", "Single-use stdin or Windows escaped commands cannot be faithfully represented by Linux Compose deployments.", name, "config", true)
	}
	environment := map[string]any{}
	seen := map[string]bool{}
	for _, assignment := range config.Env {
		key, value, hasValue := strings.Cut(assignment, "=")
		if ValidateEnvKey(key) != nil || seen[key] || !hasValue {
			r.issue("environment_not_reproducible", "Environment contains duplicate, invalid or inherited variable names. Set explicit unique values before adoption.", name, "environment", true)
			continue
		}
		seen[key] = true
		environment[key] = r.privateValue(name, "env_"+key, value)
	}
	service["environment"] = environment
	labels := map[string]any{}
	for key, value := range config.Labels {
		if strings.HasPrefix(key, "com.docker.compose.") || strings.HasPrefix(key, "io.just-dashboard.") {
			continue
		}
		labels[key] = r.privateValue(name, "label_"+key, value)
	}
	if len(labels) > 0 {
		service["labels"] = labels
	}
	if config.Healthcheck != nil {
		health := map[string]any{"test": append([]string{}, config.Healthcheck.Test...)}
		for field, duration := range map[string]time.Duration{"interval": config.Healthcheck.Interval, "timeout": config.Healthcheck.Timeout, "start_period": config.Healthcheck.StartPeriod, "start_interval": config.Healthcheck.StartInterval} {
			if duration != 0 {
				health[field] = duration.String()
			}
		}
		if config.Healthcheck.Retries != 0 {
			health["retries"] = config.Healthcheck.Retries
		}
		service["healthcheck"] = health
	}
	if len(config.ExposedPorts) > 0 {
		expose := []string{}
		for port := range config.ExposedPorts {
			expose = append(expose, string(port))
		}
		sort.Strings(expose)
		service["expose"] = expose
	}
	if stack {
		service["container_name"] = strings.TrimPrefix(insp.Name, "/")
	} else {
		delete(service, "container_name")
	}
	ports := []any{}
	for port, configured := range host.PortBindings {
		bindings := configured
		if insp.NetworkSettings != nil && len(insp.NetworkSettings.Ports[port]) > 0 {
			bindings = insp.NetworkSettings.Ports[port]
		}
		for _, binding := range bindings {
			number, err := strconv.Atoi(binding.HostPort)
			if err != nil || number == 0 {
				r.issue("port_unresolved", "A published port has no fixed current host binding.", name, "ports", true)
				continue
			}
			ports = append(ports, map[string]any{"target": port.Int(), "published": strconv.Itoa(number), "host_ip": binding.HostIP, "protocol": port.Proto()})
		}
	}
	sort.Slice(ports, func(i, j int) bool { return string(mustJSON(ports[i])) < string(mustJSON(ports[j])) })
	if len(ports) > 0 {
		service["ports"] = ports
	}
	r.recoverMounts(name, service, capture)
	r.recoverNetworks(name, service, capture, stack)
	var fields map[string]any
	_ = json.Unmarshal(mustJSON(host), &fields)
	mapped := map[string]bool{"Binds": true, "Mounts": true, "PortBindings": true, "NetworkMode": true, "VolumeDriver": true, "LogConfig": true, "RestartPolicy": true, "ConsoleSize": true, "MaskedPaths": true, "ReadonlyPaths": true}
	translations := map[string]string{
		"Privileged": "privileged", "ReadonlyRootfs": "read_only", "CapAdd": "cap_add", "CapDrop": "cap_drop", "CgroupnsMode": "cgroup", "Dns": "dns", "DnsOptions": "dns_opt", "DnsSearch": "dns_search", "ExtraHosts": "extra_hosts", "GroupAdd": "group_add", "IpcMode": "ipc", "OomScoreAdj": "oom_score_adj", "PidMode": "pid", "SecurityOpt": "security_opt", "StorageOpt": "storage_opt", "UTSMode": "uts", "UsernsMode": "userns_mode", "ShmSize": "shm_size", "Sysctls": "sysctls", "Runtime": "runtime", "Init": "init", "CpuShares": "cpu_shares", "Memory": "mem_limit", "MemoryReservation": "mem_reservation", "MemorySwap": "memswap_limit", "MemorySwappiness": "mem_swappiness", "OomKillDisable": "oom_kill_disable", "CpuPeriod": "cpu_period", "CpuQuota": "cpu_quota", "CpuRealtimePeriod": "cpu_rt_period", "CpuRealtimeRuntime": "cpu_rt_runtime", "CpusetCpus": "cpuset", "CgroupParent": "cgroup_parent", "DeviceCgroupRules": "device_cgroup_rules", "PidsLimit": "pids_limit",
	}
	for engine, compose := range translations {
		mapped[engine] = true
		if value, exists := fields[engine]; exists && !emptyEngineValue(value) {
			if number, ok := value.(float64); ok {
				service[compose] = int64(number)
			} else {
				service[compose] = value
			}
		}
	}
	mapped["NanoCpus"] = true
	if host.NanoCPUs != 0 {
		service["cpus"] = float64(host.NanoCPUs) / 1e9
	}
	mapped["Isolation"] = true
	if host.Isolation != "" && host.Isolation != "default" {
		r.issue("windows_isolation", "Windows container isolation cannot be adopted as a Linux deployment.", name, "isolation", true)
	}
	mapped["Devices"] = true
	if len(host.Devices) > 0 {
		devices := []string{}
		for _, device := range host.Devices {
			if _, err := r.paths.Resolve(device.PathOnHost); err != nil {
				r.issue("device_path_outside_roots", "A host device path is outside the permitted file roots.", name, "devices", true)
			}
			devices = append(devices, device.PathOnHost+":"+device.PathInContainer+":"+device.CgroupPermissions)
		}
		service["devices"] = devices
	}
	mapped["Ulimits"] = true
	if len(host.Ulimits) > 0 {
		limits := map[string]any{}
		for _, limit := range host.Ulimits {
			limits[limit.Name] = map[string]any{"soft": limit.Soft, "hard": limit.Hard}
		}
		service["ulimits"] = limits
	}
	mapped["Tmpfs"] = true
	if len(host.Tmpfs) > 0 {
		tmpfs := []string{}
		for path, options := range host.Tmpfs {
			value := path
			if options != "" {
				value += ":" + options
			}
			tmpfs = append(tmpfs, value)
		}
		sort.Strings(tmpfs)
		service["tmpfs"] = tmpfs
	}
	mapped["Annotations"] = true
	if len(host.Annotations) > 0 {
		service["annotations"] = host.Annotations
	}
	mapped["BlkioWeight"], mapped["BlkioWeightDevice"], mapped["BlkioDeviceReadBps"], mapped["BlkioDeviceWriteBps"], mapped["BlkioDeviceReadIOps"], mapped["BlkioDeviceWriteIOps"] = true, true, true, true, true, true
	blkio := map[string]any{}
	if host.BlkioWeight != 0 {
		blkio["weight"] = host.BlkioWeight
	}
	for engine, compose := range map[string]string{"BlkioWeightDevice": "weight_device", "BlkioDeviceReadBps": "device_read_bps", "BlkioDeviceWriteBps": "device_write_bps", "BlkioDeviceReadIOps": "device_read_iops", "BlkioDeviceWriteIOps": "device_write_iops"} {
		if devices, ok := fields[engine].([]any); ok && len(devices) > 0 {
			mappedDevices := []any{}
			for _, raw := range devices {
				device := object(raw)
				path, _ := device["Path"].(string)
				if _, err := r.paths.Resolve(path); err != nil {
					r.issue("device_path_outside_roots", "A block device path is outside the permitted file roots.", name, "blkio", true)
				}
				if engine == "BlkioWeightDevice" {
					mappedDevices = append(mappedDevices, map[string]any{"path": path, "weight": device["Weight"]})
				} else {
					mappedDevices = append(mappedDevices, map[string]any{"path": path, "rate": device["Rate"]})
				}
			}
			blkio[compose] = mappedDevices
		}
	}
	if len(blkio) > 0 {
		service["blkio_config"] = blkio
	}
	if host.RestartPolicy.Name != "" {
		restart := string(host.RestartPolicy.Name)
		if host.RestartPolicy.MaximumRetryCount > 0 {
			restart += ":" + strconv.Itoa(host.RestartPolicy.MaximumRetryCount)
		}
		service["restart"] = restart
	} else {
		service["restart"] = "no"
	}
	logging := map[string]any{"driver": host.LogConfig.Type}
	if host.LogConfig.Type == "" {
		delete(logging, "driver")
	}
	if len(host.LogConfig.Config) > 0 {
		options := map[string]any{}
		for key, value := range host.LogConfig.Config {
			options[key] = r.privateValue(name, "log_"+key, value)
		}
		logging["options"] = options
	}
	if len(logging) > 0 {
		service["logging"] = logging
	}
	for field, value := range fields {
		if !mapped[field] && !emptyEngineValue(value) {
			r.issue("engine_option_unsupported", "The Engine setting "+field+" has no proven deployment mapping; adoption would change it.", name, field, true)
		}
	}
	if !defaultMaskedPaths(host.MaskedPaths) || !defaultReadonlyPaths(host.ReadonlyPaths) {
		r.issue("custom_kernel_path_masks", "Custom masked or read-only kernel paths have no equivalent Compose mapping.", name, "maskedPaths", true)
	}
}

func emptyEngineValue(value any) bool {
	if value == nil {
		return true
	}
	switch typed := value.(type) {
	case bool:
		return !typed
	case float64:
		return typed == 0
	case string:
		return typed == ""
	case []any:
		for _, child := range typed {
			if !emptyEngineValue(child) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, child := range typed {
			if !emptyEngineValue(child) {
				return false
			}
		}
		return true
	}
	return false
}

func (r *dockerRecovery) recoverMounts(name string, service map[string]any, capture *dockerx.AdoptionContainer) {
	insp, host := capture.Inspection, capture.Inspection.HostConfig
	volumes := object(r.model["volumes"])
	if volumes == nil {
		volumes = map[string]any{}
		r.model["volumes"] = volumes
	}
	mounts := []any{}
	for _, actual := range insp.Mounts {
		if actual.Type == mount.TypeTmpfs {
			continue
		}
		entry := map[string]any{"type": string(actual.Type), "target": actual.Destination, "read_only": !actual.RW}
		switch actual.Type {
		case mount.TypeBind:
			path, err := r.paths.Resolve(actual.Source)
			if err != nil {
				r.issue("bind_path_outside_roots", "A bind mount source is outside the permitted file roots.", name, "mounts", true)
				continue
			}
			entry["source"] = path
			options := map[string]any{"create_host_path": false}
			if actual.Propagation != "" {
				options["propagation"] = string(actual.Propagation)
			}
			if strings.Contains(actual.Mode, "Z") {
				options["selinux"] = "Z"
			} else if strings.Contains(actual.Mode, "z") {
				options["selinux"] = "z"
			}
			entry["bind"] = options
		case mount.TypeVolume:
			if actual.Name == "" {
				r.issue("volume_identity_missing", "A mount has no existing Docker volume name.", name, "mounts", true)
				continue
			}
			key := "volume_" + strings.TrimPrefix(digestBytes([]byte(actual.Name)), "sha256:")[:12]
			volumes[key] = map[string]any{"name": actual.Name, "external": true}
			entry["source"] = key
		default:
			r.issue("mount_type_unsupported", "This workload uses a mount type that cannot be preserved by the deployment adapter.", name, "mounts", true)
		}
		for _, original := range host.Mounts {
			if original.Target != actual.Destination {
				continue
			}
			if original.Consistency != "" {
				entry["consistency"] = string(original.Consistency)
			}
			if original.BindOptions != nil && (original.BindOptions.NonRecursive || original.BindOptions.ReadOnlyNonRecursive || original.BindOptions.ReadOnlyForceRecursive) {
				r.issue("recursive_mount_options", "Recursive bind-mount options cannot be preserved by this Compose adapter.", name, "mounts", true)
			}
			if original.VolumeOptions != nil {
				options := map[string]any{"nocopy": original.VolumeOptions.NoCopy}
				if original.VolumeOptions.Subpath != "" {
					options["subpath"] = original.VolumeOptions.Subpath
				}
				entry["volume"] = options
			}
		}
		mounts = append(mounts, entry)
	}
	for _, original := range host.Mounts {
		if original.Type != mount.TypeTmpfs {
			continue
		}
		entry := map[string]any{"type": "tmpfs", "target": original.Target}
		if original.TmpfsOptions != nil {
			if len(original.TmpfsOptions.Options) > 0 {
				r.issue("tmpfs_options_unsupported", "Custom tmpfs options require a proven mapping before adoption.", name, "mounts", true)
			}
			entry["tmpfs"] = map[string]any{"size": original.TmpfsOptions.SizeBytes, "mode": uint32(original.TmpfsOptions.Mode)}
		}
		mounts = append(mounts, entry)
	}
	sort.Slice(mounts, func(i, j int) bool { return string(mustJSON(mounts[i])) < string(mustJSON(mounts[j])) })
	if len(mounts) > 0 {
		service["volumes"] = mounts
	}
}

func (r *dockerRecovery) recoverNetworks(name string, service map[string]any, capture *dockerx.AdoptionContainer, stack bool) {
	insp, host := capture.Inspection, capture.Inspection.HostConfig
	mode := string(host.NetworkMode)
	if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		service["network_mode"] = mode
		if strings.HasPrefix(mode, "container:") {
			r.issue("shared_container_namespace", "This workload shares another container's network namespace. Its lifecycle dependency must be adopted together first.", name, "networkMode", true)
		}
		return
	}
	if insp.NetworkSettings == nil {
		r.issue("network_capture_missing", "The original network endpoints could not be captured.", name, "networks", true)
		return
	}
	networks := object(r.model["networks"])
	if networks == nil {
		networks = map[string]any{}
		r.model["networks"] = networks
	}
	attached := map[string]any{}
	for actual, endpoint := range insp.NetworkSettings.Networks {
		key := "network_" + strings.TrimPrefix(digestBytes([]byte(actual)), "sha256:")[:12]
		networks[key] = map[string]any{"name": actual, "external": true}
		settings := map[string]any{}
		aliases := []string{}
		for _, alias := range endpoint.Aliases {
			if alias != insp.ID && alias != insp.ID[:min(12, len(insp.ID))] {
				aliases = append(aliases, alias)
			}
		}
		if !stack && actual != "bridge" {
			aliases = append(aliases, strings.TrimPrefix(insp.Name, "/"))
		}
		aliases = uniqueSorted(aliases)
		if len(aliases) > 0 {
			settings["aliases"] = aliases
		}
		if endpoint.IPAMConfig != nil {
			if endpoint.IPAMConfig.IPv4Address != "" {
				settings["ipv4_address"] = endpoint.IPAMConfig.IPv4Address
			}
			if endpoint.IPAMConfig.IPv6Address != "" {
				settings["ipv6_address"] = endpoint.IPAMConfig.IPv6Address
			}
			if len(endpoint.IPAMConfig.LinkLocalIPs) > 0 {
				settings["link_local_ips"] = endpoint.IPAMConfig.LinkLocalIPs
			}
		}
		if len(endpoint.DriverOpts) > 0 {
			settings["driver_opts"] = endpoint.DriverOpts
		}
		if endpoint.MacAddress != "" {
			settings["mac_address"] = endpoint.MacAddress
		}
		if endpoint.GwPriority != 0 {
			settings["gw_priority"] = endpoint.GwPriority
		}
		if len(endpoint.Links) > 0 {
			r.issue("legacy_network_links", "Legacy endpoint links cannot be safely reassigned to a managed runtime.", name, "networks", true)
		}
		attached[key] = settings
	}
	if len(attached) > 0 {
		service["networks"] = attached
	}
}

func defaultMaskedPaths(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	expected := []string{"/proc/asound", "/proc/acpi", "/proc/interrupts", "/proc/kcore", "/proc/keys", "/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug", "/proc/scsi", "/sys/firmware", "/sys/devices/virtual/powercap"}
	return samePaths(paths, expected)
}

func defaultReadonlyPaths(paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	return samePaths(paths, []string{"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"})
}

func samePaths(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return reflect.DeepEqual(left, right)
}
