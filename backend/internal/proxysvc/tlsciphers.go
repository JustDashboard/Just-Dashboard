package proxysvc

import (
	"context"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Which cipher suites and key exchange groups a server accepts, version by
// version, read from its own answers to the ClientHellos in tlshello.go.
//
// Each version is listed by elimination: offer every suite known here, note
// the one the server picks, offer the rest, and so on until it refuses. That
// asks once per accepted suite rather than once per suite in the catalogue —
// a dozen connections rather than a hundred — and, when the server keeps its
// own order, lists the suites in that order. A last offer of the accepted
// suites reversed says whose order it is.

// SuiteResult is one accepted cipher suite and how it rates.
type SuiteResult struct {
	ID uint16 `json:"id"`
	// Name is the IANA name, OpenSSL the one ssl_ciphers takes.
	Name    string `json:"name"`
	OpenSSL string `json:"openssl"`
	// Kex is the key exchange (ECDHE, DHE, RSA, ECDH, or "any" for TLS
	// 1.3, where the group decides it) and Auth the certificate it needs.
	Kex    string `json:"kex"`
	Auth   string `json:"auth"`
	Cipher string `json:"cipher"`
	Bits   int    `json:"bits"`
	// ForwardSecrecy is a key exchange that a stolen server key cannot
	// undo afterwards; AEAD a cipher that authenticates as it encrypts.
	ForwardSecrecy bool `json:"forwardSecrecy"`
	AEAD           bool `json:"aead"`
	// Rating is "strong", "weak" or "insecure", and Reasons why it is not
	// strong.
	Rating  string   `json:"rating"`
	Reasons []string `json:"reasons"`
}

// VersionSuites is one protocol version's answer.
type VersionSuites struct {
	Name string `json:"name"`
	// Status is "accepted", "refused" or "unknown", as the version probe
	// reads them: "unknown" is no answer to stand behind.
	Status string `json:"status"`
	// Detail is the server's refusal, why nothing could be read, or where a
	// listing stopped short.
	Detail string `json:"detail,omitempty"`
	// Complete is false when the listing stopped before the server refused.
	Complete bool `json:"complete"`
	// Order is whose preference picks the suite — "server", "client" or
	// "unclear" — once two or more are accepted.
	Order  string        `json:"order,omitempty"`
	Suites []SuiteResult `json:"suites"`
}

// GroupResult is one key exchange group asked for on its own.
type GroupResult struct {
	ID          uint16 `json:"id"`
	Name        string `json:"name"`
	PostQuantum bool   `json:"postQuantum"`
	// Status is "accepted", "refused" or "unknown".
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// suiteInfo is a catalogue entry.
type suiteInfo struct {
	id      uint16
	name    string
	openssl string
	kex     string
	auth    string
	cipher  string
	bits    int
	// mode is "AEAD", "CBC", "stream" or "none", and mac the record MAC.
	mode string
	mac  string
}

func suite(id uint16, name, openssl, kex, auth, cipher string, bits int, mode, mac string) suiteInfo {
	return suiteInfo{id, name, openssl, kex, auth, cipher, bits, mode, mac}
}

// tls13 reports a TLS 1.3 suite, which no other version takes.
func (s suiteInfo) tls13() bool { return s.kex == "any" }

// tls12Only reports a suite TLS 1.2 introduced: an AEAD or a SHA-2 MAC.
func (s suiteInfo) tls12Only() bool {
	return s.mode == "AEAD" || s.mac == "SHA256" || s.mac == "SHA384"
}

func (s suiteInfo) forwardSecret() bool {
	return s.kex == "ECDHE" || s.kex == "DHE" || s.kex == "any"
}

// suiteCatalogue is every suite the deep scan offers, strongest first, so a
// server that takes the client's order still lists its best first. It covers
// what servers are really configured with: the TLS 1.3 suites, every
// ECDHE/DHE/RSA/static-ECDH suite with AES, ChaCha20, Camellia, ARIA, SEED,
// IDEA, 3DES, RC4 or DES, and the export, anonymous and NULL suites a scan
// exists to catch. PSK and SRP suites need a secret agreed beforehand and
// GOST ones a GOST certificate, which no scan of a website meets, so they are
// left out.
var suiteCatalogue = []suiteInfo{
	suite(0x1301, "TLS_AES_128_GCM_SHA256", "TLS_AES_128_GCM_SHA256", "any", "any", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0x1302, "TLS_AES_256_GCM_SHA384", "TLS_AES_256_GCM_SHA384", "any", "any", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0x1303, "TLS_CHACHA20_POLY1305_SHA256", "TLS_CHACHA20_POLY1305_SHA256", "any", "any", "ChaCha20-Poly1305", 256, "AEAD", "AEAD"),
	suite(0x1304, "TLS_AES_128_CCM_SHA256", "TLS_AES_128_CCM_SHA256", "any", "any", "AES-128-CCM", 128, "AEAD", "AEAD"),
	suite(0x1305, "TLS_AES_128_CCM_8_SHA256", "TLS_AES_128_CCM_8_SHA256", "any", "any", "AES-128-CCM-8", 128, "AEAD", "AEAD"),

	suite(0xc02b, "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256", "ECDHE-ECDSA-AES128-GCM-SHA256", "ECDHE", "ECDSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc02f, "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "ECDHE-RSA-AES128-GCM-SHA256", "ECDHE", "RSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc02c, "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384", "ECDHE-ECDSA-AES256-GCM-SHA384", "ECDHE", "ECDSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc030, "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384", "ECDHE-RSA-AES256-GCM-SHA384", "ECDHE", "RSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xcca9, "TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256", "ECDHE-ECDSA-CHACHA20-POLY1305", "ECDHE", "ECDSA", "ChaCha20-Poly1305", 256, "AEAD", "AEAD"),
	suite(0xcca8, "TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256", "ECDHE-RSA-CHACHA20-POLY1305", "ECDHE", "RSA", "ChaCha20-Poly1305", 256, "AEAD", "AEAD"),
	suite(0xc0ac, "TLS_ECDHE_ECDSA_WITH_AES_128_CCM", "ECDHE-ECDSA-AES128-CCM", "ECDHE", "ECDSA", "AES-128-CCM", 128, "AEAD", "AEAD"),
	suite(0xc0ad, "TLS_ECDHE_ECDSA_WITH_AES_256_CCM", "ECDHE-ECDSA-AES256-CCM", "ECDHE", "ECDSA", "AES-256-CCM", 256, "AEAD", "AEAD"),
	suite(0x009e, "TLS_DHE_RSA_WITH_AES_128_GCM_SHA256", "DHE-RSA-AES128-GCM-SHA256", "DHE", "RSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0x009f, "TLS_DHE_RSA_WITH_AES_256_GCM_SHA384", "DHE-RSA-AES256-GCM-SHA384", "DHE", "RSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xccaa, "TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256", "DHE-RSA-CHACHA20-POLY1305", "DHE", "RSA", "ChaCha20-Poly1305", 256, "AEAD", "AEAD"),
	suite(0xc09e, "TLS_DHE_RSA_WITH_AES_128_CCM", "DHE-RSA-AES128-CCM", "DHE", "RSA", "AES-128-CCM", 128, "AEAD", "AEAD"),
	suite(0xc09f, "TLS_DHE_RSA_WITH_AES_256_CCM", "DHE-RSA-AES256-CCM", "DHE", "RSA", "AES-256-CCM", 256, "AEAD", "AEAD"),
	suite(0xc05c, "TLS_ECDHE_ECDSA_WITH_ARIA_128_GCM_SHA256", "ECDHE-ECDSA-ARIA128-GCM-SHA256", "ECDHE", "ECDSA", "ARIA-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc05d, "TLS_ECDHE_ECDSA_WITH_ARIA_256_GCM_SHA384", "ECDHE-ECDSA-ARIA256-GCM-SHA384", "ECDHE", "ECDSA", "ARIA-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc060, "TLS_ECDHE_RSA_WITH_ARIA_128_GCM_SHA256", "ECDHE-ARIA128-GCM-SHA256", "ECDHE", "RSA", "ARIA-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc061, "TLS_ECDHE_RSA_WITH_ARIA_256_GCM_SHA384", "ECDHE-ARIA256-GCM-SHA384", "ECDHE", "RSA", "ARIA-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc052, "TLS_DHE_RSA_WITH_ARIA_128_GCM_SHA256", "DHE-RSA-ARIA128-GCM-SHA256", "DHE", "RSA", "ARIA-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc053, "TLS_DHE_RSA_WITH_ARIA_256_GCM_SHA384", "DHE-RSA-ARIA256-GCM-SHA384", "DHE", "RSA", "ARIA-256-GCM", 256, "AEAD", "AEAD"),
	suite(0x00a2, "TLS_DHE_DSS_WITH_AES_128_GCM_SHA256", "DHE-DSS-AES128-GCM-SHA256", "DHE", "DSS", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0x00a3, "TLS_DHE_DSS_WITH_AES_256_GCM_SHA384", "DHE-DSS-AES256-GCM-SHA384", "DHE", "DSS", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc056, "TLS_DHE_DSS_WITH_ARIA_128_GCM_SHA256", "DHE-DSS-ARIA128-GCM-SHA256", "DHE", "DSS", "ARIA-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc057, "TLS_DHE_DSS_WITH_ARIA_256_GCM_SHA384", "DHE-DSS-ARIA256-GCM-SHA384", "DHE", "DSS", "ARIA-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc0ae, "TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8", "ECDHE-ECDSA-AES128-CCM8", "ECDHE", "ECDSA", "AES-128-CCM-8", 128, "AEAD", "AEAD"),
	suite(0xc0af, "TLS_ECDHE_ECDSA_WITH_AES_256_CCM_8", "ECDHE-ECDSA-AES256-CCM8", "ECDHE", "ECDSA", "AES-256-CCM-8", 256, "AEAD", "AEAD"),
	suite(0xc0a2, "TLS_DHE_RSA_WITH_AES_128_CCM_8", "DHE-RSA-AES128-CCM8", "DHE", "RSA", "AES-128-CCM-8", 128, "AEAD", "AEAD"),
	suite(0xc0a3, "TLS_DHE_RSA_WITH_AES_256_CCM_8", "DHE-RSA-AES256-CCM8", "DHE", "RSA", "AES-256-CCM-8", 256, "AEAD", "AEAD"),

	suite(0xc023, "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256", "ECDHE-ECDSA-AES128-SHA256", "ECDHE", "ECDSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc027, "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256", "ECDHE-RSA-AES128-SHA256", "ECDHE", "RSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc024, "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA384", "ECDHE-ECDSA-AES256-SHA384", "ECDHE", "ECDSA", "AES-256-CBC", 256, "CBC", "SHA384"),
	suite(0xc028, "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384", "ECDHE-RSA-AES256-SHA384", "ECDHE", "RSA", "AES-256-CBC", 256, "CBC", "SHA384"),
	suite(0xc009, "TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA", "ECDHE-ECDSA-AES128-SHA", "ECDHE", "ECDSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0xc013, "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA", "ECDHE-RSA-AES128-SHA", "ECDHE", "RSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0xc00a, "TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA", "ECDHE-ECDSA-AES256-SHA", "ECDHE", "ECDSA", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0xc014, "TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA", "ECDHE-RSA-AES256-SHA", "ECDHE", "RSA", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0xc072, "TLS_ECDHE_ECDSA_WITH_CAMELLIA_128_CBC_SHA256", "ECDHE-ECDSA-CAMELLIA128-SHA256", "ECDHE", "ECDSA", "Camellia-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc073, "TLS_ECDHE_ECDSA_WITH_CAMELLIA_256_CBC_SHA384", "ECDHE-ECDSA-CAMELLIA256-SHA384", "ECDHE", "ECDSA", "Camellia-256-CBC", 256, "CBC", "SHA384"),
	suite(0xc076, "TLS_ECDHE_RSA_WITH_CAMELLIA_128_CBC_SHA256", "ECDHE-RSA-CAMELLIA128-SHA256", "ECDHE", "RSA", "Camellia-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc077, "TLS_ECDHE_RSA_WITH_CAMELLIA_256_CBC_SHA384", "ECDHE-RSA-CAMELLIA256-SHA384", "ECDHE", "RSA", "Camellia-256-CBC", 256, "CBC", "SHA384"),
	suite(0x0067, "TLS_DHE_RSA_WITH_AES_128_CBC_SHA256", "DHE-RSA-AES128-SHA256", "DHE", "RSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0x006b, "TLS_DHE_RSA_WITH_AES_256_CBC_SHA256", "DHE-RSA-AES256-SHA256", "DHE", "RSA", "AES-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0033, "TLS_DHE_RSA_WITH_AES_128_CBC_SHA", "DHE-RSA-AES128-SHA", "DHE", "RSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0039, "TLS_DHE_RSA_WITH_AES_256_CBC_SHA", "DHE-RSA-AES256-SHA", "DHE", "RSA", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0x00be, "TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA256", "DHE-RSA-CAMELLIA128-SHA256", "DHE", "RSA", "Camellia-128-CBC", 128, "CBC", "SHA256"),
	suite(0x00c4, "TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA256", "DHE-RSA-CAMELLIA256-SHA256", "DHE", "RSA", "Camellia-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0045, "TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA", "DHE-RSA-CAMELLIA128-SHA", "DHE", "RSA", "Camellia-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0088, "TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA", "DHE-RSA-CAMELLIA256-SHA", "DHE", "RSA", "Camellia-256-CBC", 256, "CBC", "SHA1"),
	suite(0x009a, "TLS_DHE_RSA_WITH_SEED_CBC_SHA", "DHE-RSA-SEED-SHA", "DHE", "RSA", "SEED-CBC", 128, "CBC", "SHA1"),
	suite(0x0040, "TLS_DHE_DSS_WITH_AES_128_CBC_SHA256", "DHE-DSS-AES128-SHA256", "DHE", "DSS", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0x006a, "TLS_DHE_DSS_WITH_AES_256_CBC_SHA256", "DHE-DSS-AES256-SHA256", "DHE", "DSS", "AES-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0032, "TLS_DHE_DSS_WITH_AES_128_CBC_SHA", "DHE-DSS-AES128-SHA", "DHE", "DSS", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0038, "TLS_DHE_DSS_WITH_AES_256_CBC_SHA", "DHE-DSS-AES256-SHA", "DHE", "DSS", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0x00bd, "TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA256", "DHE-DSS-CAMELLIA128-SHA256", "DHE", "DSS", "Camellia-128-CBC", 128, "CBC", "SHA256"),
	suite(0x00c3, "TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA256", "DHE-DSS-CAMELLIA256-SHA256", "DHE", "DSS", "Camellia-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0044, "TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA", "DHE-DSS-CAMELLIA128-SHA", "DHE", "DSS", "Camellia-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0087, "TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA", "DHE-DSS-CAMELLIA256-SHA", "DHE", "DSS", "Camellia-256-CBC", 256, "CBC", "SHA1"),

	suite(0x009c, "TLS_RSA_WITH_AES_128_GCM_SHA256", "AES128-GCM-SHA256", "RSA", "RSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0x009d, "TLS_RSA_WITH_AES_256_GCM_SHA384", "AES256-GCM-SHA384", "RSA", "RSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc09c, "TLS_RSA_WITH_AES_128_CCM", "AES128-CCM", "RSA", "RSA", "AES-128-CCM", 128, "AEAD", "AEAD"),
	suite(0xc09d, "TLS_RSA_WITH_AES_256_CCM", "AES256-CCM", "RSA", "RSA", "AES-256-CCM", 256, "AEAD", "AEAD"),
	suite(0xc0a0, "TLS_RSA_WITH_AES_128_CCM_8", "AES128-CCM8", "RSA", "RSA", "AES-128-CCM-8", 128, "AEAD", "AEAD"),
	suite(0xc0a1, "TLS_RSA_WITH_AES_256_CCM_8", "AES256-CCM8", "RSA", "RSA", "AES-256-CCM-8", 256, "AEAD", "AEAD"),
	suite(0xc050, "TLS_RSA_WITH_ARIA_128_GCM_SHA256", "ARIA128-GCM-SHA256", "RSA", "RSA", "ARIA-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc051, "TLS_RSA_WITH_ARIA_256_GCM_SHA384", "ARIA256-GCM-SHA384", "RSA", "RSA", "ARIA-256-GCM", 256, "AEAD", "AEAD"),
	suite(0x003c, "TLS_RSA_WITH_AES_128_CBC_SHA256", "AES128-SHA256", "RSA", "RSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0x003d, "TLS_RSA_WITH_AES_256_CBC_SHA256", "AES256-SHA256", "RSA", "RSA", "AES-256-CBC", 256, "CBC", "SHA256"),
	suite(0x002f, "TLS_RSA_WITH_AES_128_CBC_SHA", "AES128-SHA", "RSA", "RSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0035, "TLS_RSA_WITH_AES_256_CBC_SHA", "AES256-SHA", "RSA", "RSA", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0x00ba, "TLS_RSA_WITH_CAMELLIA_128_CBC_SHA256", "CAMELLIA128-SHA256", "RSA", "RSA", "Camellia-128-CBC", 128, "CBC", "SHA256"),
	suite(0x00c0, "TLS_RSA_WITH_CAMELLIA_256_CBC_SHA256", "CAMELLIA256-SHA256", "RSA", "RSA", "Camellia-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0041, "TLS_RSA_WITH_CAMELLIA_128_CBC_SHA", "CAMELLIA128-SHA", "RSA", "RSA", "Camellia-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0084, "TLS_RSA_WITH_CAMELLIA_256_CBC_SHA", "CAMELLIA256-SHA", "RSA", "RSA", "Camellia-256-CBC", 256, "CBC", "SHA1"),
	suite(0x0096, "TLS_RSA_WITH_SEED_CBC_SHA", "SEED-SHA", "RSA", "RSA", "SEED-CBC", 128, "CBC", "SHA1"),
	suite(0x0007, "TLS_RSA_WITH_IDEA_CBC_SHA", "IDEA-CBC-SHA", "RSA", "RSA", "IDEA-CBC", 128, "CBC", "SHA1"),

	suite(0xc02d, "TLS_ECDH_ECDSA_WITH_AES_128_GCM_SHA256", "ECDH-ECDSA-AES128-GCM-SHA256", "ECDH", "ECDSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc02e, "TLS_ECDH_ECDSA_WITH_AES_256_GCM_SHA384", "ECDH-ECDSA-AES256-GCM-SHA384", "ECDH", "ECDSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc031, "TLS_ECDH_RSA_WITH_AES_128_GCM_SHA256", "ECDH-RSA-AES128-GCM-SHA256", "ECDH", "RSA", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0xc032, "TLS_ECDH_RSA_WITH_AES_256_GCM_SHA384", "ECDH-RSA-AES256-GCM-SHA384", "ECDH", "RSA", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0xc025, "TLS_ECDH_ECDSA_WITH_AES_128_CBC_SHA256", "ECDH-ECDSA-AES128-SHA256", "ECDH", "ECDSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc026, "TLS_ECDH_ECDSA_WITH_AES_256_CBC_SHA384", "ECDH-ECDSA-AES256-SHA384", "ECDH", "ECDSA", "AES-256-CBC", 256, "CBC", "SHA384"),
	suite(0xc029, "TLS_ECDH_RSA_WITH_AES_128_CBC_SHA256", "ECDH-RSA-AES128-SHA256", "ECDH", "RSA", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0xc02a, "TLS_ECDH_RSA_WITH_AES_256_CBC_SHA384", "ECDH-RSA-AES256-SHA384", "ECDH", "RSA", "AES-256-CBC", 256, "CBC", "SHA384"),
	suite(0xc004, "TLS_ECDH_ECDSA_WITH_AES_128_CBC_SHA", "ECDH-ECDSA-AES128-SHA", "ECDH", "ECDSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0xc005, "TLS_ECDH_ECDSA_WITH_AES_256_CBC_SHA", "ECDH-ECDSA-AES256-SHA", "ECDH", "ECDSA", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0xc00e, "TLS_ECDH_RSA_WITH_AES_128_CBC_SHA", "ECDH-RSA-AES128-SHA", "ECDH", "RSA", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0xc00f, "TLS_ECDH_RSA_WITH_AES_256_CBC_SHA", "ECDH-RSA-AES256-SHA", "ECDH", "RSA", "AES-256-CBC", 256, "CBC", "SHA1"),

	suite(0xc008, "TLS_ECDHE_ECDSA_WITH_3DES_EDE_CBC_SHA", "ECDHE-ECDSA-DES-CBC3-SHA", "ECDHE", "ECDSA", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0xc012, "TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA", "ECDHE-RSA-DES-CBC3-SHA", "ECDHE", "RSA", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0x0016, "TLS_DHE_RSA_WITH_3DES_EDE_CBC_SHA", "EDH-RSA-DES-CBC3-SHA", "DHE", "RSA", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0x0013, "TLS_DHE_DSS_WITH_3DES_EDE_CBC_SHA", "EDH-DSS-DES-CBC3-SHA", "DHE", "DSS", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0x000a, "TLS_RSA_WITH_3DES_EDE_CBC_SHA", "DES-CBC3-SHA", "RSA", "RSA", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0xc003, "TLS_ECDH_ECDSA_WITH_3DES_EDE_CBC_SHA", "ECDH-ECDSA-DES-CBC3-SHA", "ECDH", "ECDSA", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0xc00d, "TLS_ECDH_RSA_WITH_3DES_EDE_CBC_SHA", "ECDH-RSA-DES-CBC3-SHA", "ECDH", "RSA", "3DES-CBC", 112, "CBC", "SHA1"),

	suite(0xc007, "TLS_ECDHE_ECDSA_WITH_RC4_128_SHA", "ECDHE-ECDSA-RC4-SHA", "ECDHE", "ECDSA", "RC4", 128, "stream", "SHA1"),
	suite(0xc011, "TLS_ECDHE_RSA_WITH_RC4_128_SHA", "ECDHE-RSA-RC4-SHA", "ECDHE", "RSA", "RC4", 128, "stream", "SHA1"),
	suite(0x0005, "TLS_RSA_WITH_RC4_128_SHA", "RC4-SHA", "RSA", "RSA", "RC4", 128, "stream", "SHA1"),
	suite(0x0004, "TLS_RSA_WITH_RC4_128_MD5", "RC4-MD5", "RSA", "RSA", "RC4", 128, "stream", "MD5"),
	suite(0xc002, "TLS_ECDH_ECDSA_WITH_RC4_128_SHA", "ECDH-ECDSA-RC4-SHA", "ECDH", "ECDSA", "RC4", 128, "stream", "SHA1"),
	suite(0xc00c, "TLS_ECDH_RSA_WITH_RC4_128_SHA", "ECDH-RSA-RC4-SHA", "ECDH", "RSA", "RC4", 128, "stream", "SHA1"),

	suite(0x0015, "TLS_DHE_RSA_WITH_DES_CBC_SHA", "EDH-RSA-DES-CBC-SHA", "DHE", "RSA", "DES-CBC", 56, "CBC", "SHA1"),
	suite(0x0012, "TLS_DHE_DSS_WITH_DES_CBC_SHA", "EDH-DSS-DES-CBC-SHA", "DHE", "DSS", "DES-CBC", 56, "CBC", "SHA1"),
	suite(0x0009, "TLS_RSA_WITH_DES_CBC_SHA", "DES-CBC-SHA", "RSA", "RSA", "DES-CBC", 56, "CBC", "SHA1"),
	suite(0x0014, "TLS_DHE_RSA_EXPORT_WITH_DES40_CBC_SHA", "EXP-EDH-RSA-DES-CBC-SHA", "DHE", "RSA", "DES40-CBC", 40, "CBC", "SHA1"),
	suite(0x0011, "TLS_DHE_DSS_EXPORT_WITH_DES40_CBC_SHA", "EXP-EDH-DSS-DES-CBC-SHA", "DHE", "DSS", "DES40-CBC", 40, "CBC", "SHA1"),
	suite(0x0008, "TLS_RSA_EXPORT_WITH_DES40_CBC_SHA", "EXP-DES-CBC-SHA", "RSA", "RSA", "DES40-CBC", 40, "CBC", "SHA1"),
	suite(0x0006, "TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5", "EXP-RC2-CBC-MD5", "RSA", "RSA", "RC2-40-CBC", 40, "CBC", "MD5"),
	suite(0x0003, "TLS_RSA_EXPORT_WITH_RC4_40_MD5", "EXP-RC4-MD5", "RSA", "RSA", "RC4-40", 40, "stream", "MD5"),

	suite(0x00a6, "TLS_DH_anon_WITH_AES_128_GCM_SHA256", "ADH-AES128-GCM-SHA256", "DHE", "anon", "AES-128-GCM", 128, "AEAD", "AEAD"),
	suite(0x00a7, "TLS_DH_anon_WITH_AES_256_GCM_SHA384", "ADH-AES256-GCM-SHA384", "DHE", "anon", "AES-256-GCM", 256, "AEAD", "AEAD"),
	suite(0x006c, "TLS_DH_anon_WITH_AES_128_CBC_SHA256", "ADH-AES128-SHA256", "DHE", "anon", "AES-128-CBC", 128, "CBC", "SHA256"),
	suite(0x006d, "TLS_DH_anon_WITH_AES_256_CBC_SHA256", "ADH-AES256-SHA256", "DHE", "anon", "AES-256-CBC", 256, "CBC", "SHA256"),
	suite(0x0034, "TLS_DH_anon_WITH_AES_128_CBC_SHA", "ADH-AES128-SHA", "DHE", "anon", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0x003a, "TLS_DH_anon_WITH_AES_256_CBC_SHA", "ADH-AES256-SHA", "DHE", "anon", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0x0046, "TLS_DH_anon_WITH_CAMELLIA_128_CBC_SHA", "ADH-CAMELLIA128-SHA", "DHE", "anon", "Camellia-128-CBC", 128, "CBC", "SHA1"),
	suite(0x0089, "TLS_DH_anon_WITH_CAMELLIA_256_CBC_SHA", "ADH-CAMELLIA256-SHA", "DHE", "anon", "Camellia-256-CBC", 256, "CBC", "SHA1"),
	suite(0x001b, "TLS_DH_anon_WITH_3DES_EDE_CBC_SHA", "ADH-DES-CBC3-SHA", "DHE", "anon", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0x0018, "TLS_DH_anon_WITH_RC4_128_MD5", "ADH-RC4-MD5", "DHE", "anon", "RC4", 128, "stream", "MD5"),
	suite(0xc018, "TLS_ECDH_anon_WITH_AES_128_CBC_SHA", "AECDH-AES128-SHA", "ECDHE", "anon", "AES-128-CBC", 128, "CBC", "SHA1"),
	suite(0xc019, "TLS_ECDH_anon_WITH_AES_256_CBC_SHA", "AECDH-AES256-SHA", "ECDHE", "anon", "AES-256-CBC", 256, "CBC", "SHA1"),
	suite(0xc017, "TLS_ECDH_anon_WITH_3DES_EDE_CBC_SHA", "AECDH-DES-CBC3-SHA", "ECDHE", "anon", "3DES-CBC", 112, "CBC", "SHA1"),
	suite(0xc016, "TLS_ECDH_anon_WITH_RC4_128_SHA", "AECDH-RC4-SHA", "ECDHE", "anon", "RC4", 128, "stream", "SHA1"),
	suite(0xc015, "TLS_ECDH_anon_WITH_NULL_SHA", "AECDH-NULL-SHA", "ECDHE", "anon", "NULL", 0, "none", "SHA1"),

	suite(0xc006, "TLS_ECDHE_ECDSA_WITH_NULL_SHA", "ECDHE-ECDSA-NULL-SHA", "ECDHE", "ECDSA", "NULL", 0, "none", "SHA1"),
	suite(0xc010, "TLS_ECDHE_RSA_WITH_NULL_SHA", "ECDHE-RSA-NULL-SHA", "ECDHE", "RSA", "NULL", 0, "none", "SHA1"),
	suite(0x003b, "TLS_RSA_WITH_NULL_SHA256", "NULL-SHA256", "RSA", "RSA", "NULL", 0, "none", "SHA256"),
	suite(0x0002, "TLS_RSA_WITH_NULL_SHA", "NULL-SHA", "RSA", "RSA", "NULL", 0, "none", "SHA1"),
	suite(0x0001, "TLS_RSA_WITH_NULL_MD5", "NULL-MD5", "RSA", "RSA", "NULL", 0, "none", "MD5"),
}

var suiteByID = func() map[uint16]suiteInfo {
	m := make(map[uint16]suiteInfo, len(suiteCatalogue))
	for _, s := range suiteCatalogue {
		m[s.id] = s
	}
	return m
}()

func isECDHE(id uint16) bool { s, ok := suiteByID[id]; return ok && s.kex == "ECDHE" }
func isDHE(id uint16) bool   { s, ok := suiteByID[id]; return ok && s.kex == "DHE" }

// rateSuite says how good a suite is and why. Insecure is a suite that gives
// away the connection to someone watching or in the middle; weak is one with
// a known flaw that needs more than that, or no forward secrecy, which turns
// one stolen key into every recorded session. Strong is forward-secret AEAD.
func rateSuite(s suiteInfo) (string, []string) {
	insecure := []string{}
	switch {
	case s.cipher == "NULL":
		insecure = append(insecure, "no encryption")
	case s.bits <= 40:
		insecure = append(insecure, "export grade, 40-bit")
	case s.cipher == "DES-CBC":
		insecure = append(insecure, "56-bit DES")
	case strings.HasPrefix(s.cipher, "RC4"):
		insecure = append(insecure, "RC4, broken")
	}
	if s.auth == "anon" {
		insecure = append(insecure, "no authentication")
	}
	if len(insecure) > 0 {
		return "insecure", insecure
	}
	weak := []string{}
	if !s.forwardSecret() {
		weak = append(weak, "no forward secrecy")
	}
	if strings.HasPrefix(s.cipher, "3DES") || strings.HasPrefix(s.cipher, "IDEA") {
		weak = append(weak, "64-bit block")
	}
	if s.mode == "CBC" {
		weak = append(weak, "CBC mode")
	}
	if strings.HasSuffix(s.cipher, "CCM-8") {
		weak = append(weak, "short tag")
	}
	if len(weak) > 0 {
		return "weak", weak
	}
	return "strong", []string{}
}

func suiteResult(s suiteInfo) SuiteResult {
	rating, reasons := rateSuite(s)
	return SuiteResult{
		ID: s.id, Name: s.name, OpenSSL: s.openssl, Kex: s.kex, Auth: s.auth,
		Cipher: s.cipher, Bits: s.bits, ForwardSecrecy: s.forwardSecret(), AEAD: s.mode == "AEAD",
		Rating: rating, Reasons: reasons,
	}
}

// versionSSL30 is the version probe's name for SSL 3.0; crypto/tls keeps the
// constant only as deprecated.
const versionSSL30 = 0x0300

// deepVersions are listed newest first, as the report shows them.
var deepVersions = []struct {
	name    string
	version uint16
}{
	{"TLS 1.3", tls.VersionTLS13},
	{"TLS 1.2", tls.VersionTLS12},
	{"TLS 1.1", tls.VersionTLS11},
	{"TLS 1.0", tls.VersionTLS10},
	{"SSL 3.0", versionSSL30},
}

// suitesFor is the catalogue a version can use: TLS 1.3's own, or the rest
// less what came with TLS 1.2.
func suitesFor(version uint16) []uint16 {
	ids := []uint16{}
	for _, s := range suiteCatalogue {
		switch {
		case version == tls.VersionTLS13:
			if s.tls13() {
				ids = append(ids, s.id)
			}
		case s.tls13():
		case version < tls.VersionTLS12 && s.tls12Only():
		default:
			ids = append(ids, s.id)
		}
	}
	return ids
}

// Key exchange groups by their IANA numbers.
const (
	groupP256               = 0x0017
	groupP384               = 0x0018
	groupP521               = 0x0019
	groupX25519             = 0x001d
	groupX448               = 0x001e
	groupFFDHE2048          = 0x0100
	groupFFDHE3072          = 0x0101
	groupFFDHE4096          = 0x0102
	groupSecP256r1MLKEM768  = 0x11eb
	groupX25519MLKEM768     = 0x11ec
	groupSecP384r1MLKEM1024 = 0x11ed
)

type groupInfo struct {
	id          uint16
	name        string
	postQuantum bool
}

// tls13Groups are asked about one at a time: the post-quantum hybrids first,
// the order the report lists them in.
var tls13Groups = []groupInfo{
	{groupX25519MLKEM768, "X25519MLKEM768", true},
	{groupSecP256r1MLKEM768, "SecP256r1MLKEM768", true},
	{groupSecP384r1MLKEM1024, "SecP384r1MLKEM1024", true},
	{groupX25519, "X25519", false},
	{groupP256, "P-256", false},
	{groupP384, "P-384", false},
	{groupP521, "P-521", false},
	{groupX448, "X448", false},
	{groupFFDHE2048, "ffdhe2048", false},
	{groupFFDHE3072, "ffdhe3072", false},
	{groupFFDHE4096, "ffdhe4096", false},
}

func groupName(id uint16) string {
	for _, g := range tls13Groups {
		if g.id == id {
			return g.name
		}
	}
	return fmt.Sprintf("group 0x%04x", id)
}

// browserGroups is what a current browser offers, with shares for the first
// two, so the group the server settles on is the one a visitor gets.
var browserGroups = []uint16{groupX25519MLKEM768, groupX25519, groupP256, groupP384}

// keyShares makes the key shares for a probe. The keys are never used: they
// are there so the server can answer as it would a browser, which sends one.
// A group this library cannot make a share for is asked about with none,
// which a server answers with a HelloRetryRequest naming it.
func keyShares(groups ...uint16) ([]keyShare, error) {
	out := []keyShare{}
	for _, g := range groups {
		var data []byte
		var err error
		switch g {
		case groupX25519:
			data, err = ecdhShare(ecdh.X25519())
		case groupP256:
			data, err = ecdhShare(ecdh.P256())
		case groupP384:
			data, err = ecdhShare(ecdh.P384())
		case groupP521:
			data, err = ecdhShare(ecdh.P521())
		case groupX25519MLKEM768:
			// The ML-KEM key comes first in this one hybrid, and second in
			// the other two (draft-ietf-tls-ecdhe-mlkem).
			var key *mlkem.DecapsulationKey768
			if key, err = mlkem.GenerateKey768(); err == nil {
				var x []byte
				x, err = ecdhShare(ecdh.X25519())
				data = append(key.EncapsulationKey().Bytes(), x...)
			}
		case groupSecP256r1MLKEM768:
			var key *mlkem.DecapsulationKey768
			if key, err = mlkem.GenerateKey768(); err == nil {
				data, err = ecdhShare(ecdh.P256())
				data = append(data, key.EncapsulationKey().Bytes()...)
			}
		case groupSecP384r1MLKEM1024:
			var key *mlkem.DecapsulationKey1024
			if key, err = mlkem.GenerateKey1024(); err == nil {
				data, err = ecdhShare(ecdh.P384())
				data = append(data, key.EncapsulationKey().Bytes()...)
			}
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, keyShare{group: g, data: data})
	}
	return out, nil
}

func ecdhShare(curve ecdh.Curve) ([]byte, error) {
	key, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return key.PublicKey().Bytes(), nil
}

// deepProber sends the deep scan's ClientHellos to one address, at most
// eight at a time, and counts them.
type deepProber struct {
	addr       string
	serverName string
	slots      chan struct{}
	sent       atomic.Int32
	// shares are the X25519 and P-256 shares every TLS 1.3 suite probe
	// sends, made once.
	shares []keyShare
}

func newDeepProber(addr, serverName string) (*deepProber, error) {
	shares, err := keyShares(groupX25519, groupP256)
	if err != nil {
		return nil, err
	}
	return &deepProber{addr: addr, serverName: serverName, slots: make(chan struct{}, 8), shares: shares}, nil
}

func (p *deepProber) hello(ctx context.Context, o helloOffer) helloAnswer {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return helloAnswer{err: ctx.Err()}
	}
	defer func() { <-p.slots }()
	p.sent.Add(1)
	o.serverName = p.serverName
	return sendHello(ctx, p.addr, o)
}

// suiteOffer asks for version with suites, with the groups and shares a
// current client sends, so no suite is refused for want of a curve.
func (p *deepProber) suiteOffer(version uint16, suites []uint16) helloOffer {
	o := helloOffer{version: version, suites: suites}
	switch version {
	case tls.VersionTLS13:
		o.groups = []uint16{groupX25519, groupP256, groupP384, groupP521}
		o.shares = p.shares
	case versionSSL30:
	default:
		o.groups = []uint16{groupX25519, groupP256, groupP384, groupP521}
	}
	return o
}

// listSuites lists what one version accepts, by elimination.
func (p *deepProber) listSuites(ctx context.Context, name string, version uint16) VersionSuites {
	out := VersionSuites{Name: name, Status: "unknown", Suites: []SuiteResult{}}
	remaining := suitesFor(version)
	picks := []uint16{}
	for len(remaining) > 0 {
		a := p.hello(ctx, p.suiteOffer(version, remaining))
		stopped := ""
		switch {
		case a.err != nil:
			stopped = probeFailure(a.err)
		case !a.accepted:
			if len(picks) == 0 {
				out.Status, out.Detail = "refused", refusalDetail(a.alert, len(remaining))
			}
			out.Complete = true
		case a.version != version:
			if len(picks) == 0 {
				out.Status = "refused"
				out.Detail = "The server answered with " + versionName(a.version) + " instead."
			}
			out.Complete = true
		case !slices.Contains(remaining, a.suite):
			stopped = fmt.Sprintf("The server chose suite 0x%04x, which was not offered.", a.suite)
		default:
			picks = append(picks, a.suite)
			remaining = slices.DeleteFunc(remaining, func(id uint16) bool { return id == a.suite })
			if len(remaining) == 0 {
				out.Complete = true
			}
			continue
		}
		if stopped != "" {
			if len(picks) == 0 {
				out.Detail = stopped
			} else {
				out.Detail = fmt.Sprintf("The listing stopped after %d: %s", len(picks), stopped)
			}
		}
		break
	}
	if len(picks) == 0 {
		return out
	}
	out.Status = "accepted"
	for _, id := range picks {
		out.Suites = append(out.Suites, suiteResult(suiteByID[id]))
	}
	if len(picks) > 1 {
		out.Order = p.suiteOrder(ctx, version, picks)
	}
	return out
}

// suiteOrder offers the accepted suites in reverse. A server that keeps its
// own order picks its first again; one that takes the client's picks what
// was offered first.
func (p *deepProber) suiteOrder(ctx context.Context, version uint16, picks []uint16) string {
	reversed := slices.Clone(picks)
	slices.Reverse(reversed)
	a := p.hello(ctx, p.suiteOffer(version, reversed))
	switch {
	case !a.accepted || a.err != nil:
		return "unclear"
	case a.suite == picks[0]:
		return "server"
	case a.suite == reversed[0]:
		return "client"
	}
	return "unclear"
}

// refusalDetail words a refusal of the first offer, which held every suite
// known here: nothing the server would take was among them.
func refusalDetail(alert string, offered int) string {
	if alert == "closed the connection" {
		return fmt.Sprintf("The server closed the connection on an offer of %d suites.", offered)
	}
	return fmt.Sprintf("The server answered an offer of %d suites with: %s.", offered, alert)
}

// probeFailure says why a probe heard nothing it could read.
func probeFailure(err error) string {
	switch {
	case isTimeout(err):
		return "The server did not answer within 5 seconds."
	case errors.Is(err, context.Canceled):
		return "The scan was cancelled."
	}
	return "The probe failed: " + err.Error() + "."
}

func versionName(v uint16) string {
	if v == versionSSL30 {
		return "SSL 3.0"
	}
	return tls.VersionName(v)
}

// listGroups asks for each TLS 1.3 group on its own, then with a browser's
// offer: which one the server picks there is the one visitors get.
func (p *deepProber) listGroups(ctx context.Context) ([]GroupResult, string, error) {
	suites := suitesFor(tls.VersionTLS13)
	out := make([]GroupResult, len(tls13Groups))
	var wg sync.WaitGroup
	var shareErr error
	var mu sync.Mutex
	for i, g := range tls13Groups {
		shares, err := keyShares(g.id)
		if err != nil {
			mu.Lock()
			shareErr = err
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := p.hello(ctx, helloOffer{version: tls.VersionTLS13, suites: suites, groups: []uint16{g.id}, shares: shares})
			out[i] = groupAnswer(g, a, tls.VersionTLS13)
		}()
	}
	shares, err := keyShares(browserGroups[:2]...)
	if err != nil {
		return nil, "", err
	}
	browser := p.hello(ctx, helloOffer{version: tls.VersionTLS13, suites: suites, groups: browserGroups, shares: shares})
	wg.Wait()
	if shareErr != nil {
		return nil, "", shareErr
	}
	picked := ""
	if browser.accepted && browser.version == tls.VersionTLS13 && browser.group != 0 {
		picked = groupName(browser.group)
	}
	return out, picked, nil
}

// tls12Curve is the curve a browser's TLS 1.2 offer gets, offering the ECDHE
// suites the server took, read from the key exchange that names it.
func (p *deepProber) tls12Curve(ctx context.Context, ecdhe []uint16) string {
	a := p.hello(ctx, helloOffer{version: tls.VersionTLS12, suites: ecdhe,
		groups: []uint16{groupX25519, groupP256, groupP384, groupP521}, keyExchange: true})
	if !a.accepted || a.version != tls.VersionTLS12 || a.group == 0 {
		return ""
	}
	return groupName(a.group)
}

// groupAnswer reads a probe that offered one group.
func groupAnswer(g groupInfo, a helloAnswer, version uint16) GroupResult {
	out := GroupResult{ID: g.id, Name: g.name, PostQuantum: g.postQuantum, Status: "unknown"}
	switch {
	case a.err != nil:
		out.Detail = probeFailure(a.err)
	case !a.accepted:
		out.Status = "refused"
		if a.alert == "closed the connection" {
			out.Detail = "The server closed the connection."
		} else {
			out.Detail = "The server answered: " + a.alert + "."
		}
	case a.version != version:
		out.Status = "refused"
		out.Detail = "The server answered with " + versionName(a.version) + " instead."
	case a.group == g.id:
		out.Status = "accepted"
		if a.retry {
			out.Detail = "Taken after asking the client for a key share."
		}
	case a.group == 0:
		out.Detail = "The server's answer did not name a group."
	default:
		out.Detail = "The server answered with " + groupName(a.group) + ", which was not offered."
	}
	return out
}

// dhSize reads the size of the group a DHE suite uses.
func (p *deepProber) dhSize(ctx context.Context, version uint16, dhe []uint16) int {
	a := p.hello(ctx, helloOffer{version: version, suites: dhe, keyExchange: true})
	if !a.accepted || a.version != version {
		return 0
	}
	return a.dhBits
}
