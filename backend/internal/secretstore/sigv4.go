package secretstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AWS Signature Version 4, as the standard library signs it: the request is
// summarised into a canonical form, that form is hashed into a string to
// sign, and a key derived from the secret access key by a chain of
// HMAC-SHA256 over the date, region, service and a constant signs it. No SDK
// is used: a Secrets Manager call is one POST with a JSON body, and the
// signing is a page of code whose correctness is checked against AWS's
// documented example (sigv4_test.go).
//
// https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html

// awsCredentials signs a request: the access key pair and, for temporary
// credentials, their session token.
type awsCredentials struct {
	accessKeyID, secretAccessKey, sessionToken string
}

// sigV4 is the signing of one request, kept apart so a test can check each
// documented intermediate value.
type sigV4 struct {
	canonicalRequest string
	stringToSign     string
	signingKey       []byte
	signature        string
	credentialScope  string
	signedHeaders    string
}

const (
	sigV4Algorithm = "AWS4-HMAC-SHA256"
	sigV4Request   = "aws4_request"
)

// signAWSV4 signs a request in place for a service in a region at a moment:
// it sets X-Amz-Date, X-Amz-Security-Token when the credentials have one,
// and Authorization. body is the request's exact payload, which is hashed.
func signAWSV4(request *http.Request, body []byte, credentials awsCredentials, region, service string, now time.Time) sigV4 {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	request.Header.Set("X-Amz-Date", amzDate)
	if credentials.sessionToken != "" {
		request.Header.Set("X-Amz-Security-Token", credentials.sessionToken)
	}
	// Every header set on the request is signed, the Host and Content-Length
	// the transport sends included, so nothing sent can be altered unsigned.
	names := []string{"host"}
	headers := map[string]string{"host": request.Host}
	if headers["host"] == "" {
		headers["host"] = request.URL.Host
	}
	if len(body) > 0 {
		names = append(names, "content-length")
		headers["content-length"] = strconv.Itoa(len(body))
	}
	for name, values := range request.Header {
		lower := strings.ToLower(name)
		switch lower {
		case "user-agent", "accept", "authorization", "content-length":
			continue
		}
		names = append(names, lower)
		headers[lower] = strings.Join(trimmedAll(values), ",")
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	payloadHash := sha256.Sum256(body)
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI(request.URL.Path),
		canonicalQuery(request.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		hex.EncodeToString(payloadHash[:]),
	}, "\n")
	date := now.Format("20060102")
	scope := strings.Join([]string{date, region, service, sigV4Request}, "/")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{sigV4Algorithm, amzDate, scope, hex.EncodeToString(requestHash[:])}, "\n")
	key := signingKey(credentials.secretAccessKey, date, region, service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))
	request.Header.Set("Authorization", sigV4Algorithm+" Credential="+credentials.accessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
	return sigV4{canonicalRequest: canonicalRequest, stringToSign: stringToSign, signingKey: key, signature: signature,
		credentialScope: scope, signedHeaders: signedHeaders}
}

// signingKey derives the day's key for a region and service: HMAC chains
// from "AWS4" + secret through the date, region, service and the constant.
func signingKey(secretAccessKey, date, region, service string) []byte {
	key := hmacSHA256([]byte("AWS4"+secretAccessKey), []byte(date))
	key = hmacSHA256(key, []byte(region))
	key = hmacSHA256(key, []byte(service))
	return hmacSHA256(key, []byte(sigV4Request))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// canonicalURI is the path with each segment URI-encoded, twice, as every
// service but S3 expects; "/" for an empty path.
func canonicalURI(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		segments[index] = uriEncode(uriEncode(segment))
	}
	return strings.Join(segments, "/")
}

// canonicalQuery sorts the parameters by name, then value, each URI-encoded.
func canonicalQuery(values url.Values) string {
	pairs := []string{}
	for name, list := range values {
		for _, value := range list {
			pairs = append(pairs, uriEncode(name)+"="+uriEncode(value))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode is RFC 3986 encoding as SigV4 wants it: unreserved characters
// (letters, digits, "-", "_", ".", "~") stay, every other byte is %XX in
// upper case.
func uriEncode(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); index++ {
		c := text[index]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			out.WriteByte(c)
		default:
			out.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return out.String()
}

func trimmedAll(values []string) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = strings.Join(strings.Fields(value), " ")
	}
	return out
}
