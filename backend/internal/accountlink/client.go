package accountlink

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// requestTimeout bounds every exchange with the site; a long poll adds the
// time it asks the site to hold it.
const requestTimeout = 15 * time.Second

// APIError is the site refusing a request in the dashboard's own error shape.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("the site answered %d %s: %s", e.Status, e.Code, e.Message)
	case e.Code != "":
		return fmt.Sprintf("the site answered %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("the site answered HTTP %d", e.Status)
}

func isCode(err error, status int, code string) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == status && (code == "" || api.Code == code)
}

// Client speaks the link protocol to one site. It signs every request that the
// protocol asks to be signed with the install's identity, takes a persisted
// sequence number for each, and never follows a redirect.
type Client struct {
	origin  string
	http    *http.Client
	id      *Identity
	next    func(context.Context) (int64, error)
	now     func() time.Time
	version string
}

// NewHTTPClient is the transport every exchange uses: TLS 1.2 at least, the
// ordinary proxy environment, and a refusal of any redirect.
func NewHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport: t,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the site redirected a link request, which is never followed")
		},
	}
}

type wireSiteKey struct {
	Kid       string `json:"kid"`
	PublicKey string `json:"publicKey"`
	NotAfter  *int64 `json:"notAfter"`
}

type siteInfo struct {
	Protocol   []int         `json:"protocol"`
	SiteKeys   []wireSiteKey `json:"siteKeys"`
	ApproveURL string        `json:"approveUrl"`
}

type serverDescription struct {
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type createRequestBody struct {
	PublicKey string            `json:"publicKey"`
	Scope     Scope             `json:"scope"`
	Server    serverDescription `json:"server"`
}

type createRequestResponse struct {
	RequestID   string `json:"requestId"`
	UserCode    string `json:"userCode"`
	ApproveURL  string `json:"approveUrl"`
	ExpiresAt   int64  `json:"expiresAt"`
	PollSeconds int    `json:"pollSeconds"`
}

type pollResponse struct {
	Status   string `json:"status"`
	ServerID string `json:"serverId,omitempty"`
	SiteKey  *struct {
		Kid       string `json:"kid"`
		PublicKey string `json:"publicKey"`
	} `json:"siteKey,omitempty"`
	HeartbeatSeconds int `json:"heartbeatSeconds,omitempty"`
	PollWaitSeconds  int `json:"pollWaitSeconds,omitempty"`
	Owner            *struct {
		Label string `json:"label"`
	} `json:"owner,omitempty"`
}

type heartbeatResponse struct {
	OK               bool          `json:"ok"`
	HeartbeatSeconds int           `json:"heartbeatSeconds"`
	PendingCommands  int           `json:"pendingCommands"`
	SiteKeys         []wireSiteKey `json:"siteKeys,omitempty"`
}

type commandsResponse struct {
	Commands []Command `json:"commands"`
}

// Command is one thing the site asks this dashboard to do, as it arrives.
type Command struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Args      json.RawMessage `json:"args"`
	IssuedBy  string          `json:"issuedBy"`
	IssuedAt  int64           `json:"issuedAt"`
	ExpiresAt int64           `json:"expiresAt"`
	Kid       string          `json:"kid"`
	Signature string          `json:"signature"`
}

// CommandResult is what the dashboard reports back for a command.
type CommandResult struct {
	Status     string `json:"status"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
}

const (
	resultSucceeded = "succeeded"
	resultFailed    = "failed"
	resultRefused   = "refused"
)

// call performs one exchange. A signed call takes a fresh sequence number for
// every attempt and is retried when the site says that number was not the
// newest it had seen: two requests of this process can reach the site in the
// opposite order to the one their numbers were taken in, and a request refused
// for its sequence has not been acted on, so sending it again is safe.
func (c *Client) call(ctx context.Context, method, pathWithQuery string, body any, server string, hold time.Duration, out any) (int, error) {
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return 0, err
		}
		if len(raw) > maxRequestBody {
			return 0, fmt.Errorf("the request body is %d bytes, above the %d the protocol allows", len(raw), maxRequestBody)
		}
	}
	var status int
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		status, err = c.once(ctx, method, pathWithQuery, raw, server, hold, out)
		if !isCode(err, http.StatusUnauthorized, "replayed_sequence") || server == "" {
			return status, err
		}
	}
	return status, err
}

func (c *Client) once(ctx context.Context, method, pathWithQuery string, raw []byte, server string, hold time.Duration, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout+hold)
	defer cancel()
	var reader io.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+pathWithQuery, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "just-dashboard/"+c.version+" account-link")
	req.Header.Set(headerProtocol, strconv.Itoa(ProtocolVersion))
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if server != "" {
		seq, err := c.next(ctx)
		if err != nil {
			return 0, err
		}
		at := c.now().Unix()
		req.Header.Set(headerServer, server)
		req.Header.Set(headerTime, strconv.FormatInt(at, 10))
		req.Header.Set(headerSequence, strconv.FormatInt(seq, 10))
		req.Header.Set(headerSignature, signRequest(c.id.key, method, pathWithQuery, raw, at, seq))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(data) > maxResponseBody {
		return resp.StatusCode, fmt.Errorf("the site's response is larger than the %d bytes the protocol allows", maxResponseBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, decodeAPIError(resp.StatusCode, data)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if err := decodeStrict(data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("the site's response is not what protocol %d describes: %w", ProtocolVersion, err)
	}
	return resp.StatusCode, nil
}

func decodeAPIError(status int, data []byte) error {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// An error that does not parse is still an error; only its detail is lost.
	_ = json.Unmarshal(data, &body)
	return &APIError{Status: status, Code: clip(body.Error.Code, 64), Message: clip(body.Error.Message, 300)}
}

func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if len(s) > n {
		s = s[:n]
		// A cut through the middle of a character is not a string to send.
		for len(s) > 0 && !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

func (c *Client) site(ctx context.Context) (siteInfo, error) {
	var info siteInfo
	_, err := c.call(ctx, http.MethodGet, basePath+"/site", nil, "", 0, &info)
	return info, err
}

func (c *Client) createRequest(ctx context.Context, body createRequestBody) (createRequestResponse, error) {
	var out createRequestResponse
	_, err := c.call(ctx, http.MethodPost, basePath+"/requests", body, "", 0, &out)
	return out, err
}

func (c *Client) pollRequest(ctx context.Context, requestID string) (pollResponse, error) {
	var out pollResponse
	_, err := c.call(ctx, http.MethodGet, basePath+"/requests/"+requestID, nil, requestID, 0, &out)
	return out, err
}

func (c *Client) heartbeat(ctx context.Context, serverID string, body json.RawMessage) (heartbeatResponse, error) {
	var out heartbeatResponse
	_, err := c.call(ctx, http.MethodPost, basePath+"/heartbeat", body, serverID, 0, &out)
	return out, err
}

func (c *Client) commands(ctx context.Context, serverID string, wait int) (commandsResponse, error) {
	var out commandsResponse
	path := basePath + "/commands?wait=" + strconv.Itoa(wait)
	_, err := c.call(ctx, http.MethodGet, path, nil, serverID, time.Duration(wait)*time.Second, &out)
	return out, err
}

func (c *Client) result(ctx context.Context, serverID, commandID string, res CommandResult) error {
	_, err := c.call(ctx, http.MethodPost, basePath+"/commands/"+commandID+"/result", res, serverID, 0, nil)
	return err
}

func (c *Client) unlink(ctx context.Context, serverID string) error {
	_, err := c.call(ctx, http.MethodPost, basePath+"/unlink", nil, serverID, 0, nil)
	return err
}
