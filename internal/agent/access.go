package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AccessServiceToken authenticates a non-interactive client to Cloudflare Access.
type AccessServiceToken struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func (token *AccessServiceToken) validate() error {
	if token == nil {
		return nil
	}
	for _, value := range []string{token.ClientID, token.ClientSecret} {
		if value == "" || strings.ContainsAny(value, "\r\n") || strings.TrimSpace(value) != value {
			return fmt.Errorf("Cloudflare Access requires a non-empty client_id and client_secret without surrounding whitespace or newlines")
		}
	}
	return nil
}

// Apply adds Access headers without replacing Hako's Authorization header.
func (token *AccessServiceToken) Apply(request *http.Request) {
	if token != nil {
		request.Header.Set("CF-Access-Client-Id", token.ClientID)
		request.Header.Set("CF-Access-Client-Secret", token.ClientSecret)
	}
}

// ReadAccessServiceToken loads a token pair from a private JSON file.
func ReadAccessServiceToken(path string) (*AccessServiceToken, error) {
	data, err := ReadSecret(path)
	if err != nil {
		return nil, err
	}
	var token AccessServiceToken
	if err := json.Unmarshal([]byte(data), &token); err != nil {
		return nil, fmt.Errorf("invalid Cloudflare Access service token JSON")
	}
	if err := token.validate(); err != nil {
		return nil, err
	}
	return &token, nil
}

// ConfigureAccess updates Access credentials without changing the worker identity.
func ConfigureAccess(path string, token *AccessServiceToken) error {
	data, err := ReadSecret(path)
	if err != nil {
		return err
	}
	var config Config
	if err := json.Unmarshal([]byte(data), &config); err != nil {
		return fmt.Errorf("invalid worker configuration")
	}
	config.CloudflareAccess = token
	if err := config.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return SavePrivate(path, append(encoded, '\n'))
}
