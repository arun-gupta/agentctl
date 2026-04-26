# Adapters

agentctl uses a **YAML adapter system** to launch and resume coding agents. Any tool vendor, user, or editor extension can add a new adapter by dropping a single YAML file into a config directory — no Go knowledge or pull request required.

## How it works

One code path handles all adapters. Built-in and user-defined adapters are the same type, loaded by the same loader. The binary ships with five built-in adapters embedded directly — plain YAML files, not special Go code.

## Adapter resolution

Resolved in priority order — first match wins:

| Priority | Location | Path |
|----------|----------|------|
| 1 (highest) | **Project-local** | `.agentctl/adapters/<name>.yml` (relative to cwd) |
| 2 | **User-level** | `~/.config/agentctl/adapters/<name>.yml` |
| 3 (lowest) | **Built-in** | Embedded in the binary |

Both `.yml` and `.yaml` are accepted. When both exist in the same directory, `.yml` wins and agentctl prints a warning to stderr. Dropping a file at level 1 or 2 with the same name as a built-in overrides it completely — useful for pinning flags without waiting for a release.

## YAML schema

```yaml
# Binary to invoke. Required. Space-separated for multi-token binaries
# (e.g. "opencode run") — first token is the executable.
binary: <string>          # REQUIRED

# Flag used to pass the prompt text.
prompt: <string>          # default: -p

# Flag used to pass the session ID on launch.
session: <string>         # default: (none — no session flag)

# Flag used to pass the session ID on resume.
resume_id: <string>       # default: same as session

# How session continuity works:
#   flag      — session ID passed via session/resume_id flags (default)
#   directory — no session flags; continuity is implicit in the worktree
session_type: flag | directory   # default: flag

# Full launch command override. Ignores binary/prompt/session for launch.
# resume_cmd still falls back to structured fields if not set.
launch: <string>          # default: derived from binary + prompt + session

# Full resume command override. Ignores binary/prompt/resume_id for resume.
# launch still falls back to structured fields if not set.
resume_cmd: <string>      # default: derived from binary + prompt + resume_id

# Hint shown in the error message when the binary is not found on PATH.
# agentctl never executes this string — it is displayed to the user only.
install: <string>         # optional; e.g. "npm install -g @anthropic-ai/claude-code"
```

The adapter name is always the filename stem (`cursor.yml` → `cursor`). There is no `name:` field.

### Placeholders (for `launch` and `resume_cmd` only)

Placeholders must be standalone tokens (surrounded by whitespace):

#### `launch` placeholders

| Placeholder | Value |
|-------------|-------|
| `{kickoff}` | Multi-line kickoff prompt |
| `{session_id}` | UUID assigned by agentctl |

#### `resume_cmd` placeholders

| Placeholder | Value |
|-------------|-------|
| `{prompt}` | Resume/revision prompt |
| `{session_id}` | UUID assigned by agentctl |

## Examples

### One line — minimum viable adapter

```yaml
binary: cursor
```

Gives: `cursor -p {kickoff}` on launch, `cursor -p {prompt}` on resume.

### Structured fields

```yaml
binary: codex
prompt: -q
session: --session
resume_id: --resume
```

### Full command override

```yaml
binary: my-bot
launch: my-bot --init {kickoff} --id {session_id}
resume_cmd: my-bot --continue {prompt} --id {session_id}
```

`launch` and `resume_cmd` are independent — set one without the other and the unset side falls back to structured fields.

## Built-in adapters

The binary ships with these five built-in adapters. Override any by dropping a same-named file in a higher-priority location.

### claude

```yaml
binary: claude
launch: claude --permission-mode bypassPermissions -p {kickoff} --session-id {session_id}
resume_cmd: claude -p {prompt} --resume {session_id}
install: npm install -g @anthropic-ai/claude-code
```

### gemini

```yaml
binary: gemini
session_type: directory
install: npm install -g @google/gemini-cli
```

### opencode

```yaml
binary: opencode run
session: --session
install: npm install -g opencode@latest
```

### codex

```yaml
binary: codex
prompt: -q
session: --session
resume_id: --resume
install: npm install -g @openai/codex
```

### copilot

```yaml
binary: copilot
session: --session-id
install: npm install -g @github/copilot
```

## Drop-in locations

### Project-local (per-repo override)

Create `.agentctl/adapters/` in your application repository root:

```
your-app-repo/
└── .agentctl/
    └── adapters/
        └── cursor.yml    ← new adapter available to everyone on this repo
```

### User-level (personal override or custom adapter)

Create `~/.config/agentctl/adapters/` (respects `$XDG_CONFIG_HOME`):

```
~/.config/agentctl/adapters/
├── claude.yml            ← overrides built-in claude adapter
└── my-custom-agent.yml   ← new personal adapter
```

## Command assembly (structured fields)

When `launch` / `resume_cmd` are not set, commands are assembled from structured fields:

- **launch**: `{binary_parts} {prompt_flag} {kickoff} [{session} {session_id}]`
- **resume**: `{binary_parts} {prompt_flag} {prompt} [{resume_id} {session_id}]`

`strings.Fields(binary)` splits multi-token binaries — `opencode run` becomes `exec.Command("opencode", "run", ...)`. No shell is involved.

## Validation

| Condition | Behaviour |
|-----------|-----------|
| `binary` missing | Error at load time with file path |
| Invalid YAML | Error at load time with file path |
| Unknown fields | Ignored (forward compatibility) |
| Both `.yml` and `.yaml` exist | `.yml` wins, warning to stderr |
| Binary not on PATH | Error at invocation time with `install` hint when set |
