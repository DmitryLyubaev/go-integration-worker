package acceptance

import (
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"github.com/DmitryLyubaev/go-integration-worker/internal/queue"
	"time"
)

type Config struct{ Binary, Fixture, Output, SourceCommit, SourceManifest, SourceArchive, SourceRoot string }
type FileEntry struct{ Path, SHA256 string }
type Attempt struct {
	EventID              string
	Number               int
	Token, Phase, Reason string
	Ambiguous            bool
	At                   time.Time
}
type Snapshot struct {
	At       time.Time
	Jobs     []queue.Job
	Attempts []Attempt
	Receipts []domain.Receipt
	Effects  []domain.Event
}
type CaseResult struct {
	Name, Snapshot, ReceiverLog string
	WorkerLogs                  []string
	Checkpoints                 map[string]string
}
type ProcessResult struct {
	Case, Role, Signal string
	PID, ExitCode      int
	Started, Stopped   time.Time
	ShutdownSeconds    float64
}
type Result struct {
	Root                                                              string `json:"-"`
	SourceCommit, SourceTreeHash, FixtureHash, BinaryHash, ConfigHash string
	Started, Finished                                                 time.Time
	Cases                                                             []CaseResult
	Processes                                                         []ProcessResult
	Inventory                                                         []FileEntry
	TraceFiles                                                        []string
	Cleaned                                                           bool
	Failure                                                           string
	Database                                                          string
}
