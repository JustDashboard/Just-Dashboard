package proxysvc

import (
	"fmt"
	"regexp"
	"strings"
)

// SiteSplit is how a deployment route picks between releases on nginx,
// beside the site's Pool, which is the live release Live.
//
// A canary sends Percent of the requests to Servers, the release Candidate,
// each request given one of the two at random — split_clients over nginx's
// own $request_id — so the share holds per request rather than per visitor.
//
// Skew protection (CookieMaxAge) keeps a visitor on the release that first
// answered them: a request whose jd_release cookie names the live release,
// the candidate or one of Pinned — a release still warm after a cutover — is
// answered by that release, and every other request is given one as above
// and told which in a cookie of CookieMaxAge seconds. A cookie naming a
// release that is none of these is ignored and replaced.
//
// The access log records which release answered as rel=, so the releases can
// be read side by side.
type SiteSplit struct {
	Percent   int          `json:"percent,omitempty"`
	Servers   []PoolServer `json:"servers,omitempty"`
	Live      string       `json:"live"`
	Candidate string       `json:"candidate,omitempty"`
	// Pinned are other releases still answering the visitors whose cookie
	// names them; only skew protection has any.
	Pinned       []SitePinned `json:"pinned,omitempty"`
	CookieMaxAge int          `json:"cookieMaxAge,omitempty"`
}

// SitePinned is a release that keeps its own visitors.
type SitePinned struct {
	Release string       `json:"release"`
	Servers []PoolServer `json:"servers"`
}

// ReleaseCookie is the cookie skew protection keeps a visitor's release in.
const ReleaseCookie = "jd_release"

// MaxReleaseCookieAge is the longest a release cookie is kept: the longest
// warm window, an hour.
const MaxReleaseCookieAge = 3600

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
	if !splitLabelRe.MatchString(split.Live) {
		return fmt.Errorf("the live release needs a name of letters, digits, dashes or underscores")
	}
	seen := map[string]bool{split.Live: true}
	if split.Candidate != "" {
		if split.Percent < 1 || split.Percent > 99 {
			return fmt.Errorf("the share sent to the second pool must be between 1 and 99 percent")
		}
		if err := validSplitServers(split.Servers); err != nil {
			return err
		}
		if !splitLabelRe.MatchString(split.Candidate) || seen[split.Candidate] {
			return fmt.Errorf("the two pools need different names of letters, digits, dashes or underscores")
		}
		seen[split.Candidate] = true
	} else if split.Percent != 0 || len(split.Servers) != 0 {
		return fmt.Errorf("a share of the requests goes to a named release")
	}
	if split.CookieMaxAge < 0 || split.CookieMaxAge > MaxReleaseCookieAge {
		return fmt.Errorf("the release cookie lasts between 1 and %d seconds", MaxReleaseCookieAge)
	}
	if len(split.Pinned) > 0 && split.CookieMaxAge == 0 {
		return fmt.Errorf("a release keeps its visitors only by the release cookie")
	}
	for _, pinned := range split.Pinned {
		if !splitLabelRe.MatchString(pinned.Release) || seen[pinned.Release] {
			return fmt.Errorf("each release kept for its visitors needs a name of its own")
		}
		seen[pinned.Release] = true
		if err := validSplitServers(pinned.Servers); err != nil {
			return err
		}
	}
	if split.Candidate == "" && split.CookieMaxAge == 0 {
		return fmt.Errorf("a route picks between releases for a canary or by the release cookie")
	}
	return nil
}

func validSplitServers(servers []PoolServer) error {
	if len(servers) == 0 || len(servers) > maxPoolServers {
		return fmt.Errorf("a release's pool needs between 1 and %d servers", maxPoolServers)
	}
	for _, server := range servers {
		if err := validPoolAddress(server.Address); err != nil {
			return err
		}
	}
	return nil
}

func (spec *SiteSpec) splitName() string { return NginxIdent(spec.Name) + "_canary" }

// pinnedName is the upstream block of a release kept for its visitors. A
// label is letters, digits, dashes and underscores already (splitLabelRe).
func (spec *SiteSpec) pinnedName(release string) string {
	return NginxIdent(spec.Name) + "_release_" + release
}

func (spec *SiteSpec) splitDrawVar() string { return spec.varIdent() + "_draw" }

func (spec *SiteSpec) splitReleaseVar() string { return spec.varIdent() + "_release" }

func (spec *SiteSpec) splitUpstreamVar() string { return spec.varIdent() + "_upstream" }

func (spec *SiteSpec) splitCookieVar() string { return spec.varIdent() + "_release_cookie" }

func (spec *SiteSpec) splitSecureVar() string { return spec.varIdent() + "_release_secure" }

// renderSplit writes the other releases' upstream blocks and the variables
// that pick between the releases: the one a request draws, the one it is
// answered by — its cookie's, when that names a release this route still
// serves — the upstream block that names it, and the cookie to set.
func renderSplit(l *lines, spec *SiteSpec) {
	split := spec.Split
	block := func(name string, servers []PoolServer) {
		l.add("upstream %s {", name)
		if spec.Pool.Method != "" {
			l.add("    %s", poolMethods[spec.Pool.Method])
		}
		for _, server := range servers {
			l.add("%s;", poolServerLine(server))
		}
		l.add("}")
	}
	// The release a request draws: with a canary, split_clients' own pick;
	// without one, the live release. With the cookie the draw is only for a
	// visitor it does not already name a release for.
	drawn := split.Live
	if split.Candidate != "" {
		l.add("# A canary: %d%% of requests, chosen at random, go to release %s;", split.Percent, split.Candidate)
		l.add("# the rest stay on release %s.", split.Live)
		block(spec.splitName(), split.Servers)
		l.blank()
		into := spec.splitReleaseVar()
		if split.CookieMaxAge > 0 {
			into = spec.splitDrawVar()
		}
		l.add("split_clients \"${request_id}\" $%s {", into)
		l.add("    %d%% %s;", split.Percent, split.Candidate)
		l.add("    * %s;", split.Live)
		l.add("}")
		l.blank()
		drawn = "$" + into
	}
	for _, pinned := range split.Pinned {
		l.add("# Release %s answers the visitors it answered first, until its window ends.", pinned.Release)
		block(spec.pinnedName(pinned.Release), pinned.Servers)
		l.blank()
	}
	known := split.releases()
	if split.CookieMaxAge > 0 {
		l.add("# A visitor stays on the release that first answered them: their")
		l.add("# %s cookie names it while this route still serves it.", ReleaseCookie)
		l.add("map $cookie_%s $%s {", ReleaseCookie, spec.splitReleaseVar())
		for _, release := range known {
			l.add("    %s %s;", release, release)
		}
		l.add("    default %s;", drawn)
		l.add("}")
		l.blank()
	}
	l.add("map $%s $%s {", spec.splitReleaseVar(), spec.splitUpstreamVar())
	if split.Candidate != "" {
		l.add("    %s %s;", split.Candidate, spec.splitName())
	}
	for _, pinned := range split.Pinned {
		l.add("    %s %s;", pinned.Release, spec.pinnedName(pinned.Release))
	}
	l.add("    default %s;", spec.poolName())
	l.add("}")
	if split.CookieMaxAge == 0 {
		return
	}
	// Set on the first answer, and on an answer to a cookie naming a release
	// the route no longer serves: never when the cookie already says it.
	l.blank()
	l.add("map $scheme $%s {", spec.splitSecureVar())
	l.add("    https \"; Secure\";")
	l.add("    default \"\";")
	l.add("}")
	l.add("map \"$cookie_%s:$%s\" $%s {", ReleaseCookie, spec.splitReleaseVar(), spec.splitCookieVar())
	for _, release := range known {
		l.add("    \"%s:%s\" \"\";", release, release)
	}
	l.add("    default \"%s=$%s; Path=/; Max-Age=%d; HttpOnly; SameSite=Lax$%s\";",
		ReleaseCookie, spec.splitReleaseVar(), split.CookieMaxAge, spec.splitSecureVar())
	l.add("}")
}

// releases are every release the route serves, live first.
func (split *SiteSplit) releases() []string {
	out := []string{split.Live}
	if split.Candidate != "" {
		out = append(out, split.Candidate)
	}
	for _, pinned := range split.Pinned {
		out = append(out, pinned.Release)
	}
	return out
}

// renderReleaseCookie is the header that tells a visitor which release
// answered them; empty, and so not sent, when their cookie already did.
func renderReleaseCookie(l *lines, spec *SiteSpec) {
	if spec.Pool == nil || spec.Split == nil || spec.Split.CookieMaxAge == 0 {
		return
	}
	l.add("    add_header Set-Cookie $%s always;", spec.splitCookieVar())
}

// splitLogFormat is the timed format with the release that answered.
func splitLogFormat(spec *SiteSpec) string {
	return strings.TrimSuffix(timedLogFormat, "'") + " rel=$" + spec.splitReleaseVar() + "'"
}
