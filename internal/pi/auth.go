package pi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// authSnapshot excludes renewable credentials and unknown fields while keeping
// native expiry handling. Renew an expired login on the host before resuming.
func authSnapshot(source string) ([]byte, error) {
	data, err := os.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		return []byte("{}"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read host Pi login: %w", err)
	}

	var credentials map[string]json.RawMessage
	if json.Unmarshal(data, &credentials) != nil || credentials == nil {
		// Parser errors may include credential values. Never propagate them.
		return nil, errors.New("cannot decode host Pi login")
	}
	projected := make(map[string]any, len(credentials))
	for provider, raw := range credentials {
		var credential struct {
			Type          string            `json:"type"`
			Key           string            `json:"key,omitempty"`
			Env           map[string]string `json:"env,omitempty"`
			Access        string            `json:"access,omitempty"`
			Expires       int64             `json:"expires,omitempty"`
			ClientID      string            `json:"clientId,omitempty"`
			Scopes        []string          `json:"scopes,omitempty"`
			EnterpriseURL string            `json:"enterpriseUrl,omitempty"`
		}
		if json.Unmarshal(raw, &credential) != nil {
			return nil, errors.New("cannot decode host Pi login credential")
		}
		switch credential.Type {
		case "api_key":
			projected[provider] = struct {
				Type string            `json:"type"`
				Key  string            `json:"key,omitempty"`
				Env  map[string]string `json:"env,omitempty"`
			}{Type: credential.Type, Key: credential.Key, Env: credential.Env}
		case "oauth":
			projected[provider] = struct {
				Type          string   `json:"type"`
				Access        string   `json:"access"`
				Refresh       string   `json:"refresh"`
				Expires       int64    `json:"expires"`
				ClientID      string   `json:"clientId,omitempty"`
				Scopes        []string `json:"scopes,omitempty"`
				EnterpriseURL string   `json:"enterpriseUrl,omitempty"`
			}{Type: credential.Type, Access: credential.Access, Expires: credential.Expires,
				ClientID: credential.ClientID, Scopes: credential.Scopes, EnterpriseURL: credential.EnterpriseURL}
		default:
			return nil, errors.New("unsupported host Pi login credential type")
		}
	}
	return json.Marshal(projected)
}
