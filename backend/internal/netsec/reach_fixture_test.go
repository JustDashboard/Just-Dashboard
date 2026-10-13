package netsec

import (
	"net"
	"strconv"
)

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad test address " + s)
	}
	return ip
}
func ufwRules(lines ...string) []Rule {
	rules := []Rule{}
	for _, line := range lines {
		m := ufwNumberedRe.FindStringSubmatch(line)
		num, _ := strconv.Atoi(m[1])
		r := parseUFWRule(num, m[2])
		annotateRule(&r)
		rules = append(rules, r)
	}
	return rules
}
