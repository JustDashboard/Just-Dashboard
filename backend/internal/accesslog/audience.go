package accesslog

import (
	"sort"
	"strings"
	"time"
)

// Audience answers "how many people came, and who were they", which the
// Clients table cannot: it ranks addresses, most of what reaches a public host
// is a crawler or a scanner, and a browser that opened one page asked for
// thirty files. A visitor here is a remote address together with the whole
// user agent it sent — the nearest a request log gets to one person on one
// device — and it counts once it has opened a page. The estimate is off in
// both directions: two people behind one office address on the same browser
// build are one visitor, and one person on wifi and then on mobile data is two.
type Audience struct {
	// Visitors is the people: a browser that opened at least one page. A
	// request for /.env and the scripts a page dragged in are not opening one.
	// A record with no user agent at all — Apache's common log keeps none — is
	// keyed by address alone and counts on the same terms, because reading
	// every such site as having no visitors would be the wrong answer.
	Visitors int `json:"visitors"`
	// Bots is how many distinct addresses sent an agent that says it is a
	// program (IsBot), however many requests each made.
	Bots int `json:"bots"`
	// HumanRequests and BotRequests split the window's requests by who sent
	// them. HumanRequests are the ones a browser sent — an agent that starts
	// "Mozilla/" and is not a bot, or none at all — whether or not that browser
	// ever opened a page: one that fetched only a script is still a person's,
	// and leaving its requests out would understate their share. BotRequests
	// are the ones from agents IsBot names. The two need not add up to the
	// window: a program that neither looks like a browser nor says what it is
	// ("Dalvik/2.1.0") is in neither.
	HumanRequests int `json:"humanRequests"`
	BotRequests   int `json:"botRequests"`
	// Devices, Browsers and Systems count visitors, not requests: a person who
	// clicked through forty pages is one phone, and counted by request the
	// table would name whichever device browses the most. Errors is the
	// visitors who met at least one 5xx. Browsers are the same families the
	// Agents filter takes, so a row narrows to its own visitors.
	Devices  []Facet `json:"devices"`
	Browsers []Facet `json:"browsers"`
	Systems  []Facet `json:"systems"`
	// Top is the most active visitors: most pages opened first, then most
	// requests, then the most recent.
	Top []Visitor `json:"top"`
}

// Visitor is one address and browser, with what it did in the window.
type Visitor struct {
	Client string `json:"client"`
	// Agent is the whole user agent. It is not parsed into a browser, a system
	// and a device here: the page already does that for every other agent it
	// draws, and a second reading of the same string would disagree with it.
	Agent    string `json:"agent,omitempty"`
	Requests int    `json:"requests"`
	Pages    int    `json:"pages"`
	// Errors and Refused are the 5xx and the 4xx answers they met: the first
	// is the site's fault and the second usually theirs, and the difference is
	// whether to go looking.
	Errors  int   `json:"errors"`
	Refused int   `json:"refused"`
	Bytes   int64 `json:"bytes"`
	// First and Last are the instants of their first and latest request in the
	// window, RFC 3339 in UTC.
	First string `json:"first"`
	Last  string `json:"last"`
	// Landing is the first page they opened, and Referer the outside site the
	// first request that named one came from. Either is absent when the record
	// does not say.
	Landing string `json:"landing,omitempty"`
	Referer string `json:"referer,omitempty"`
	// Journey is the distinct pages they opened in the order they first opened
	// them, the first few. A reader wants to see where a person went; a
	// session that wandered a hundred pages is not a longer answer to that.
	Journey []string `json:"journey"`
}

const (
	// topVisitors is how many visitors the reading names.
	topVisitors = 12
	// journeyKept bounds the pages remembered per visitor, so a crawler-like
	// browser that opens every page of a large site costs the same as one that
	// opened three.
	journeyKept = 6
	// minuteVisitorsCap bounds the visitors one minute remembers for the
	// chart, as the 4096 samples a minute keeps for latency bound that.
	minuteVisitorsCap = 4096
)

// visitorKey is a struct rather than "ip|agent" so that looking one up costs no
// allocation: both strings are the ones the entry already holds, interned by
// the Store, and a concatenation per request would be a new string for every
// line of the record.
type visitorKey struct{ ip, agent string }

// visitor is what the pass remembers about one key. It is created by a
// browser's first request of any kind, not by its first page, so that a
// visitor's request and byte counts include the files it fetched before the
// page that made it one.
type visitor struct {
	// id is the small integer the per-minute sets hold in place of the key.
	id              uint32
	requests, pages int
	errors, refused int
	bytes           int64
	first, last     int64
	referer         string
	journey         []string
}

// audienceTally is the audience as the pass has seen it so far.
type audienceTally struct {
	visitors map[visitorKey]*visitor
	// browsing are the held browsers that have not opened a page yet: the
	// ones a page view may take the place of once the bound is reached.
	browsing                   map[visitorKey]struct{}
	bots                       map[string]struct{}
	humanRequests, botRequests int
	nextID                     uint32
}

func newAudienceTally() audienceTally {
	return audienceTally{
		visitors: map[visitorKey]*visitor{},
		browsing: map[visitorKey]struct{}{},
		bots:     map[string]struct{}{},
	}
}

// feedAudience folds one request that already passed the filter into the
// audience, so the people answer the same question every other figure does.
// page says it was a page view and not a probe, and source is the outside site
// that sent it, if any.
func (c *Collector) feedAudience(e Entry, page, failed, refused bool, source string, minute *minuteBucket) {
	agent := e.UserAgent
	switch {
	case IsBot(agent):
		c.audience.botRequests++
		if _, seen := c.audience.bots[e.RemoteIP]; !seen && e.RemoteIP != "" {
			if len(c.audience.bots) >= facetCap {
				c.truncated = true
			} else {
				c.audience.bots[e.RemoteIP] = struct{}{}
			}
		}
		return
	case agent != "" && !strings.HasPrefix(agent, "Mozilla/"):
		return
	}
	c.audience.humanRequests++

	key := visitorKey{ip: e.RemoteIP, agent: agent}
	v := c.audience.visitors[key]
	if v == nil {
		// Past the bound a browser that has opened nothing is not admitted, and
		// a page view takes the place of one that has opened nothing — a
		// hotlinked image or a script dressed as a browser — so the slots go to
		// people. With none of those left, the reading is a floor, and says so.
		if len(c.audience.visitors) >= facetCap {
			if !page || !c.audience.evictBrowsing() {
				c.truncated = true
				return
			}
		}
		v = &visitor{id: c.audience.nextID, first: e.at, last: e.at}
		c.audience.nextID++
		c.audience.visitors[key] = v
		c.audience.browsing[key] = struct{}{}
	}
	v.requests++
	v.bytes += e.Size
	v.errors += boolInt(failed)
	v.refused += boolInt(refused)
	if e.at < v.first {
		v.first = e.at
	}
	if e.at > v.last {
		v.last = e.at
	}
	if v.referer == "" {
		v.referer = source
	}
	if !page {
		return
	}
	if v.pages == 0 {
		delete(c.audience.browsing, key)
	}
	v.pages++
	v.open(e.Path)
	c.seen(minute, v.id)
}

// evictBrowsing lets go of one held browser that has opened no page, so a
// person can have its place. Which one does not matter: none of them is
// counted, and none is in a minute's set.
func (a *audienceTally) evictBrowsing() bool {
	for key := range a.browsing {
		delete(a.browsing, key)
		delete(a.visitors, key)
		return true
	}
	return false
}

// open adds a page to the journey if it is new to it. The record is read oldest
// first, so the order they arrive in is the order they were opened in.
func (v *visitor) open(path string) {
	if len(v.journey) >= journeyKept {
		return
	}
	for _, opened := range v.journey {
		if opened == path {
			return
		}
	}
	v.journey = append(v.journey, path)
}

// seen records that a visitor opened a page in this minute. Only the minutes a
// visitor opened a page in are kept, so a column of the chart counts people who
// were looking at the site then and not a browser still fetching a script.
func (c *Collector) seen(minute *minuteBucket, id uint32) {
	if _, ok := minute.visitors[id]; ok {
		return
	}
	if len(minute.visitors) >= minuteVisitorsCap {
		c.truncated = true
		return
	}
	if minute.visitors == nil {
		minute.visitors = map[uint32]struct{}{}
	}
	minute.visitors[id] = struct{}{}
}

// reading is the audience as the page draws it.
func (t *audienceTally) reading() Audience {
	type person struct {
		key visitorKey
		*visitor
	}
	out := Audience{
		Bots: len(t.bots), HumanRequests: t.humanRequests, BotRequests: t.botRequests,
		Top: []Visitor{},
	}
	devices, browsers, systems := map[string]*Facet{}, map[string]*Facet{}, map[string]*Facet{}
	people := make([]person, 0, len(t.visitors))
	for key, v := range t.visitors {
		// A browser that never opened a page is held so its counts carry
		// forward if it does, and is not anybody yet.
		if v.pages == 0 {
			continue
		}
		people = append(people, person{key, v})
		failed := v.errors > 0
		tally(devices, deviceOf(key.agent), failed)
		tally(browsers, AgentFamily(key.agent), failed)
		tally(systems, systemOf(key.agent), failed)
	}
	out.Visitors = len(people)
	out.Devices, out.Browsers, out.Systems = rank(devices, 8), rank(browsers, 8), rank(systems, 8)

	sort.Slice(people, func(i, j int) bool {
		a, b := people[i], people[j]
		switch {
		case a.pages != b.pages:
			return a.pages > b.pages
		case a.requests != b.requests:
			return a.requests > b.requests
		case a.last != b.last:
			return a.last > b.last
		case a.key.ip != b.key.ip:
			return a.key.ip < b.key.ip
		}
		return a.key.agent < b.key.agent
	})
	if len(people) > topVisitors {
		people = people[:topVisitors]
	}
	for _, p := range people {
		v := Visitor{
			Client: p.key.ip, Agent: p.key.agent,
			Requests: p.requests, Pages: p.pages, Errors: p.errors, Refused: p.refused, Bytes: p.bytes,
			First:   time.Unix(0, p.first).UTC().Format(time.RFC3339Nano),
			Last:    time.Unix(0, p.last).UTC().Format(time.RFC3339Nano),
			Referer: p.referer,
			Journey: append([]string{}, p.journey...),
		}
		// The first page opened is always the journey's first entry; the bound
		// only ever drops the later ones.
		if len(p.journey) > 0 {
			v.Landing = p.journey[0]
		}
		out.Top = append(out.Top, v)
	}
	return out
}

// tally counts one visitor under a value. A value the agent does not give —
// the system of a record with no user agent — is skipped rather than shown as
// a row named "", which reads as a finding.
func tally(into map[string]*Facet, value string, failed bool) {
	if value == "" {
		return
	}
	facet := into[value]
	if facet == nil {
		facet = &Facet{Value: value}
		into[value] = facet
	}
	facet.Count++
	facet.Errors += boolInt(failed)
}

// deviceOf reads the kind of device from a browser's user agent, by the same
// rules as the page's own `parseAgent` so the two never disagree about a
// visitor. An Android agent without "Mobi" is a tablet, because Chrome leaves
// that token out of the tablet build; an iPad says "Mobile" like a phone and is
// told apart first. An agent that is not a browser has no device to name.
func deviceOf(agent string) string {
	if !strings.HasPrefix(agent, "Mozilla/") {
		return ""
	}
	switch {
	case hasAny(agent, "iPad", "Tablet") || (strings.Contains(agent, "Android") && !strings.Contains(agent, "Mobi")):
		return "tablet"
	case hasAny(agent, "Mobi", "iPhone", "iPod"):
		return "phone"
	}
	return "desktop"
}

// operatingSystems is the page's `SYSTEMS` table, in its order. Order matters because
// the names overlap: an iPhone's agent says "like Mac OS X", an Android's says
// "Linux", and a Chromebook's says "X11", so the specific system is tried
// before the one it imitates.
var operatingSystems = []struct {
	name  string
	marks []string
}{
	{"Windows", []string{"Windows"}},
	{"iPadOS", []string{"iPad"}},
	{"iOS", []string{"iPhone", "iPod"}},
	{"Android", []string{"Android"}},
	{"ChromeOS", []string{"CrOS"}},
	{"macOS", []string{"Mac OS X", "Macintosh"}},
	{"Ubuntu", []string{"Ubuntu"}},
	{"Fedora", []string{"Fedora"}},
	{"Linux", []string{"Linux", "X11"}},
}

// systemOf names the operating system a user agent says it runs on, or "" when
// it says nothing the table knows.
func systemOf(agent string) string {
	for _, system := range operatingSystems {
		if hasAny(agent, system.marks...) {
			return system.name
		}
	}
	return ""
}

func hasAny(s string, marks ...string) bool {
	for _, mark := range marks {
		if strings.Contains(s, mark) {
			return true
		}
	}
	return false
}
