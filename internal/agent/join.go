package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReadSecret reads a private credential file without accepting shared permissions.
func ReadSecret(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("credential file must be a regular file with mode 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SavePrivate atomically stores a credential without granting access to other users.
func SavePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".hako-credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// Join consumes one enrollment key and stores a stable installation identity.
func Join(ctx context.Context, server, key, path string) error {
	if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid enrollment key")
	}
	server = strings.TrimRight(server, "/")
	config := Config{Server: server}
	if _, err := os.Stat(path); err == nil {
		text, err := ReadSecret(path)
		if err != nil {
			return err
		}
		if json.Unmarshal([]byte(text), &config) != nil {
			return fmt.Errorf("invalid existing worker configuration")
		}
		if !config.Pending {
			return fmt.Errorf("worker is already enrolled; preserve its credentials when upgrading")
		}
		if config.Server != server {
			return fmt.Errorf("pending registration belongs to a different server")
		}
	} else if !os.IsNotExist(err) {
		return err
	} else {
		config.Pending = true
		var id [16]byte
		var token [32]byte
		if _, err := rand.Read(id[:]); err != nil {
			return err
		}
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		config.DeviceID = hex.EncodeToString(id[:])
		config.Token = hex.EncodeToString(token[:])
	}
	if err := config.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := SavePrivate(path, append(data, '\n')); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(config.Token))
	body, _ := json.Marshal(struct {
		DeviceID  string `json:"device_id"`
		TokenHash string `json:"token_hash"`
	}{config.DeviceID, hex.EncodeToString(digest[:])})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/enroll", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("registration returned HTTP %d; the pending identity is saved for retry", response.StatusCode)
	}
	config.Pending = false
	data, _ = json.MarshalIndent(config, "", "  ")
	return SavePrivate(path, append(data, '\n'))
}
