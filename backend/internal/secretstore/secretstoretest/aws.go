// Package secretstoretest stands in for the hosted secret managers that have
// no server to run in a container: AWS Secrets Manager and Azure Key Vault.
// Each fake follows the service's documented API closely enough that the
// client is exercised as the real service would — the AWS one checks every
// request's Signature Version 4 with an implementation of its own, which
// LocalStack does not — and answers refusals in the service's own words.
package secretstoretest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// AWSVersion is one version of a secret: its id, the staging labels on it,
// and its value as a string or as bytes.
type AWSVersion struct {
	ID     string
	Stages []string
	String string
	Binary []byte
}

// AWS is a Secrets Manager for one region and one access key pair. Secrets
// maps a name to its versions, oldest first.
type AWS struct {
	Region, AccessKeyID, SecretAccessKey, SessionToken string
	Secrets                                            map[string][]AWSVersion
	// ListDenied answers ListSecrets with AccessDeniedException, as an
	// identity allowed only to read named secrets is answered.
	ListDenied bool

	mu       sync.Mutex
	requests []string
}

// Requests are the operations asked so far, as "Operation SecretId".
func (f *AWS) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func awsError(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": kind, "Message": message})
}

func (f *AWS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-amz-json-1.1" {
		awsError(w, http.StatusNotFound, "UnknownOperationException", "")
		return
	}
	if problem, status, kind := f.verify(r, body); problem != "" {
		awsError(w, status, kind, problem)
		return
	}
	operation := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), "secretsmanager.")
	var request struct {
		SecretID     string `json:"SecretId"`
		VersionID    string `json:"VersionId"`
		VersionStage string `json:"VersionStage"`
		MaxResults   int    `json:"MaxResults"`
	}
	_ = json.Unmarshal(body, &request)
	f.mu.Lock()
	f.requests = append(f.requests, strings.TrimSpace(operation+" "+request.SecretID))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	switch operation {
	case "ListSecrets":
		if f.ListDenied {
			awsError(w, http.StatusBadRequest, "AccessDeniedException",
				"User: arn:aws:iam::123456789012:user/just-dashboard is not authorized to perform: secretsmanager:ListSecrets on resource: * because no identity-based policy allows the secretsmanager:ListSecrets action")
			return
		}
		names := make([]string, 0, len(f.Secrets))
		for name := range f.Secrets {
			names = append(names, name)
		}
		sort.Strings(names)
		list := []map[string]any{}
		for _, name := range names {
			if request.MaxResults > 0 && len(list) >= request.MaxResults {
				break
			}
			list = append(list, map[string]any{"Name": name, "ARN": "arn:aws:secretsmanager:" + f.Region + ":123456789012:secret:" + name + "-AbCdEf"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"SecretList": list})
	case "GetSecretValue":
		versions, ok := f.Secrets[request.SecretID]
		if !ok || len(versions) == 0 {
			awsError(w, http.StatusBadRequest, "ResourceNotFoundException", "Secrets Manager can't find the specified secret.")
			return
		}
		var found *AWSVersion
		switch {
		case request.VersionID != "":
			for index := range versions {
				if versions[index].ID == request.VersionID {
					found = &versions[index]
				}
			}
			if found == nil {
				awsError(w, http.StatusBadRequest, "ResourceNotFoundException", "Secrets Manager can't find the specified secret value for VersionId: "+request.VersionID)
				return
			}
		default:
			stage := request.VersionStage
			if stage == "" {
				stage = "AWSCURRENT"
			}
			for index := range versions {
				for _, label := range versions[index].Stages {
					if label == stage {
						found = &versions[index]
					}
				}
			}
			if found == nil {
				awsError(w, http.StatusBadRequest, "ResourceNotFoundException", "Secrets Manager can't find the specified secret value for staging label: "+stage)
				return
			}
		}
		answer := map[string]any{
			"ARN": "arn:aws:secretsmanager:" + f.Region + ":123456789012:secret:" + request.SecretID + "-AbCdEf", "Name": request.SecretID,
			"VersionId": found.ID, "VersionStages": found.Stages, "CreatedDate": 1.7e9,
		}
		if found.Binary != nil {
			answer["SecretBinary"] = base64.StdEncoding.EncodeToString(found.Binary)
		} else {
			answer["SecretString"] = found.String
		}
		_ = json.NewEncoder(w).Encode(answer)
	default:
		awsError(w, http.StatusBadRequest, "UnknownOperationException", "")
	}
}

// verify checks the request's Signature Version 4 the way the service does:
// the credential names this key and scope, the date is near now, the signed
// headers are all present, and the signature is the one this implementation
// computes from the request — independently of the client's.
func (f *AWS) verify(r *http.Request, body []byte) (problem string, status int, kind string) {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 ") {
		return "Missing Authentication Token", http.StatusForbidden, "MissingAuthenticationTokenException"
	}
	parts := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(authorization, "AWS4-HMAC-SHA256 "), ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		parts[name] = value
	}
	scope := strings.Split(parts["Credential"], "/")
	if len(scope) != 5 || scope[0] != f.AccessKeyID {
		return "The security token included in the request is invalid.", http.StatusForbidden, "UnrecognizedClientException"
	}
	amzDate := r.Header.Get("X-Amz-Date")
	at, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil || scope[1] != at.Format("20060102") || scope[2] != f.Region || scope[3] != "secretsmanager" || scope[4] != "aws4_request" {
		return "Credential should be scoped to a valid region.", http.StatusForbidden, "InvalidSignatureException"
	}
	if skew := time.Since(at); skew > 5*time.Minute || skew < -5*time.Minute {
		return "Signature expired", http.StatusForbidden, "InvalidSignatureException"
	}
	if f.SessionToken != "" && r.Header.Get("X-Amz-Security-Token") != f.SessionToken {
		return "The security token included in the request is invalid.", http.StatusForbidden, "UnrecognizedClientException"
	}
	signed := strings.Split(parts["SignedHeaders"], ";")
	for _, want := range []string{"host", "x-amz-date", "x-amz-target", "content-type"} {
		if !contains(signed, want) {
			return "'" + want + "' is not signed", http.StatusForbidden, "InvalidSignatureException"
		}
	}
	var canonicalHeaders strings.Builder
	for _, name := range signed {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		canonicalHeaders.WriteString(name + ":" + strings.Join(strings.Fields(value), " ") + "\n")
	}
	payload := sha256.Sum256(body)
	canonical := strings.Join([]string{r.Method, "/", "", canonicalHeaders.String(), parts["SignedHeaders"], hex.EncodeToString(payload[:])}, "\n")
	canonicalHash := sha256.Sum256([]byte(canonical))
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, strings.Join(scope[1:], "/"), hex.EncodeToString(canonicalHash[:])}, "\n")
	key := []byte("AWS4" + f.SecretAccessKey)
	for _, part := range []string{scope[1], scope[2], scope[3], scope[4]} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(stringToSign))
	if hex.EncodeToString(mac.Sum(nil)) != parts["Signature"] {
		return "The request signature we calculated does not match the signature you provided. Check your AWS Secret Access Key and signing method. Consult the service documentation for details.",
			http.StatusForbidden, "InvalidSignatureException"
	}
	return "", 0, ""
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
