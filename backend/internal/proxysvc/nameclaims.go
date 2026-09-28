package proxysvc

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// NameClaim is a server block that claims a server name nginx warned about,
// at its server_name line.
//
// nginx's commonest warning names no file: `conflicting server name "a.test"
// on 0.0.0.0:80, ignored` is all it says when a second site claims a name the
// first already holds, and the second site then never answers for it. The
// warning is placed by finding every block that claims the name on that
// address, in the order nginx read them: the first holds it, and each after
// it is where nginx ignored it.
type NameClaim struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Ignored is a block read after another had claimed the name on the same
	// address, whose claim nginx set aside.
	Ignored bool `json:"ignored"`
}

// conflictingName is nginx's warning for a server name claimed twice on one
// address, as nginx 1.26 writes it; the name is lowercased, as nginx keeps it.
var conflictingName = regexp.MustCompile(`^conflicting server name "(.*)" on (\S+), ignored$`)

// PlaceNameConflicts returns res with each conflicting-server-name warning
// placed at the blocks that claim the name, read from the configuration as it
// is now; res itself when there is none to place or the configuration cannot
// be read in ctx's time. It runs `nginx -T`, cached, and so must not be called
// with s.mu held.
func (s *Service) PlaceNameConflicts(ctx context.Context, res *ValidationResult) *ValidationResult {
	if res == nil || !hasUnplacedConflict(res) {
		return res
	}
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		return res
	}
	tree, err := NginxTree(files)
	if err != nil {
		return res
	}
	// nginx warns once for each block after the first, so a name and address
	// warned about k times has k+1 claims. Any other count means a block the
	// tree cannot see — a file outside the proxy's directories, a listen on a
	// hostname — and an order that might put the wrong site first.
	warned := map[string]int{}
	for _, d := range res.Diagnostics {
		if m := conflictingName.FindStringSubmatch(d.Message); m != nil && d.File == "" {
			warned[m[1]+" "+m[2]]++
		}
	}
	placed := res.clone()
	for i, d := range placed.Diagnostics {
		m := conflictingName.FindStringSubmatch(d.Message)
		if m == nil || d.File != "" {
			continue
		}
		claims, known := nameClaims(tree, m[1], m[2])
		if !known || len(claims) != warned[m[1]+" "+m[2]]+1 {
			continue
		}
		for j := range claims {
			claims[j].File = resolvedFile(claims[j].File)
		}
		placed.Diagnostics[i].Claims = claims
	}
	return placed
}

func hasUnplacedConflict(res *ValidationResult) bool {
	for _, d := range res.Diagnostics {
		if d.File == "" && d.Claims == nil && conflictingName.MatchString(d.Message) {
			return true
		}
	}
	return false
}

// nameClaims finds the http server blocks that claim name on addr, in the
// order nginx reads them. known is false when a block that claims the name
// listens somewhere that cannot be told from the file, so no order is given.
func nameClaims(tree []Directive, name, addr string) (claims []NameClaim, known bool) {
	known = true
	var walk func([]Directive)
	walk = func(directives []Directive) {
		for _, d := range directives {
			if d.Name == "server" && d.Block != nil && len(d.Context) == 1 && d.Context[0] == "http" {
				line := claimLine(d.Block, name)
				if line == nil {
					continue
				}
				on, sure := serverAddresses(d.Block)
				if !sure {
					known = false
				}
				if on[addr] {
					claims = append(claims, NameClaim{File: line.File, Line: line.Line, Ignored: len(claims) > 0})
				}
				continue
			}
			walk(d.Block)
		}
	}
	walk(tree)
	return claims, known
}

// claimLine is the server_name directive in a server block that names name.
func claimLine(block []Directive, name string) *Directive {
	for i, d := range block {
		if d.Name != "server_name" {
			continue
		}
		for _, arg := range d.Args {
			if strings.ToLower(arg) == name {
				return &block[i]
			}
		}
	}
	return nil
}

// serverAddresses is every address a server block listens on, as nginx names
// one in a warning. A block with no listen is on *:80, or *:8000 for an
// nginx without root; both are given, since only the warning says which.
// sure is false when a listen names a host, which nginx resolves and this
// cannot.
func serverAddresses(block []Directive) (map[string]bool, bool) {
	on, sure, listens := map[string]bool{}, true, 0
	for _, d := range block {
		if d.Name != "listen" || len(d.Args) == 0 {
			continue
		}
		listens++
		if addr, ok := listenAddress(d.Args[0]); ok {
			on[addr] = true
		} else {
			sure = false
		}
	}
	if listens == 0 {
		on["0.0.0.0:80"], on["0.0.0.0:8000"] = true, true
	}
	return on, sure
}

// listenAddress is a listen directive's address as nginx prints it: `80`,
// `*:80` and `0.0.0.0:80` are all 0.0.0.0:80, `[::]:80` stays itself, and an
// address with no port is on 80. A host name is not an address this can know.
func listenAddress(arg string) (string, bool) {
	if strings.HasPrefix(arg, "unix:") {
		return arg, true
	}
	host, port := arg, "80"
	switch {
	case strings.HasPrefix(arg, "["):
		end := strings.Index(arg, "]")
		if end < 0 {
			return "", false
		}
		host = arg[1:end]
		if rest := arg[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", false
			}
			port = rest[1:]
		}
	case strings.Contains(arg, ":"):
		i := strings.LastIndex(arg, ":")
		host, port = arg[:i], arg[i+1:]
	default:
		if _, err := strconv.Atoi(arg); err == nil {
			host, port = "*", arg
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return "", false
	}
	if host == "*" {
		return "0.0.0.0:" + strconv.Itoa(n), true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", false
	}
	if ip.To4() != nil {
		return ip.String() + ":" + strconv.Itoa(n), true
	}
	return "[" + ip.String() + "]:" + strconv.Itoa(n), true
}
