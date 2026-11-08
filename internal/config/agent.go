package config

import (
	"errors"
	"net/url"
	"os"
)

type AgentConfig struct {
	GitURL     *url.URL
	BindingURL string
}

func LoadAgent() (AgentConfig, error) {
	gitURL, err := url.Parse(os.Getenv("PACT_GIT_URL"))
	if err != nil || gitURL.Scheme != "http" || gitURL.Host == "" {
		return AgentConfig{}, errors.New("PACT_GIT_URL is missing or invalid")
	}

	return AgentConfig{
		GitURL:     gitURL,
		BindingURL: os.Getenv("PACT_BINDING_URL"),
	}, nil
}
