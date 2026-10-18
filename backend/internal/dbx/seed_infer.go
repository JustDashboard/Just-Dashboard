package dbx

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Which generator a column starts with.
//
// A column's name says more about its values than its type does: a VARCHAR
// called email holds addresses and one called city holds cities, and mock
// data that gets those right is data a developer can read a screen of. So
// the name is read first, in words (`createdAt`, `created_at` and `CREATED_AT`
// are the same two), and the type second, and a choice the type rules out is
// never made: a column called email that is an integer gets a number.
//
// What the column's own definition constrains it to — an enum's labels, a
// CHECK's list or bounds — beats both, because a value outside them is one
// the table refuses.

// seedNameWords splits a column name into lower-case words.
func seedNameWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && len(cur) > 0 &&
			(unicode.IsLower(cur[len(cur)-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// seedPeopleTables are the tables whose rows are people, where a bare
// "name" is somebody's name.
var seedPeopleTables = map[string]bool{
	"user": true, "users": true, "person": true, "people": true, "persons": true, "customer": true,
	"customers": true, "author": true, "authors": true, "employee": true, "employees": true,
	"member": true, "members": true, "contact": true, "contacts": true, "student": true,
	"students": true, "patient": true, "patients": true, "client": true, "clients": true,
	"staff": true, "account": true, "accounts": true, "teacher": true, "teachers": true,
	"actor": true, "actors": true, "artist": true, "artists": true, "player": true, "players": true,
	"owner": true, "owners": true, "subscriber": true, "subscribers": true, "profile": true, "profiles": true,
}

var seedProductTables = map[string]bool{
	"product": true, "products": true, "item": true, "items": true, "goods": true,
	"article": true, "articles": true, "sku": true, "skus": true, "inventory": true,
}

var seedCompanyTables = map[string]bool{
	"company": true, "companies": true, "organization": true, "organizations": true,
	"organisation": true, "organisations": true, "vendor": true, "vendors": true,
	"supplier": true, "suppliers": true, "business": true, "businesses": true, "tenant": true, "tenants": true,
}

// seedWordTables are the tables whose rows are named by a word: a category,
// a tag.
var seedWordTables = map[string]bool{
	"category": true, "categories": true, "tag": true, "tags": true, "genre": true, "genres": true,
	"topic": true, "topics": true, "label": true, "labels": true, "department": true,
	"departments": true, "team": true, "teams": true, "group": true, "groups": true, "role": true, "roles": true,
}

// seedNameRule is one reading of a name: the words that must all appear, and
// the generator that follows from them.
type seedNameRule struct {
	all []string
	gen string
}

// seedNameRules are tried in order; the first whose words all appear wins.
var seedNameRules = []seedNameRule{
	{[]string{"email"}, "email"}, {[]string{"mail"}, "email"},
	{[]string{"first", "name"}, "firstName"}, {[]string{"firstname"}, "firstName"},
	{[]string{"given", "name"}, "firstName"}, {[]string{"fname"}, "firstName"},
	{[]string{"last", "name"}, "lastName"}, {[]string{"lastname"}, "lastName"},
	{[]string{"family", "name"}, "lastName"}, {[]string{"surname"}, "lastName"}, {[]string{"lname"}, "lastName"},
	{[]string{"full", "name"}, "fullName"}, {[]string{"fullname"}, "fullName"},
	{[]string{"display", "name"}, "fullName"}, {[]string{"contact", "name"}, "fullName"},
	{[]string{"user", "name"}, "username"}, {[]string{"username"}, "username"}, {[]string{"login"}, "username"},
	{[]string{"handle"}, "username"}, {[]string{"nickname"}, "username"},
	{[]string{"company"}, "company"}, {[]string{"organization"}, "company"}, {[]string{"organisation"}, "company"},
	{[]string{"employer"}, "company"}, {[]string{"vendor"}, "company"},
	{[]string{"job", "title"}, "jobTitle"}, {[]string{"occupation"}, "jobTitle"}, {[]string{"position"}, "jobTitle"},
	{[]string{"product", "name"}, "productName"},
	{[]string{"phone"}, "phone"}, {[]string{"mobile"}, "phone"}, {[]string{"tel"}, "phone"},
	{[]string{"telephone"}, "phone"}, {[]string{"fax"}, "phone"},
	{[]string{"street"}, "street"}, {[]string{"address"}, "street"}, {[]string{"addr"}, "street"},
	{[]string{"line1"}, "street"},
	{[]string{"city"}, "city"}, {[]string{"town"}, "city"},
	{[]string{"country", "code"}, "countryCode"}, {[]string{"country"}, "country"},
	{[]string{"zip"}, "postalCode"}, {[]string{"postal"}, "postalCode"}, {[]string{"postcode"}, "postalCode"},
	{[]string{"zipcode"}, "postalCode"},
	{[]string{"latitude"}, "latitude"}, {[]string{"lat"}, "latitude"},
	{[]string{"longitude"}, "longitude"}, {[]string{"lng"}, "longitude"}, {[]string{"lon"}, "longitude"},
	{[]string{"avatar"}, "imageUrl"}, {[]string{"image"}, "imageUrl"}, {[]string{"photo"}, "imageUrl"},
	{[]string{"picture"}, "imageUrl"}, {[]string{"thumbnail"}, "imageUrl"}, {[]string{"logo"}, "imageUrl"},
	{[]string{"url"}, "url"}, {[]string{"website"}, "url"}, {[]string{"homepage"}, "url"},
	{[]string{"link"}, "url"}, {[]string{"href"}, "url"}, {[]string{"uri"}, "url"},
	{[]string{"ip"}, "ipAddress"},
	{[]string{"slug"}, "slug"},
	{[]string{"password"}, "password"}, {[]string{"passwd"}, "password"}, {[]string{"pwd"}, "password"},
	{[]string{"hash"}, "password"}, {[]string{"digest"}, "password"},
	{[]string{"colour"}, "color"}, {[]string{"color"}, "color"},
	{[]string{"currency"}, "currency"},
	{[]string{"uuid"}, "uuid"}, {[]string{"guid"}, "uuid"},
	{[]string{"price"}, "price"}, {[]string{"amount"}, "price"}, {[]string{"cost"}, "price"},
	{[]string{"total"}, "price"}, {[]string{"subtotal"}, "price"}, {[]string{"balance"}, "price"},
	{[]string{"salary"}, "price"}, {[]string{"fee"}, "price"}, {[]string{"revenue"}, "price"},
	{[]string{"wage"}, "price"}, {[]string{"budget"}, "price"}, {[]string{"tax"}, "price"},
	{[]string{"quantity"}, "quantity"}, {[]string{"qty"}, "quantity"}, {[]string{"stock"}, "quantity"},
	{[]string{"count"}, "quantity"}, {[]string{"units"}, "quantity"},
	{[]string{"age"}, "age"},
	{[]string{"rating"}, "rating"}, {[]string{"stars"}, "rating"},
	{[]string{"percent"}, "percent"}, {[]string{"percentage"}, "percent"}, {[]string{"pct"}, "percent"},
	{[]string{"ratio"}, "percent"},
	{[]string{"year"}, "year"},
	{[]string{"birth"}, "birthDate"}, {[]string{"birthday"}, "birthDate"}, {[]string{"birthdate"}, "birthDate"},
	{[]string{"dob"}, "birthDate"},
	{[]string{"expires"}, "futureMoment"}, {[]string{"expiry"}, "futureMoment"}, {[]string{"due"}, "futureMoment"},
	{[]string{"until"}, "futureMoment"}, {[]string{"deadline"}, "futureMoment"},
	{[]string{"scheduled"}, "futureMoment"}, {[]string{"starts"}, "futureMoment"}, {[]string{"ends"}, "futureMoment"},
	{[]string{"code"}, "code"}, {[]string{"sku"}, "code"}, {[]string{"ref"}, "code"}, {[]string{"isbn"}, "code"},
	{[]string{"barcode"}, "code"}, {[]string{"ean"}, "code"}, {[]string{"upc"}, "code"},
	{[]string{"reference"}, "code"}, {[]string{"serial"}, "code"}, {[]string{"token"}, "code"},
	{[]string{"title"}, "title"}, {[]string{"subject"}, "title"}, {[]string{"headline"}, "title"},
	{[]string{"heading"}, "title"}, {[]string{"label"}, "title"}, {[]string{"caption"}, "title"},
	{[]string{"description"}, "paragraph"}, {[]string{"summary"}, "paragraph"}, {[]string{"bio"}, "paragraph"},
	{[]string{"about"}, "paragraph"}, {[]string{"body"}, "paragraph"}, {[]string{"content"}, "paragraph"},
	{[]string{"notes"}, "paragraph"}, {[]string{"note"}, "paragraph"}, {[]string{"comment"}, "paragraph"},
	{[]string{"message"}, "paragraph"}, {[]string{"details"}, "paragraph"}, {[]string{"text"}, "paragraph"},
	{[]string{"tag"}, "word"}, {[]string{"category"}, "word"}, {[]string{"kind"}, "word"},
	{[]string{"type"}, "word"}, {[]string{"status"}, "word"}, {[]string{"state"}, "word"},
	{[]string{"genre"}, "word"}, {[]string{"keyword"}, "word"},
}

// seedMomentWords mark a date or time column whose moments are behind us.
var seedMomentWords = map[string]bool{
	"at": true, "on": true, "date": true, "time": true, "timestamp": true, "created": true,
	"updated": true, "deleted": true, "modified": true, "changed": true, "since": true,
	"published": true, "seen": true, "joined": true, "registered": true, "logged": true,
}

// seedFlagWords mark a whole-number column that holds a yes or a no.
var seedFlagWords = map[string]bool{
	"is": true, "has": true, "can": true, "flag": true, "enabled": true, "active": true,
	"deleted": true, "verified": true, "published": true, "visible": true, "archived": true,
	"allow": true, "allowed": true, "disabled": true, "locked": true,
}

// inferSeedGenerator is the generator a column opens with.
func inferSeedGenerator(c *seedColumn, table string) *seedGenerator {
	fits := func(id string) *seedGenerator {
		if g := seedGeneratorByID[id]; g != nil && g.fits(c) {
			return g
		}
		return nil
	}
	if len(c.values) > 0 {
		return seedGeneratorByID["choice"]
	}
	words := seedNameWords(c.name)
	has := map[string]bool{}
	for _, w := range words {
		has[w] = true
	}
	tableWords := seedNameWords(table)
	last := ""
	if len(tableWords) > 0 {
		last = tableWords[len(tableWords)-1]
	}

	switch {
	case c.t.Kind == ormBool:
		return seedGeneratorByID["boolean"]
	case c.isWhole() && len(words) > 0 && (seedFlagWords[words[0]] || (c.t.Kind == ormInt8 && seedFlagWords[words[len(words)-1]])):
		return seedGeneratorByID["boolean"]
	case c.t.Kind == ormUUID,
		// Thirty-six characters exactly, or sixteen bytes, is how a UUID is
		// kept where the engine has no type for one.
		c.t.Kind == ormChar && c.t.Length == 36,
		c.t.Kind == ormBytes && c.t.Length == 16 && c.t.Name != "rowversion":
		return seedGeneratorByID["uuid"]
	case c.t.Kind == ormYear:
		return seedGeneratorByID["year"]
	case c.t.Kind == ormTime:
		return seedGeneratorByID["time"]
	}

	if len(words) == 1 && words[0] == "name" && c.isText() {
		switch {
		case seedPeopleTables[last]:
			return seedGeneratorByID["fullName"]
		case seedProductTables[last]:
			return seedGeneratorByID["productName"]
		case seedCompanyTables[last]:
			return seedGeneratorByID["company"]
		case last == "city" || last == "cities":
			return seedGeneratorByID["city"]
		case last == "country" || last == "countries":
			return seedGeneratorByID["country"]
		case seedWordTables[last]:
			return seedGeneratorByID["word"]
		}
		return seedGeneratorByID["title"]
	}

	for _, rule := range seedNameRules {
		match := true
		for _, w := range rule.all {
			if !has[w] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if g := fits(rule.gen); g != nil {
			if rule.gen == "paragraph" && c.maxLength() > 0 && c.maxLength() < 200 {
				return seedGeneratorByID["sentence"]
			}
			if rule.gen == "country" && c.maxLength() > 0 && c.maxLength() <= 3 {
				return seedGeneratorByID["countryCode"]
			}
			return g
		}
	}

	if c.isMoment() {
		for _, w := range words {
			if seedMomentWords[w] {
				return seedGeneratorByID["pastMoment"]
			}
		}
		return seedGeneratorByID["pastMoment"]
	}

	switch {
	case c.isText():
		if c.maxLength() > 0 && c.maxLength() < 4 {
			return seedGeneratorByID["code"]
		}
		return seedGeneratorByID["text"]
	case c.isWhole():
		if c.unique {
			return seedGeneratorByID["sequence"]
		}
		return seedGeneratorByID["integer"]
	case c.isFraction():
		return seedGeneratorByID["decimal"]
	}
	for _, id := range []string{"ipAddress", "json", "bytes"} {
		if g := fits(id); g != nil {
			return g
		}
	}
	return nil
}

// --- what a CHECK allows -----------------------------------------------------

// A CHECK is SQL, and reading SQL in general is not something to do here. Two
// shapes cover most of what a schema checks a single column for, and both are
// read: the column is one of a list, and the column lies between bounds.
// Anything else is left to the table, which refuses a row that breaks it,
// and the seed leaves that row out and says so.

var (
	// seedCheckList is `col IN ('a', 'b')` in any engine's spelling, the
	// column quoted or not, cast or not: PostgreSQL prints the same check as
	// `col = ANY (ARRAY['a', 'b'])`.
	seedCheckIn  = regexp.MustCompile(`(?is)^\(*\s*[\x60"\[]?([\w$]+)[\x60"\]]?\s*\)?(?:::[\w ]+)?\)?\s+in\s*\((.*)\)\s*\)*$`)
	seedCheckAny = regexp.MustCompile(`(?is)^\(*\s*"?([\w$]+)"?\s*\)?(?:::[\w ]+)?\)?\s*=\s*any\s*\(+\s*array\s*\[(.*?)\]\s*\)*(?:::[\w \[\]]+)?\)*$`)
	// seedCheckCompare is one comparison of a column with a number.
	seedCheckCompare = regexp.MustCompile(`(?is)^\(*\s*[\x60"\[]?([\w$]+)[\x60"\]]?\s*\)?\s*(>=|<=|>|<)\s*\(*\s*(-?[0-9]+(?:\.[0-9]+)?)\s*\)*(?:::[\w ]+)?\)*$`)
	seedCheckBetween = regexp.MustCompile(`(?is)^\(*\s*[\x60"\[]?([\w$]+)[\x60"\]]?\s*\)?\s+between\s+\(*(-?[0-9]+(?:\.[0-9]+)?)\)*(?:::[\w ]+)?\s+and\s+\(*(-?[0-9]+(?:\.[0-9]+)?)\)*(?:::[\w ]+)?\)*$`)
	// seedCheckEquals is one column equal to one value.
	seedCheckEquals = regexp.MustCompile(`(?is)^\(*\s*[\x60"\[]?([\w$]+)[\x60"\]]?\s*\)?(?:::[\w ]+)?\)?\s*=\s*(.+)$`)
	// seedCheckLiteral is one item of a list: a quoted text, with the cast or
	// the character-set introducer an engine prints around it, or a number.
	seedCheckLiteral = regexp.MustCompile(`(?s)^\(*\s*(?:_\w+)?(?:N)?'((?:[^']|'')*)'\s*\)*(?:::[\w \[\]]+)?\)*$|^\(*\s*(-?[0-9]+(?:\.[0-9]+)?)\s*\)*(?:::[\w ]+)?$`)
)

// seedCheck is what a table's checks say about one column.
type seedCheck struct {
	values   []string
	lo, hi   *float64
	exclLo   bool
	exclHi   bool
	numbered bool
}

// readSeedChecks reads the checks a table's definition carries into what each
// column is held to. definitions are each CHECK's condition, with or without
// the CHECK keyword around it.
func readSeedChecks(definitions []string) map[string]*seedCheck {
	out := map[string]*seedCheck{}
	get := func(col string) *seedCheck {
		key := strings.ToLower(col)
		if out[key] == nil {
			out[key] = &seedCheck{}
		}
		return out[key]
	}
	for _, def := range definitions {
		body := strings.TrimSpace(def)
		if upper := strings.ToUpper(body); strings.HasPrefix(upper, "CHECK") {
			body = strings.TrimSpace(body[len("CHECK"):])
		}
		body = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(body, "NOT VALID")), ";")
		for _, part := range splitTopLevelAnd(stripOuterParens(body)) {
			part = strings.TrimSpace(part)
			if col, values, numbered, ok := seedCheckEither(part); ok {
				c := get(col)
				c.values, c.numbered = values, numbered
				continue
			}
			if m := seedCheckIn.FindStringSubmatch(part); m != nil {
				if values, numbered, ok := seedCheckValues(m[2]); ok {
					c := get(m[1])
					c.values, c.numbered = values, numbered
				}
				continue
			}
			if m := seedCheckAny.FindStringSubmatch(part); m != nil {
				if values, numbered, ok := seedCheckValues(m[2]); ok {
					c := get(m[1])
					c.values, c.numbered = values, numbered
				}
				continue
			}
			if m := seedCheckBetween.FindStringSubmatch(part); m != nil {
				lo, err1 := strconv.ParseFloat(m[2], 64)
				hi, err2 := strconv.ParseFloat(m[3], 64)
				if err1 == nil && err2 == nil {
					c := get(m[1])
					c.lo, c.hi = &lo, &hi
				}
				continue
			}
			if m := seedCheckCompare.FindStringSubmatch(part); m != nil {
				v, err := strconv.ParseFloat(m[3], 64)
				if err != nil {
					continue
				}
				c := get(m[1])
				switch m[2] {
				case ">":
					c.lo, c.exclLo = &v, true
				case ">=":
					c.lo, c.exclLo = &v, false
				case "<":
					c.hi, c.exclHi = &v, true
				case "<=":
					c.hi, c.exclHi = &v, false
				}
			}
		}
	}
	return out
}

// seedCheckEither reads a column that equals one value or another — which is
// how SQL Server keeps an IN list it was given — into the list of them.
func seedCheckEither(part string) (col string, values []string, numbered, ok bool) {
	alternatives := splitTopLevelWord(part, "OR")
	if len(alternatives) < 2 {
		return "", nil, false, false
	}
	var literals []string
	for _, alt := range alternatives {
		m := seedCheckEquals.FindStringSubmatch(stripOuterParens(alt))
		if m == nil || (col != "" && !strings.EqualFold(col, m[1])) {
			return "", nil, false, false
		}
		col = m[1]
		literals = append(literals, m[2])
	}
	values, numbered, ok = seedCheckValues(strings.Join(literals, ","))
	if !ok {
		return "", nil, false, false
	}
	return col, values, numbered, true
}

// seedCheckValues reads the items of a list, all texts or all numbers.
func seedCheckValues(list string) ([]string, bool, bool) {
	var values []string
	texts, numbers := 0, 0
	for _, item := range splitTopLevelComma(list) {
		m := seedCheckLiteral.FindStringSubmatch(strings.TrimSpace(item))
		if m == nil {
			return nil, false, false
		}
		if m[2] != "" {
			numbers++
			values = append(values, m[2])
			continue
		}
		texts++
		values = append(values, strings.ReplaceAll(m[1], "''", "'"))
	}
	if len(values) == 0 || (texts > 0 && numbers > 0) {
		return nil, false, false
	}
	return values, numbers > 0, true
}

// stripOuterParens takes off parentheses that wrap the whole condition.
func stripOuterParens(s string) string {
	for {
		s = strings.TrimSpace(s)
		if len(s) < 2 || s[0] != '(' || s[len(s)-1] != ')' {
			return s
		}
		depth, closes := 0, -1
		quoted := false
		for i := 0; i < len(s); i++ {
			switch {
			case s[i] == '\'':
				quoted = !quoted
			case quoted:
			case s[i] == '(':
				depth++
			case s[i] == ')':
				depth--
				if depth == 0 {
					closes = i
					i = len(s)
				}
			}
		}
		if closes != len(s)-1 {
			return s
		}
		s = s[1 : len(s)-1]
	}
}

// splitTopLevelAnd splits a condition at the ANDs that are not inside
// parentheses or quotes, and not the AND of a BETWEEN.
func splitTopLevelAnd(s string) []string {
	var parts []string
	depth, quoted, start := 0, false, 0
	between := false
	upper := strings.ToUpper(s)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			quoted = !quoted
		case quoted:
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
		case depth == 0 && seedKeywordAt(upper, i, "BETWEEN"):
			between = true
		case depth == 0 && seedKeywordAt(upper, i, "AND"):
			if between {
				between = false
				continue
			}
			parts = append(parts, stripOuterParens(s[start:i]))
			start = i + len("AND")
		}
	}
	return append(parts, stripOuterParens(s[start:]))
}

// splitTopLevelWord splits a condition at a keyword outside parentheses and
// quotes.
func splitTopLevelWord(s, word string) []string {
	var parts []string
	depth, quoted, start := 0, false, 0
	upper := strings.ToUpper(s)
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			quoted = !quoted
		case quoted:
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
		case depth == 0 && seedKeywordAt(upper, i, word):
			parts = append(parts, s[start:i])
			start = i + len(word)
		}
	}
	return append(parts, s[start:])
}

func seedKeywordAt(upper string, i int, word string) bool {
	if !strings.HasPrefix(upper[i:], word) {
		return false
	}
	before := i == 0 || !isWordByte(upper[i-1])
	after := i+len(word) >= len(upper) || !isWordByte(upper[i+len(word)])
	return before && after
}

func splitTopLevelComma(s string) []string {
	var parts []string
	depth, quoted, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			quoted = !quoted
		case quoted:
		case s[i] == '(' || s[i] == '[':
			depth++
		case s[i] == ')' || s[i] == ']':
			depth--
		case s[i] == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// checkClauses finds each CHECK's condition in a CREATE statement, for the
// engines whose catalogue the generator read keeps a table's checks only
// there (SQLite, MySQL).
func checkClauses(create string) []string {
	var out []string
	upper := strings.ToUpper(create)
	for i := 0; i < len(upper); i++ {
		if !seedKeywordAt(upper, i, "CHECK") {
			continue
		}
		j := i + len("CHECK")
		for j < len(create) && (create[j] == ' ' || create[j] == '\n' || create[j] == '\t' || create[j] == '\r') {
			j++
		}
		if j >= len(create) || create[j] != '(' {
			continue
		}
		depth, quoted := 0, false
		for k := j; k < len(create); k++ {
			switch {
			case create[k] == '\'':
				quoted = !quoted
			case quoted:
			case create[k] == '(':
				depth++
			case create[k] == ')':
				depth--
				if depth == 0 {
					out = append(out, create[j:k+1])
					i = k
					k = len(create)
				}
			}
		}
	}
	return out
}
