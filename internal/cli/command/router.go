package command

import (
	"context"
	"io"

	"github.com/massivemoose/chomp"
)

var ErrUsage = chomp.ErrUsage

type Command = chomp.Command

type HiddenCommand = chomp.HiddenCommand

type UsageError = chomp.UsageError

type Router = chomp.Router

func UsageCommand(err error) (Command, bool) {
	return chomp.UsageCommand(err)
}

func NewRouter(name string, summary string, commands ...Command) *Router {
	return chomp.NewRouter(name, summary, commands...)
}

var _ interface {
	Name() string
	Summary() string
	Run(context.Context, []string) error
	Usage(io.Writer)
} = (*Router)(nil)
