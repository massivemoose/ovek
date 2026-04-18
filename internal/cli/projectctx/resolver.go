package projectctx

import (
	"fmt"
	"strings"
)

type Resolver interface {
	Resolve(args []string) (string, error)
}

type ExplicitResolver struct {
	CommandPath string
}

func (resolver ExplicitResolver) Resolve(args []string) (string, error) {
	if len(args) != 1 {
		if resolver.CommandPath == "" {
			resolver.CommandPath = "alces"
		}
		return "", fmt.Errorf("%s requires exactly one <project> argument", resolver.CommandPath)
	}

	projectName := strings.TrimSpace(args[0])
	if projectName == "" {
		return "", fmt.Errorf("%s requires a non-empty <project> argument", resolver.CommandPath)
	}

	return projectName, nil
}
