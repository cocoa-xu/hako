package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/identity"
	"github.com/cocoa-xu/hako/internal/report"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// ErrStreamingUnsupported means the receiver or proxy must be upgraded for streaming.
var ErrStreamingUnsupported = errors.New("report streaming is unavailable; upgrade the server and enable WebSocket proxying")

var errInventoryRequired = errors.New("receiver requested startup inventory")
var errStreamRenewal = errors.New("receiver requested a new connection")

type reportSender interface {
	send(context.Context, report.Snapshot) error
	close()
}

type streamResult struct {
	ack report.StreamAck
	err error
}

type reportStream struct {
	connection *websocket.Conn
	results    chan streamResult
	cancel     context.CancelFunc
	done       chan struct{}
	sequence   uint64
}

type retryAfterError struct {
	status int
	delay  time.Duration
}

func (err *retryAfterError) Error() string {
	return fmt.Sprintf("stream handshake returned HTTP %d", err.status)
}

func (a *Agent) connect(ctx context.Context) (reportSender, error) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+a.config.Token)
	headers.Set("X-Hako-Device-ID", identity.Normalize(a.config.DeviceID))
	a.config.CloudflareAccess.Apply(&http.Request{Header: headers})
	connection, response, err := websocket.Dial(ctx, strings.TrimRight(a.config.Server, "/")+"/api/reports", &websocket.DialOptions{
		HTTPClient: a.client, HTTPHeader: headers, Subprotocols: []string{report.StreamProtocol},
	})
	if err != nil {
		if response != nil {
			switch response.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden, 301, 302, 303, 307, 308:
				return nil, ErrUnauthorized
			case http.StatusOK, http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusUpgradeRequired, http.StatusNotImplemented:
				return nil, ErrStreamingUnsupported
			default:
				return nil, &retryAfterError{status: response.StatusCode, delay: retryAfter(response.Header.Get("Retry-After"))}
			}
		}
		return nil, err
	}
	if connection.Subprotocol() != report.StreamProtocol {
		_ = connection.CloseNow()
		return nil, ErrStreamingUnsupported
	}
	connection.SetReadLimit(1024)
	read, cancel := context.WithCancel(ctx)
	stream := &reportStream{connection: connection, results: make(chan streamResult, 1), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		for {
			var result streamResult
			result.err = streamError(wsjson.Read(read, connection, &result.ack))
			select {
			case stream.results <- result:
			case <-read.Done():
				return
			}
			if result.err != nil {
				return
			}
		}
	}()
	return stream, nil
}

func (stream *reportStream) close() {
	stream.cancel()
	_ = stream.connection.CloseNow()
	<-stream.done
}

func (stream *reportStream) send(ctx context.Context, snapshot report.Snapshot) error {
	select {
	case result := <-stream.results:
		if result.err != nil {
			return result.err
		}
		return fmt.Errorf("unsolicited report acknowledgement")
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stream.sequence++
	if err := wsjson.Write(ctx, stream.connection, report.StreamReport{Sequence: stream.sequence, Snapshot: snapshot}); err != nil {
		return streamError(err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-stream.results:
		if result.err != nil {
			return result.err
		}
		if result.ack.Sequence != stream.sequence {
			return fmt.Errorf("unexpected report acknowledgement sequence")
		}
		switch result.ack.Status {
		case http.StatusNoContent:
			return nil
		case http.StatusConflict:
			return errInventoryRequired
		case http.StatusUnauthorized, http.StatusForbidden:
			return ErrUnauthorized
		default:
			return fmt.Errorf("report acknowledgement returned status %d", result.ack.Status)
		}
	}
}

func streamError(err error) error {
	switch websocket.CloseStatus(err) {
	case websocket.StatusPolicyViolation:
		return ErrUnauthorized
	case websocket.StatusGoingAway:
		return errStreamRenewal
	default:
		return err
	}
}

func retryDelay(attempt int) time.Duration {
	base := 2 * time.Second * time.Duration(1<<min(attempt, 5))
	base = min(base, time.Minute)
	return base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		return time.Duration(min(seconds, 3600)) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return max(0, min(time.Until(when), time.Hour))
	}
	return 0
}

func waitToReconnect(ctx context.Context, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
