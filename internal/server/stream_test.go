package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func dialReports(t *testing.T, origin string, config agent.Config) *websocket.Conn {
	t.Helper()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+config.Token)
	headers.Set("X-Hako-Device-ID", config.DeviceID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, origin+"/api/reports", &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{report.StreamProtocol}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func streamReport(t *testing.T, connection *websocket.Conn, sequence uint64, snapshot report.Snapshot) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, connection, report.StreamReport{Sequence: sequence, Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	var ack report.StreamAck
	if err := wsjson.Read(ctx, connection, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Sequence != sequence {
		t.Fatal("acknowledgement mismatch")
	}
	return ack.Status
}

func TestReportStreamPersistsInventoryAndClearsHTTPDeadlines(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewUnstartedServer(s.Handler())
	receiver.Config.ReadTimeout = 50 * time.Millisecond
	receiver.Config.WriteTimeout = 50 * time.Millisecond
	receiver.Start()
	defer receiver.Close()
	connection := dialReports(t, receiver.URL, config)
	time.Sleep(100 * time.Millisecond)
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now().UTC()}
	if status := streamReport(t, connection, 1, snapshot); status != 409 {
		t.Fatal("missing inventory accepted", status)
	}
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot.Hardware = &hardware
	if status := streamReport(t, connection, 2, snapshot); status != 204 {
		t.Fatal(status)
	}
	snapshot.Hardware = nil
	snapshot.System = report.OK(report.System{Hostname: "updated-mac"})
	if status := streamReport(t, connection, 3, snapshot); status != 204 {
		t.Fatal(status)
	}
	_ = connection.CloseNow()
	s = restartServer(t, s)
	machine := s.machines[config.DeviceID]
	if machine.Snapshot.Hardware.Data.ModelName != "Test Mac" || machine.Snapshot.System.Data.Hostname != "updated-mac" {
		t.Fatal("stream report or inventory was lost on restart")
	}
}

func TestStreamAuthenticationCannotUseAdminOrOtherWorkerIdentity(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewServer(s.Handler())
	defer receiver.Close()
	for _, test := range []struct {
		token, id, origin, protocol string
		want                        int
	}{
		{"", config.DeviceID, "", report.StreamProtocol, 401},
		{s.adminKey, config.DeviceID, "", report.StreamProtocol, 401},
		{config.Token, identity.New(), "", report.StreamProtocol, 401},
		{config.Token, config.DeviceID, "https://attacker.example", report.StreamProtocol, 403},
		{config.Token, config.DeviceID, "", "wrong-protocol", 400},
	} {
		headers := make(http.Header)
		headers.Set("Authorization", "Bearer "+test.token)
		headers.Set("X-Hako-Device-ID", test.id)
		headers.Set("Origin", test.origin)
		connection, response, err := websocket.Dial(context.Background(), receiver.URL+"/api/reports", &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{test.protocol}})
		if err == nil {
			connection.CloseNow()
			t.Fatal("invalid stream accepted")
		}
		if response == nil || response.StatusCode != test.want {
			t.Fatal("unexpected rejection", response, err)
		}
	}
	connection := dialReports(t, receiver.URL, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = wsjson.Write(ctx, connection, report.StreamReport{Sequence: 1, Snapshot: report.Snapshot{DeviceID: identity.New()}})
	_, _, err := connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("stream identity was not bound", err)
	}
}

func TestRevocationClosesIdleStreamsAndPreventsReconnect(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewServer(s.Handler())
	defer receiver.Close()
	connection := dialReports(t, receiver.URL, config)
	if response := requestAPI(t, s, "DELETE", "/api/machines/"+config.DeviceID, s.adminKey, nil); response.Code != 204 {
		t.Fatal(response.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := connection.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("revocation left stream connected", err)
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+config.Token)
	headers.Set("X-Hako-Device-ID", config.DeviceID)
	connection, response, err := websocket.Dial(ctx, receiver.URL+"/api/reports", &websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{report.StreamProtocol}})
	if err == nil {
		connection.CloseNow()
		t.Fatal("revoked worker reconnected")
	}
	if response == nil || response.StatusCode != 401 {
		t.Fatal("wrong revocation response")
	}
}

func TestStreamRejectsOversizedOrInvalidFrames(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewServer(s.Handler())
	defer receiver.Close()
	for _, payload := range []string{"{}", `{"sequence":1} {}`, strings.Repeat(" ", (1<<20)+1)} {
		connection := dialReports(t, receiver.URL, config)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = connection.Write(ctx, websocket.MessageText, []byte(payload))
		_, _, err := connection.Read(ctx)
		cancel()
		if code := websocket.CloseStatus(err); code != websocket.StatusUnsupportedData && code != websocket.StatusMessageTooBig {
			t.Fatal("invalid frame accepted", err)
		}
		connection.CloseNow()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.machines) != 0 {
		t.Fatal("invalid stream data persisted")
	}
}

func TestServerCloseTerminatesHijackedConnections(t *testing.T) {
	s, config := setup(t)
	receiver := httptest.NewServer(s.Handler())
	defer receiver.Close()
	connection := dialReports(t, receiver.URL, config)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := connection.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("shutdown left WebSocket open", err)
	}
}

func TestReportPersistenceDoesNotBlockHeartbeat(t *testing.T) {
	s, config := setup(t)
	timing := streamTiming{lease: time.Minute, heartbeatInterval: 25 * time.Millisecond, heartbeatTimeout: 250 * time.Millisecond}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.receiveStreamWithTiming(w, r, timing)
	}))
	defer receiver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+config.Token)
	headers.Set("X-Hako-Device-ID", config.DeviceID)
	var pinged atomic.Int64
	connection, _, err := websocket.Dial(ctx, receiver.URL+"/api/reports", &websocket.DialOptions{
		HTTPHeader: headers, Subprotocols: []string{report.StreamProtocol},
		OnPingReceived: func(context.Context, []byte) bool { pinged.Add(1); return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	var ack report.StreamAck
	result := make(chan error, 1)
	go func() { result <- wsjson.Read(ctx, connection, &ack) }()
	s.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.mu.Unlock()
		}
	}()
	hardware := report.OK(report.Hardware{ModelName: "Test Mac"})
	snapshot := report.Snapshot{SchemaVersion: report.SchemaVersion, DeviceID: config.DeviceID, ReportedAt: time.Now(), Hardware: &hardware}
	if err := wsjson.Write(ctx, connection, report.StreamReport{Sequence: 1, Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatal("server closed a responsive worker while persistence was blocked", err)
	case <-time.After(3 * timing.heartbeatTimeout):
	}
	if pinged.Load() < 2 {
		t.Fatal("test did not exercise repeated heartbeats")
	}
	s.mu.Unlock()
	locked = false
	select {
	case err := <-result:
		if err != nil || ack.Sequence != 1 || ack.Status != http.StatusNoContent {
			t.Fatal("report was not acknowledged after persistence resumed", ack, err)
		}
	case <-ctx.Done():
		t.Fatal("report did not resume", ctx.Err())
	}
}

func TestStreamReaderCancellationStopsHandler(t *testing.T) {
	finished := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		defer close(finished)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		messages, done := readStreamMessages(ctx, connection)
		message := <-messages
		if message.err != nil {
			t.Error(message.err)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("reader did not stop after cancellation")
		}
	}))
	defer receiver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, receiver.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for i := 0; i < 3; i++ {
		_ = connection.Write(ctx, websocket.MessageText, []byte("queued"))
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("stream handler leaked after cancellation")
	}
}
