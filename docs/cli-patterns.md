# CLI Implementation Patterns

This note records the current Ovek CLI conventions for command parsing and
plain-text output. It is developer-facing guidance, not a public user manual.

## Argument Parsing

Use [`github.com/massivemoose/chomp`](https://github.com/massivemoose/chomp)
for commands that combine flags and positional arguments, especially when
users naturally place flags after positionals:

```go
parsed, err := ovekCommand("workflow", "set").
	String("image", chomp.Required()).
	String("schedule").
	Positionals(2, 2, "project", "name").
	Parse(args)
if err != nil {
	return workflowSetArgs{}, normalizeChompError(err)
}
```

Chomp is intentionally small. It supports long and single-short flags,
interspersed positionals, defaults, usage rendering, `--`, help, required
flags, and positional counts. It does not own subcommand routing, shell
completion, short flag clusters, env/config binding, aliases, or generated
docs.

Go's standard `flag` package is still fine for simple commands that are
flag-only or have no interspersed positional/flag ergonomics to preserve.

## Output

Use `internal/cli/output` for common human-readable shapes:

- `WriteSection` for section headers.
- `WriteKeyValues` for detail blocks.
- `WriteTable` or `WriteAdaptiveTable` for lists.
- `WriteSuccess` for successful mutations.
- `WriteEmpty` for empty states.
- `WriteNote` and `WriteWarning` for secondary guidance.
- `WriteNextStep` or `WriteCommandSuggestion` for follow-up commands.

Keep output plain ASCII text by default. Do not add color, icons, progress bars,
or terminal-specific behavior unless a later CLI design explicitly introduces
those features.

Log streaming commands should keep writing log lines directly as they arrive.
