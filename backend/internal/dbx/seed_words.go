package dbx

// What seed rows are made of.
//
// Every address, number and name here is one that cannot reach anybody: the
// domains are the ones RFC 2606 reserves for examples, the addresses are the
// documentation ranges of RFC 5737 and the benchmark range of RFC 2544, the
// telephone numbers are the ranges set aside for fiction (555-01xx in North
// America, 07700 900xxx in the United Kingdom). A table seeded here and then
// mailed, called or dialled by somebody's code reaches no one.
//
// The names are spelt without accents. A column's length is a count of
// characters on some engines and of bytes on others, and a name that fits
// VARCHAR(20) in one should fit it in all of them.

var seedFirstNames = []string{
	"Olivia", "Liam", "Amara", "Noah", "Sofia", "Mateo", "Aisha", "Lucas", "Mei", "Elijah",
	"Priya", "James", "Zara", "Hiroshi", "Isla", "Omar", "Freya", "Diego", "Leila", "Henry",
	"Ingrid", "Kofi", "Chloe", "Ravi", "Hannah", "Tomas", "Yara", "Samuel", "Nadia", "Felix",
	"Grace", "Arjun", "Elena", "Kwame", "Maya", "Jonas", "Fatima", "Oscar", "Lina", "Daniel",
	"Ana", "Theo", "Sana", "Hugo", "Clara", "Malik", "Emma", "Andrei", "Noor", "Leo",
	"Ines", "Kenji", "Ruby", "Tariq", "Alice", "Emil", "Asha", "Victor", "Lucia", "Ethan",
	"Mila", "Rafael", "Selin", "Adam", "Nora", "Ibrahim", "Eva", "Marco", "Ayumi", "Jack",
	"Julia", "Sipho", "Ella", "Pavel", "Rosa", "Kai", "Dalia", "Finn", "Lea", "Ahmed",
}

var seedLastNames = []string{
	"Martin", "Okafor", "Nguyen", "Garcia", "Kowalski", "Smith", "Haddad", "Tanaka", "Rossi", "Silva",
	"Johansson", "Patel", "Murphy", "Dubois", "Kim", "Novak", "Mensah", "Fischer", "Lopez", "Chen",
	"Ivanova", "Brown", "Sato", "Moreau", "Hansen", "Ali", "Costa", "Walker", "Popescu", "Schmidt",
	"Wilson", "Jensen", "Ahmed", "Bianchi", "Park", "Rahman", "Taylor", "Kovacs", "Moreno", "Wang",
	"Andersen", "Diallo", "Hughes", "Petrov", "Santos", "Becker", "Clarke", "Yilmaz", "Larsen", "Gomez",
	"Romano", "Edwards", "Nakamura", "Mendes", "Horvat", "Owusu", "Bauer", "Reyes", "Lindqvist", "Ward",
}

var seedEmailDomains = []string{"example.com", "example.org", "example.net"}

var seedJobTitles = []string{
	"Software Engineer", "Product Manager", "Data Analyst", "Designer", "Account Executive",
	"Support Specialist", "Operations Lead", "Marketing Manager", "Accountant", "Research Scientist",
	"Site Reliability Engineer", "Recruiter", "Sales Director", "Copywriter", "Office Manager",
	"Quality Engineer", "Customer Success Manager", "Financial Analyst", "Architect", "Nurse",
}

var seedCompanyWords = []string{
	"Northwind", "Bluefield", "Copperline", "Harbor", "Lumen", "Granite", "Juniper", "Meridian",
	"Ironwood", "Silverleaf", "Brightpath", "Redwood", "Tidewater", "Summit", "Kestrel", "Orchard",
	"Cobalt", "Evergreen", "Foxglove", "Atlas",
}

var seedCompanySuffixes = []string{"Labs", "Group", "Ltd", "Systems", "Partners", "Studio", "Works", "Co"}

var seedProductAdjectives = []string{
	"Ergonomic", "Compact", "Rustic", "Sleek", "Durable", "Lightweight", "Handmade", "Classic",
	"Modern", "Vintage", "Portable", "Smart", "Premium", "Recycled", "Soft", "Wireless",
}

var seedProductMaterials = []string{
	"Steel", "Oak", "Cotton", "Leather", "Ceramic", "Glass", "Bamboo", "Wool", "Granite", "Linen",
	"Aluminium", "Walnut",
}

var seedProductNouns = []string{
	"Chair", "Lamp", "Backpack", "Mug", "Desk", "Notebook", "Kettle", "Jacket", "Speaker", "Bottle",
	"Blanket", "Watch", "Shelf", "Headphones", "Wallet", "Planter",
}

// seedCity is a city and the country it is in, so a row's city and country
// can agree.
type seedCity struct {
	city, country, code string
}

var seedCities = []seedCity{
	{"Lisbon", "Portugal", "PT"}, {"Lagos", "Nigeria", "NG"}, {"Osaka", "Japan", "JP"},
	{"Toronto", "Canada", "CA"}, {"Krakow", "Poland", "PL"}, {"Austin", "United States", "US"},
	{"Lyon", "France", "FR"}, {"Pune", "India", "IN"}, {"Bergen", "Norway", "NO"},
	{"Accra", "Ghana", "GH"}, {"Melbourne", "Australia", "AU"}, {"Valencia", "Spain", "ES"},
	{"Hamburg", "Germany", "DE"}, {"Busan", "South Korea", "KR"}, {"Cork", "Ireland", "IE"},
	{"Curitiba", "Brazil", "BR"}, {"Gothenburg", "Sweden", "SE"}, {"Nairobi", "Kenya", "KE"},
	{"Porto Alegre", "Brazil", "BR"}, {"Edinburgh", "United Kingdom", "GB"}, {"Turin", "Italy", "IT"},
	{"Brno", "Czechia", "CZ"}, {"Izmir", "Turkey", "TR"}, {"Monterrey", "Mexico", "MX"},
	{"Chiang Mai", "Thailand", "TH"}, {"Rotterdam", "Netherlands", "NL"}, {"Cluj-Napoca", "Romania", "RO"},
	{"Wellington", "New Zealand", "NZ"}, {"Ghent", "Belgium", "BE"}, {"Tampere", "Finland", "FI"},
	{"Seattle", "United States", "US"}, {"Kyoto", "Japan", "JP"}, {"Cape Town", "South Africa", "ZA"},
	{"Montevideo", "Uruguay", "UY"}, {"Tallinn", "Estonia", "EE"}, {"Graz", "Austria", "AT"},
}

var seedStreetNames = []string{
	"Maple", "Station", "Church", "Mill", "Park", "Harbour", "Elm", "Victoria", "Bridge", "Orchard",
	"King", "Willow", "Garden", "Market", "Chapel", "Meadow", "High", "River", "Cedar", "Hill",
}

var seedStreetKinds = []string{"Street", "Road", "Avenue", "Lane", "Way", "Place", "Close", "Drive"}

var seedCurrencies = []string{"EUR", "USD", "GBP", "JPY", "CHF", "CAD", "AUD", "SEK", "NOK", "PLN", "BRL", "INR"}

var seedLorem = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing", "elit", "sed", "do",
	"eiusmod", "tempor", "incididunt", "ut", "labore", "et", "dolore", "magna", "aliqua", "enim",
	"ad", "minim", "veniam", "quis", "nostrud", "exercitation", "ullamco", "laboris", "nisi",
	"aliquip", "ex", "ea", "commodo", "consequat", "duis", "aute", "irure", "in", "reprehenderit",
	"voluptate", "velit", "esse", "cillum", "fugiat", "nulla", "pariatur", "excepteur", "sint",
	"occaecat", "cupidatat", "non", "proident", "sunt", "culpa", "qui", "officia", "deserunt",
	"mollit", "anim", "id", "est", "laborum",
}

// seedTitleWords are what a title, a tag or a name of a thing is made of:
// words that read as English, where lorem ipsum reads as filler.
var seedTitleWords = []string{
	"quiet", "harbor", "signal", "amber", "north", "garden", "atlas", "copper", "river", "summer",
	"lantern", "orbit", "maple", "studio", "field", "echo", "canvas", "pilot", "meadow", "beacon",
	"cedar", "horizon", "velvet", "ember", "falcon", "glacier", "island", "juniper", "kite", "lotus",
}

// seedPasswordChars is bcrypt's own alphabet, so a password column holds what
// looks like the hash a login form would store — of nothing anybody typed.
const seedPasswordChars = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// seedCodeChars leaves out the letters a person reads as digits.
const seedCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
