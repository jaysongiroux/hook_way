package deliverysnapshot

// Publish freezes broker bytes and identities for redrive.
type Publish struct {
	GroupID          string `json:"group_id,omitempty"`
	DelaySeconds     *int32 `json:"delay_seconds,omitempty"`
	Subject          string `json:"subject,omitempty"`
	MessageStructure string `json:"message_structure,omitempty"`
	NodeID           string `json:"node_id"`
	Destination      string `json:"destination"`
	Target           string `json:"target"`
	WireBody         string `json:"wire_body"`
	Body             []byte `json:"body"`
	FIFO             *FIFO  `json:"fifo,omitempty"`
	Retry            *Retry `json:"retry,omitempty"`
}

type FIFO struct {
	GroupID         string `json:"group_id"`
	DeduplicationID string `json:"deduplication_id"`
	ContentBased    bool   `json:"content_based"`
}

type Retry struct {
	MaxAttempts    int   `json:"max_attempts"`
	InitialBackoff int64 `json:"initial_backoff_ns"`
	MaxBackoff     int64 `json:"max_backoff_ns"`
}
