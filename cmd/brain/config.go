package main

import (
	"errors"
	"os"
	"strings"
)

type config struct {
	BrainAPIKey string
}

func loadConfig() (config, error) {
	apiKey := strings.TrimSpace(os.Getenv("BRAIN_API_KEY"))
	if apiKey == "" {
		return config{}, errors.New("BRAIN_API_KEY is required")
	}

	return config{
		BrainAPIKey: apiKey,
	}, nil
}
