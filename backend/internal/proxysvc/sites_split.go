package proxysvc

import (
	"fmt"
	"regexp"
	"strings"
)

// SiteSplit is a deployment route's canary on nginx: Percent of the requests
// go to Servers, the candidate release, and the rest to the site's Pool, the
// live one. Each request is given one of the two at random — split_clients
// over nginx's own $request_id — so the share holds per request rather than
// per visitor, and the access log records which release answered as rel=,
// Live or Candidate, for the two to be read side by side.
type SiteSplit struct {
	Percent   int          `json:"percent"`
	Servers   []PoolServer `json:"servers"`
	Live      string       `json:"live"`
	Candidate string       `json:"candidate"`
}

// splitLabelRe is what a release label may be: it is written into the
// configuration and the log unquoted.
var splitLabelRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func validateSplit(spec *SiteSpec) error {
	split := spec.Split
	if split == nil {
		return nil
	}
	if spec.Kind != "proxy" || spec.Pool == nil {
		return fmt.Errorf("only a site that forwards to a pool of servers can share it with another")
	}
	if split.Percent < 1 || split.Percent > 99 {
		return fmt.Errorf("the share sent to the second pool must be between 1 and 99 percent")
	}
	if len(split.Servers) == 0 || len(split.Servers) > maxPoolServers {
		return fmt.Errorf("the second pool needs between 1 and %d servers", maxPoolServers)
	}
	for _, server := range split.Servers {
		if err := validPoolAddress(server.Address); err != nil {
			return err
		}
	}
	if !splitLabelRe.MatchString(split.Live) || !splitLabelRe.MatchString(split.Candidate) || split.Live == split.Candidate {
		return fmt.Errorf("the two pools need different names of letters, digits, dashes or underscores")
	}
	return nil
}

func (spec *SiteSpec) splitName() string { return NginxIdent(spec.Name) + "_canary" }

func (spec *SiteSpec) splitReleaseVar() string { return spec.varIdent() + "_release" }

func (spec *SiteSpec) splitUpstreamVar() string { return spec.varIdent() + "_upstream" }

// renderSplit writes the candidate's upstream block and the two variables
// that pick between the pools: the release a request was given, and the
// upstream block that names it.
func renderSplit(l *lines, spec *SiteSpec) {
	split := spec.Split
	l.add("# A canary: %d%% of requests, chosen at random, go to release %s;", split.Percent, split.Candidate)
	l.add("# the rest stay on release %s.", split.Live)
	l.add("upstream %s {", spec.splitName())
	if spec.Pool.Method != "" {
		l.add("    %s", poolMethods[spec.Pool.Method])
	}
	for _, server := range split.Servers {
		l.add("%s;", poolServerLine(server))
	}
	l.add("}")
	l.blank()
	l.add("split_clients \"${request_id}\" $%s {", spec.splitReleaseVar())
	l.add("    %d%% %s;", split.Percent, split.Candidate)
	l.add("    * %s;", split.Live)
	l.add("}")
	l.blank()
	l.add("map $%s $%s {", spec.splitReleaseVar(), spec.splitUpstreamVar())
	l.add("    %s %s;", split.Candidate, spec.splitName())
	l.add("    default %s;", spec.poolName())
	l.add("}")
}

// splitLogFormat is the timed format with the release that answered.
func splitLogFormat(spec *SiteSpec) string {
	return strings.TrimSuffix(timedLogFormat, "'") + " rel=$" + spec.splitReleaseVar() + "'"
}
