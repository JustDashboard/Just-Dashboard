package api

import (
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// An image, a certificate, a stream and a PM2 application each have a page of
// their own. The map sent a press to the address of the sheet each page
// replaced (?image=, ?cert=, ?stream=, ?app=) and relied on the redirect.
func TestMapItemsLinkToTheirOwnPages(t *testing.T) {
	const digest = "0f3a9c1d5b7e2a4c6d8f0b1a3c5e7f9a0b2c4d6e8f1a3b5c7d9e0f2a4b6c8d0e"
	for name, c := range map[string]struct{ got, want string }{
		"an image, by its id without the algorithm": {
			mapImage(dockerx.Image{ID: "sha256:" + digest}).Href,
			"/docker/images/" + digest,
		},
		"a certificate, by its name and its file": {
			mapCertificate(proxysvc.Certificate{Name: "example.com", Path: "/etc/ssl/a b/fullchain.pem"}).Href,
			"/proxy/certificates/example.com?path=%2Fetc%2Fssl%2Fa+b%2Ffullchain.pem",
		},
		"a certificate the inventory could not name": {
			mapCertificate(proxysvc.Certificate{Path: "/etc/ssl/x.pem"}).Href,
			"/proxy/certificates/certificate?path=%2Fetc%2Fssl%2Fx.pem",
		},
		"a stream, by its name": {
			mapStream(proxysvc.StreamEntry{StreamSpec: proxysvc.StreamSpec{Name: "pg main"}}).Href,
			"/proxy/streams/pg%20main",
		},
		"a PM2 application, by its name and the account that runs it": {
			mapPM2(procs.PM2Process{Name: "api/v2", DaemonID: "deploy", ID: 3}).Href,
			"/processes/pm2/api%2Fv2?account=deploy",
		},
	} {
		if c.got != c.want {
			t.Errorf("%s: Href = %q, want %q", name, c.got, c.want)
		}
	}
}
