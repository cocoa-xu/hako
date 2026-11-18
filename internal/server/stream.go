package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const streamLease = 5 * time.Minute

type streamTiming struct {
	lease             time.Duration
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
}

type streamMessage struct {
	kind websocket.MessageType
	data []byte
	err  error
}

func (s *Server) receiveStream(w http.ResponseWriter, r *http.Request) {
	s.receiveStreamWithTiming(w, r, streamTiming{
		lease: streamLease, heartbeatInterval: 20 * time.Second, heartbeatTimeout: 10 * time.Second,
	})
}

func (s *Server) receiveStreamWithTiming(w http.ResponseWriter, r *http.Request, timing streamTiming) {
	id := identity.Normalize(r.Header.Get("X-Hako-Device-ID"))
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	authorized := bearer && validID(id) && s.workerAuthorizedLocked(id, token)
	closing := s.closing
	s.mu.Unlock()
	if closing {
		http.Error(w, "Receiver is shutting down.", http.StatusServiceUnavailable)
		return
	}
	if !authorized {
		http.Error(w, "Invalid agent credential.", http.StatusUnauthorized)
		return
	}
	if !slices.ContainsFunc(strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ","), func(value string) bool { return strings.TrimSpace(value) == report.StreamProtocol }) {
		http.Error(w, "Worker report protocol required.", http.StatusBadRequest)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Time{}); err != nil {
		http.Error(w, "Streaming is unavailable.", http.StatusInternalServerError)
		return
	}
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "Streaming is unavailable.", http.StatusInternalServerError)
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{report.StreamProtocol}})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(1 << 20)
	s.mu.Lock()
	authorized = s.workerAuthorizedLocked(id, token)
	closing = s.closing
	if authorized && !closing {
		s.streams[connection] = id
	}
	s.mu.Unlock()
	if !authorized {
		_ = connection.Close(websocket.StatusPolicyViolation, "Worker revoked")
		return
	}
	if closing {
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.streams, connection)
		s.mu.Unlock()
	}()
	lease := time.AfterFunc(timing.lease, func() { _ = connection.Close(websocket.StatusGoingAway, "Renew Access authentication") })
	defer lease.Stop()
	ctx, cancel := context.WithCancel(r.Context())
	messages, readDone := readStreamMessages(ctx, connection)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		keepStreamAlive(ctx, connection, timing.heartbeatInterval, timing.heartbeatTimeout)
	}()
	defer func() {
		cancel()
		_ = connection.CloseNow()
		<-readDone
		<-heartbeatDone
	}()
	var lastSequence uint64
	for {
		var frame streamMessage
		select {
		case <-ctx.Done():
			return
		case frame = <-messages:
		}
		if frame.err != nil {
			return
		}
		var message report.StreamReport
		if frame.kind != websocket.MessageText || json.Unmarshal(frame.data, &message) != nil || message.Sequence == 0 || message.Sequence <= lastSequence {
			_ = connection.Close(websocket.StatusUnsupportedData, "Invalid report frame")
			return
		}
		lastSequence = message.Sequence
		if identity.Normalize(message.Snapshot.DeviceID) != id {
			_ = connection.Close(websocket.StatusPolicyViolation, "Worker identity mismatch")
			return
		}
		status, _ := s.storeReport(message.Snapshot, token)
		write, stop := context.WithTimeout(ctx, 10*time.Second)
		err = wsjson.Write(write, connection, report.StreamAck{Sequence: message.Sequence, Status: status})
		stop()
		if err != nil {
			return
		}
		if status == http.StatusUnauthorized {
			_ = connection.Close(websocket.StatusPolicyViolation, "Worker revoked")
			return
		}
		if status != http.StatusNoContent && status != http.StatusConflict {
			_ = connection.Close(websocket.StatusInternalError, "Report rejected")
			return
		}
	}
}

func readStreamMessages(ctx context.Context, connection *websocket.Conn) (<-chan streamMessage, <-chan struct{}) {
	// An unbuffered handoff bounds queued data while the reader handles control frames.
	messages := make(chan streamMessage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			kind, data, err := connection.Read(ctx)
			select {
			case messages <- streamMessage{kind, data, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return messages, done
}

func keepStreamAlive(ctx context.Context, connection *websocket.Conn, interval, timeout time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ping, cancel := context.WithTimeout(ctx, timeout)
			err := connection.Ping(ping)
			cancel()
			if err != nil {
				_ = connection.CloseNow()
				return
			}
		}
	}
}

func (s *Server) closeReportStreams() {
	s.mu.Lock()
	s.closing = true
	connections := make([]*websocket.Conn, 0, len(s.streams))
	for connection := range s.streams {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.CloseNow()
	}
}
