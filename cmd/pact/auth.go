package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// createAuthSnapshot projects only native ChatGPT login fields. The empty
// refresh_token satisfies Codex's parser without allowing container refreshes.
// Preserve last_refresh so native login-age handling stays in effect.
func createAuthSnapshot(source string) (string, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read host Codex login: %w", err)
	}

	var login struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			IDToken      string `json:"id_token"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		LastRefresh *time.Time `json:"last_refresh,omitempty"`
	}

	if json.Unmarshal(data, &login) != nil {
		// Parser errors may include credential values. Never propagate them.
		return "", errors.New("cannot decode host Codex login")
	}

	if login.Mode != "chatgpt" || login.Tokens.IDToken == "" || login.Tokens.AccessToken == "" || login.Tokens.AccountID == "" {
		return "", errors.New("Pact requires a file-based ChatGPT login with ID token, access token, and account ID")
	}

	login.Tokens.RefreshToken = ""
	file, err := os.CreateTemp("", "pact-auth-*.json")
	if err != nil {
		return "", fmt.Errorf("create authentication snapshot: %w", err)
	}

	writeErr := json.NewEncoder(file).Encode(login)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		os.Remove(file.Name())
		return "", fmt.Errorf("write authentication snapshot: %w", err)
	}

	return file.Name(), nil
}
