package netx

import (
	"fmt"
	"path/filepath"
	"strings"
)

const networkBootOwners = "network-online.target systemd-networkd.service NetworkManager.service networking.service docker.service ufw.service firewalld.service"

func renderRecoveryUnit(paths Paths) string {
	return generatedHeader + "[Unit]\nDescription=Recover interrupted Just Dashboard network changes\nWants=network-online.target\nAfter=local-fs.target " + networkBootOwners + "\nBefore=" + UnitName + "\n\n[Service]\nType=oneshot\nExecStart=" + filepath.Join(paths.Dir, recoveryBinary) + " --network-recover-boot " + paths.Dir + " pending\n\n[Install]\nWantedBy=multi-user.target\n"
}

// A reboot loses unchanged managed devices as well as changed ones. Capture
// their typed creation and address dependencies before apply; the regular
// timer never replays them over a running native manager's concurrent edits.
func bootRecoveryDependencies(old *Spec) ([]recoveryCommand, error) {
	for _, ns := range old.Namespaces {
		if err := ValidNamespace(ns.Name); err != nil {
			return nil, err
		}
	}
	for _, l := range old.Links {
		if _, err := linkAddArgs(l); err != nil {
			return nil, err
		}
		if l.Master != "" {
			if err := ValidIfName(l.Master); err != nil {
				return nil, err
			}
		}
		if l.MTU != 0 && (l.MTU < 68 || l.MTU > 65535) {
			return nil, fmt.Errorf("the saved MTU of %s is invalid", l.Name)
		}
		for _, addr := range l.Addresses {
			if _, err := ParsePrefix(addr); err != nil {
				return nil, err
			}
		}
	}
	for _, a := range old.Addresses {
		if err := ValidIfName(a.Link); err != nil {
			return nil, err
		}
		if _, err := ParsePrefix(a.CIDR); err != nil {
			return nil, err
		}
	}
	dependencies := &Spec{Namespaces: old.Namespaces, Links: old.Links, Addresses: old.Addresses}
	var commands []recoveryCommand
	for _, line := range batchLines(dependencies) {
		args := strings.Fields(line)
		commands = append(commands, recoveryCommand{Tool: "ip", Args: args, AllowExists: recoveryCreationArgs(args)})
	}
	return commands, nil
}

func validBootRecoveryCommand(args []string) bool {
	if len(args) >= 5 && args[0] == "netns" && args[1] == "exec" {
		if ValidNamespace(args[2]) != nil || args[3] != "ip" {
			return false
		}
		args = args[4:]
	}
	if len(args) < 3 {
		return false
	}
	switch args[0] {
	case "netns":
		return len(args) == 3 && args[1] == "add" && ValidNamespace(args[2]) == nil
	case "link":
		return (args[1] == "add" || args[1] == "set") && ValidIfName(args[2]) == nil
	case "addr":
		return args[1] == "add"
	default:
		return false
	}
}

// Only creation commands can report an expected duplicate. Permission,
// missing-parent and property-setting errors always remain visible.
func recoveryCreationArgs(args []string) bool {
	if len(args) >= 5 && args[0] == "netns" && args[1] == "exec" && args[3] == "ip" {
		args = args[4:]
	}
	if len(args) > 0 && (args[0] == "-4" || args[0] == "-6") {
		args = args[1:]
	}
	if len(args) < 3 || args[1] != "add" {
		return false
	}
	switch args[0] {
	case "netns", "link", "addr", "route", "rule":
		return true
	default:
		return false
	}
}

func recoveryExpectedExistence(tool string, args []string, out string, err error) bool {
	if tool == "bridge" {
		return strings.Contains(strings.ToLower(out+err.Error()), "file exists")
	}
	if tool != "ip" || !recoveryCreationArgs(args) {
		return false
	}
	message := strings.ToLower(out + err.Error())
	if strings.Contains(message, "file exists") {
		return true
	}
	if len(args) >= 5 && args[0] == "netns" && args[1] == "exec" && args[3] == "ip" {
		args = args[4:]
	}
	if len(args) > 0 && (args[0] == "-4" || args[0] == "-6") {
		args = args[1:]
	}
	return len(args) >= 3 && args[0] == "addr" && args[1] == "add" && strings.Contains(message, "address already assigned")
}
