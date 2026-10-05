package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// WorkloadCandidate deliberately carries no process argv, environment values,
// label values or configuration contents. Registration preserves those at their
// original manager rather than reconstructing a deployment from a partial view.
type WorkloadCandidate struct {
	Key                    string            `json:"key"`
	Kind                   string            `json:"kind"`
	Name                   string            `json:"name"`
	ResourceID             string            `json:"resourceId"`
	State                  string            `json:"state"`
	Running                int               `json:"running"`
	Total                  int               `json:"total"`
	Services               []WorkloadService `json:"services"`
	SourcePath             string            `json:"sourcePath,omitempty"`
	ManagerURL             string            `json:"managerUrl"`
	ConfigurationAvailable bool              `json:"configurationAvailable"`
	Warnings               []string          `json:"warnings"`
	Digest                 string            `json:"digest"`
	ImportedProjectID      int64             `json:"importedProjectId,omitempty"`
}

type WorkloadService struct {
	Name       string                `json:"name"`
	ResourceID string                `json:"resourceId"`
	State      string                `json:"state"`
	Health     string                `json:"health,omitempty"`
	Image      string                `json:"image,omitempty"`
	Ports      []dockerx.PortMapping `json:"ports"`
	PID        int32                 `json:"pid,omitempty"`
	CreatedAt  int64                 `json:"createdAt,omitempty"`
}

type WorkloadDiscovery struct {
	CheckedAt time.Time           `json:"checkedAt"`
	Items     []WorkloadCandidate `json:"items"`
	Silences  []string            `json:"silences"`
}

// WorkloadDigest fences replacement and topology changes without treating a
// healthy process's next restart, or a stopped service starting, as new settings.
func WorkloadDigest(candidate WorkloadCandidate) string {
	candidate.Digest = ""
	candidate.State, candidate.Running = "", 0
	candidate.ImportedProjectID = 0
	candidate.Services = append([]WorkloadService(nil), candidate.Services...)
	for index := range candidate.Services {
		candidate.Services[index].State = ""
		candidate.Services[index].Health = ""
		if candidate.Kind != "process" {
			candidate.Services[index].PID = 0
		}
	}
	encoded, _ := json.Marshal(candidate)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
