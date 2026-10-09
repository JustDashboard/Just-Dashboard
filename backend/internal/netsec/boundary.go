package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The dashboard is reached through a handful of boundaries, each held by a
// different owner, and any network change can cut one of them without
// touching the dashboard itself: Caddy's listener on its bind address, the
// pre-auth allowlist in front of authentication, the tailnet the allowlist
// usually names, the SSH listener an `ssh -L` tunnel rides on, and the
// tailnet-only preview ports. AccessBoundary reads them as they are; a
// proposal is judged against them before it is applied, and a pending change
// compares them before and after so confirmation can say which still hold.

// Preview ports are tailscaled's to serve on the dashboard's behalf
// (selfcfg.TailnetPortMin..Max), each to a loopback upstream and never
// funnelled.
const (
	previewPortMin = 21000
	previewPortMax = 21999
	// tailscaledPort is the UDP port tailscaled listens on for direct
	// WireGuard connections; refused, peers fall back to DERP relays.
	tailscaledPort = "41641"
)

// BoundaryListener is one socket the boundary is made of.
type BoundaryListener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint32 `json:"port"`
	Process  string `json:"process,omitempty"`
	// Caddy marks the dashboard's own proxy; Dashboard one of its other
	// sockets (the backend, the web app), which must stay on loopback.
	Caddy     bool `json:"caddy,omitempty"`
	Dashboard bool `json:"dashboard,omitempty"`
}

// PreviewServe is one tailnet serve mapping in the preview range. Upstream
// is the loopback port a plain, unfunnelled proxy mapping points at, and 0
// for anything else (selfcfg.ServeEntry.LoopbackUpstream).
type PreviewServe struct {
	Port     int `json:"port"`
	Upstream int `json:"upstream,omitempty"`
}

// BoundaryInput is everything the boundary is read from, gathered by the API
// layer: the running allowlist, the configured ingress, the host's sockets,
// the tailnet and sshd. A nil or empty field is "not read", said as such.
type BoundaryInput struct {
	Allowlist []*net.IPNet
	// Client is the request's address; Operator the address of the SSH
	// session carrying it where the request arrived on loopback.
	Client, Operator string
	CaddyPort        int
	Binds            []string
	Listeners        []BoundaryListener
	ListenersRead    bool
	TailnetIP        string
	TailnetUp        bool
	SSHPorts         []string
	Previews         []PreviewServe
	PreviewsRead     bool
	Now              time.Time
}

// BoundaryCheck is one boundary's state.
type BoundaryCheck struct {
	ID string `json:"id"`
	// State is held, broken or unknown.
	State  string   `json:"state"`
	Title  string   `json:"title"`
	Detail string   `json:"detail"`
	Facts  []string `json:"facts,omitempty"`
}

// AccessBoundary is every boundary, read together.
type AccessBoundary struct {
	Checks    []BoundaryCheck `json:"checks"`
	CheckedAt time.Time       `json:"checkedAt"`
	// The parts a proposal is judged against.
	Allowlist  []string `json:"allowlist"`
	Client     string   `json:"client,omitempty"`
	Operator   string   `json:"operator,omitempty"`
	CaddyPort  int      `json:"caddyPort"`
	SSHPorts   []string `json:"sshPorts"`
	TailnetIP  string   `json:"tailnetIp,omitempty"`
	PreviewMin int      `json:"previewMin"`
	PreviewMax int      `json:"previewMax"`
}

// Boundary state words.
const (
	BoundaryHeld    = "held"
	BoundaryBroken  = "broken"
	BoundaryUnknown = "unknown"
)

// DescribeBoundary reads every boundary. Pure, so each state is pinned.
func DescribeBoundary(in BoundaryInput) AccessBoundary {
	b := AccessBoundary{CheckedAt: in.Now.UTC(), Allowlist: []string{}, Client: in.Client, Operator: in.Operator,
		CaddyPort: in.CaddyPort, SSHPorts: in.SSHPorts, TailnetIP: in.TailnetIP, PreviewMin: previewPortMin, PreviewMax: previewPortMax}
	if b.SSHPorts == nil {
		b.SSHPorts = []string{}
	}
	for _, n := range in.Allowlist {
		b.Allowlist = append(b.Allowlist, n.String())
	}
	b.Checks = []BoundaryCheck{ingressCheck(in), allowlistCheck(in), tailnetCheck(in), sshCheck(in), previewCheck(in)}
	return b
}

func ingressCheck(in BoundaryInput) BoundaryCheck {
	c := BoundaryCheck{ID: "ingress", Title: "Caddy is the only routable listener"}
	if !in.ListenersRead {
		c.State, c.Detail = BoundaryUnknown, "The host's listening sockets could not be read."
		return c
	}
	var caddy, strays []string
	for _, l := range in.Listeners {
		switch {
		case l.Caddy && int(l.Port) == in.CaddyPort:
			caddy = append(caddy, net.JoinHostPort(l.Address, strconv.Itoa(int(l.Port))))
		case l.Dashboard && !loopbackAddress(l.Address):
			strays = append(strays, fmt.Sprintf("%s on %s", processOr(l.Process), net.JoinHostPort(l.Address, strconv.Itoa(int(l.Port)))))
		}
	}
	sort.Strings(caddy)
	c.Facts = append(c.Facts, "binds "+strings.Join(in.Binds, ", "))
	switch {
	case len(strays) > 0:
		c.State = BoundaryBroken
		c.Detail = "A dashboard socket other than Caddy answers on a routable address: " + strings.Join(strays, "; ") + "."
	case len(caddy) == 0:
		c.State = BoundaryBroken
		c.Detail = fmt.Sprintf("Nothing that is Caddy listens on port %d, so the dashboard has no way in but whatever else holds that port.", in.CaddyPort)
	default:
		c.State = BoundaryHeld
		c.Detail = fmt.Sprintf("Caddy holds port %d on %s; the backend and the web app answer on loopback alone.", in.CaddyPort, strings.Join(caddy, ", "))
	}
	return c
}

func allowlistCheck(in BoundaryInput) BoundaryCheck {
	c := BoundaryCheck{ID: "allowlist", Title: "The allowlist admits this session before sign-in"}
	if len(in.Allowlist) == 0 {
		c.State, c.Detail = BoundaryBroken, "The running allowlist is empty."
		return c
	}
	for _, n := range in.Allowlist {
		c.Facts = append(c.Facts, n.String())
	}
	client := net.ParseIP(in.Client)
	switch {
	case client == nil:
		c.State, c.Detail = BoundaryUnknown, "This request's address could not be read."
	case !ipnetsContain(in.Allowlist, client):
		// The request got here, so something in front rewrote its address;
		// saying "held" would be a guess.
		c.State, c.Detail = BoundaryUnknown, "This request's address "+in.Client+" is outside the allowlist it passed, so a proxy in front rewrote it."
	case client.IsLoopback() && in.Operator != "" && in.Operator != in.Client:
		c.State, c.Detail = BoundaryHeld, "This session arrives on loopback through an SSH tunnel from "+in.Operator+"."
	default:
		c.State, c.Detail = BoundaryHeld, in.Client+" is inside the allowlist that runs before authentication."
	}
	return c
}

func tailnetCheck(in BoundaryInput) BoundaryCheck {
	c := BoundaryCheck{ID: "tailnet", Title: "The tailnet path is up"}
	listed := false
	for _, n := range in.Allowlist {
		if tailscaleNet.Contains(n.IP) || n.Contains(net.ParseIP("100.64.0.1")) {
			listed = true
		}
	}
	switch {
	case in.TailnetIP == "" && !listed:
		c.State, c.Detail = BoundaryHeld, "This dashboard is not reached over a tailnet; nothing here depends on one."
	case !in.TailnetUp || in.TailnetIP == "":
		c.State, c.Detail = BoundaryBroken, "The allowlist admits the tailnet and no tailscale interface is up on this host."
	default:
		c.State = BoundaryHeld
		c.Detail = "tailscale0 is up with " + in.TailnetIP + "."
		c.Facts = append(c.Facts, "UDP "+tailscaledPort+" for direct connections")
	}
	return c
}

func sshCheck(in BoundaryInput) BoundaryCheck {
	c := BoundaryCheck{ID: "ssh", Title: "SSH answers for a tunnel"}
	if len(in.SSHPorts) == 0 {
		c.State, c.Detail = BoundaryUnknown, "sshd's ports could not be read."
		return c
	}
	c.Facts = append(c.Facts, "ports "+strings.Join(in.SSHPorts, ", "))
	if !in.ListenersRead {
		c.State, c.Detail = BoundaryUnknown, "sshd is configured for "+strings.Join(in.SSHPorts, ", ")+"; the listening sockets could not be read."
		return c
	}
	for _, port := range in.SSHPorts {
		for _, l := range in.Listeners {
			if l.Protocol == "tcp" && strconv.Itoa(int(l.Port)) == port {
				c.State, c.Detail = BoundaryHeld, "Port "+port+" is listening, so an SSH tunnel to the dashboard's loopback address still works."
				return c
			}
		}
	}
	c.State, c.Detail = BoundaryBroken, "No socket listens on sshd's port "+strings.Join(in.SSHPorts, ", ")+"."
	return c
}

func previewCheck(in BoundaryInput) BoundaryCheck {
	c := BoundaryCheck{ID: "previews", Title: "Previews stay tailnet-only"}
	if !in.PreviewsRead {
		c.State, c.Detail = BoundaryUnknown, "tailscaled's serve configuration could not be read."
		return c
	}
	var bad []string
	plain := 0
	for _, p := range in.Previews {
		if p.Port < previewPortMin || p.Port > previewPortMax {
			continue
		}
		if p.Upstream == 0 {
			bad = append(bad, fmt.Sprintf("%d is funnelled or serves something other than a loopback port", p.Port))
			continue
		}
		plain++
	}
	if len(bad) > 0 {
		c.State, c.Detail = BoundaryBroken, "Preview ports outside the boundary: "+strings.Join(bad, "; ")+"."
		return c
	}
	c.State = BoundaryHeld
	c.Detail = fmt.Sprintf("%d preview %s served on the tailnet, each to a loopback port and none funnelled.", plain, map[bool]string{true: "port", false: "ports"}[plain == 1])
	return c
}

func loopbackAddress(addr string) bool {
	ip := net.ParseIP(strings.Trim(addr, "[]"))
	return ip != nil && ip.IsLoopback()
}

func processOr(p string) string {
	if p == "" {
		return "a process"
	}
	return p
}

func ipnetsContain(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// BoundaryProposal is a change about to be made, in the terms the boundary is
// judged in.
type BoundaryProposal struct {
	// Kind is ban, firewall.rule, firewall.policy or ssh.
	Kind string `json:"kind"`
	// Target is the address or range a ban or a rule refuses.
	Target string `json:"target,omitempty"`
	// Action is deny, reject, allow or limit; Port and Protocol what a rule
	// names ("" for every port).
	Action   string `json:"action,omitempty"`
	Port     string `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	// Policy is the inbound default a firewall.policy proposal sets.
	Policy string `json:"policy,omitempty"`
	// Settings are an SSH change's directives.
	Settings map[string]string `json:"settings,omitempty"`
}

// BoundaryImpact is what a proposal does to one boundary. A cut certainly
// removes the requester's way in and is refused; an effect touches the
// boundary for somebody else and needs an explicit acknowledgement.
type BoundaryImpact struct {
	Boundary string `json:"boundary"`
	Level    string `json:"level"`
	Text     string `json:"text"`
}

// Impact levels.
const (
	ImpactCuts    = "cuts"
	ImpactAffects = "affects"
)

// BoundaryImpacts judges a proposal against the boundary. Pure.
func BoundaryImpacts(b AccessBoundary, p BoundaryProposal) []BoundaryImpact {
	out := []BoundaryImpact{}
	switch p.Kind {
	case "ban":
		out = append(out, refusedSourceImpacts(b, p.Target, "", "a ban")...)
	case "firewall.rule":
		action := strings.ToLower(p.Action)
		if action != "deny" && action != "reject" && action != "drop" {
			break
		}
		out = append(out, refusedSourceImpacts(b, p.Target, p.Port, "this rule")...)
		if refusesEverywhere(p.Target) && ruleCoversPort(p.Port, tailscaledPort) && strings.ToLower(p.Protocol) != "tcp" {
			out = append(out, BoundaryImpact{Boundary: "tailnet", Level: ImpactAffects,
				Text: "Refusing UDP " + tailscaledPort + " from everywhere sends every tailnet peer through a DERP relay: slower, not cut."})
		}
	case "firewall.policy":
		if strings.ToLower(p.Policy) == "deny" || strings.ToLower(p.Policy) == "reject" {
			out = append(out, BoundaryImpact{Boundary: "ingress", Level: ImpactAffects,
				Text: fmt.Sprintf("An inbound default of %s leaves port %d and sshd's %s reachable only where a rule admits them; check the rules first.",
					strings.ToLower(p.Policy), b.CaddyPort, strings.Join(b.SSHPorts, ", "))})
		}
	case "ssh":
		out = append(out, sshImpacts(b, p.Settings)...)
	}
	return out
}

// refusedSourceImpacts judges refusing target (an address or a range) on port
// ("" for every port) against the allowlist, this session and the tunnel.
func refusedSourceImpacts(b AccessBoundary, target, port, what string) []BoundaryImpact {
	var out []BoundaryImpact
	refused, ok := blockPrefix(target)
	everywhere := refusesEverywhere(target)
	if !ok && !everywhere {
		return out
	}
	hits := func(addr string) bool {
		if everywhere {
			return true
		}
		a, err := netip.ParseAddr(addr)
		return err == nil && refused.Contains(a.Unmap())
	}
	dashboardPort := ruleCoversPort(port, strconv.Itoa(b.CaddyPort))
	sshPort := false
	for _, sp := range b.SSHPorts {
		if ruleCoversPort(port, sp) {
			sshPort = true
		}
	}
	for _, who := range []string{b.Client, b.Operator} {
		if who == "" || !hits(who) {
			continue
		}
		if a, err := netip.ParseAddr(who); err == nil && a.IsLoopback() {
			continue
		}
		if dashboardPort || sshPort {
			out = append(out, BoundaryImpact{Boundary: "allowlist", Level: ImpactCuts,
				Text: fmt.Sprintf("%s covers %s, the address this session arrives from: %s would end it.", targetWords(target), who, what)})
			return out
		}
	}
	if dashboardPort || sshPort {
		out = append(out, allowlistOverlaps(b, refused, everywhere, target, what, dashboardPort)...)
	}
	if b.TailnetIP != "" && (everywhere || refused.Overlaps(netip.MustParsePrefix("100.64.0.0/10"))) && ruleCoversPreviews(port) {
		out = append(out, BoundaryImpact{Boundary: "previews", Level: ImpactAffects,
			Text: fmt.Sprintf("%s overlaps the tailnet that previews are served to on ports %d–%d.", targetWords(target), previewPortMin, previewPortMax)})
	}
	return out
}

func allowlistOverlaps(b AccessBoundary, refused netip.Prefix, everywhere bool, target, what string, dashboardPort bool) []BoundaryImpact {
	var out []BoundaryImpact
	for _, entry := range b.Allowlist {
		allowed, err := netip.ParsePrefix(entry)
		// Loopback is the tunnel's, judged above; an allowlist admitting
		// every address is no boundary for a ban to cross.
		if err != nil || allowed.Addr().IsLoopback() || allowed.Bits() == 0 {
			continue
		}
		if everywhere || allowed.Overlaps(refused) {
			where := "the dashboard's port"
			if !dashboardPort {
				where = "SSH, the tunnel's way in"
			}
			out = append(out, BoundaryImpact{Boundary: "allowlist", Level: ImpactAffects,
				Text: fmt.Sprintf("%s overlaps %s, which the allowlist admits: %s refuses %s to anyone else arriving from it.", targetWords(target), entry, what, where)})
		}
	}
	return out
}

func sshImpacts(b AccessBoundary, settings map[string]string) []BoundaryImpact {
	var out []BoundaryImpact
	tunnel := false
	if a, err := netip.ParseAddr(b.Client); err == nil && a.IsLoopback() {
		tunnel = true
	}
	if port, ok := settings["port"]; ok && !containsString(b.SSHPorts, port) {
		text := fmt.Sprintf("sshd moves from %s to %s: every SSH tunnel to the dashboard must be opened on %s, and an allowlist or firewall rule naming %s no longer applies to it.",
			strings.Join(b.SSHPorts, ", "), port, port, strings.Join(b.SSHPorts, ", "))
		if tunnel {
			text += " This session's tunnel stays up until it closes."
		}
		out = append(out, BoundaryImpact{Boundary: "ssh", Level: ImpactAffects, Text: text})
	}
	if v, ok := settings["allowtcpforwarding"]; ok && (v == "no" || v == "remote") {
		level := ImpactAffects
		if tunnel {
			level = ImpactCuts
		}
		out = append(out, BoundaryImpact{Boundary: "ssh", Level: level,
			Text: "Without local TCP forwarding nobody can open an `ssh -L` tunnel to the dashboard's loopback address."})
	}
	if v, ok := settings["allowusers"]; ok && strings.TrimSpace(v) != "" {
		out = append(out, BoundaryImpact{Boundary: "ssh", Level: ImpactAffects,
			Text: "Only " + strings.Join(strings.Fields(v), ", ") + " may then log in, so only they can open a tunnel to the dashboard."})
	}
	return out
}

func targetWords(target string) string {
	if refusesEverywhere(target) {
		return "Refusing every source"
	}
	return strings.TrimSpace(target)
}

func refusesEverywhere(target string) bool {
	t := strings.TrimSpace(strings.ToLower(target))
	return t == "" || t == "any" || t == "anywhere" || t == "0.0.0.0/0" || t == "::/0"
}

// rulePortEntries reads a rule's port ("22", "22/tcp", "80,443",
// "8000:8010"); nil is every port.
func rulePortEntries(port string) []string {
	port = strings.TrimSpace(port)
	if i := strings.IndexByte(port, '/'); i >= 0 {
		port = port[:i]
	}
	if port == "" {
		return nil
	}
	return splitPorts(port)
}

// ruleCoversPort reports whether a rule naming port reaches want.
func ruleCoversPort(port, want string) bool {
	entries := rulePortEntries(port)
	if entries == nil {
		return true
	}
	for _, entry := range entries {
		if portCovers(entry, want) {
			return true
		}
	}
	return false
}

// ruleCoversPreviews reports whether a rule naming port reaches any port in
// the preview range.
func ruleCoversPreviews(port string) bool {
	entries := rulePortEntries(port)
	if entries == nil {
		return true
	}
	for _, entry := range entries {
		lo, hi, ranged := strings.Cut(entry, ":")
		if !ranged {
			hi = lo
		}
		l, lerr := strconv.Atoi(lo)
		h, herr := strconv.Atoi(hi)
		if lerr == nil && herr == nil && l <= previewPortMax && h >= previewPortMin {
			return true
		}
	}
	return false
}

// BoundaryChange is one boundary as it was before a change and as it is now.
type BoundaryChange struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Before string `json:"before"`
	After  string `json:"after"`
	Detail string `json:"detail"`
	// Lost is a boundary that held before and does not now.
	Lost bool `json:"lost"`
}

// CompareBoundaries pairs each boundary before and after a change.
func CompareBoundaries(before, after AccessBoundary) []BoundaryChange {
	prior := map[string]BoundaryCheck{}
	for _, c := range before.Checks {
		prior[c.ID] = c
	}
	out := []BoundaryChange{}
	for _, c := range after.Checks {
		was, ok := prior[c.ID]
		if !ok {
			was = BoundaryCheck{State: BoundaryUnknown}
		}
		out = append(out, BoundaryChange{ID: c.ID, Title: c.Title, Before: was.State, After: c.State, Detail: c.Detail,
			Lost: was.State == BoundaryHeld && c.State != BoundaryHeld})
	}
	return out
}
