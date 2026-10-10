package files

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

// Find is the "just type the name" half of the file manager.
//
// Search already existed and answers a different question: it takes a literal
// substring or a regular expression and walks looking for it, optionally
// inside file contents, and it is the right tool when you know what you are
// looking for. It is the wrong tool for the thing people actually do fifty
// times a day, which is remembering three letters of a name and most of where
// it lives — `srcapp` for src/app, `ngcnf` for nginx.conf. Every editor solved
// that with a fuzzy matcher a decade ago and every file manager in this class
// still makes you click.
//
// So this is a subsequence matcher with the scoring that makes one useful: a
// match at the start of a name beats one in the middle, consecutive characters
// beat scattered ones, a match after a separator beats one inside a word, and
// the basename beats the directories above it. Terms are ANDed, so
// "app tsx" narrows rather than widening.
type FindHit struct {
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Rel      string    `json:"rel"`
	Dir      string    `json:"dir"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Score    int       `json:"score"`
	// Matches are the positions inside Name that matched, in the UTF-16 code
	// units the browser slices strings by — Go counts bytes, and a highlight
	// drawn from byte offsets paints the wrong run the moment a name carries
	// an accent. Empty when only the directory part matched.
	Matches []int `json:"matches,omitempty"`
}

type FindOptions struct {
	Root   string
	Query  string
	Limit  int
	Hidden bool
	// MaxDepth, MaxVisit and Budget are what make this safe to point at "/".
	// A fuzzy search has no natural stopping point, and the operator is
	// waiting: it is better to answer from most of the tree quickly and say
	// so than to be complete and take a minute.
	MaxDepth int
	MaxVisit int
	Budget   time.Duration
}

type FindResult struct {
	Root string    `json:"root"`
	Hits []FindHit `json:"hits"`
	// Visited is how many entries were looked at, and Truncated says the walk
	// stopped early — on the budget, the visit cap or the match cap. The UI
	// says "showing the best of what was reached", because a fuzzy search
	// that quietly answers from a third of the disk is worse than one that
	// admits it.
	Truncated bool `json:"truncated"`
	// Total is how many matched before the best Limit were kept. More matches
	// than fit is not a partial walk, and saying "partial" for it sent people
	// widening a search that had already looked everywhere.
	Total     int `json:"total"`
	Visited   int `json:"visited"`
	ElapsedMS int `json:"elapsedMs"`
}

const (
	findDefaultLimit  = 60
	findDefaultVisit  = 120_000
	findDefaultBudget = 900 * time.Millisecond
	findMaxMatches    = 2_000
)

func (s *Service) Find(ctx context.Context, opts FindOptions) (*FindResult, error) {
	root, err := s.Resolve(opts.Root)
	if err != nil {
		return nil, err
	}
	if opts.Limit <= 0 || opts.Limit > 500 {
		opts.Limit = findDefaultLimit
	}
	if opts.MaxDepth <= 0 || opts.MaxDepth > 32 {
		opts.MaxDepth = 12
	}
	if opts.MaxVisit <= 0 || opts.MaxVisit > 1_000_000 {
		opts.MaxVisit = findDefaultVisit
	}
	if opts.Budget <= 0 || opts.Budget > 5*time.Second {
		opts.Budget = findDefaultBudget
	}

	terms := compileTerms(opts.Query)
	result := &FindResult{Root: root, Hits: []FindHit{}}
	if len(terms) == 0 {
		return result, nil
	}

	started := time.Now()
	deadline := started.Add(opts.Budget)
	visited := 0

	// Breadth first, a level at a time. A depth-first walk spent its whole
	// budget inside the first large directory it met — a checkout, a runner's
	// work tree — and answered "partial" without ever reaching the folder
	// beside it whose name was typed. Shallow results are the ones the scoring
	// prefers anyway, so they are the ones the walk reaches first.
	type pending struct {
		path  string
		depth int
	}
	queue := []pending{{path: root}}
walk:
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		children, err := os.ReadDir(dir.path)
		if err != nil {
			// An unreadable directory is a fact about this host, not a failure
			// of the search: skip it and keep going.
			continue
		}
		for _, d := range children {
			visited++
			// The clock and the caller are consulted rarely rather than per
			// entry: time.Now on every one of a hundred thousand entries is
			// itself a measurable part of the walk.
			if visited%512 == 0 && (ctx.Err() != nil || time.Now().After(deadline)) {
				result.Truncated = true
				break walk
			}
			if visited >= opts.MaxVisit || len(result.Hits) >= findMaxMatches {
				result.Truncated = true
				break walk
			}
			name := d.Name()
			path := filepath.Join(dir.path, name)
			hidden := strings.HasPrefix(name, ".")
			if hidden && !opts.Hidden {
				continue
			}
			if d.IsDir() && !skipDirs[name] && dir.depth+1 < opts.MaxDepth {
				queue = append(queue, pending{path: path, depth: dir.depth + 1})
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			score, matches, ok := scoreCandidate(terms, name, rel)
			if !ok {
				continue
			}
			if d.IsDir() {
				// A folder is a destination as well as a result: opening one is
				// usually what a search for a directory name was for.
				score += 8
			}
			hit := FindHit{
				Path: path, Name: name, Rel: rel, Dir: filepath.Dir(rel),
				IsDir: d.IsDir(), Score: score, Matches: matches,
			}
			if info, err := d.Info(); err == nil {
				hit.Size = info.Size()
				hit.Modified = info.ModTime().UTC()
			}
			result.Hits = append(result.Hits, hit)
		}
	}
	if ctx.Err() != nil {
		result.Truncated = true
	}

	sort.SliceStable(result.Hits, func(i, j int) bool {
		a, b := result.Hits[i], result.Hits[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if len(a.Rel) != len(b.Rel) {
			return len(a.Rel) < len(b.Rel)
		}
		return a.Rel < b.Rel
	})
	result.Total = len(result.Hits)
	if len(result.Hits) > opts.Limit {
		result.Hits = result.Hits[:opts.Limit]
	}
	result.Visited = visited
	result.ElapsedMS = int(time.Since(started).Milliseconds())
	return result, nil
}

// term is one whitespace-separated word of the query, kept lowercased as runes
// so the per-candidate loop does no allocation of its own.
type term struct {
	runes []rune
	text  string
}

func compileTerms(query string) []term {
	out := []term{}
	for _, field := range strings.Fields(query) {
		lower := strings.ToLower(field)
		out = append(out, term{runes: []rune(lower), text: lower})
	}
	return out
}

// scoreCandidate requires every term to match, preferring the basename. A term
// that matches only the directory part still counts — that is what makes
// "nginx conf" find sites-available/example.conf — but scores lower and
// contributes no highlight, because the highlight is drawn on the name.
func scoreCandidate(terms []term, name, rel string) (int, []int, bool) {
	nameRunes := []rune(strings.ToLower(name))
	relRunes := []rune(strings.ToLower(rel))
	total := 0
	var positions []int
	for _, t := range terms {
		if score, pos, ok := fuzzyScore(t.runes, nameRunes, []rune(name)); ok {
			total += score + 60
			positions = append(positions, pos...)
			if strings.EqualFold(t.text, name) {
				total += 120
			} else if strings.HasPrefix(strings.ToLower(name), t.text) {
				total += 45
			}
			continue
		}
		score, _, ok := fuzzyScore(t.runes, relRunes, []rune(rel))
		if !ok {
			return 0, nil, false
		}
		total += score
	}
	// A shallow result is nearly always the one meant: two directories down
	// beats the same name eight down inside a build output.
	total -= strings.Count(rel, string(os.PathSeparator)) * 3
	total -= len([]rune(name)) / 4
	if len(positions) > 1 {
		sort.Ints(positions)
		positions = dedupeInts(positions)
	}
	return total, utf16Positions(name, positions), true
}

// fuzzyScore is the subsequence match: a forward pass to prove the characters
// appear in order, then a backward pass from where the forward one ended to
// pull the match as tight as it will go. Without the second pass "app" against
// "a-package-application" matches the first three scattered letters and scores
// worse than the run it should have found.
//
// A run of the typed characters, where one exists, is scored as well and the
// better of the two kept. The tightening pass only ever finds the run that
// ends where the forward pass did, so "promo" against
// "proxy-tls-monitor.spec.ts" scattered across two words used to outscore
// "Just-Dashboard-Promo.mp4", which holds the word itself.
func fuzzyScore(needle, haystack, original []rune) (int, []int, bool) {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return 0, nil, false
	}
	end := -1
	k := 0
	for i := 0; i < len(haystack); i++ {
		if haystack[i] == needle[k] {
			k++
			if k == len(needle) {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	positions := make([]int, len(needle))
	k = len(needle) - 1
	for i := end; i >= 0 && k >= 0; i-- {
		if haystack[i] == needle[k] {
			positions[k] = i
			k--
		}
	}
	score := scorePositions(positions, haystack, original)

	for start := 0; start+len(needle) <= len(haystack); start++ {
		if !runesAt(haystack, needle, start) {
			continue
		}
		run := make([]int, len(needle))
		for i := range run {
			run[i] = start + i
		}
		// The bonus is what a person means by "it has the word in it": a run
		// beats any scatter of the same letters, wherever in the name it sits.
		if s := scorePositions(run, haystack, original) + 40; s > score {
			score, positions = s, run
		}
	}
	return score, positions, true
}

func runesAt(haystack, needle []rune, start int) bool {
	for i, r := range needle {
		if haystack[start+i] != r {
			return false
		}
	}
	return true
}

func scorePositions(positions []int, haystack, original []rune) int {
	score := 0
	for i, pos := range positions {
		score += 12
		if i > 0 && pos == positions[i-1]+1 {
			score += 18
		}
		switch {
		case pos == 0:
			score += 30
		case isBoundary(haystack[pos-1]):
			score += 16
		case len(original) == len(haystack) &&
			unicode.IsLower(original[pos-1]) && unicode.IsUpper(original[pos]):
			// camelCase reads as a boundary to a person typing "fC" for
			// fileContent, and the lowercased haystack cannot see it.
			score += 12
		}
	}
	span := positions[len(positions)-1] - positions[0] + 1
	score -= (span - len(positions)) * 3
	score -= positions[0]
	score -= len(haystack) / 4
	return score
}

func isBoundary(r rune) bool {
	switch r {
	case '/', '\\', '-', '_', '.', ' ', '@', ':':
		return true
	}
	return false
}

func dedupeInts(in []int) []int {
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// utf16Positions converts rune indices into the code units JavaScript counts,
// for the same reason logsx.Filter does: a highlight drawn from Go's own
// indices lands one character left for every astral or accented rune before it.
func utf16Positions(text string, runeIdx []int) []int {
	if len(runeIdx) == 0 {
		return nil
	}
	ascii := true
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return runeIdx
	}
	units := make([]int, 0, len([]rune(text)))
	acc := 0
	for _, r := range text {
		units = append(units, acc)
		acc += utf16.RuneLen(r)
	}
	out := make([]int, 0, len(runeIdx))
	for _, i := range runeIdx {
		if i >= 0 && i < len(units) {
			out = append(out, units[i])
		}
	}
	return out
}
