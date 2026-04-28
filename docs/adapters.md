# Adapters

agentctl uses a **YAML adapter system** to launch and resume coding agents. Any tool vendor, user, or editor extension can add a new adapter by dropping a single YAML file into a config directory — no Go knowledge or pull request required.

## Available adapters

### Tested

| Adapter | Type | Install |
|---------|------|---------|
| [claude](#claude) | Built-in (default) | `npm install -g @anthropic-ai/claude-code` |
| [codex](#codex) | Built-in | `npm install -g @openai/codex` |
| [copilot](#copilot) | Built-in | `npm install -g @github/copilot` |
| [openhands](#openhands) | Pluggable | `uv tool install openhands --python 3.12` |

### Untested

The following adapters have not been verified end-to-end. The YAML is provided as a best-effort starting point — flags or command shapes may need adjustment.

| Adapter | Type | Install |
|---------|------|---------|
| [gemini](#gemini) | Built-in | `npm install -g @google/gemini-cli` |
| [opencode](#opencode) | Built-in | `npm install -g opencode@latest` |
| [kilocode](#kilocode) | Pluggable | `npm install -g @kilocode/cli` |
| [cursor](#cursor) | Pluggable | `brew install --cask cursor-cli` |

**Built-in** adapters are embedded in the binary and work out of the box.  
**Pluggable** adapters require saving a YAML file to a [config directory](#drop-in-locations) first.

---

## Tested adapters

### claude

The default adapter. Uses Claude Code with `bypassPermissions` so it can edit files and run commands without prompts.

```yaml
binary: claude
launch: claude --permission-mode bypassPermissions -p {kickoff} --session-id {session_id}
resume_cmd: claude -p {prompt} --resume {session_id}
install: npm install -g @anthropic-ai/claude-code
```

### codex

[OpenAI Codex](https://github.com/openai/codex) — OpenAI's CLI coding agent.

```yaml
binary: codex
launch: codex exec --dangerously-bypass-approvals-and-sandbox -C {worktree} {kickoff}
resume_cmd: codex exec resume --last --dangerously-bypass-approvals-and-sandbox {prompt}
session_type: directory
install: npm install -g @openai/codex
```

### copilot

[GitHub Copilot](https://github.com/github/copilot-cli) agent mode.

```yaml
binary: copilot
launch: copilot --add-dir {worktree} --allow-all-tools -p {kickoff}
resume_cmd: copilot --add-dir {worktree} --allow-all-tools -p {prompt} --continue
session_type: directory
install: npm install -g @github/copilot
```

### openhands

[OpenHands](https://openhands.dev/) is an open-source AI agent platform. It runs headlessly with structured JSON output and uses directory-based session continuity.

**One-time setup:** run `openhands` interactively once to configure your LLM provider and API key before using the headless adapter. Settings are saved to `~/.openhands/agent_settings.json`.

Save to a [drop-in location](#drop-in-locations) to enable:

```yaml
binary: openhands
launch: openhands --headless --always-approve --json -t {kickoff}
resume_cmd: openhands --headless --always-approve --json --resume --last -t {prompt}
session_type: directory
install: uv tool install openhands --python 3.12
```

agentctl automatically injects `GITHUB_TOKEN` from `gh auth token` into the agent environment so `git push` works without requiring keychain access.

---

## Untested adapters

These adapters have not been verified end-to-end. Flags or command shapes may need adjustment.

### gemini

[Gemini CLI](https://github.com/google-gemini/gemini-cli) — Google's coding agent. Uses directory-based session continuity.

```yaml
binary: gemini
session_type: directory
install: npm install -g @google/gemini-cli
```

### opencode

[OpenCode](https://opencode.ai/) — an open-source coding agent. Uses `--continue` to resume the previous session in the worktree.

```yaml
binary: opencode run
launch: opencode run {kickoff}
resume_cmd: opencode run {prompt} --continue
session_type: directory
install: npm install -g opencode@latest
```

### kilocode

[KiloCode](https://kilocode.ai/) — a standalone AI coding agent CLI. Save to a [drop-in location](#drop-in-locations) to enable.

```yaml
binary: kilo
launch: kilo run --auto {kickoff}
resume_cmd: kilo run --auto --continue {prompt}
install: npm install -g @kilocode/cli
```

### cursor

Cursor's CLI agent (`cursor-agent`) is installed separately from the Cursor IDE via the `cursor-cli` cask. Save to a [drop-in location](#drop-in-locations) to enable.

```yaml
binary: cursor-agent
launch: cursor-agent -p {kickoff}
resume_cmd: cursor-agent --continue -p {prompt}
install: brew install --cask cursor-cli
```

---

## Drop-in locations

### Project-local (per-repo override)

Create `.agentctl/adapters/` in your application repository root:

```
your-app-repo/
└── .agentctl/
    └── adapters/
        └── openhands.yml    ← available to everyone on this repo
```

### User-level (personal override or custom adapter)

Create `~/.config/agentctl/adapters/` (respects `$XDG_CONFIG_HOME`):

```
~/.config/agentctl/adapters/
├── claude.yml               ← overrides built-in claude adapter
└── my-custom-agent.yml      ← new personal adapter
```

## Adapter resolution

Resolved in priority order — first match wins:

| Priority | Location | Path |
|----------|----------|------|
| 1 (highest) | **Project-local** | `.agentctl/adapters/<name>.yml` (relative to cwd) |
| 2 | **User-level** | `~/.config/agentctl/adapters/<name>.yml` |
| 3 (lowest) | **Built-in** | Embedded in the binary |

Both `.yml` and `.yaml` are accepted. When both exist in the same directory, `.yml` wins and agentctl prints a warning to stderr.

## Writing a custom adapter

### YAML schema

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

### Placeholders

Placeholders must be standalone tokens (surrounded by whitespace):

#### `launch` placeholders

| Placeholder | Value |
|-------------|-------|
| `{kickoff}` | Multi-line kickoff prompt |
| `{session_id}` | UUID assigned by agentctl |
| `{worktree}` | Absolute path to the linked worktree directory |

#### `resume_cmd` placeholders

| Placeholder | Value |
|-------------|-------|
| `{prompt}` | Resume/revision prompt |
| `{session_id}` | UUID assigned by agentctl |
| `{worktree}` | Absolute path to the linked worktree directory |

### Examples

**Minimum viable adapter:**

```yaml
binary: cursor
```

Gives: `cursor -p {kickoff}` on launch, `cursor -p {prompt}` on resume.

**Structured fields:**

```yaml
binary: codex
prompt: -q
session: --session
resume_id: --resume
```

**Full command override:**

```yaml
binary: my-bot
launch: my-bot --init {kickoff} --id {session_id}
resume_cmd: my-bot --continue {prompt} --id {session_id}
```

`launch` and `resume_cmd` are independent — set one without the other and the unset side falls back to structured fields.

### Command assembly

When `launch` / `resume_cmd` are not set, commands are assembled from structured fields:

- **launch**: `{binary_parts} {prompt_flag} {kickoff} [{session} {session_id}]`
- **resume**: `{binary_parts} {prompt_flag} {prompt} [{resume_id} {session_id}]`

`strings.Fields(binary)` splits multi-token binaries — `opencode run` becomes `exec.Command("opencode", "run", ...)`. No shell is involved.

### Validation

| Condition | Behaviour |
|-----------|-----------|
| `binary` missing | Error at load time with file path |
| Invalid YAML | Error at load time with file path |
| Unknown fields | Ignored (forward compatibility) |
| Both `.yml` and `.yaml` exist | `.yml` wins, warning to stderr |
| Binary not on PATH | Error at invocation time with `install` hint when set |
