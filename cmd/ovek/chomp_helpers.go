package main

import (
	"errors"

	"github.com/massivemoose/chomp"
	"github.com/massivemoose/ovek/internal/cli/command"
)

func normalizeChompError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, chomp.ErrHelp) {
		return command.ErrUsage
	}
	return err
}

func ovekCommand(parts ...string) *chomp.Spec {
	return chomp.New(append([]string{"ovek"}, parts...)...)
}
