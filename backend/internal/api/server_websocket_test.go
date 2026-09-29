package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
	"github.com/gorilla/websocket"
)

func TestWebSocketOriginMatchesTheConfiguredTransport(t *testing.T) {
	for _, tt := range []struct {
		mode          string
		allowedScheme string
		deniedScheme  string
	}{
		{selfcfg.TLSOff, "http", "https"},
		{selfcfg.TLSInternal, "https", "http"},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			s, _ := terminalServerWithTLS(t, tt.mode)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := s.WS.Upgrade(w, r)
				if err == nil {
					conn.Close()
				}
			}))
			defer server.Close()

			address := strings.TrimPrefix(server.URL, "http://")
			for _, origin := range []struct {
				scheme string
				allow  bool
			}{
				{tt.allowedScheme, true},
				{tt.deniedScheme, false},
			} {
				header := http.Header{"Origin": {origin.scheme + "://" + address}}
				conn, response, err := websocket.DefaultDialer.Dial("ws://"+address, header)
				if conn != nil {
					conn.Close()
				}
				if origin.allow && err != nil {
					t.Fatalf("%s origin rejected: %v", origin.scheme, err)
				}
				if !origin.allow && (err == nil || response == nil || response.StatusCode != http.StatusForbidden) {
					t.Fatalf("%s origin accepted or returned the wrong status: %v, %v", origin.scheme, err, response)
				}
			}
		})
	}
}
