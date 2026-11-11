// Package control provides administrator requests to a Hako receiver.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cocoa-xu/hako/internal/agent"
)

// Options controls access proxy credentials and private-network transport.
type Options struct {
	CloudflareAccess *agent.AccessServiceToken
	AllowHTTP        bool
}

// Call sends one request without following redirects or leaking credentials.
func Call(ctx context.Context, server, key, method, path string, value any, options Options) ([]byte, error) {
	config := agent.Config{Server: server, DeviceID: strings.Repeat("0", 32), Token: key, CloudflareAccess: options.CloudflareAccess, AllowHTTP: options.AllowHTTP}
	if key == "" {
		config.Token = strings.Repeat("0", 32)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(server, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("X-Hako-Request", "cli")
	}
	options.CloudflareAccess.Apply(req)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("administrator request returned HTTP %d", response.StatusCode)
	}
	return data, nil
}
