package updates

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var aptTarget = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(?::[a-z0-9-]+)?$`)
var aptVersion = regexp.MustCompile(`^[0-9A-Za-z.+:~_-]+$`)

func aptSecurityUpgradeArgs(packages []Package, simulate func([]string) (string, error)) ([]string, error) {
	selected := make(map[string]string)
	for _, p := range packages {
		if !p.Security || p.Current == "" {
			continue
		}
		name := p.Name
		if p.Architecture != "" && !strings.Contains(name, ":") {
			name += ":" + p.Architecture
		}
		if !aptTarget.MatchString(name) || !aptVersion.MatchString(p.Candidate) {
			return nil, fmt.Errorf("invalid security package candidate %q", name)
		}
		selected[name] = p.Candidate
	}
	if len(selected) == 0 {
		return nil, errors.New("no security updates are currently available")
	}
	_, args, _ := (aptManager{}).UpgradeCommand(true)
	var targets []string
	for name, version := range selected {
		targets = append(targets, name+"="+version)
	}
	sort.Strings(targets)
	args = append(args, targets...)
	output, err := simulate(args)
	if err != nil {
		return nil, fmt.Errorf("simulate security updates: %w", err)
	}
	// Explicit targets can still make APT select dependencies. Refuse a plan
	// that widens the promised scope instead of silently upgrading other apps.
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Remv ") {
			return nil, errors.New("security updates would remove packages; review the full upgrade manually")
		}
		p, ok := parseInstLine(line)
		if !ok {
			continue
		}
		name := p.Name
		if p.Architecture != "" && !strings.Contains(name, ":") {
			name += ":" + p.Architecture
		}
		if p.Current == "" || selected[name] != p.Candidate || !p.Security {
			return nil, fmt.Errorf("security updates would also change %s outside the security plan; review the full upgrade manually", p.Name)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return args, nil
}
