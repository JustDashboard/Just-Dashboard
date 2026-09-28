package proxysvc

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A site's access options beyond its own allow list and password: letting an
// allowed address in without the password, taking a shared access list in,
// asking for client certificates, refusing crawlers and scanners by user
// agent, refusing hotlinked media, and serving security.txt and robots.txt.

// SiteClientCert asks visitors for a certificate signed by CAPath.
type SiteClientCert struct {
	// CAPath is the PEM file of the CA, or CAs, a certificate must chain to.
	CAPath string `json:"caPath"`
	// Mode is empty to refuse a request without a valid certificate, which
	// nginx answers 400, or "optional" to let it through and tell the
	// application with PassSubject — which then has to refuse it itself.
	Mode string `json:"mode,omitempty"`
	// PassSubject sends the application whether the certificate was
	// verified and whose it is. Proxy sites only.
	PassSubject bool `json:"passSubject,omitempty"`
}

// SiteBotBlock refuses requests whose User-Agent names a crawler. It only
// stops clients that say what they are: anything can send a browser's.
type SiteBotBlock struct {
	AI       bool `json:"ai,omitempty"`
	Scanners bool `json:"scanners,omitempty"`
	// Custom are further names, matched anywhere in the User-Agent and
	// regardless of case.
	Custom []string `json:"custom,omitempty"`
}

// SiteHotlink refuses the site's media to pages on other sites.
type SiteHotlink struct {
	// Allow are further names whose pages may embed the site's media,
	// beside the site's own. A leading *. covers subdomains.
	Allow []string `json:"allow,omitempty"`
}

// aiCrawlers are the user agents AI companies' crawlers and fetchers
// announce themselves with. Tokens only robots.txt reads, such as
// Google-Extended, are not here: no request carries them.
var aiCrawlers = []string{
	"GPTBot", "ChatGPT-User", "OAI-SearchBot", "ClaudeBot", "Claude-User", "Claude-SearchBot",
	"anthropic-ai", "CCBot", "PerplexityBot", "Perplexity-User", "Bytespider", "Amazonbot",
	"meta-externalagent", "meta-externalfetcher", "cohere-ai", "Diffbot", "YouBot", "Timpibot",
	"ImagesiftBot", "Omgilibot",
}

// scannerAgents are vulnerability scanners that name themselves by default.
var scannerAgents = []string{
	"sqlmap", "Nikto", "Nmap Scripting Engine", "masscan", "zgrab", "Nuclei", "WPScan",
	"DirBuster", "gobuster", "Fuzz Faster U Fool",
}

// hotlinkTypes are the extensions hotlink protection covers: media, which is
// what other sites embed. Pages and scripts are left alone, since a link to
// a page from elsewhere is how anyone arrives.
const hotlinkTypes = `png|jpe?g|gif|webp|avif|svg|ico|bmp|mp4|webm|ogv|mp3|ogg|m4a|wav|flac`

// securityTxtPath and robotsTxtPath are where the two files are served.
const (
	securityTxtPath = "/.well-known/security.txt"
	robotsTxtPath   = "/robots.txt"
)

// accessListDirName is the folder under the nginx directory that the access
// lists page keeps its lists in, one <name>.conf each.
const accessListDirName = "jd-access"

var (
	// siteAccessListRe is a list's name as the access lists page allows it.
	siteAccessListRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	// A user agent is matched as a regex inside double quotes; a dot is
	// escaped and nothing else with a meaning to either is allowed.
	botNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/-]{0,63}$`)

	botMapRe     = regexp.MustCompile(`^\$http_user_agent\s+\$jd_\w+_bot\s*\{$`)
	hotlinkMapRe = regexp.MustCompile(`^\$uri\s+\$jd_\w+_hotlink_type\s*\{$`)
)

func (spec *SiteSpec) botVar() string         { return NginxIdent(spec.Name) + "_bot" }
func (spec *SiteSpec) hotlinkTypeVar() string { return NginxIdent(spec.Name) + "_hotlink_type" }
func (spec *SiteSpec) hotlinkVar() string     { return NginxIdent(spec.Name) + "_hotlink" }

// accessListPath is the file the site's access list is in.
func (spec *SiteSpec) accessListPath() string {
	dir := spec.AccessListDir
	if dir == "" {
		dir = "/etc/nginx/" + accessListDirName
	}
	return filepath.Join(dir, spec.AccessList+".conf")
}

// mayPassWithout says whether a request can get in on a password or an
// address alone: satisfy any, said by the site or perhaps by its list.
// Where the site then denies outright, that has to be said as satisfy all,
// or a password would open it.
func (spec *SiteSpec) mayPassWithout() bool { return spec.SatisfyAny || spec.AccessList != "" }

// SetAccessListDir points a spec's include at this host's access lists. Like
// PagesDir it is the service's, never a request's.
func (s *Service) SetAccessListDir(spec *SiteSpec) {
	spec.AccessListDir = ""
	if spec.AccessList != "" {
		spec.AccessListDir = filepath.Join(s.nginxDir, accessListDirName)
	}
}

// botAgents are every user agent the site refuses, presets first, each once.
func (b *SiteBotBlock) agents() []string {
	var out []string
	add := func(names []string) {
		for _, name := range names {
			if !containsFold(out, name) {
				out = append(out, name)
			}
		}
	}
	if b.AI {
		add(aiCrawlers)
	}
	if b.Scanners {
		add(scannerAgents)
	}
	add(b.Custom)
	return out
}

func validateAccessOptions(spec *SiteSpec) error {
	if spec.SatisfyAny && (spec.BasicAuthFile == "" || len(spec.AllowFrom) == 0) {
		return fmt.Errorf("letting allowed addresses in without a password needs both an allow list and a password file")
	}
	if spec.AccessList != "" {
		if !siteAccessListRe.MatchString(spec.AccessList) {
			return fmt.Errorf("an access list name is lowercase letters, digits, dashes or underscores")
		}
		// Two sources in one block do not stack the way they read: nginx
		// refuses a second auth_basic, and two allow lists become one list
		// in file order under whichever satisfy comes last.
		if spec.BasicAuthFile != "" || len(spec.AllowFrom) > 0 || len(spec.DenyFrom) > 0 || spec.SatisfyAny {
			return fmt.Errorf("a site with an access list takes its addresses and password from the list — clear the site's own")
		}
	}
	if spec.AccessListDir != "" && !absPathRe.MatchString(spec.AccessListDir) {
		return fmt.Errorf("the access list directory must be an absolute path")
	}
	if c := spec.ClientCert; c != nil {
		// A site that also answers plain HTTP would serve everything the
		// certificate guards to anyone on port 80.
		if !spec.TLS || !spec.ForceHTTPS {
			return fmt.Errorf("client certificates need HTTPS with plain HTTP redirected, or port 80 would serve the site without one")
		}
		if !absPathRe.MatchString(c.CAPath) {
			return fmt.Errorf("the client CA must be an absolute path to a PEM file")
		}
		switch c.Mode {
		case "":
		case "optional":
			if !c.PassSubject {
				return fmt.Errorf("an optional client certificate only does something if the application is told about it")
			}
		default:
			return fmt.Errorf("a client certificate is required or optional")
		}
		if c.PassSubject && spec.Kind != "proxy" {
			return fmt.Errorf("the certificate's subject can only be passed to an application behind a proxy site")
		}
	}
	if b := spec.BlockBots; b != nil {
		for _, name := range b.Custom {
			if !botNameRe.MatchString(name) {
				return fmt.Errorf("%q: a user agent to block is letters, digits, spaces, dots, dashes, slashes or underscores", name)
			}
		}
		if len(b.agents()) == 0 {
			return fmt.Errorf("choose which user agents to block")
		}
	}
	if h := spec.Hotlink; h != nil {
		if spec.Kind == "redirect" {
			return fmt.Errorf("a redirect site serves no files to protect from hotlinking")
		}
		for _, name := range h.Allow {
			if !domainRe.MatchString(name) {
				return fmt.Errorf("%q is not a domain name", name)
			}
		}
	}
	if (spec.SecurityTxt || spec.RobotsTxt) && spec.Kind == "redirect" {
		return fmt.Errorf("a redirect site answers every path with its redirect, security.txt and robots.txt included")
	}
	return nil
}

// renderAccessMaps writes the http-level maps the bot and hotlink checks
// read.
func renderAccessMaps(l *lines, spec *SiteSpec) {
	if b := spec.BlockBots; b != nil {
		l.add("# Refused by user agent, matched anywhere in it and regardless of case.")
		l.add("map $http_user_agent $%s {", spec.botVar())
		l.add("    default 0;")
		for _, name := range b.agents() {
			l.add("    \"~*%s\" 1;", strings.ReplaceAll(name, ".", `\.`))
		}
		l.add("}")
	}
	if spec.Hotlink != nil {
		if spec.BlockBots != nil {
			l.blank()
		}
		l.add("# The files hotlink protection covers.")
		l.add("map $uri $%s {", spec.hotlinkTypeVar())
		l.add("    default \"\";")
		l.add("    \"~*\\.(%s)$\" 1;", hotlinkTypes)
		l.add("}")
	}
}

// renderRequestChecks refuses bots and hotlinks in the server's rewrite
// phase, before any location is chosen.
func renderRequestChecks(l *lines, spec *SiteSpec) {
	if spec.BlockBots == nil && spec.Hotlink == nil {
		return
	}
	if spec.BlockBots != nil {
		l.add("    if ($%s) {", spec.botVar())
		l.add("        return 403;")
		l.add("    }")
	}
	if h := spec.Hotlink; h != nil {
		l.add("    # A media file asked for from another site's page is refused. No")
		l.add("    # Referer at all, or one a firewall blanked, is let through: that")
		l.add("    # is a visitor typing the address, not an embed.")
		l.add("    valid_referers %s;", strings.Join(append([]string{"none", "blocked", "server_names"}, h.Allow...), " "))
		l.add("    set $%s $%s$invalid_referer;", spec.hotlinkVar(), spec.hotlinkTypeVar())
		l.add("    if ($%s = 11) {", spec.hotlinkVar())
		l.add("        return 403;")
		l.add("    }")
	}
	l.blank()
}

// renderClientCert asks for the client's certificate in the handshake.
func renderClientCert(l *lines, spec *SiteSpec) {
	c := spec.ClientCert
	if c == nil {
		return
	}
	l.add("    ssl_client_certificate %s;", c.CAPath)
	if c.Mode == "optional" {
		l.add("    # A request without a valid certificate still reaches the")
		l.add("    # application, which is told and has to refuse it itself.")
		l.add("    ssl_verify_client optional;")
		return
	}
	l.add("    # A request without a certificate signed by that CA is answered 400.")
	l.add("    ssl_verify_client on;")
}

// renderClientCertHeaders tells the application about the certificate. Set
// here, they replace whatever headers of the same name the visitor sent.
func renderClientCertHeaders(l *lines, spec *SiteSpec) {
	if spec.ClientCert == nil || !spec.ClientCert.PassSubject {
		return
	}
	l.add("        proxy_set_header X-Client-Verify   $ssl_client_verify;")
	l.add("        proxy_set_header X-Client-Subject  $ssl_client_s_dn;")
}

// clientCertHeaders are the request headers renderClientCertHeaders sets.
var clientCertHeaders = []string{"X-Client-Verify", "X-Client-Subject"}

// renderWellKnown serves the site's security.txt and robots.txt, whatever
// the application or folder has at those paths.
func renderWellKnown(l *lines, spec *SiteSpec, dir string) {
	file := func(path, name string) {
		l.add("    location = %s {", path)
		l.add("        alias %s/%s;", dir, name)
		l.add("        default_type text/plain;")
		l.add("    }")
	}
	if spec.SecurityTxt {
		file(securityTxtPath, "security.txt")
	}
	if spec.RobotsTxt {
		file(robotsTxtPath, "robots.txt")
	}
}

// wellKnownFile is the page a well-known location serves.
func wellKnownFile(location string) (string, bool) {
	switch location {
	case securityTxtPath:
		return "security", true
	case robotsTxtPath:
		return "robots", true
	}
	return "", false
}

// accessParse collects the access options as the parser reads them.
type accessParse struct {
	inBots     bool
	bots       []string
	botCheck   bool
	hotlinkMap bool
	referers   []string
	sawReferer bool
	caPath     string
	verify     string
	passCert   bool
}

func (p *accessParse) object(name, value string) {
	if name != "map" {
		return
	}
	if botMapRe.MatchString(value) {
		p.inBots = true
	}
	if hotlinkMapRe.MatchString(value) {
		p.hotlinkMap = true
	}
}

func (p *accessParse) entry(raw string) {
	fields := strings.Fields(strings.TrimSuffix(raw, ";"))
	if !p.inBots || len(fields) < 2 || fields[0] == "default" {
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(strings.Join(fields[:len(fields)-1], " "), `"~*`), `"`)
	p.bots = append(p.bots, strings.ReplaceAll(name, `\.`, "."))
}

func (p *accessParse) closed() { p.inBots = false }

// ifOpen reads the bot check's if.
func (p *accessParse) ifOpen(raw string) bool {
	if !botIfRe.MatchString(raw) {
		return false
	}
	p.botCheck = true
	return true
}

var botIfRe = regexp.MustCompile(`^if\s*\(\$jd_\w+_bot\)\s*\{$`)

// directive reads an access option. It says whether it was one.
func (p *accessParse) directive(spec *SiteSpec, name, value string, serverLevel bool) bool {
	switch name {
	case "proxy_set_header":
		header, _, _ := strings.Cut(value, " ")
		if !slices.Contains(clientCertHeaders, header) {
			return false
		}
		p.passCert = true
		return true
	case "include":
		if !serverLevel || filepath.Base(filepath.Dir(value)) != accessListDirName {
			return false
		}
		list, ok := strings.CutSuffix(filepath.Base(value), ".conf")
		if !ok || !siteAccessListRe.MatchString(list) {
			return false
		}
		spec.AccessList, spec.AccessListDir = list, filepath.Dir(value)
		return true
	}
	if !serverLevel {
		return false
	}
	switch name {
	case "satisfy":
		spec.SatisfyAny = value == "any"
	case "valid_referers":
		p.sawReferer = true
		for _, field := range strings.Fields(value) {
			if field != "none" && field != "blocked" && field != "server_names" {
				p.referers = append(p.referers, field)
			}
		}
	case "ssl_client_certificate":
		p.caPath = value
	case "ssl_verify_client":
		p.verify = value
	default:
		return false
	}
	return true
}

func (p *accessParse) settle(spec *SiteSpec) {
	if p.botCheck && len(p.bots) > 0 {
		b := &SiteBotBlock{}
		rest := p.bots
		take := func(preset []string) bool {
			for _, name := range preset {
				if !containsFold(rest, name) {
					return false
				}
			}
			rest = slices.DeleteFunc(rest, func(name string) bool { return containsFold(preset, name) })
			return true
		}
		b.AI = take(aiCrawlers)
		b.Scanners = take(scannerAgents)
		b.Custom = rest
		spec.BlockBots = b
	}
	if p.sawReferer && p.hotlinkMap {
		spec.Hotlink = &SiteHotlink{Allow: p.referers}
	}
	if p.caPath != "" && (p.verify == "on" || p.verify == "optional") {
		c := &SiteClientCert{CAPath: p.caPath, PassSubject: p.passCert}
		if p.verify == "optional" {
			c.Mode = "optional"
		}
		spec.ClientCert = c
	}
}
