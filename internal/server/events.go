package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	updates := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[updates] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subscribers, updates)
		s.mu.Unlock()
	}()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		payload, err := json.Marshal(s.fleet())
		if err != nil {
			return
		}
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
			return
		}
		if _, err := fmt.Fprintf(w, "retry: 2000\nevent: machines\ndata: %s\n\n", payload); err != nil {
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-updates:
		case <-heartbeat.C:
		}
	}
}
