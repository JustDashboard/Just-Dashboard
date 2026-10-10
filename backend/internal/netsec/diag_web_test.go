package netsec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func trustServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := trustRoots
	t.Cleanup(func() { trustRoots = old })
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	trustRoots = pool
}

func TestHTTPCheckSeparatesTransportTrustFromTheResponseAndTimesStages(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<p>ok</p>"))
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)

	res, err := New().HTTPCheck(t.Context(), host, port)
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if res.Verdict != ProbeFindings || !hasFinding(res, "untrusted") || !strings.HasPrefix(factValue(res, "Transport trust"), "not trusted") || factValue(res, "HTTP result") != "200 OK" {
		t.Fatalf("an untrusted 200 was not separated: %+v", res)
	}
	for _, id := range []string{"connect", "tls", "response"} {
		if stageStatus(res, id) != StagePassed {
			t.Fatalf("stage %s = %q in %+v", id, stageStatus(res, id), res.Stages)
		}
	}
	if stageStatus(res, "dns") != StageSkipped {
		t.Fatalf("a literal address reported a DNS stage: %+v", res.Stages)
	}
	if v, ok := metricValue(res, "first_byte"); !ok || v <= 0 {
		t.Fatalf("first byte = %v %v", v, ok)
	}
	if table := tableByID(res, "requests"); table == nil || table.Rows[0][3] == "" {
		t.Fatalf("requests = %+v", table)
	}

	trustServer(t, srv)
	res, _ = New().HTTPCheck(t.Context(), host, port)
	if res.Verdict != ProbeOK || !strings.HasPrefix(factValue(res, "Transport trust"), "trusted for 127.0.0.1") {
		t.Fatalf("trusted = %+v", res)
	}
}

func TestHTTPCheckFlagsDowngradeAndNamesTheFailedStage(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/landing", http.StatusFound)
	}))
	defer secure.Close()
	trustServer(t, secure)
	host, port := hostPort(t, secure.URL)
	res, err := New().HTTPCheck(t.Context(), host, port)
	if err != nil || !hasFinding(res, "downgrade") {
		t.Fatalf("downgrade = %+v %v", res, err)
	}
	if v, _ := metricValue(res, "redirects"); v != 1 {
		t.Fatalf("redirects = %v", v)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	res, _ = New().HTTPCheck(t.Context(), "127.0.0.1", closed)
	if res.OK || res.Verdict != ProbeFailed || stageStatus(res, "connect") != StageFailed || stageStatus(res, "tls") != StageSkipped || stageStatus(res, "response") != StageSkipped {
		t.Fatalf("refused = %+v", res)
	}

	old := httpTransport
	t.Cleanup(func() { httpTransport = old })
	httpTransport = func() *http.Transport {
		return &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "missing.example.test", IsNotFound: true}
		}}
	}
	res, _ = New().HTTPCheck(t.Context(), "missing.example.test", 443)
	if stageStatus(res, "dns") != StageFailed || stageStatus(res, "connect") != StageSkipped {
		t.Fatalf("dns failure = %+v", res.Stages)
	}
}

func TestHeaderGradeAppliesOnlyWhatFitsTheResponseKind(t *testing.T) {
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer api.Close()
	host, port := hostPort(t, api.URL)
	res, err := New().HTTPSecurity(t.Context(), host, port, "auto")
	if err != nil || res.Verdict != ProbeOK || len(res.Findings) != 0 || !strings.HasPrefix(factValue(res, "Response kind"), "api") {
		t.Fatalf("api graded as a page: %+v %v", res, err)
	}
	headers := tableByID(res, "headers")
	if headers == nil || headers.Rows[1][1] != applyOptional || headers.Rows[2][1] != applyNA {
		t.Fatalf("applicability = %+v", headers)
	}
	if v, _ := metricValue(res, "applicable"); v != 2 {
		t.Fatalf("applicable = %v", v)
	}
	res, _ = New().HTTPSecurity(t.Context(), host, port, "page")
	if res.Verdict != ProbeFindings || !hasFinding(res, "header-content-security-policy") || !hasFinding(res, "header-x-frame-options") || !strings.Contains(factValue(res, "Response kind"), "chosen") {
		t.Fatalf("page profile = %+v", res)
	}

	page := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer page.Close()
	phost, pport := hostPort(t, page.URL)
	res, _ = New().HTTPSecurity(t.Context(), phost, pport, "auto")
	var hsts ProbeFinding
	for _, f := range res.Findings {
		if f.ID == "header-strict-transport-security" {
			hsts = f
		}
	}
	if hsts.Level != "warning" || hasFinding(res, "header-x-frame-options") || !hasFinding(res, "header-referrer-policy") {
		t.Fatalf("page findings = %+v", res.Findings)
	}
}

func TestTLSCertStructuresTheChainForComparison(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	res, err := New().TLSCert(t.Context(), host, port)
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	chain := tableByID(res, "chain")
	if chain == nil || len(chain.Rows) == 0 || chain.Rows[0][0] != "leaf" || len(chain.Rows[0][7]) != 64 {
		t.Fatalf("chain = %+v", chain)
	}
	leafFP := ""
	for _, r := range res.Records {
		if strings.HasPrefix(r, "leaf sha256 ") {
			leafFP = r
		}
	}
	if leafFP != "leaf sha256 "+chain.Rows[0][7] || !hasFinding(res, "untrusted") || res.Verdict != ProbeFindings {
		t.Fatalf("records/findings = %v %+v", res.Records, res.Findings)
	}
	if _, ok := metricValue(res, "days_left"); !ok || !hasLink(res, "/proxy/tls") {
		t.Fatalf("metrics/links = %+v %+v", res.Metrics, res.Links)
	}
	trustServer(t, srv)
	res, _ = New().TLSCert(t.Context(), host, port)
	if !strings.HasPrefix(factValue(res, "Trust"), "Trusted for 127.0.0.1") {
		t.Fatalf("trusted = %+v", res.Facts)
	}
}

func TestTLSSurveySeparatesRejectionFromNetworkFailure(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	res, err := New().TLSSurvey(t.Context(), host, port)
	if err != nil || !res.OK || res.Verdict != ProbeOK {
		t.Fatalf("%+v %v", res, err)
	}
	versions := tableByID(res, "versions")
	if versions.Rows[0][1] != "rejected by the server" || versions.Rows[1][1] != "rejected by the server" || versions.Rows[2][1] != "offered" || versions.Rows[3][1] != "offered" {
		t.Fatalf("versions = %+v", versions.Rows)
	}
	if !hasLink(res, "/proxy/tls") {
		t.Fatal("no link to the proxy TLS tooling")
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	res, _ = New().TLSSurvey(t.Context(), "127.0.0.1", closed)
	versions = tableByID(res, "versions")
	if res.OK || res.Verdict != ProbeFailed || versions.Rows[0][1] != "network failure" || versions.Rows[3][1] != "not tested" {
		t.Fatalf("closed port = %+v", res)
	}

	plain, _ := net.Listen("tcp", "127.0.0.1:0")
	defer plain.Close()
	go func() {
		for {
			c, err := plain.Accept()
			if err != nil {
				return
			}
			fmt.Fprintf(c, "SSH-2.0-not-tls\r\n")
			c.Close()
		}
	}()
	res, _ = New().TLSSurvey(t.Context(), "127.0.0.1", plain.Addr().(*net.TCPAddr).Port)
	if res.Verdict != ProbeFailed || !strings.Contains(res.Summary, "does not speak TLS") {
		t.Fatalf("plain port = %+v", res)
	}
}

func TestClassifyHandshake(t *testing.T) {
	for msg, want := range map[string]string{
		"remote error: tls: protocol version not supported":                 "rejected by the server",
		"remote error: tls: handshake failure":                              "no common parameters",
		"tls: first record does not look like a TLS handshake":              "not TLS",
		"read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer": "closed during handshake",
		"EOF": "closed during handshake",
	} {
		if got, _ := classifyHandshake(errors.New(msg)); got != want {
			t.Errorf("%q => %q, want %q", msg, got, want)
		}
	}
}

func TestSiteAuditNamesOwnersAndStages(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)
	res, err := New().SiteAudit(t.Context(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stages) != 3 || stageStatus(res, "http") != StageWarning || stageStatus(res, "tls") != StageWarning || stageStatus(res, "headers") != StageWarning {
		t.Fatalf("stages = %+v", res.Stages)
	}
	if res.Verdict != ProbeFindings || !hasFinding(res, "tls-untrusted") || !hasFinding(res, "headers-header-strict-transport-security") {
		t.Fatalf("findings = %+v", res.Findings)
	}
	for _, f := range res.Findings {
		if f.Owner == "" || f.Action == "" {
			t.Fatalf("finding without an owner or action: %+v", f)
		}
	}
	AttributeSiteOwner(res, "blog")
	for _, f := range res.Findings {
		switch {
		case strings.HasPrefix(f.ID, "tls-") && (f.Href != "/proxy/certificates" || !strings.Contains(f.Owner, "blog")):
			t.Fatalf("certificate owner = %+v", f)
		case strings.HasPrefix(f.ID, "headers-") && f.Href != "/proxy/sites/blog":
			t.Fatalf("header owner = %+v", f)
		}
	}
	if factValue(res, "Served by") != "proxy site blog on this host" || !hasLink(res, "/proxy/sites/blog") {
		t.Fatalf("served by = %+v", res.Facts)
	}
	other := &ProbeResult{Tool: "siteaudit", Target: "example.test"}
	AttributeSiteOwner(other, "")
	if !strings.Contains(factValue(other, "Served by"), "whoever operates example.test") {
		t.Fatalf("foreign owner = %+v", other.Facts)
	}
}

// A login page's 401 is a finding about the response, not a failed audit.
func TestSiteAuditDoesNotFailAnAuthenticatedSite(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	trustServer(t, srv)
	host, port := hostPort(t, srv.URL)
	res, err := New().SiteAudit(t.Context(), host, port)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Verdict != ProbeFindings || stageStatus(res, "http") != StageWarning || res.Error != "" || !hasFinding(res, "http-client-error") {
		t.Fatalf("401 audit = %+v", res)
	}
	sec, _ := New().HTTPSecurity(t.Context(), host, port, "auto")
	if sec.Verdict != ProbeFindings || !hasFinding(sec, "error-response") {
		t.Fatalf("graded an error page without saying so: %+v", sec)
	}
}
