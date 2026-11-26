package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Fitting nginx's server-name hash to the names the dashboard writes.
//
// nginx keeps the server names of each address in a hash whose buckets are
// server_names_hash_bucket_size bytes. The size defaults to the CPU's cache
// line, 64 on most hosts, and Debian's and Ubuntu's nginx.conf leave the
// directive commented out. One name takes align(len+2, 8)+16 bytes of a
// bucket (ngx_hash_init on a 64-bit build), so at 64 the longest name nginx
// takes is 46 characters, and a 47th fails `nginx -t` for the whole
// configuration: "could not build server_names_hash, you should increase
// server_names_hash_bucket_size: 64". A generated deployment name,
// <slug>-<6 hex>.<a-b-c-d>.sslip.io, passes that from a project name of about
// 15 characters, so a first deploy of "riverside-garden-store" stopped at its
// certificate with nothing but "configuration failed validation".
//
// The dashboard raises the limit itself, the way the operator would, and only
// when nginx refuses for that reason: on that exact error it writes (or
// raises) a managed http-level file, tests again, and puts the file back as
// it was when the second test still fails. The bucket it writes is the
// smallest power of two that fits the longest server name nginx reads, and
// never below 128; the second form of the refusal, many names rather than a
// long one, also raises server_names_hash_max_size. A directive the operator
// set anywhere in the configuration is theirs: nginx refuses a second one,
// and the dashboard does not override a value somebody chose. When that value
// is too small the refusal names its file, its line and the value needed.

// serverNamesFile is the managed file's name, in a directory nginx includes
// inside http.
const serverNamesFile = "just-dashboard-server-names.conf"

const (
	bucketDirective  = "server_names_hash_bucket_size"
	maxSizeDirective = "server_names_hash_max_size"
)

const (
	// minServerNamesBucket is the smallest bucket the dashboard writes: 110
	// characters, twice the default, so a name a little longer than the last
	// one does not need another raise.
	minServerNamesBucket = 128
	// maxServerNamesBucket and maxServerNamesHash bound the raises. A DNS
	// name of 253 characters fits a bucket of 512; anything past these is
	// not a configuration the dashboard can fit by itself.
	maxServerNamesBucket = 4096
	maxServerNamesHash   = 65536
	// defaultServerNamesHash is nginx's own server_names_hash_max_size.
	defaultServerNamesHash = 512
	// assumedCacheLine is what nginx rounds a bucket size up to on the hosts
	// the dashboard runs on (x86-64 and arm64 report 64).
	assumedCacheLine = 64
	// fitRounds bounds the tests one fit runs: the max-size form can take a
	// raise of either directive, and each is a test of its own.
	fitRounds = 6
	// maxTreeFiles bounds the files a fit reads to find the names.
	maxTreeFiles = 50000
)

// hashRefusalRe is nginx's refusal to build the server-name hash, in both of
// its forms: one name too long for a bucket, or too many names for the hash.
var hashRefusalRe = regexp.MustCompile(`could not build server_names_hash, you should increase (?:either server_names_hash_max_size: (\d+) or )?server_names_hash_bucket_size: (\d+)`)

// hashRefusal is what a failed test said about the hash: the bucket size
// nginx used, and the max size when it asked for either.
type hashRefusal struct {
	bucket  int
	maxSize int // 0 for the bucket form
}

func readHashRefusal(res *ValidationResult) (hashRefusal, bool) {
	if res == nil || res.Valid {
		return hashRefusal{}, false
	}
	m := hashRefusalRe.FindStringSubmatch(res.Output)
	if m == nil {
		return hashRefusal{}, false
	}
	r := hashRefusal{}
	r.bucket, _ = strconv.Atoi(m[2])
	if m[1] != "" {
		r.maxSize, _ = strconv.Atoi(m[1])
	}
	return r, true
}

// serverNameBytes is what one name takes of a bucket: ngx_hash_init's
// NGX_HASH_ELT_SIZE plus the pointer that ends the bucket, on a 64-bit build.
func serverNameBytes(length int) int {
	return alignUp(length+2, 8) + 16
}

// bucketFits says whether a bucket of size, as nginx rounds it, takes a name
// of length characters.
func bucketFits(size, length int) bool {
	return alignUp(size, assumedCacheLine) >= serverNameBytes(length)
}

// bucketFor is the bucket the dashboard writes for a longest name of length
// characters: the smallest power of two it fits, never below 128.
func bucketFor(length int) int {
	return max(minServerNamesBucket, nextPowerOfTwo(serverNameBytes(length)))
}

func alignUp(n, to int) int {
	return (n + to - 1) / to * to
}

func nextPowerOfTwo(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// serverNamesState is what the configuration says about the hash.
type serverNamesState struct {
	// longest is the longest name in a server block in http, and names how
	// many names there are.
	longest string
	names   int
	// bucket and maxSize are the directives set in http outside the
	// managed file, the operator's; nil where nobody set one.
	bucket, maxSize *Directive
	// ownBucket and ownMaxSize are the managed file's values, 0 where it
	// sets none.
	ownBucket, ownMaxSize int
}

// readServerNames walks a configuration tree for its server names and the two
// directives.
func readServerNames(tree []Directive, own func(Directive) bool) serverNamesState {
	st := serverNamesState{}
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, d := range ds {
			inHTTP := len(d.Context) > 0 && d.Context[0] == "http"
			switch {
			case d.Name == "server_name" && inHTTP && d.Context[len(d.Context)-1] == "server":
				for _, name := range d.Args {
					// A regular expression is not hashed, and a variable
					// is not a name nginx can measure here.
					if strings.HasPrefix(name, "~") || strings.Contains(name, "$") {
						continue
					}
					st.names++
					if len(name) > len(st.longest) {
						st.longest = name
					}
				}
			case (d.Name == bucketDirective || d.Name == maxSizeDirective) && len(d.Context) == 1 && d.Context[0] == "http":
				value, _ := strconv.Atoi(firstArg(d))
				if own(d) {
					if d.Name == bucketDirective {
						st.ownBucket = value
					} else {
						st.ownMaxSize = value
					}
					continue
				}
				found := d
				if d.Name == bucketDirective && st.bucket == nil {
					st.bucket = &found
				} else if d.Name == maxSizeDirective && st.maxSize == nil {
					st.maxSize = &found
				}
			}
			walk(d.Block)
		}
	}
	walk(tree)
	return st
}

func firstArg(d Directive) string {
	if len(d.Args) == 0 {
		return ""
	}
	return d.Args[0]
}

// diskTree is a configuration read from disk the way nginx reads it, from
// main down through every include, each file in the blocks its include sits
// in. `nginx -T` cannot be asked: it prints nothing for a configuration that
// fails its test, which is the one a fit is for. includeScan (the Files page)
// follows the same includes but keeps no blocks, and the names count only
// inside http. includes keeps each include directive, with its context.
type diskTree struct {
	main       string
	directives []Directive
	includes   []Directive
	files      int
	open       map[string]bool
}

func readDiskTree(main string) (*diskTree, error) {
	t := &diskTree{main: main, open: map[string]bool{}}
	directives, err := t.file(main, nil)
	if err != nil {
		return nil, err
	}
	t.directives = directives
	return t, nil
}

func (t *diskTree) file(path string, context []string) ([]Directive, error) {
	t.files++
	if t.files > maxTreeFiles {
		return nil, fmt.Errorf("the configuration includes more than %d files", maxTreeFiles)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	directives, err := ParseNginxFile(path, string(raw), context)
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(path)
	t.open[clean] = true
	defer delete(t.open, clean)
	return t.expand(directives)
}

func (t *diskTree) expand(directives []Directive) ([]Directive, error) {
	out := make([]Directive, 0, len(directives))
	for _, d := range directives {
		if d.Name == "include" && d.Block == nil && len(d.Args) == 1 {
			t.includes = append(t.includes, d)
			for _, path := range t.match(d.Args[0]) {
				if t.open[path] {
					continue
				}
				included, err := t.file(path, d.Context)
				if err != nil {
					// A file this process cannot read is one nginx reads
					// as root, and a link to nothing is nginx's own error
					// to report; their names are not counted, and a raise
					// still has the second test to answer to.
					if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
						continue
					}
					return nil, err
				}
				out = append(out, included...)
			}
			continue
		}
		if d.Block != nil {
			block, err := t.expand(d.Block)
			if err != nil {
				return nil, err
			}
			d.Block = block
		}
		out = append(out, d)
	}
	return out, nil
}

// match is the files an include reads, found the way the Files page finds
// them (includeScan.match): relative to the main file's directory, as glob(3)
// matches for nginx.
func (t *diskTree) match(pattern string) []string {
	scan := includeScan{prefix: filepath.Dir(t.main)}
	matches := scan.match(IncludeSite{}, pattern)
	for i, path := range matches {
		matches[i] = filepath.Clean(path)
	}
	return matches
}

// namesPlacement is where the managed directives go: their own file in a
// directory nginx includes inside http, or, where there is none, marked lines
// at the top of nginx.conf's http block — the approach the stream include
// takes when nginx.conf includes no directory it can use.
type namesPlacement struct {
	path string
	// inMain is the marked-lines form, in the main file at path.
	inMain bool
}

// placeServerNames finds where the managed directives belong. conf.d comes
// first: Debian, Ubuntu, RHEL and its rebuilds, and nginx.org's packages and
// image all include conf.d/*.conf inside http. Any other directory nginx
// includes inside http and the dashboard manages will do (Alpine's http.d),
// but not the site directories, whose every file the Sites page lists.
func (s *Service) placeServerNames(tree *diskTree) (namesPlacement, bool) {
	root := resolvePath(s.nginxDir)
	var fallback string
	for _, inc := range tree.includes {
		if len(inc.Context) != 1 || inc.Context[0] != "http" {
			continue
		}
		pattern := inc.Args[0]
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(filepath.Dir(tree.main), pattern)
		}
		dir := filepath.Dir(filepath.Clean(pattern))
		if strings.ContainsAny(dir, "*?[") {
			continue
		}
		resolved := resolvePath(dir)
		if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			continue
		}
		switch filepath.Base(dir) {
		case "sites-enabled", "sites-available":
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if ok, _ := filepath.Match(globToMatch(filepath.Base(pattern)), serverNamesFile); !ok {
			continue
		}
		candidate := filepath.Join(dir, serverNamesFile)
		if raw, err := os.ReadFile(candidate); err == nil && !strings.Contains(string(raw), managedMarker) {
			// A file of this name that is not the dashboard's is left
			// alone.
			continue
		}
		if filepath.Base(dir) == "conf.d" {
			return namesPlacement{path: candidate}, true
		}
		if fallback == "" {
			fallback = candidate
		}
	}
	if fallback != "" {
		return namesPlacement{path: fallback}, true
	}
	for _, d := range tree.directives {
		if d.Name == "http" && d.Block != nil && len(d.Context) == 0 && filepath.Clean(d.File) == filepath.Clean(tree.main) {
			return namesPlacement{path: tree.main, inMain: true}, true
		}
	}
	return namesPlacement{}, false
}

// owns says whether a directive is one the placement wrote.
func (p namesPlacement) owns(content string) func(Directive) bool {
	marked := map[int]bool{}
	if p.inMain {
		for i, line := range strings.Split(content, "\n") {
			if isNamesLine(line) {
				marked[i+1] = true
			}
		}
	}
	return func(d Directive) bool {
		if filepath.Clean(d.File) != filepath.Clean(p.path) {
			return false
		}
		return !p.inMain || marked[d.Line]
	}
}

// namesLineMark closes each line the dashboard adds to nginx.conf's http
// block.
const namesLineMark = "# server names: added by Just Dashboard"

func isNamesLine(line string) bool {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	return strings.HasSuffix(trimmed, namesLineMark) &&
		(strings.HasPrefix(trimmed, bucketDirective+" ") || strings.HasPrefix(trimmed, maxSizeDirective+" "))
}

// renderServerNames is the managed file for a bucket and a max size, either
// 0 for a directive it does not set.
func renderServerNames(bucket, maxSize int, longest string) string {
	l := &lines{}
	l.add(managedMarker)
	l.add("# nginx's server-name hash, sized for the longest server name it reads.")
	if longest != "" {
		l.add("# Written when %s (%d characters) did not fit; the dashboard raises", longest, len(longest))
	} else {
		l.add("# Written when a server name did not fit; the dashboard raises")
	}
	l.add("# these values when a longer name arrives and never lowers them. To set")
	l.add("# them yourself, delete this file and set them in the http block.")
	if bucket > 0 {
		l.add("%s %d;", bucketDirective, bucket)
	}
	if maxSize > 0 {
		l.add("%s %d;", maxSizeDirective, maxSize)
	}
	return l.String()
}

// spliceServerNames is the main file with the marked lines for bucket and
// maxSize at the top of its http block, in place of any it had.
func spliceServerNames(content string, bucket, maxSize int) (string, bool) {
	nl := lineEnding(content)
	kept := []string{}
	for _, line := range strings.SplitAfter(content, "\n") {
		if !isNamesLine(line) {
			kept = append(kept, line)
		}
	}
	content = strings.Join(kept, "")
	tokens, err := tokenizeNginx("nginx.conf", content)
	if err != nil {
		return "", false
	}
	depth := 0
	for i, tok := range tokens {
		if tok.quoted {
			continue
		}
		switch tok.text {
		case "{":
			depth++
			continue
		case "}":
			depth--
			continue
		}
		if depth != 0 || tok.text != "http" || i+1 >= len(tokens) || tokens[i+1].quoted || tokens[i+1].text != "{" {
			continue
		}
		at := tokens[i+1].end
		var add strings.Builder
		if bucket > 0 {
			add.WriteString(nl + "    " + fmt.Sprintf("%s %d; %s", bucketDirective, bucket, namesLineMark))
		}
		if maxSize > 0 {
			add.WriteString(nl + "    " + fmt.Sprintf("%s %d; %s", maxSizeDirective, maxSize, namesLineMark))
		}
		end := strings.Index(content[at:], "\n")
		if end < 0 {
			end = len(content) - at
		}
		rest := strings.TrimSuffix(content[at:at+end], "\r")
		if trimmed := strings.TrimLeft(rest, " \t"); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			// The block goes on past the brace on the same line, and may
			// close there (`http { include sites-enabled/*; }`): what
			// follows the line is outside it. The marked lines go straight
			// after the brace, and the rest of the line onto its own, since
			// a marked line is dropped whole on the next fit.
			return content[:at] + add.String() + nl + "    " + content[at+len(rest)-len(trimmed):], true
		}
		// Only a comment, or nothing, follows the brace: it stays on the
		// opener's line, and the marked lines go after it, so nothing else
		// moves to another line number but the lines below them.
		if at+end == len(content) {
			return content + add.String() + nl, true
		}
		at += len(rest)
		return content[:at] + add.String() + content[at:], true
	}
	return "", false
}

// namesFit is a raise of the hash the dashboard made and kept. undo puts the
// file back as it was before, for a caller whose own change is rolled back
// after the test that needed the raise passed.
type namesFit struct {
	s       *Service
	path    string
	before  string
	existed bool
	after   string
	// Note is the sentence a result carries about the raise.
	Note string
}

func (f *namesFit) undo(ctx context.Context) {
	if f == nil {
		return
	}
	if err := f.s.restoreNamesFile(f.path, f.before, f.existed); err != nil {
		return
	}
	change := Change{Path: f.path, Action: ChangeWrite, Before: []byte(f.after), BeforeExisted: true, After: []byte(f.before)}
	if !f.existed {
		change.Action, change.After = ChangeDelete, nil
	}
	f.s.recordChange(ctx, change)
}

func (s *Service) restoreNamesFile(path, before string, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAtomic(path, before)
}

// testNginx is `nginx -t` with the server-name hash fitted: the raise a pass
// needed is kept and recorded. A refused test comes back as nginx gave it,
// with the managed file as it was.
func (s *Service) testNginx(ctx context.Context) *ValidationResult {
	res, _, _ := s.fitServerNames(ctx, "", "", true, func() (*ValidationResult, error) {
		return runValidator(ctx, "nginx", "-t"), nil
	})
	return res
}

// fitServerNames runs test and, when nginx refuses to build the server-name
// hash, raises the managed directives and runs it again, for up to fitRounds
// tests. main is the configuration test reads, "" for nginx.conf, and
// editing the file the caller has staged, which a fit never writes to: a
// candidate for nginx.conf or for the managed file itself is the editor's
// word on those directives. A raise is kept when keep is set and a test
// passed with it, and is otherwise put back before this returns; the result
// then says what the save will raise. The caller holds s.mu.
func (s *Service) fitServerNames(ctx context.Context, main, editing string, keep bool, test func() (*ValidationResult, error)) (*ValidationResult, *namesFit, error) {
	res, err := test()
	if err != nil {
		return res, nil, err
	}
	refusal, ok := readHashRefusal(res)
	if !ok {
		return res, nil, nil
	}
	if main == "" {
		main = s.pendingMain()
	}
	tree, treeErr := readDiskTree(main)
	if treeErr != nil {
		// Without the tree there is no knowing where the operator set the
		// directive, so nothing is written and nginx's word stands.
		return res, nil, nil
	}
	place, ok := s.placeServerNames(tree)
	if !ok {
		refuseFit(res, nil, "nginx.conf has no http block the dashboard can add server_names_hash_bucket_size to; set it in the http block")
		return res, nil, nil
	}
	if editing != "" && resolvePath(place.path) == resolvePath(editing) {
		return res, nil, nil
	}
	raw, readErr := os.ReadFile(place.path)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return res, nil, nil
	}
	before := string(raw)
	st := readServerNames(tree.directives, place.owns(before))

	bucket, maxSize := st.ownBucket, st.ownMaxSize
	written := before
	restore := func() error {
		s.forgetEffective()
		return s.restoreNamesFile(place.path, before, existed)
	}
	changed := false
	for round := 0; round < fitRounds; round++ {
		var why string
		bucket, maxSize, why = nextServerNames(refusal, st, bucket, maxSize)
		if why != "" {
			if changed {
				if err := restore(); err != nil {
					return res, nil, err
				}
			}
			refuseFit(res, refusingDirective(refusal, st), why)
			return res, nil, nil
		}
		var content string
		if place.inMain {
			spliced, ok := spliceServerNames(before, bucket, maxSize)
			if !ok {
				if changed {
					if err := restore(); err != nil {
						return res, nil, err
					}
				}
				return res, nil, nil
			}
			content = spliced
		} else {
			content = renderServerNames(bucket, maxSize, st.longest)
		}
		if content == written {
			// Nothing new to try: the same file again would only repeat
			// the refusal.
			break
		}
		if !changed {
			s.keepLoaded(place.path)
		}
		if err := writeAtomic(place.path, content); err != nil {
			if changed {
				_ = restore()
			}
			return res, nil, nil
		}
		s.forgetEffective()
		changed, written = true, content
		next, err := test()
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				return next, nil, restoreErr
			}
			return next, nil, err
		}
		if next.Valid {
			fit := &namesFit{s: s, path: place.path, before: before, existed: existed, after: written}
			fit.Note = namesNote(keep, place, bucket, maxSize, st.longest)
			next.Note = joinNotes(next.Note, fit.Note)
			if !keep {
				if err := restore(); err != nil {
					return next, nil, err
				}
				return next, nil, nil
			}
			s.recordChange(ctx, Change{Path: place.path, Action: ChangeWrite,
				Before: []byte(before), BeforeExisted: existed, After: []byte(written)})
			return next, fit, nil
		}
		res = next
		if refusal, ok = readHashRefusal(next); !ok {
			// The raise answered the hash and nginx now stops at something
			// else; that is the caller's to report, and the raise goes.
			if err := restore(); err != nil {
				return res, nil, err
			}
			return res, nil, nil
		}
	}
	if err := restore(); err != nil {
		return res, nil, err
	}
	return res, nil, nil
}

// nextServerNames is the next values to write for a refusal, or why there
// are none: the operator's own value is too small, or the raises are spent.
func nextServerNames(r hashRefusal, st serverNamesState, bucket, maxSize int) (int, int, string) {
	length := len(st.longest)
	if st.bucket == nil {
		// The file always says the bucket the longest name needs, so a
		// raise for many names does not leave it at the default.
		bucket = max(bucket, bucketFor(length))
	}
	if r.maxSize == 0 {
		// One name is too long for a bucket of r.bucket.
		if st.bucket != nil {
			value, _ := strconv.Atoi(firstArg(*st.bucket))
			name := "a server name nginx reads"
			if st.longest != "" {
				name = fmt.Sprintf("the server name %s (%d characters)", st.longest, length)
			}
			return bucket, maxSize, fmt.Sprintf("%s is %d, too small for %s; set it to %d",
				bucketDirective, value, name, max(bucketFor(length), value*2))
		}
		if bucket <= r.bucket {
			// nginx measured a name this walk did not count — one in a
			// file it could not read — so the bucket doubles.
			bucket = r.bucket * 2
		}
		if bucket > maxServerNamesBucket {
			return bucket, maxSize, fmt.Sprintf("a server name needs %s above %d", bucketDirective, maxServerNamesBucket)
		}
		return bucket, maxSize, ""
	}
	// Too many names for the hash: a larger hash first, then larger
	// buckets, whichever is the dashboard's to raise.
	current := max(r.maxSize, maxSize, defaultServerNamesHash)
	if st.maxSize == nil && current < maxServerNamesHash {
		maxSize = min(maxServerNamesHash, max(current*2, nextPowerOfTwo(st.names*2)))
		return bucket, maxSize, ""
	}
	if st.bucket == nil && max(bucket, r.bucket)*2 <= maxServerNamesBucket {
		bucket = max(bucket, r.bucket) * 2
		return bucket, maxSize, ""
	}
	if st.maxSize != nil {
		value, _ := strconv.Atoi(firstArg(*st.maxSize))
		return bucket, maxSize, fmt.Sprintf("%s is %d, too small for the %d server names nginx reads; set it to %d",
			maxSizeDirective, value, st.names, max(value*2, nextPowerOfTwo(st.names*2)))
	}
	return bucket, maxSize, fmt.Sprintf("the %d server names nginx reads need %s above %d", st.names, maxSizeDirective, maxServerNamesHash)
}

// refusingDirective is the operator's directive a refusal is about.
func refusingDirective(r hashRefusal, st serverNamesState) *Directive {
	if r.maxSize == 0 || st.maxSize == nil {
		return st.bucket
	}
	return st.maxSize
}

// refuseFit puts why a fit could not be made ahead of nginx's own errors, at
// the operator's directive where there is one, so the headline says what to
// change and where.
func refuseFit(res *ValidationResult, at *Directive, why string) {
	d := Diagnostic{Level: "emerg", Message: why}
	line := why
	if at != nil {
		d.File, d.Line = resolvedFile(at.File), at.Line
		line = fmt.Sprintf("%s in %s:%d", why, d.File, d.Line)
	}
	res.Diagnostics = append([]Diagnostic{d}, res.Diagnostics...)
	res.Output = "just-dashboard: " + line + "\n" + res.Output
}

func namesNote(keep bool, place namesPlacement, bucket, maxSize int, longest string) string {
	set := fmt.Sprintf("%s %d", bucketDirective, bucket)
	if maxSize > 0 {
		set += fmt.Sprintf(" and %s %d", maxSizeDirective, maxSize)
	}
	why := "a server name is longer than nginx's default takes"
	if longest != "" {
		why = fmt.Sprintf("%s is %d characters, longer than nginx's default takes", longest, len(longest))
	}
	if keep {
		return fmt.Sprintf("The dashboard set %s in %s: %s.", set, place.path, why)
	}
	return fmt.Sprintf("nginx takes this once %s is set, which the dashboard does in %s when the change is made: %s.", set, place.path, why)
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " " + b
}
