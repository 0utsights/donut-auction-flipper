package network

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

const ProtocolVersion byte = 1
const MaxFrameSize = 64 << 10

type Priority byte

const (
	P0 Priority = iota
	P1
	P2
)

type MessageType byte

const (
	MsgSnapshot MessageType = iota + 1
	MsgPriceUpdate
	MsgPriceInvalidation
	MsgListingObserved
	MsgFlipDetected
	MsgPurchaseResult
	MsgWorkerHeartbeat
	MsgAssignment
	MsgChat
	MsgPresence
	MsgListing
	MsgSnapshotChunk
)

type Frame struct {
	Priority Priority
	Type     MessageType
	Payload  []byte
}

func Encode(priority Priority, typ MessageType, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxFrameSize {
		return nil, errors.New("payload exceeds 64 KiB limit")
	}
	out := make([]byte, 7+len(payload))
	out[0] = ProtocolVersion
	out[1] = byte(priority)
	out[2] = byte(typ)
	binary.BigEndian.PutUint32(out[3:7], uint32(len(payload)))
	copy(out[7:], payload)
	return out, nil
}
func Decode(data []byte) (Frame, error) {
	if len(data) < 7 {
		return Frame{}, errors.New("short frame")
	}
	if data[0] != ProtocolVersion {
		return Frame{}, errors.New("unsupported protocol version")
	}
	n := int(binary.BigEndian.Uint32(data[3:7]))
	if n > MaxFrameSize || n != len(data)-7 {
		return Frame{}, errors.New("invalid payload length")
	}
	p := Priority(data[1])
	if p > P2 {
		return Frame{}, errors.New("invalid priority")
	}
	return Frame{Priority: p, Type: MessageType(data[2]), Payload: data[7:]}, nil
}

type ChatMessage struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	Text    string `json:"text"`
	SentAt  int64  `json:"sent_at"`
}
type TelemetryEvent struct {
	ID          string            `json:"id"`
	ClientID    string            `json:"client_id"`
	Kind        string            `json:"kind"`
	Fingerprint string            `json:"fingerprint,omitempty"`
	Signature   string            `json:"signature,omitempty"`
	Price       int64             `json:"price,omitempty"`
	LatencyNS   int64             `json:"latency_ns,omitempty"`
	Success     bool              `json:"success,omitempty"`
	ObservedAt  int64             `json:"observed_at"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
