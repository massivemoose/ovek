package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
)

var ErrUsage = errors.New("usage")

type Command interface {
	Name() string
	Summary() string
	Run(context.Context, []string) error
	Usage(io.Writer)
}

type HiddenCommand interface {
	Hidden() bool
}

type Router struct {
	name     string
	summary  string
	commands map[string]Command
	order    []string
}

func NewRouter(name string, summary string, commands ...Command) *Router {
	router := &Router{
		name:     name,
		summary:  summary,
		commands: make(map[string]Command, len(commands)),
		order:    make([]string, 0, len(commands)),
	}

	for _, command := range commands {
		if command == nil {
			continue
		}
		commandName := command.Name()
		router.commands[commandName] = command
		router.order = append(router.order, commandName)
	}

	sort.Strings(router.order)

	return router
}

func (router *Router) Name() string {
	return router.name
}

func (router *Router) Summary() string {
	return router.summary
}

func (router *Router) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return ErrUsage
	}

	command, ok := router.commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}

	return command.Run(ctx, args[1:])
}

func (router *Router) Usage(w io.Writer) {
	if router.summary != "" {
		_, _ = fmt.Fprintf(w, "%s\n\n", router.summary)
	}
	_, _ = fmt.Fprintf(w, "Usage:\n  %s <command>\n\n", router.name)
	_, _ = fmt.Fprintf(w, "Commands:\n")
	for _, commandName := range router.order {
		command := router.commands[commandName]
		if hidden, ok := command.(HiddenCommand); ok && hidden.Hidden() {
			continue
		}
		_, _ = fmt.Fprintf(w, "  %-12s %s\n", command.Name(), command.Summary())
	}
}
