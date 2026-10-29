package secretstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// --- AWS Secrets Manager -----------------------------------------------------

// awsSession reads Secrets Manager through its JSON 1.1 protocol: every
// operation is a POST to the regional endpoint with the operation in
// X-Amz-Target, signed with SigV4 from the connection's access key pair. A
// secret is one value — a string, usually a JSON object of fields, or
// binary — with versions named by a version id and by staging labels
// (AWSCURRENT, AWSPREVIOUS, the operator's own). There is nothing to sign in
// to and nothing to revoke: each request carries its own signature.
type awsSession struct {
	endpoint, region string
	credentials      awsCredentials
	client           *http.Client
	now              func() time.Time
}

const awsService = "secretsmanager"

func openAWS(item *row, client *http.Client) *awsSession {
	return &awsSession{
		endpoint: item.Endpoint, region: item.Settings.Region, client: client, now: time.Now,
		credentials: awsCredentials{
			accessKeyID: item.credentials["accessKeyId"], secretAccessKey: item.credentials["secretAccessKey"],
			sessionToken: item.credentials["sessionToken"],
		},
	}
}

// call sends one operation and decodes its answer, or says what Secrets
// Manager answered instead, in its own words with the status.
func (s *awsSession) call(ctx context.Context, operation string, body any, into any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	status, raw, err := exchangeBytes(ctx, s.client, http.MethodPost, s.endpoint+"/", map[string]string{"X-Amz-Target": awsService + "." + operation},
		encoded, "application/x-amz-json-1.1", func(request *http.Request) error {
			signAWSV4(request, encoded, s.credentials, s.region, awsService, s.now())
			return nil
		})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return &awsError{status: status, kind: awsErrorType(raw), message: answerMessage(status, raw)}
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return errors.New("Secrets Manager did not answer as expected")
	}
	return nil
}

// awsError is a Secrets Manager refusal with its __type kept, so a caller
// can tell "may not list" from "is not who it says".
type awsError struct {
	status  int
	kind    string
	message string
}

func (e *awsError) Error() string { return e.message }

func awsErrorType(raw []byte) string {
	var shaped struct {
		Type string `json:"__type"`
	}
	_ = json.Unmarshal(raw, &shaped)
	return shaped.Type[strings.LastIndex(shaped.Type, "#")+1:]
}

// secretAnswer is GetSecretValue's answer: the value as a string or as
// bytes, and the version it is.
type secretAnswer struct {
	Name          string   `json:"Name"`
	VersionID     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages"`
	SecretString  *string  `json:"SecretString"`
	SecretBinary  string   `json:"SecretBinary"`
}

// read is one GetSecretValue: the current version, or the version a version
// id or staging label names. A version of 32 characters or more is a version
// id (Secrets Manager's are 32 to 64); shorter, it is a staging label.
func (s *awsSession) read(ctx context.Context, name, version string) (map[string]secretValue, error) {
	body := map[string]string{"SecretId": name}
	if version != "" {
		if len(version) >= 32 {
			body["VersionId"] = version
		} else {
			body["VersionStage"] = version
		}
	}
	var answer secretAnswer
	if err := s.call(ctx, "GetSecretValue", body, &answer); err != nil {
		return nil, err
	}
	if answer.SecretString == nil {
		if answer.SecretBinary == "" {
			return nil, fmt.Errorf("%s answered without a value", name)
		}
		// A binary secret is bytes the operator stored as such; a variable
		// carries them as they are.
		decoded, err := base64.StdEncoding.DecodeString(answer.SecretBinary)
		if err != nil {
			return nil, fmt.Errorf("%s holds binary data that could not be decoded", name)
		}
		whole := string(decoded)
		answer.SecretString = &whole
	}
	return wholeAndFields(*answer.SecretString, answer.VersionID), nil
}

// wholeAndFields is a whole secret as a reference without a key reads it
// (under ""), and, when the value is a JSON object, each of its fields
// under its key, so ${{store.aws/apps/web#DATABASE_URL}} is one field.
func wholeAndFields(value, version string) map[string]secretValue {
	out := map[string]secretValue{"": {value: value, version: version}}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &fields) == nil {
		for key, raw := range fields {
			out[key] = secretValue{value: jsonValue(raw), version: version}
		}
	}
	return out
}

func (s *awsSession) readPath(ctx context.Context, path string) (map[string]secretValue, error) {
	return s.read(ctx, path, "")
}

func (s *awsSession) readVersion(ctx context.Context, path, key, version string) (secretValue, error) {
	values, err := s.read(ctx, path, version)
	if err != nil {
		return secretValue{}, err
	}
	value, ok := values[key]
	if !ok {
		return secretValue{}, fmt.Errorf("version %s of %s has no field %s: its value is not a JSON object with that key", version, path, key)
	}
	return value, nil
}

// identity lists one secret: the one call that proves the key pair is
// accepted without naming a secret. An identity allowed only to read named
// secrets is still signed in, and says so; a probe then shows what it can
// read.
func (s *awsSession) identity(ctx context.Context) (string, error) {
	var answer struct {
		SecretList []struct {
			Name string `json:"Name"`
		} `json:"SecretList"`
	}
	where := "Secrets Manager in " + s.region
	err := s.call(ctx, "ListSecrets", map[string]any{"MaxResults": 1}, &answer)
	var refused *awsError
	if errors.As(err, &refused) && refused.kind == "AccessDeniedException" {
		return fmt.Sprintf("signed in to %s; this identity may not list secrets (%s)", where, refused.message), nil
	}
	if err != nil {
		return "", err
	}
	if len(answer.SecretList) == 0 {
		return "signed in to " + where + "; no secret is listed", nil
	}
	return fmt.Sprintf("signed in to %s; the first secret listed is %s", where, answer.SecretList[0].Name), nil
}

func (s *awsSession) close(context.Context) {}
