// Package netflows retains bounded native socket observations. It deliberately
// does not promise complete flows, billing totals or end-to-end packet loss.
package netflows

import (
	"fmt"
	"time"
)

const (
	MaxSockets     = 1024
	MaxSources     = 4
	MaxOutputBytes = 2 << 20
	MaxRows        = 50000
	MaxStoredBytes = 32 << 20
	MaxExportRows  = 1000
	MaxExportBytes = 2 << 20
)

type Settings struct {
	Enabled               bool `json:"enabled"`
	KernelObserverEnabled bool `json:"kernelObserverEnabled"`
	IntervalSeconds       int  `json:"intervalSeconds"`
	RetentionDays         int  `json:"retentionDays"`
}

var DefaultSettings = Settings{IntervalSeconds: 30, RetentionDays: 7}

func (s Settings) Validate() error {
	if s.IntervalSeconds < 10 || s.IntervalSeconds > 300 || s.RetentionDays < 1 || s.RetentionDays > 31 {
		return fmt.Errorf("select a sample interval from 10 to 300 seconds and retention from 1 to 31 days")
	}
	return nil
}

type Owner struct {
	Status             string `json:"status"`
	Program            string `json:"program,omitempty"`
	PID                int    `json:"pid,omitempty"`
	StartTicks         string `json:"startTicks,omitempty"`
	ContainerID        string `json:"containerId,omitempty"`
	ContainerName      string `json:"containerName,omitempty"`
	ContainerStartedAt string `json:"containerStartedAt,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type Socket struct {
	Protocol       string  `json:"protocol"`
	State          string  `json:"state"`
	LocalAddress   string  `json:"localAddress"`
	LocalEndpoint  string  `json:"localEndpoint"`
	LocalPort      int     `json:"localPort"`
	RemoteAddress  string  `json:"remoteAddress,omitempty"`
	RemoteEndpoint string  `json:"remoteEndpoint"`
	RemotePort     int     `json:"remotePort,omitempty"`
	Cookie         string  `json:"cookie,omitempty"`
	Inode          string  `json:"inode,omitempty"`
	Tx             *uint64 `json:"tx,omitempty"`
	Rx             *uint64 `json:"rx,omitempty"`
	Retrans        *uint64 `json:"retrans,omitempty"`
	Lost           *uint64 `json:"lost,omitempty"`
	Owner          Owner   `json:"owner"`
	owners         []socketOwner
}

type socketOwner struct {
	Name    string
	PID, FD int
}

type Source struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Namespace           string    `json:"namespace,omitempty"`
	Status              string    `json:"status"`
	Error               string    `json:"error,omitempty"`
	Sockets             int       `json:"sockets"`
	TCP                 int       `json:"tcp"`
	UDP                 int       `json:"udp"`
	TCPTxCounters       int       `json:"tcpTxCounters"`
	TCPRxCounters       int       `json:"tcpRxCounters"`
	UnverifiedOwners    int       `json:"unverifiedOwners"`
	Truncated           bool      `json:"truncated"`
	Malformed           int       `json:"malformed"`
	IdentityUnavailable int       `json:"identityUnavailable"`
	UnconnectedUDP      int       `json:"unconnectedUdp"`
	ObservedAt          time.Time `json:"observedAt"`
	Values              []Socket  `json:"-"`
}

type Cycle struct {
	At                   time.Time         `json:"at"`
	FinishedAt           time.Time         `json:"finishedAt"`
	BootID               string            `json:"bootId,omitempty"`
	KernelRelease        string            `json:"kernelRelease,omitempty"`
	Tool                 string            `json:"tool"`
	ToolVersion          string            `json:"toolVersion,omitempty"`
	ToolVersionError     string            `json:"toolVersionError,omitempty"`
	ToolVersionCheckedAt time.Time         `json:"toolVersionCheckedAt"`
	Sources              []Source          `json:"sources"`
	DockerStatus         string            `json:"dockerStatus"`
	DockerError          string            `json:"dockerError,omitempty"`
	OmittedSources       int               `json:"omittedSources"`
	DiscardedIntervals   int               `json:"discardedIntervals"`
	ElapsedMillis        int64             `json:"elapsedMillis"`
	DroppedEvents        *uint64           `json:"droppedEvents,string"`
	Observer             *ObserverEvidence `json:"observer,omitempty"`
}

type CoverageHour struct {
	Hour               time.Time       `json:"hour"`
	FirstSampleAt      time.Time       `json:"firstSampleAt"`
	LastSampleAt       time.Time       `json:"lastSampleAt"`
	Samples            int             `json:"samples"`
	FailedSources      int             `json:"failedSources"`
	TruncatedSources   int             `json:"truncatedSources"`
	OmittedSources     int             `json:"omittedSources"`
	DiscardedIntervals int             `json:"discardedIntervals"`
	CaptureMillis      int64           `json:"captureMillis"`
	MaxCaptureMillis   int64           `json:"maxCaptureMillis"`
	LastCycle          Cycle           `json:"lastCycle"`
	ObserverQuality    ObserverQuality `json:"observerQuality"`
}

type Bucket struct {
	ID                     string    `json:"id"`
	Hour                   time.Time `json:"hour"`
	FirstSeen              time.Time `json:"firstSeen"`
	LastSeen               time.Time `json:"lastSeen"`
	SourceID               string    `json:"sourceId"`
	SourceName             string    `json:"sourceName"`
	Namespace              string    `json:"namespace"`
	BootID                 string    `json:"bootId"`
	Socket                 Socket    `json:"socket"`
	Samples                int       `json:"samples"`
	TxBytes                *uint64   `json:"txBytes,string"`
	RxBytes                *uint64   `json:"rxBytes,string"`
	Retransmissions        *uint64   `json:"retransmissions,string"`
	LostGaugeMax           *uint64   `json:"lostGaugeMax,string"`
	MeasuredIntervals      int       `json:"measuredIntervals"`
	TxIntervals            int       `json:"txIntervals"`
	RxIntervals            int       `json:"rxIntervals"`
	RetransIntervals       int       `json:"retransIntervals"`
	SkippedIntervals       int       `json:"skippedIntervals"`
	Evidence               string    `json:"evidence,omitempty"`
	SocketCgroup           string    `json:"socketCgroup,omitempty"`
	ObservedTxBytes        *uint64   `json:"observedTxBytes,string"`
	ObservedRxBytes        *uint64   `json:"observedRxBytes,string"`
	ObservedPackets        uint64    `json:"observedPackets,string"`
	ObservedTxPackets      uint64    `json:"observedTxPackets,string"`
	ObservedRxPackets      uint64    `json:"observedRxPackets,string"`
	ObservedTxKnownPackets uint64    `json:"observedTxKnownPackets,string"`
	ObservedRxKnownPackets uint64    `json:"observedRxKnownPackets,string"`
	ObservedTxByteGaps     uint64    `json:"observedTxByteGaps,string"`
	ObservedRxByteGaps     uint64    `json:"observedRxByteGaps,string"`
	TimestampUncertain     bool      `json:"timestampUncertain"`
	ObservedSYN            uint64    `json:"observedSyn,string"`
	ObservedFIN            uint64    `json:"observedFin,string"`
	ObservedRST            uint64    `json:"observedRst,string"`
}

type Query struct {
	From, To             time.Time
	Address, ContainerID string
	Limit                int
}

type Report struct {
	CheckedAt          time.Time        `json:"checkedAt"`
	From               time.Time        `json:"from"`
	To                 time.Time        `json:"to"`
	Status             string           `json:"status"`
	Settings           Settings         `json:"settings"`
	RecordingSince     *time.Time       `json:"recordingSince"`
	CollectorStartedAt time.Time        `json:"collectorStartedAt"`
	LastCycle          *Cycle           `json:"lastCycle"`
	Rows               []Bucket         `json:"rows"`
	CoverageHours      []CoverageHour   `json:"coverageHours"`
	Truncated          bool             `json:"truncated"`
	CoverageTruncated  bool             `json:"coverageTruncated"`
	RetainedRows       int              `json:"retainedRows"`
	RetainedBytes      int64            `json:"retainedBytes"`
	RetainedFrom       *time.Time       `json:"retainedFrom"`
	PrunedRows         int64            `json:"prunedRows"`
	Error              string           `json:"error,omitempty"`
	Coverage           []string         `json:"coverage"`
	KernelObserver     ObserverEvidence `json:"kernelObserver"`
}

// Quality counters distinguish failure to deliver an event from intentionally
// omitted work and from bytes/owners that the declared hooks cannot prove.
type ObserverQuality struct {
	Events              uint64 `json:"events,string"`
	RingDrops           uint64 `json:"ringDrops,string"`
	BudgetOmissions     uint64 `json:"budgetOmissions,string"`
	HeaderGaps          uint64 `json:"headerGaps,string"`
	IdentityGaps        uint64 `json:"identityGaps,string"`
	StateAdmissionGaps  uint64 `json:"stateAdmissionGaps,string"`
	ParserGaps          uint64 `json:"parserGaps,string"`
	ByteGaps            uint64 `json:"byteGaps,string"`
	PendingOmissions    uint64 `json:"pendingOmissions,string"`
	AttributionGaps     uint64 `json:"attributionGaps,string"`
	ReaderBudgetPauses  uint64 `json:"readerBudgetPauses,string"`
	UnsavedEvents       uint64 `json:"unsavedEvents,string"`
	TimestampGaps       uint64 `json:"timestampGaps,string"`
	ShutdownTailUnknown bool   `json:"shutdownTailUnknown"`
}
type ObserverEvidence struct {
	BatchID              string          `json:"batchId,omitempty"`
	AttachmentsRetained  bool            `json:"attachmentsRetained"`
	DockerStatus         string          `json:"dockerStatus"`
	DockerError          string          `json:"dockerError,omitempty"`
	DockerCheckedAt      time.Time       `json:"dockerCheckedAt"`
	TimestampReason      string          `json:"timestampReason,omitempty"`
	Status               string          `json:"status"`
	Reason               string          `json:"reason"`
	Digest               string          `json:"digest,omitempty"`
	KernelRelease        string          `json:"kernelRelease,omitempty"`
	BootID               string          `json:"bootId,omitempty"`
	TargetCgroup         string          `json:"targetCgroup,omitempty"`
	StartedAt            *time.Time      `json:"startedAt"`
	CheckedAt            time.Time       `json:"checkedAt"`
	StoppedAt            *time.Time      `json:"stoppedAt"`
	ProgramIDs           []uint32        `json:"programIds"`
	LinkIDs              []uint32        `json:"linkIds"`
	RingBytes            int             `json:"ringBytes"`
	EventsPerSecond      int             `json:"eventsPerSecond"`
	SocketCapacity       int             `json:"socketCapacity"`
	PendingCapacity      int             `json:"pendingCapacity"`
	DockerSources        int             `json:"dockerSources"`
	OmittedDockerSources int             `json:"omittedDockerSources"`
	Quality              ObserverQuality `json:"quality"`
}

var Coverage = []string{
	"Native ss socket snapshots; observed TCP counter deltas only, not complete bandwidth accounting.",
	"TCP bytes_sent and bytes_received are retained only when native fields are present in both reads of the same socket identity.",
	"UDP peer metadata has no per-socket byte totals; unconnected UDP has no observed destination.",
	"Sockets born and closed between snapshots are missed. Disappearance is not a measured close event; dropped-event count is unknown.",
	"First sight, restarts, counter resets, failed reads, retention boundaries and UTC-hour crossing intervals do not contribute lifetime bytes.",
	"Retransmissions are sampled sender counters; lost is a maximum sampled outstanding-loss gauge, not end-to-end loss rate.",
	"Rows identify observed descriptor owners and peers; they do not prove which side initiated a connection or its NAT/provider path.",
}
