package report

// StreamProtocol identifies the acknowledged worker report protocol.
const StreamProtocol = "hako.reports.v1"

// StreamReport carries one observation on a worker connection.
type StreamReport struct {
	Sequence uint64   `json:"sequence"`
	Snapshot Snapshot `json:"snapshot"`
}

// StreamAck confirms persistence or requests corrective action using HTTP status codes.
type StreamAck struct {
	Sequence uint64 `json:"sequence"`
	Status   int    `json:"status"`
}
