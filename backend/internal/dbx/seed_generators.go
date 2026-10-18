package dbx

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The generators a seed column can be filled by.
//
// Each one makes a value as the text a file being imported would carry, so
// a seeded row reaches the table by the same road an imported one does
// (bindValue, and on SQL Server the conversion written around each marker):
// one implementation of "how does this engine take a date", proven by the
// import's own tests, rather than a second one here.
//
// A generator is pure over its column and its random stream. Row n's value
// depends on the seed, the table, the column and n, and on nothing else —
// not on another table's row count, not on the order the tables are written
// in — which is what lets the preview show the rows that will be written.

// Generator ids the plan and the request share.
const (
	seedAuto      = "auto"
	seedComputed  = "computed"
	seedDefault   = "default"
	seedNull      = "null"
	seedReference = "reference"
)

// seedGroup orders the generators in the picker.
var seedGroups = []string{"Person", "Place", "Business", "Internet", "Text", "Number", "Time", "Other", "Server"}

type seedGenerator struct {
	id    string
	label string
	group string
	// fits says the generator writes values a column of this type takes.
	fits func(c *seedColumn) bool
	// value makes row n's value. null is a NULL.
	value func(c *seedColumn, r *rand.Rand, row *seedRow, n int) (text string, null bool)
	// distinct is how the generator keeps a unique column's values apart.
	distinct seedDistinct
}

// seedDistinct is how a generator's values are kept apart in a unique column.
type seedDistinct int

const (
	// seedSuffix appends the row's own number to the value.
	seedSuffix seedDistinct = iota
	// seedCounting counts up from past the largest value already there.
	seedCounting
	// seedApart moves each row's moment one step from the last.
	seedApart
	// seedInherent values are apart already: a random UUID.
	seedInherent
	// seedFew has a handful of values, and a unique column gets as many rows
	// as there are values.
	seedFew
	// seedNotDistinct is a fill that writes no value of its own.
	seedNotDistinct
)

// seedRow is what the columns of one row share, so that its name, its email
// and its username are one person's, and its city and its country agree.
type seedRow struct {
	first, last string
	place       seedCity
	street      string
	domain      string
}

func newSeedRow(r *rand.Rand) *seedRow {
	return &seedRow{
		first:  seedPick(r, seedFirstNames),
		last:   seedPick(r, seedLastNames),
		place:  seedCities[r.IntN(len(seedCities))],
		street: fmt.Sprintf("%d %s %s", 1+r.IntN(240), seedPick(r, seedStreetNames), seedPick(r, seedStreetKinds)),
		domain: seedPick(r, seedEmailDomains),
	}
}

func seedPick(r *rand.Rand, list []string) string { return list[r.IntN(len(list))] }

// --- what a column takes ---------------------------------------------------

func (c *seedColumn) isText() bool {
	switch c.t.Kind {
	case ormChar, ormVarchar, ormText:
		return !c.t.Array
	}
	return false
}

func (c *seedColumn) isWhole() bool {
	if c.t.Array {
		return false
	}
	switch c.t.Kind {
	case ormInt8, ormInt16, ormInt32, ormInt64, ormBigNum:
		return true
	}
	return false
}

func (c *seedColumn) isNumber() bool {
	if c.t.Array {
		return false
	}
	switch c.t.Kind {
	case ormInt8, ormInt16, ormInt32, ormInt64, ormBigNum, ormDecimal, ormFloat32, ormFloat64, ormMoney:
		return true
	}
	return false
}

func (c *seedColumn) isFraction() bool {
	if c.t.Array {
		return false
	}
	switch c.t.Kind {
	case ormDecimal, ormFloat32, ormFloat64, ormMoney:
		return true
	}
	return false
}

func (c *seedColumn) isMoment() bool {
	if c.t.Array {
		return false
	}
	switch c.t.Kind {
	case ormDate, ormDateTime, ormDateTimeTZ:
		return true
	}
	return false
}

func textual(c *seedColumn) bool { return c.isText() }

func textOr(kinds ...ormKind) func(c *seedColumn) bool {
	return func(c *seedColumn) bool {
		if c.isText() {
			return true
		}
		for _, k := range kinds {
			if c.t.Kind == k && !c.t.Array {
				return true
			}
		}
		return false
	}
}

// --- the catalogue ---------------------------------------------------------

var seedGenerators = []*seedGenerator{
	// Person
	{id: "firstName", label: "First name", group: "Person", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.first })},
	{id: "lastName", label: "Last name", group: "Person", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.last })},
	{id: "fullName", label: "Full name", group: "Person", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.first + " " + row.last })},
	{id: "username", label: "Username", group: "Person", fits: textual,
		value: textValue(func(r *rand.Rand, row *seedRow) string {
			base := strings.ToLower(row.first)
			switch r.IntN(3) {
			case 0:
				return base + strings.ToLower(row.last[:1])
			case 1:
				return base + "_" + strings.ToLower(row.last)
			}
			return base + strconv.Itoa(10+r.IntN(90))
		})},
	{id: "email", label: "Email address", group: "Person", fits: textual, value: emailValue},
	{id: "phone", label: "Phone number", group: "Person", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			if r.IntN(2) == 0 {
				return fmt.Sprintf("+1 555-01%02d", r.IntN(100))
			}
			return fmt.Sprintf("+44 7700 900%03d", r.IntN(1000))
		})},
	{id: "jobTitle", label: "Job title", group: "Person", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return seedPick(r, seedJobTitles) })},
	{id: "birthDate", label: "Date of birth", group: "Person", fits: textOr(ormDate, ormDateTime, ormDateTimeTZ),
		value: momentValue(func(r *rand.Rand, anchor time.Time) time.Time {
			return anchor.AddDate(-18-r.IntN(62), 0, -r.IntN(365))
		}, false), distinct: seedApart},
	{id: "age", label: "Age", group: "Person", fits: (*seedColumn).isNumber,
		value: wholeValue(18, 90), distinct: seedCounting},

	// Place
	{id: "street", label: "Street address", group: "Place", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.street })},
	{id: "city", label: "City", group: "Place", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.place.city })},
	{id: "country", label: "Country", group: "Place", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.place.country })},
	{id: "countryCode", label: "Country code", group: "Place", fits: textual,
		value: textValue(func(_ *rand.Rand, row *seedRow) string { return row.place.code })},
	{id: "postalCode", label: "Postal code", group: "Place", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return fmt.Sprintf("%05d", 1000+r.IntN(98999)) })},
	{id: "latitude", label: "Latitude", group: "Place", fits: (*seedColumn).isFraction,
		value: fractionValue(-90, 90, 6), distinct: seedCounting},
	{id: "longitude", label: "Longitude", group: "Place", fits: (*seedColumn).isFraction,
		value: fractionValue(-180, 180, 6), distinct: seedCounting},

	// Business
	{id: "company", label: "Company", group: "Business", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			return seedPick(r, seedCompanyWords) + " " + seedPick(r, seedCompanySuffixes)
		})},
	{id: "productName", label: "Product name", group: "Business", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			return seedPick(r, seedProductAdjectives) + " " + seedPick(r, seedProductMaterials) + " " + seedPick(r, seedProductNouns)
		})},
	{id: "price", label: "Price", group: "Business", fits: (*seedColumn).isNumber,
		value: fractionValue(1, 1000, 2), distinct: seedCounting},
	{id: "quantity", label: "Quantity", group: "Business", fits: (*seedColumn).isNumber,
		value: wholeValue(1, 20), distinct: seedCounting},
	{id: "currency", label: "Currency code", group: "Business", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return seedPick(r, seedCurrencies) })},
	{id: "code", label: "Code", group: "Business", fits: textual, value: codeValue},

	// Internet
	{id: "url", label: "Web address", group: "Internet", fits: textual, value: urlValue("https://www.", "")},
	{id: "imageUrl", label: "Image address", group: "Internet", fits: textual, value: urlValue("https://images.", ".jpg")},
	{id: "ipAddress", label: "IP address", group: "Internet", fits: ipColumn, value: ipValue},
	{id: "slug", label: "Slug", group: "Internet", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			return seedPick(r, seedTitleWords) + "-" + seedPick(r, seedTitleWords)
		})},
	{id: "password", label: "Password hash", group: "Internet", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			var b strings.Builder
			b.WriteString("$2b$10$")
			for range 53 {
				b.WriteByte(seedPasswordChars[r.IntN(len(seedPasswordChars))])
			}
			return b.String()
		})},
	{id: "color", label: "Colour", group: "Internet", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return fmt.Sprintf("#%06x", r.IntN(1<<24)) })},
	{id: "uuid", label: "UUID", group: "Internet", fits: uuidColumn, value: uuidValue, distinct: seedInherent},

	// Text
	{id: "word", label: "Word", group: "Text", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return seedPick(r, seedTitleWords) })},
	{id: "title", label: "Title", group: "Text", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return capitalise(words(r, seedTitleWords, 2+r.IntN(3))) })},
	{id: "sentence", label: "Sentence", group: "Text", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return sentence(r) })},
	{id: "paragraph", label: "Paragraph", group: "Text", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string {
			parts := make([]string, 3+r.IntN(3))
			for i := range parts {
				parts[i] = sentence(r)
			}
			return strings.Join(parts, " ")
		})},
	{id: "text", label: "Text", group: "Text", fits: textual,
		value: textValue(func(r *rand.Rand, _ *seedRow) string { return words(r, seedLorem, 2+r.IntN(4)) })},

	// Number
	{id: "integer", label: "Whole number", group: "Number", fits: (*seedColumn).isNumber,
		value: wholeValue(1, 1000), distinct: seedCounting},
	{id: "sequence", label: "Counting up", group: "Number", fits: (*seedColumn).isNumber,
		value: countingValue, distinct: seedCounting},
	{id: "decimal", label: "Number", group: "Number", fits: (*seedColumn).isFraction,
		value: fractionValue(0, 1000, 2), distinct: seedCounting},
	{id: "percent", label: "Percentage", group: "Number", fits: (*seedColumn).isNumber,
		value: fractionValue(0, 100, 1), distinct: seedCounting},
	{id: "rating", label: "Rating", group: "Number", fits: (*seedColumn).isNumber,
		value: wholeValue(1, 5), distinct: seedCounting},
	{id: "year", label: "Year", group: "Number", fits: func(c *seedColumn) bool { return c.isWhole() || c.t.Kind == ormYear },
		value: wholeValue(1990, 2026), distinct: seedCounting},

	// Time
	{id: "pastMoment", label: "In the past two years", group: "Time", fits: textOr(ormDate, ormDateTime, ormDateTimeTZ),
		value: momentValue(func(r *rand.Rand, anchor time.Time) time.Time {
			return anchor.Add(-time.Duration(r.Int64N(int64(730 * 24 * time.Hour))))
		}, false), distinct: seedApart},
	{id: "futureMoment", label: "In the coming year", group: "Time", fits: textOr(ormDate, ormDateTime, ormDateTimeTZ),
		value: momentValue(func(r *rand.Rand, anchor time.Time) time.Time {
			return anchor.Add(time.Duration(1 + r.Int64N(int64(365*24*time.Hour))))
		}, true), distinct: seedApart},
	{id: "time", label: "Time of day", group: "Time", fits: textOr(ormTime),
		value: func(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
			seconds := r.IntN(86400)
			if c.unique {
				seconds = (c.start + n) % 86400
			}
			return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60), false
		}, distinct: seedApart},

	// Other
	{id: "boolean", label: "True or false", group: "Other",
		fits: func(c *seedColumn) bool { return c.t.Kind == ormBool || c.isWhole() },
		value: func(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
			on := r.IntN(2) == 0
			if c.unique {
				on = n%2 == 0
			}
			if c.t.Kind != ormBool {
				return map[bool]string{true: "1", false: "0"}[on], false
			}
			return strconv.FormatBool(on), false
		}, distinct: seedFew},
	{id: "choice", label: "One of its values", group: "Other",
		fits:     func(c *seedColumn) bool { return len(c.values) > 0 },
		value:    choiceValue,
		distinct: seedFew},
	{id: "json", label: "JSON object", group: "Other", fits: jsonColumn, value: jsonValue, distinct: seedSuffix},
	{id: "bytes", label: "Bytes", group: "Other",
		fits: func(c *seedColumn) bool { return c.t.Kind == ormBytes && !c.t.Array && c.t.Name != "rowversion" },
		value: func(c *seedColumn, r *rand.Rand, _ *seedRow, _ int) (string, bool) {
			size := 16
			if c.t.Length > 0 && c.t.Length < size {
				size = c.t.Length
			}
			b := make([]byte, size)
			for i := range b {
				b[i] = byte(r.IntN(256))
			}
			return `\x` + hex.EncodeToString(b), false
		}, distinct: seedInherent},

	// Server: what is left to the engine, or chosen once the rows are written.
	{id: seedAuto, label: "Numbered by the server", group: "Server", distinct: seedNotDistinct},
	{id: seedComputed, label: "Worked out by the server", group: "Server", distinct: seedNotDistinct},
	{id: seedDefault, label: "Its default", group: "Server", distinct: seedNotDistinct},
	{id: seedNull, label: "Left empty", group: "Server", distinct: seedNotDistinct},
	{id: seedReference, label: "A row it points at", group: "Server", distinct: seedNotDistinct},
}

var seedGeneratorByID = func() map[string]*seedGenerator {
	out := make(map[string]*seedGenerator, len(seedGenerators))
	for _, g := range seedGenerators {
		out[g.id] = g
	}
	return out
}()

// writesValue is true of a generator that makes the column's value itself.
func (g *seedGenerator) writesValue() bool { return g.value != nil }

// --- value makers ----------------------------------------------------------

// textValue makes a text, cut to the column's length and, in a unique column,
// with the row's own number after it.
func textValue(draw func(r *rand.Rand, row *seedRow) string) func(*seedColumn, *rand.Rand, *seedRow, int) (string, bool) {
	return func(c *seedColumn, r *rand.Rand, row *seedRow, n int) (string, bool) {
		text := draw(r, row)
		if c.unique {
			return c.fitDistinct(text, " ", c.start+n), false
		}
		return c.fit(text), false
	}
}

func emailValue(c *seedColumn, r *rand.Rand, row *seedRow, n int) (string, bool) {
	local := strings.ToLower(row.first + "." + row.last)
	if c.unique {
		local += "." + strconv.Itoa(c.start+n)
	} else if r.IntN(3) == 0 {
		local += strconv.Itoa(1 + r.IntN(99))
	}
	address := local + "@" + row.domain
	max := c.maxLength()
	if max <= 0 || utf8.RuneCountInString(address) <= max {
		return address, false
	}
	// What is cut is the name, never the number that keeps it apart or the
	// domain that makes it an address — until there is no room for both, when
	// a unique column keeps the number.
	keep := max - len(row.domain) - 1
	if !c.unique {
		if keep < 1 {
			return c.fit(address), false
		}
		return clipRunes(local, keep) + "@" + row.domain, false
	}
	number := "." + strconv.Itoa(c.start+n)
	if keep <= len(number) {
		return c.fitDistinct("", "", c.start+n), false
	}
	return clipRunes(strings.ToLower(row.first+"."+row.last), keep-len(number)) + number + "@" + row.domain, false
}

func codeValue(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
	var b strings.Builder
	if c.unique {
		// A prefix that reads like a code, and the row's number in base 32
		// after it: apart from every other row's, and as short as it can be.
		b.WriteByte(seedCodeChars[r.IntN(len(seedCodeChars))])
		b.WriteByte(seedCodeChars[r.IntN(len(seedCodeChars))])
		b.WriteString("-")
		b.WriteString(strings.ToUpper(strconv.FormatInt(int64(c.start+n), 32)))
		text := b.String()
		if max := c.maxLength(); max > 0 && len(text) > max {
			text = strings.ToUpper(strconv.FormatInt(int64(c.start+n), 32))
		}
		return c.fit(text), false
	}
	for range 4 {
		b.WriteByte(seedCodeChars[r.IntN(len(seedCodeChars))])
	}
	b.WriteString("-")
	for range 4 {
		b.WriteByte(seedCodeChars[r.IntN(len(seedCodeChars))])
	}
	return c.fit(b.String()), false
}

func urlValue(prefix, suffix string) func(*seedColumn, *rand.Rand, *seedRow, int) (string, bool) {
	return func(c *seedColumn, r *rand.Rand, row *seedRow, n int) (string, bool) {
		path := seedPick(r, seedTitleWords) + "-" + seedPick(r, seedTitleWords)
		if c.unique {
			path += "-" + strconv.Itoa(c.start+n)
		}
		return c.fit(prefix + row.domain + "/" + path + suffix), false
	}
}

func ipColumn(c *seedColumn) bool {
	if c.isText() {
		return true
	}
	if c.t.Kind != ormNet || c.t.Array {
		return false
	}
	switch c.t.Name {
	case "inet", "cidr", "inet4", "ipv4":
		return true
	}
	return false
}

// ipValue is an address in a range set aside for documentation; a unique
// column's are counted through the benchmark range, which has room for more.
func ipValue(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
	if c.unique {
		at := (c.start + n) % (1 << 17)
		return c.fit(fmt.Sprintf("198.%d.%d.%d", 18+at>>16, at>>8&0xff, at&0xff)), false
	}
	ranges := []string{"192.0.2", "198.51.100", "203.0.113"}
	return c.fit(fmt.Sprintf("%s.%d", seedPick(r, ranges), 1+r.IntN(254))), false
}

func uuidColumn(c *seedColumn) bool {
	if c.t.Array {
		return false
	}
	switch c.t.Kind {
	case ormUUID:
		return true
	case ormBytes:
		return c.t.Length == 16 && c.t.Name != "rowversion"
	}
	return c.isText() && (c.maxLength() <= 0 || c.maxLength() >= 36)
}

func uuidValue(c *seedColumn, r *rand.Rand, _ *seedRow, _ int) (string, bool) {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	if c.t.Kind == ormBytes {
		return `\x` + hex.EncodeToString(b[:]), false
	}
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], false
}

func jsonColumn(c *seedColumn) bool {
	if c.t.Kind != ormJSON || c.t.Array {
		return false
	}
	// A ClickHouse "JSON" is a Map, a Tuple or Nested as often as a document,
	// and Oracle's binds through a type of its own: neither takes a text.
	switch c.driver {
	case DriverPostgres, DriverMySQL, DriverSQLite:
		return true
	}
	return false
}

func jsonValue(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
	text := fmt.Sprintf(`{"label": %q, "count": %d, "active": %t}`,
		seedPick(r, seedTitleWords), r.IntN(100), r.IntN(2) == 0)
	if c.unique {
		text = fmt.Sprintf(`{"label": %q, "n": %d}`, seedPick(r, seedTitleWords), c.start+n)
	}
	return text, false
}

func choiceValue(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
	if c.t.Kind == ormSet {
		// Some of a set's members, in the order the type lists them.
		var on []string
		for _, v := range c.values {
			if r.IntN(2) == 0 {
				on = append(on, v)
			}
		}
		if len(on) == 0 {
			on = []string{c.values[r.IntN(len(c.values))]}
		}
		return strings.Join(on, ","), false
	}
	if c.unique {
		return c.values[n%len(c.values)], false
	}
	return c.values[r.IntN(len(c.values))], false
}

// wholeValue makes a whole number in [lo, hi], narrowed to what the column's
// type and its checks take; a unique column counts up instead.
func wholeValue(lo, hi float64) func(*seedColumn, *rand.Rand, *seedRow, int) (string, bool) {
	return func(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
		if c.unique {
			return countingValue(c, r, nil, n)
		}
		lo, hi := c.within(lo, hi, true)
		return strconv.FormatInt(int64(lo)+r.Int64N(int64(hi-lo)+1), 10), false
	}
}

// fractionValue makes a number in [lo, hi] with up to digits places, as many
// as the column's scale allows; a whole-number column gets whole numbers.
func fractionValue(lo, hi float64, digits int) func(*seedColumn, *rand.Rand, *seedRow, int) (string, bool) {
	return func(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
		if c.unique {
			return countingValue(c, r, nil, n)
		}
		places := c.places(digits)
		lo, hi := c.within(lo, hi, places == 0)
		v := lo + r.Float64()*(hi-lo)
		if places == 0 {
			return strconv.FormatInt(int64(math.Round(v)), 10), false
		}
		scale := math.Pow(10, float64(places))
		// Rounded inside the range: hi itself is the largest value the
		// column's precision holds, and rounding up past it would not fit.
		v = math.Floor(v*scale) / scale
		return strconv.FormatFloat(v, 'f', places, 64), false
	}
}

// countingValue is the row's number counted from past the largest value the
// column held when the seed began.
func countingValue(c *seedColumn, _ *rand.Rand, _ *seedRow, n int) (string, bool) {
	return strconv.FormatInt(int64(c.start+n), 10), false
}

// momentValue makes a moment as the column's type spells one. future marks a
// generator whose moments are after the anchor, so a unique column's steps
// go forward from it rather than back.
func momentValue(draw func(r *rand.Rand, anchor time.Time) time.Time, future bool) func(*seedColumn, *rand.Rand, *seedRow, int) (string, bool) {
	return func(c *seedColumn, r *rand.Rand, _ *seedRow, n int) (string, bool) {
		t := draw(r, c.anchor)
		if c.unique {
			step := time.Minute
			if c.t.Kind == ormDate {
				step = 24 * time.Hour
			}
			if future {
				t = c.anchor.Add(time.Duration(c.start+n) * step)
			} else {
				t = c.anchor.Add(-time.Duration(c.start+n) * step)
			}
		}
		return c.moment(t), false
	}
}

// moment spells t the way each engine reads it for the column's type,
// whatever the session's language and date format: the date, a time to the
// second, and a zone only where the type keeps one and the engine reads one.
func (c *seedColumn) moment(t time.Time) string {
	t = t.UTC().Truncate(time.Second)
	switch c.t.Kind {
	case ormDate:
		return t.Format("2006-01-02")
	case ormDateTimeTZ:
		switch c.driver {
		case DriverPostgres, DriverMSSQL, DriverOracle:
			return t.Format("2006-01-02T15:04:05Z")
		}
	}
	if c.driver == DriverMSSQL {
		// datetime reads 2026-03-04 10:00:00 as the third of April under a
		// language that puts the day first; with the T it is read the same
		// way under every one.
		return t.Format("2006-01-02T15:04:05")
	}
	return t.Format("2006-01-02 15:04:05")
}

// --- fitting a value to its column -------------------------------------------

// maxLength is the most characters the column holds, 0 where nothing says.
func (c *seedColumn) maxLength() int {
	if c.t.Length > 0 && (c.t.Kind == ormChar || c.t.Kind == ormVarchar) {
		return c.t.Length
	}
	return 0
}

func (c *seedColumn) fit(text string) string {
	if max := c.maxLength(); max > 0 {
		return clipRunes(text, max)
	}
	return text
}

// fitDistinct is the text with the row's number after it, the text cut short
// rather than the number where the column cannot hold both.
func (c *seedColumn) fitDistinct(text, sep string, number int) string {
	tail := sep + strconv.Itoa(number)
	max := c.maxLength()
	if max <= 0 || utf8.RuneCountInString(text)+len(tail) <= max {
		return text + tail
	}
	if max <= len(tail) {
		return clipRunes(strconv.Itoa(number), max)
	}
	return clipRunes(text, max-len(tail)) + tail
}

func clipRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return strings.TrimRight(string([]rune(text)[:max]), " -.")
}

// places is how many digits after the point a generated number has: as many
// as it asks for, no more than the column keeps.
func (c *seedColumn) places(want int) int {
	switch c.t.Kind {
	case ormDecimal:
		if c.t.HasPrecision {
			return min(want, c.t.Scale)
		}
	case ormFloat32, ormFloat64, ormMoney:
	default:
		return 0
	}
	return want
}

// within narrows [lo, hi] to what the column's type holds and what its checks
// allow. Where the two leave nothing in common the column's own bounds win:
// a rating generator on a column that must be over 100 still writes a value
// the table takes.
func (c *seedColumn) within(lo, hi float64, whole bool) (float64, float64) {
	tlo, thi := c.typeRange()
	if c.lo != nil {
		tlo = math.Max(tlo, *c.lo)
	}
	if c.hi != nil {
		thi = math.Min(thi, *c.hi)
	}
	if whole {
		tlo, thi = math.Ceil(tlo), math.Floor(thi)
	}
	nlo, nhi := math.Max(lo, tlo), math.Min(hi, thi)
	if nlo > nhi {
		nlo, nhi = tlo, math.Min(thi, tlo+1000)
	}
	if nlo > nhi {
		nhi = nlo
	}
	return nlo, nhi
}

// typeRange is the smallest and largest value the column's type holds, kept
// to what a float carries exactly.
func (c *seedColumn) typeRange() (float64, float64) {
	const limit = 1 << 53
	switch c.t.Kind {
	case ormInt8:
		if c.t.Unsigned {
			return 0, 255
		}
		return -128, 127
	case ormInt16:
		if c.t.Unsigned {
			return 0, 65535
		}
		return -32768, 32767
	case ormInt32:
		if c.t.Unsigned {
			return 0, 4294967295
		}
		return -2147483648, 2147483647
	case ormInt64, ormBigNum:
		if c.t.Unsigned {
			return 0, limit
		}
		return -limit, limit
	case ormYear:
		return 1901, 2155
	case ormDecimal:
		if c.t.HasPrecision && c.t.Precision > c.t.Scale && c.t.Precision-c.t.Scale < 16 {
			top := math.Pow(10, float64(c.t.Precision-c.t.Scale)) - math.Pow(10, -float64(c.t.Scale))
			return -top, top
		}
		if c.t.HasPrecision && c.t.Precision > 0 && c.t.Precision <= c.t.Scale {
			// numeric(3,3): nothing but a fraction.
			return -0.999, 0.999
		}
	case ormMoney:
		if c.t.Name == "smallmoney" {
			return -214748, 214748
		}
	}
	return -limit, limit
}

// --- words -------------------------------------------------------------------

func words(r *rand.Rand, from []string, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = seedPick(r, from)
	}
	return strings.Join(parts, " ")
}

func sentence(r *rand.Rand) string { return capitalise(words(r, seedLorem, 6+r.IntN(8))) + "." }

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
