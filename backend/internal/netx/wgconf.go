package netx

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// A WireGuard interface here is a wg-quick file in Paths.WireGuard, because
// that is what `systemctl enable wg-quick@wg0` reads at boot and what an
// operator who has run WireGuard before expects to find and edit. The
// dashboard's own bookkeeping lives in comments inside that file, so the file
// stays valid input for wg-quick, a hand edit does not lose it, and a
// reinstalled dashboard finds its peers' names without a database.
//
// Which files the dashboard may change is decided by the first line.
// `# Managed by Just Dashboard` marks a file the dashboard created; only
// those are edited from the UI. A tunnel the operator wrote by hand is shown
// (its peers, its keys' public halves, its traffic) and never touched: the
// file is theirs, its comments and ordering are intentional, and a page that
// rewrote it on a click would be a page nobody lets near /etc/wireguard.
const wgManagedMarker = "# Managed by Just Dashboard"

// wgLine is one line of a file, kept verbatim. A line that is a `Key = Value`
// carries the key (lower case, as wg-quick compares it) and the value with
// any trailing comment removed; every other line — comments, blanks,
// anything unrecognisable — has an empty key and round-trips untouched.
type wgLine struct {
	raw string
	key string
	val string
}

// wgSection is a [Interface] or [Peer] block. lead is the run of comment and
// blank lines directly above its header: for a peer, that is where its
// `# jd:` metadata lives, and keeping it with the section is what lets the
// peer be removed whole.
type wgSection struct {
	name   string
	lead   []wgLine
	header string
	body   []wgLine
}

// wgConf is a parsed file. Nothing is normalised on the way in: render of an
// unedited parse is the original text, so a file the dashboard only reads and
// a file it edits differ only in the lines the edit touched.
type wgConf struct {
	managed  bool
	preamble []wgLine
	sections []*wgSection
}

func classifyWGLine(raw string) wgLine {
	l := wgLine{raw: raw}
	t := strings.TrimSpace(raw)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return l
	}
	k, v, ok := strings.Cut(t, "=")
	if !ok {
		return l
	}
	// wg-quick and wg both strip a trailing "# comment" from a value.
	if i := strings.Index(v, "#"); i >= 0 {
		v = v[:i]
	}
	l.key = strings.ToLower(strings.TrimSpace(k))
	l.val = strings.TrimSpace(v)
	return l
}

// isFiller is a line that carries no setting: blank or a comment.
func (l wgLine) isFiller() bool {
	return l.key == "" && !strings.HasPrefix(strings.TrimSpace(l.raw), "[")
}

func wgSectionHeader(raw string) (string, bool) {
	t := strings.TrimSpace(raw)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	end := strings.Index(t, "]")
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(t[1:end]), true
}

// parseWGConf reads a file. It is lenient on purpose: a hand-written file is
// shown as far as it can be understood and never refused for a line this code
// has no opinion about.
func parseWGConf(text string) *wgConf {
	c := &wgConf{managed: strings.HasPrefix(text, wgManagedMarker)}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" && !c.managed {
		return c
	}
	var cur *wgSection
	for _, raw := range strings.Split(text, "\n") {
		if name, ok := wgSectionHeader(raw); ok {
			// The comments and blanks that ended the previous block belong to
			// this one: they are the metadata written above its header.
			var tail []wgLine
			if cur != nil {
				cur.body, tail = wgSplitTrailingFiller(cur.body)
			} else {
				c.preamble, tail = wgSplitTrailingFiller(c.preamble)
			}
			cur = &wgSection{name: name, lead: tail, header: raw}
			c.sections = append(c.sections, cur)
			continue
		}
		l := classifyWGLine(raw)
		if cur == nil {
			c.preamble = append(c.preamble, l)
		} else {
			cur.body = append(cur.body, l)
		}
	}
	return c
}

func wgSplitTrailingFiller(lines []wgLine) (kept, tail []wgLine) {
	i := len(lines)
	for i > 0 && lines[i-1].isFiller() {
		i--
	}
	return lines[:i], append([]wgLine(nil), lines[i:]...)
}

// render is the file's text. Deterministic: the same parse renders the same
// bytes, and a file ends with exactly one newline.
func (c *wgConf) render() string {
	var b strings.Builder
	for _, l := range c.preamble {
		b.WriteString(l.raw)
		b.WriteByte('\n')
	}
	for _, s := range c.sections {
		for _, l := range s.lead {
			b.WriteString(l.raw)
			b.WriteByte('\n')
		}
		b.WriteString(s.header)
		b.WriteByte('\n')
		for _, l := range s.body {
			b.WriteString(l.raw)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// iface is the [Interface] block, nil in a file that has none.
func (c *wgConf) iface() *wgSection {
	for _, s := range c.sections {
		if strings.EqualFold(s.name, "Interface") {
			return s
		}
	}
	return nil
}

// peers are the [Peer] blocks in file order.
func (c *wgConf) peers() []*wgSection {
	var out []*wgSection
	for _, s := range c.sections {
		if strings.EqualFold(s.name, "Peer") {
			out = append(out, s)
		}
	}
	return out
}

// get is the first value of a key, empty when absent.
func (s *wgSection) get(key string) string {
	for _, l := range s.body {
		if l.key == key {
			return l.val
		}
	}
	return ""
}

// list is every value of a key across repeated lines, split at commas — the
// form Address, DNS and AllowedIPs take, where one line or several mean the
// same.
func (s *wgSection) list(key string) []string {
	var out []string
	for _, l := range s.body {
		if l.key != key {
			continue
		}
		for _, part := range strings.Split(l.val, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func wgKVLine(key, value string) wgLine {
	return wgLine{raw: key + " = " + value, key: strings.ToLower(key), val: value}
}

// set replaces a key's first line, or adds one after the last setting when
// the key is absent. Other lines of the same key are left alone: PostUp and
// friends legitimately repeat, and which of them an edit means is not ours to
// guess.
func (s *wgSection) set(key, value string) {
	lk := strings.ToLower(key)
	for i, l := range s.body {
		if l.key == lk {
			s.body[i] = wgKVLine(key, value)
			return
		}
	}
	s.add(key, value)
}

// remove drops every line of a key.
func (s *wgSection) remove(key string) {
	kept := s.body[:0]
	for _, l := range s.body {
		if l.key != key {
			kept = append(kept, l)
		}
	}
	s.body = kept
}

// add appends a setting after the section's last setting, ahead of any
// comments that trail it, so a note written at the bottom of a block stays at
// the bottom.
func (s *wgSection) add(key, value string) {
	at := len(s.body)
	for at > 0 && s.body[at-1].isFiller() {
		at--
	}
	s.body = append(s.body, wgLine{})
	copy(s.body[at+1:], s.body[at:])
	s.body[at] = wgKVLine(key, value)
}

// wgMetaPrefix opens every comment the dashboard keeps its own notes in.
const wgMetaPrefix = "jd:"

// wgMetaOf reads the `# jd:key=value` comments of a run of lines.
func wgMetaOf(lines []wgLine) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		t := strings.TrimSpace(l.raw)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimSpace(strings.TrimPrefix(t, "#"))
		if !strings.HasPrefix(t, wgMetaPrefix) {
			continue
		}
		if k, v, ok := strings.Cut(strings.TrimPrefix(t, wgMetaPrefix), "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func wgMetaLine(key, value string) wgLine {
	return wgLine{raw: "# " + wgMetaPrefix + key + "=" + value}
}

// bodyMeta is an [Interface]'s notes. They sit at the top of the block, never
// the bottom: a comment trailing the last setting of a block reads, on the
// next parse, as belonging to the block below it.
func (s *wgSection) bodyMeta() map[string]string { return wgMetaOf(s.body) }

func (s *wgSection) setBodyMeta(key, value string) {
	prefix := "# " + wgMetaPrefix + key + "="
	for i, l := range s.body {
		if strings.HasPrefix(strings.TrimSpace(l.raw), prefix) {
			s.body[i] = wgMetaLine(key, value)
			return
		}
	}
	s.body = append([]wgLine{wgMetaLine(key, value)}, s.body...)
}

// wgPeerConf is one [Peer] block read for what the dashboard shows.
type wgPeerConf struct {
	sec        *wgSection
	id         int
	name       string
	kind       string
	created    time.Time
	publicKey  string
	hasPSK     bool
	allowedIPs []string
	endpoint   string
	keepalive  int
}

func wgPeerOf(s *wgSection) wgPeerConf {
	m := wgMetaOf(s.lead)
	p := wgPeerConf{
		sec:        s,
		name:       m["name"],
		kind:       m["kind"],
		publicKey:  s.get("publickey"),
		hasPSK:     s.get("presharedkey") != "",
		allowedIPs: s.list("allowedips"),
		endpoint:   s.get("endpoint"),
	}
	p.id, _ = strconv.Atoi(m["id"])
	p.created, _ = time.Parse(time.RFC3339, m["created"])
	p.keepalive, _ = strconv.Atoi(s.get("persistentkeepalive"))
	return p
}

// wgNewPeerSection is a block as the dashboard writes it: the notes above, the
// settings below. The leading blank line keeps blocks apart.
func wgNewPeerSection(id int, name, kind string, created time.Time, settings [][2]string) *wgSection {
	s := &wgSection{
		name: "Peer",
		lead: []wgLine{
			{raw: ""},
			wgMetaLine("id", strconv.Itoa(id)),
			wgMetaLine("name", name),
			wgMetaLine("kind", kind),
			wgMetaLine("created", created.UTC().Format(time.RFC3339)),
		},
		header: "[Peer]",
	}
	for _, kv := range settings {
		s.body = append(s.body, wgKVLine(kv[0], kv[1]))
	}
	return s
}

// appendPeer adds a block at the end, dropping blank lines that trailed the
// last block so the file does not grow a gap with every peer.
func (c *wgConf) appendPeer(s *wgSection) {
	if n := len(c.sections); n > 0 {
		last := c.sections[n-1]
		for len(last.body) > 0 && strings.TrimSpace(last.body[len(last.body)-1].raw) == "" {
			last.body = last.body[:len(last.body)-1]
		}
	}
	c.sections = append(c.sections, s)
}

// removePeer drops a block, with the notes above it.
func (c *wgConf) removePeer(s *wgSection) {
	for i, x := range c.sections {
		if x == s {
			c.sections = append(c.sections[:i], c.sections[i+1:]...)
			return
		}
	}
}

// wgConfPath is where an interface's file lives. The name is checked here as
// well as at the routes, because this is the one place it becomes a path.
func (s *Service) wgConfPath(name string) (string, error) {
	if err := validWGName(name); err != nil {
		return "", err
	}
	return s.paths.WireGuard + "/" + name + ".conf", nil
}

// readWGConf loads one interface's file. A missing file is ErrNotFound.
func (s *Service) readWGConf(name string) (*wgConf, error) {
	path, err := s.wgConfPath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("WireGuard interface %s: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return parseWGConf(string(b)), nil
}

// writeWGConf writes a file 0600 through a rename. The private key is in it.
func (s *Service) writeWGConf(name string, c *wgConf) error {
	path, err := s.wgConfPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.paths.WireGuard, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(c.render()), 0o600)
}
