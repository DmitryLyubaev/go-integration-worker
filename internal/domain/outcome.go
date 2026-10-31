package domain

import "time"

type Receipt struct {
	EventID     string `json:"event_id"`
	PayloadHash string `json:"payload_hash"`
	ReceiptID   string `json:"receipt_id"`
}
type OutcomeKind string

const (
	Success      OutcomeKind = "success"
	Permanent    OutcomeKind = "permanent"
	Retryable    OutcomeKind = "retryable"
	Unrecognized OutcomeKind = "unrecognized"
	Canceled     OutcomeKind = "canceled"
)

type Outcome struct {
	Kind       OutcomeKind
	Ambiguous  bool
	RetryAfter time.Duration
	Receipt    *Receipt
	Reason     string
}
