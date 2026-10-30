package updates

import (
	"bufio"
	"context"
	"strconv"
	"strings"
	"time"
)

// apt's half of the catalogue.
//
// Two tools rather than one, and the split is not incidental: dpkg answers for
// what is *installed* and apt-cache answers for what *exists*. Asking apt-cache
// about the installed set gives the repository's idea of a package rather than
// the machine's, which differs on exactly the packages somebody has pinned or
// installed by hand.

// dpkgListFormat is one tab-separated line per package.
//
// db:Status-Abbrev is what separates a package that is installed from one that
// is merely known — a removed-but-not-purged package still has a dpkg entry,
// complete with version, and listing those reports software the machine does
// not have. Installed-Size is in kibibytes, which is why it is multiplied
// below rather than passed through.
const dpkgListFormat = "${db:Status-Abbrev}\t${binary:Package}\t${Version}\t${Architecture}\t" +
	"${Installed-Size}\t${Essential}\t${Section}\t${binary:Summary}\n"

func (aptManager) ListInstalled(ctx context.Context) ([]InstalledPackage, error) {
	out, err := run(ctx, 60*time.Second, "dpkg-query", "-W", "-f="+dpkgListFormat)
	if err != nil && out == "" {
		return nil, err
	}
	packages := parseDpkgList(out)

	// apt-mark is what separates the twelve things somebody installed from the
	// nineteen hundred that came with them. Without it every Debian host reads
	// as an unusable wall, so a failure here is not fatal — the flag simply
	// stays false and the UI's "installed by hand" filter finds nothing.
	if manual, err := run(ctx, 30*time.Second, "apt-mark", "showmanual"); err == nil {
		explicit := map[string]bool{}
		for _, line := range strings.Split(manual, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				explicit[baseName(line)] = true
			}
		}
		for i := range packages {
			packages[i].Explicit = explicit[baseName(packages[i].Name)]
		}
	}
	return packages, nil
}

// parseDpkgList reads the tab-separated table dpkgListFormat asks for.
func parseDpkgList(out string) []InstalledPackage {
	packages := []InstalledPackage{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 8 {
			continue
		}
		// "ii" is installed; "rc" is removed with configuration left behind,
		// "un" is known and absent, "iU" is half-configured.
		if strings.TrimSpace(fields[0]) != "ii" {
			continue
		}
		p := InstalledPackage{
			Name:         fields[1],
			Version:      fields[2],
			Architecture: fields[3],
			Essential:    strings.EqualFold(strings.TrimSpace(fields[5]), "yes"),
			Section:      fields[6],
			Summary:      strings.TrimSpace(fields[7]),
		}
		if kb, err := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64); err == nil {
			p.Size = kb * 1024
		}
		packages = append(packages, p)
	}
	return packages
}

func (aptManager) Search(ctx context.Context, query string) ([]CatalogEntry, error) {
	// --names-only first, because it is what keeps `nginx` returning nginx
	// rather than the four hundred packages whose description mentions it.
	out, err := run(ctx, 30*time.Second, "apt-cache", "search", "--names-only", query)
	if err != nil && out == "" {
		return nil, err
	}
	entries := parseAptSearch(out)

	// And the descriptions when that found little, because no package is
	// called "web server" — on this archive that query matches zero names and
	// three hundred descriptions, and answering nothing would be a search box
	// that only works for people who already knew the answer.
	if len(entries) < thinSearch {
		if full, err := run(ctx, 30*time.Second, "apt-cache", "search", query); err == nil {
			entries = widen(entries, parseAptSearch(full))
		}
	}

	// One policy call for the whole page of results rather than one per row.
	// apt-cache takes every name in a single invocation and prints a stanza
	// each, which is the difference between one subprocess and sixty — so the
	// list is cut to what will be shown *before* the versions are looked up.
	rankResults(entries, query)
	if len(entries) > maxSearchResults {
		entries = entries[:maxSearchResults]
	}
	if len(entries) > 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		if pol, err := run(ctx, 30*time.Second, "apt-cache", append([]string{"policy"}, names...)...); err == nil {
			candidates := parseAptPolicy(pol)
			for i := range entries {
				entries[i].Version = candidates[entries[i].Name]
			}
		}
	}
	return entries, nil
}

// parseAptSearch reads `name - summary`, one per line.
func parseAptSearch(out string) []CatalogEntry {
	entries := []CatalogEntry{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		name, summary, ok := strings.Cut(sc.Text(), " - ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		entries = append(entries, CatalogEntry{Name: name, Summary: strings.TrimSpace(summary)})
	}
	return entries
}

// parseAptPolicy reads the stanza apt-cache policy prints per package:
//
//	nginx:
//	  Installed: (none)
//	  Candidate: 1.24.0-2ubuntu7
//
// Only the candidate is taken; whether it is installed is answered from the
// dpkg index, which is the machine's own record rather than the archive's.
func parseAptPolicy(out string) map[string]string {
	candidates := map[string]string{}
	current := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, " ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			current = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:"); ok && current != "" {
			if v = strings.TrimSpace(v); v != "" && v != "(none)" {
				candidates[current] = v
			}
		}
	}
	return candidates
}

func (aptManager) Info(ctx context.Context, name string) (*PackageDetail, error) {
	out, err := run(ctx, 30*time.Second, "apt-cache", "show", name)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, ErrUnknownPackage
	}
	detail := parseAptShow(out)
	if detail == nil {
		return nil, ErrUnknownPackage
	}
	return detail, nil
}

// parseAptShow reads the first stanza of a Debian control file.
//
// Only the first: apt-cache prints one per version available, newest first,
// and merging them would produce a package whose homepage came from oldstable.
// The description is the summary line plus the indented continuation beneath
// it, where a lone "." is a paragraph break rather than a full stop.
func parseAptShow(out string) *PackageDetail {
	detail := &PackageDetail{}
	description := []string{}
	inDescription := false
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if detail.Name != "" {
				break
			}
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if inDescription {
				body := strings.TrimSpace(line)
				if body == "." {
					body = ""
				}
				description = append(description, body)
			}
			continue
		}
		inDescription = false
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Package":
			detail.Name = value
		case "Version":
			detail.Version = value
		case "Architecture":
			detail.Architecture = value
		case "Section":
			detail.Section = value
		case "Homepage":
			detail.Homepage = value
		case "Maintainer":
			detail.Maintainer = value
		case "Essential":
			detail.Essential = strings.EqualFold(value, "yes")
		case "Installed-Size":
			if kb, err := strconv.ParseInt(value, 10, 64); err == nil {
				detail.Size = kb * 1024
			}
		case "Depends":
			detail.Dependencies = splitDependencies(value)
		case "Description", "Description-en":
			detail.Summary = value
			inDescription = true
		}
	}
	if detail.Name == "" {
		return nil
	}
	detail.Description = strings.TrimSpace(strings.Join(description, "\n"))
	return detail
}

// splitDependencies keeps the names and drops the version constraints, which
// are noise in a list somebody is reading to decide whether they want this.
func splitDependencies(value string) []string {
	deps := []string{}
	for _, part := range strings.Split(value, ",") {
		// An alternative — "python3 | python3-minimal" — is one dependency
		// with two ways of satisfying it; the first is what apt would pick.
		if alt, _, ok := strings.Cut(part, "|"); ok {
			part = alt
		}
		if paren, _, ok := strings.Cut(part, "("); ok {
			part = paren
		}
		if name := strings.TrimSpace(part); name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}

func (aptManager) Files(ctx context.Context, name string) ([]string, error) {
	out, err := run(ctx, 30*time.Second, "dpkg-query", "-L", name)
	if err != nil && strings.TrimSpace(out) == "" {
		return nil, err
	}
	return splitLines(out), nil
}

func (aptManager) InstallCommand(names []string) (string, []string, []string) {
	// Recommends are left on, unlike the upgrade path. An upgrade that pulls
	// in new packages is a surprise; an install that leaves out the half of
	// the software the distribution considers part of it is a package that
	// does not work, and "it works when I install it from a shell" is the
	// worst thing a panel like this can be true of.
	args := append(append([]string{"install", "-y", "-o", "Dpkg::Options::=--force-confold"}, aptLockWait...), names...)
	return "apt-get", args, []string{
		"DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "NEEDRESTART_MODE=a",
	}
}

func (aptManager) RemoveCommand(names []string, purge bool) (string, []string, []string) {
	verb := "remove"
	if purge {
		verb = "purge"
	}
	// --auto-remove takes the dependencies that were only there for this with
	// it, which is what an operator means by "remove" and what leaving them
	// behind makes them come back and ask about.
	args := append(append([]string{verb, "-y", "--auto-remove"}, aptLockWait...), names...)
	return "apt-get", args, []string{
		"DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "NEEDRESTART_MODE=a",
	}
}

// dpkg keeps a removed package's configuration until it is purged, which is
// the one manager here where the distinction is real and worth a switch.
func (aptManager) SupportsPurge() bool { return true }

// IndexAge reads apt's own success stamp, falling back to the newest file in
// the lists directory — the stamp is written by the unattended-upgrades timer
// and is absent on a host that has never had one.
func (aptManager) IndexAge() (time.Time, bool) {
	return newestMtime(
		"/var/lib/apt/periodic/update-success-stamp",
		"/var/lib/apt/lists",
	)
}

func (aptManager) RefreshCommand() (string, []string, []string, bool) {
	return "apt-get", []string{"update"},
		[]string{"DEBIAN_FRONTEND=noninteractive", "LC_ALL=C"}, true
}

// splitLines is the shared "one path per line, blanks dropped" reader every
// file listing here needs.
func splitLines(out string) []string {
	lines := []string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
