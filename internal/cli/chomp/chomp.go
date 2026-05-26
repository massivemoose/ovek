package chomp

import (
	"errors"
	"fmt"
	"strings"
)

var ErrHelp = errors.New("help")

type Option func(*flagSpec)

func Required() Option {
	return func(flag *flagSpec) {
		flag.required = true
	}
}

type Spec struct {
	command     string
	flags       map[string]*flagSpec
	flagOrder   []string
	minPosition int
	maxPosition int
	positionals []string
}

type flagSpec struct {
	name     string
	kind     flagKind
	required bool
}

type flagKind int

const (
	flagKindString flagKind = iota
	flagKindBool
)

type Result struct {
	strings     map[string]string
	bools       map[string]bool
	positionals []string
}

func New(command string) *Spec {
	return &Spec{
		command:     strings.TrimSpace(command),
		flags:       make(map[string]*flagSpec),
		minPosition: 0,
		maxPosition: -1,
	}
}

func (spec *Spec) String(name string, options ...Option) *Spec {
	return spec.flag(name, flagKindString, options...)
}

func (spec *Spec) Bool(name string, options ...Option) *Spec {
	return spec.flag(name, flagKindBool, options...)
}

func (spec *Spec) Positionals(min int, max int, names ...string) *Spec {
	spec.minPosition = min
	spec.maxPosition = max
	spec.positionals = append([]string(nil), names...)
	return spec
}

func (spec *Spec) Parse(args []string) (Result, error) {
	result := Result{
		strings: make(map[string]string),
		bools:   make(map[string]bool),
	}
	parseFlags := true
	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		if arg == "-h" || arg == "--help" {
			return Result{}, ErrHelp
		}
		if parseFlags && arg == "--" {
			parseFlags = false
			continue
		}
		if parseFlags && strings.HasPrefix(arg, "--") {
			name, inlineValue, hasInlineValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			flag, ok := spec.flags[name]
			if !ok || name == "" {
				return Result{}, fmt.Errorf("unknown %s flag %q", shortCommandName(spec.command), arg)
			}
			switch flag.kind {
			case flagKindString:
				value := inlineValue
				if !hasInlineValue {
					index++
					if index >= len(args) {
						return Result{}, fmt.Errorf("%s --%s requires a value", spec.command, name)
					}
					value = strings.TrimSpace(args[index])
				}
				if strings.TrimSpace(value) == "" {
					return Result{}, fmt.Errorf("%s --%s requires a value", spec.command, name)
				}
				result.strings[name] = strings.TrimSpace(value)
			case flagKindBool:
				value := true
				if hasInlineValue {
					parsed, err := parseBoolValue(inlineValue)
					if err != nil {
						return Result{}, fmt.Errorf("invalid --%s value %q", name, inlineValue)
					}
					value = parsed
				} else if index+1 < len(args) {
					if parsed, ok := maybeBoolValue(args[index+1]); ok {
						index++
						value = parsed
					}
				}
				result.bools[name] = value
			}
			continue
		}
		if parseFlags && strings.HasPrefix(arg, "-") {
			return Result{}, fmt.Errorf("unknown %s flag %q", shortCommandName(spec.command), arg)
		}
		if arg == "" {
			return Result{}, fmt.Errorf("%s requires %s", spec.command, spec.positionalUsage(spec.minPosition))
		}
		result.positionals = append(result.positionals, arg)
	}

	if err := spec.validatePositionals(len(result.positionals)); err != nil {
		return Result{}, err
	}
	for _, name := range spec.flagOrder {
		flag := spec.flags[name]
		if !flag.required {
			continue
		}
		switch flag.kind {
		case flagKindString:
			if strings.TrimSpace(result.strings[name]) == "" {
				return Result{}, fmt.Errorf("%s requires --%s", spec.command, name)
			}
		case flagKindBool:
			if !result.bools[name] {
				return Result{}, fmt.Errorf("%s requires --%s", spec.command, name)
			}
		}
	}
	return result, nil
}

func (result Result) String(name string) string {
	return result.strings[name]
}

func (result Result) Bool(name string) bool {
	return result.bools[name]
}

func (result Result) Positionals() []string {
	return append([]string(nil), result.positionals...)
}

func (result Result) Positional(index int) string {
	if index < 0 || index >= len(result.positionals) {
		return ""
	}
	return result.positionals[index]
}

func (spec *Spec) flag(name string, kind flagKind, options ...Option) *Spec {
	name = strings.TrimSpace(name)
	flag := &flagSpec{name: name, kind: kind}
	for _, option := range options {
		if option != nil {
			option(flag)
		}
	}
	spec.flags[name] = flag
	spec.flagOrder = append(spec.flagOrder, name)
	return spec
}

func (spec *Spec) validatePositionals(count int) error {
	if count < spec.minPosition {
		return fmt.Errorf("%s requires %s", spec.command, spec.positionalUsage(spec.minPosition))
	}
	if spec.maxPosition >= 0 && count > spec.maxPosition {
		return fmt.Errorf("%s accepts %s", spec.command, spec.maxPositionalUsage(spec.maxPosition))
	}
	return nil
}

func parseBoolValue(value string) (bool, error) {
	parsed, ok := maybeBoolValue(value)
	if !ok {
		return false, fmt.Errorf("invalid bool")
	}
	return parsed, nil
}

func maybeBoolValue(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true, true
	case "false", "0", "no":
		return false, true
	default:
		return false, false
	}
}

func shortCommandName(command string) string {
	return strings.TrimPrefix(strings.TrimSpace(command), "ovek ")
}

func (spec *Spec) positionalUsage(count int) string {
	if len(spec.positionals) >= count && count > 0 {
		return joinPositionals(spec.positionals[:count])
	}
	switch count {
	case 0:
		return "no positionals"
	case 1:
		return "<project>"
	case 2:
		return "<project> and <name>"
	default:
		return fmt.Sprintf("%d positionals", count)
	}
}

func (spec *Spec) maxPositionalUsage(count int) string {
	if len(spec.positionals) >= count && count > 0 {
		return maxPositionalsText(spec.positionals[:count])
	}
	switch count {
	case 0:
		return "no positionals"
	case 1:
		return "one <project>"
	case 2:
		return "two positionals"
	default:
		return fmt.Sprintf("%d positionals", count)
	}
}

func joinPositionals(names []string) string {
	formatted := make([]string, 0, len(names))
	for _, name := range names {
		formatted = append(formatted, "<"+name+">")
	}
	if len(formatted) == 1 {
		return formatted[0]
	}
	return strings.Join(formatted[:len(formatted)-1], ", ") + " and " + formatted[len(formatted)-1]
}

func maxPositionalsText(names []string) string {
	if len(names) == 1 {
		return "one <" + names[0] + ">"
	}
	return fmt.Sprintf("%d positionals", len(names))
}
