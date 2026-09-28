package proxysvc

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// The scan and watch fields take what people paste, and the page parses it
// again before sending so it can say what is wrong. One table holds both
// parsers to the same answers; frontend/src/lib/scan-target.test.js reads it
// too.
func TestParseScanTarget(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/src/lib/scan-target-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input string `json:"input"`
		Port  int    `json:"port"`
		// PortText, when present, is a port given as text — ?port= or the
		// report's port field — and the case goes through ParseScanQuery.
		PortText *string `json:"portText"`
		Host     string  `json:"host"`
		Want     int     `json:"want"`
		Error    bool    `json:"error"`
		// Says, when present, is the refusal word for word, which the page
		// shows as the backend would.
		Says string `json:"says"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("only %d cases in the shared table", len(cases))
	}
	for _, c := range cases {
		call := fmt.Sprintf("ParseScanTarget(%q, %d)", c.Input, c.Port)
		got, err := ParseScanTarget(c.Input, c.Port)
		if c.PortText != nil {
			call = fmt.Sprintf("ParseScanQuery(%q, %q)", c.Input, *c.PortText)
			got, err = ParseScanQuery(c.Input, *c.PortText)
		}
		if c.Error {
			if err == nil {
				t.Errorf("%s = %+v, want an error", call, got)
			} else if c.Says != "" && err.Error() != c.Says {
				t.Errorf("%s says %q, want %q", call, err.Error(), c.Says)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", call, err)
			continue
		}
		if got.Host != c.Host || got.Port != c.Want {
			t.Errorf("%s = %s port %d, want %s port %d", call, got.Host, got.Port, c.Host, c.Want)
		}
	}
}

// The watched-domain links carried host:port in ?domain=, which the scan then
// joined with the default port into "[mail.example.com:993]:443".
func TestParseScanTargetKeepsAPortWrittenIntoTheName(t *testing.T) {
	got, err := ParseScanTarget("mail.example.com:993", 0)
	if err != nil || got != (ScanTarget{Host: "mail.example.com", Port: 993}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}
