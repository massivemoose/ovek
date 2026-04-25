package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	mobyterm "github.com/moby/term"
)

type prompter interface {
	Prompt(label string) (string, error)
	PromptPassword(label string) (string, error)
}

type stdioPrompter struct {
	stdin  io.Reader
	stdout io.Writer
	reader *bufio.Reader
}

func newStdioPrompter(stdin io.Reader, stdout io.Writer) *stdioPrompter {
	return &stdioPrompter{
		stdin:  stdin,
		stdout: stdout,
		reader: bufio.NewReader(stdin),
	}
}

func (prompter *stdioPrompter) Prompt(label string) (string, error) {
	_, _ = fmt.Fprint(prompter.stdout, label)
	value, err := prompter.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}

	return strings.TrimSpace(value), nil
}

func (prompter *stdioPrompter) PromptPassword(label string) (string, error) {
	file, ok := prompter.stdin.(*os.File)
	if !ok {
		return prompter.Prompt(label)
	}

	fd, isTerminal := mobyterm.GetFdInfo(file)
	if !isTerminal {
		return prompter.Prompt(label)
	}

	state, err := mobyterm.SaveState(fd)
	if err != nil {
		return "", err
	}
	_, _ = fmt.Fprint(prompter.stdout, label)
	if err := mobyterm.DisableEcho(fd, state); err != nil {
		return "", err
	}
	defer func() {
		_ = mobyterm.RestoreTerminal(fd, state)
		_, _ = fmt.Fprintln(prompter.stdout)
	}()

	value, err := prompter.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}

	return strings.TrimSpace(value), nil
}
