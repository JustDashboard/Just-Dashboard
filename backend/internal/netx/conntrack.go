package netx

import (
	"strconv"
)

// The fullness of the connection-tracking table at which the page warns and
// at which it calls the situation critical. The kernel drops new connections
// when the table is full, so the warning has to come while there is still
// room to raise the maximum.
const (
	conntrackWarning  = 80.0
	conntrackCritical = 95.0
)

// Conntrack is the connection-tracking table's fullness.
type Conntrack struct {
	// Available is false where the conntrack module is not loaded, so there is
	// no table to be full.
	Available bool    `json:"available"`
	Count     int     `json:"count"`
	Max       int     `json:"max"`
	Percent   float64 `json:"percent"`
	// Level is ok, warning (80% or more) or critical (95% or more).
	Level string `json:"level"`
}

// readConntrack reads the table's count and maximum.
func readConntrack() Conntrack {
	c := Conntrack{Level: "ok"}
	count, ok1 := gatewayReadSysctl("net.netfilter.nf_conntrack_count")
	limit, ok2 := gatewayReadSysctl("net.netfilter.nf_conntrack_max")
	if !ok1 || !ok2 {
		return c
	}
	n, err1 := strconv.Atoi(count)
	m, err2 := strconv.Atoi(limit)
	if err1 != nil || err2 != nil || m <= 0 {
		return c
	}
	c.Available, c.Count, c.Max = true, n, m
	c.Percent = float64(n) * 100 / float64(m)
	switch {
	case c.Percent >= conntrackCritical:
		c.Level = "critical"
	case c.Percent >= conntrackWarning:
		c.Level = "warning"
	}
	return c
}
