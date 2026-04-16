package main

import (
	"errors"
	"fmt"

	"github.com/massivemoose/alces/internal/cli/client"
	"github.com/massivemoose/alces/internal/cli/config"
)

func loadConfiguredClient(store *config.Store) (*client.Client, error) {
	cfg, err := store.Load()
	if errors.Is(err, config.ErrNotFound) {
		return nil, fmt.Errorf("CLI auth is not configured; run `alces auth login` first")
	}
	if err != nil {
		return nil, err
	}

	return client.New(cfg.Host, cfg.APIKey)
}
