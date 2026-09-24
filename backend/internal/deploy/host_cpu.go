package deploy

import (
	"os"
	"strings"
)

// HostCPUFeatures reads the instruction-set flags a database image can
// require from /proc/cpuinfo: x86's avx and arm64's atomics. Only those names
// are kept; nil means the file could not be read, which is not evidence
// either way. Preflight records it, and database provisioning refuses an
// engine this CPU cannot run before it pulls anything.
func HostCPUFeatures() []string {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return nil
	}
	return cpuFeaturesFrom(string(content))
}

func cpuFeaturesFrom(cpuinfo string) []string {
	features := []string{}
	for _, line := range strings.Split(cpuinfo, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "flags" && key != "Features" {
			continue
		}
		for _, flag := range strings.Fields(value) {
			if (flag == "avx" || flag == "atomics") && !containsString(features, flag) {
				features = append(features, flag)
			}
		}
		// Every core reports the same set; the first line is enough.
		break
	}
	return features
}

// MongoCPUUnsupported names the instruction set MongoDB 5 and later require
// that this CPU lacks: AVX on x86-64, ARMv8.2 atomics on arm64. Empty when
// they are present.
func MongoCPUUnsupported(architecture string, features []string) string {
	switch architecture {
	case "amd64":
		if !containsString(features, "avx") {
			return "x86-64 without AVX"
		}
	case "arm64":
		if !containsString(features, "atomics") {
			return "arm64 older than ARMv8.2"
		}
	}
	return ""
}
