package deploy

import (
	"strconv"
	"strings"
)

// Only one literal TCP port in the final stage is an automatic default.
// Dynamic expressions, ranges and multiple ports need an operator decision.
func detectedDockerfilePort(content []byte) int {
	ports := map[int]bool{}
	invalid := false
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			ports, invalid = map[int]bool{}, false
		case "EXPOSE":
			for _, raw := range fields[1:] {
				if strings.HasPrefix(raw, "#") {
					break
				}
				portText, protocol, _ := strings.Cut(raw, "/")
				port, err := strconv.Atoi(portText)
				if err != nil || port < 1 || port > 65535 {
					invalid = true
					continue
				}
				if protocol == "" || protocol == "tcp" {
					ports[port] = true
				}
			}
		}
	}
	if !invalid && len(ports) == 1 {
		for port := range ports {
			return port
		}
	}
	return 0
}
