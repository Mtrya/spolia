# llmloot

llmloot discovers temporary model opportunities from OpenRouter and ZenMux, applies an explicit local cost policy, and reconciles eligible models into Kimi Code without changing its active or default model.

The supported matrix is OpenRouter and ZenMux with Kimi Code 0.38.0 or newer on Linux, macOS, and Windows. Model aliases remain the exact upstream IDs. A run with no eligible model succeeds with zero candidates and never silently enables a broader policy.

## Install

Kimi Code must already be installed and available as `kimi` on `PATH`.

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Mtrya/llmloot/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Mtrya/llmloot/main/install.ps1 | iex
```

The scripts resolve the latest GitHub release, download the archive and `SHA256SUMS`, abort on a checksum mismatch, and install without elevation. The default destination is `~/.local/bin` on Linux/macOS and `%LOCALAPPDATA%\Programs\llmloot\bin` on Windows. Each script prints PATH guidance when its destination is not already available.

You can also install from source with Go 1.26 or newer:

```sh
go install github.com/Mtrya/llmloot/cmd/llmloot@latest
```

## Set up

Make one or both provider credentials available for the initial setup:

```sh
export OPENROUTER_API_KEY=...
export ZENMUX_API_KEY=...
llmloot setup
```

Interactive setup asks one question at a time, shows the target plan, performs an immediate sync, and enables one daily native per-user schedule by default. Use `llmloot setup --no-schedule` to opt out. `setup --yes` accepts the existing configuration or stealth-only defaults, but it never supplies a missing credential or enables ordinary free or discounted models.

llmloot adopts a compatible Kimi Code provider or copies the bootstrap credential into Kimi Code's normal `config.toml`. Later discovery reuses that provider credential. llmloot's own configuration, state, output, and diagnostics do not store or print provider keys.

Scheduling uses a systemd user timer on Linux, a LaunchAgent on macOS, or a per-user Task Scheduler task on Windows. Scheduled runs execute `llmloot sync --if-due --quiet`; they perform metadata discovery only and never start an inference turn. An `LLMLOOT_HOME` override is intended for isolated manual testing and cannot be combined with native scheduling.

## Policy

Every enabled job considers free stealth models. Ordinary free and discounted paid models are independent opt-ins:

```toml
[jobs.openrouter-kimi-code.policy]
include_free = false
include_discounted = false
```

Enabling `include_free` adds ordinary free models. Enabling `include_discounted` adds discounted models and requires an explicit decimal-string ceiling for every nonzero billing dimension:

```toml
[jobs.openrouter-kimi-code.policy]
include_free = false
include_discounted = true
price_ceilings = { "prompt|per_token|USD" = "0.000001", "completion|per_token|USD" = "0.000003" }
```

Ceiling keys use `dimension|unit|currency`, matching the normalized prices in `llmloot sync --dry-run --json`. A model is discounted only when the source exposes authoritative machine-readable discount evidence; llmloot does not infer discounts from website badges or comparisons.

See [config.example.toml](config.example.toml) for the complete two-source configuration.

## Commands

```text
llmloot setup [--yes] [--no-schedule] [--json]
llmloot sync [job] [--dry-run] [--if-due] [--quiet] [--json]
llmloot doctor [--json]
llmloot uninstall [--dry-run] [--yes] [--json]
```

`sync --dry-run` performs real authenticated discovery, policy evaluation, target inspection, and Kimi Code validation without writing. A failed job preserves its previous managed models while unrelated jobs may still update. `doctor` is read-only. `uninstall` removes only the owned scheduler, Kimi Code entries, configuration, and state; it leaves the executable and user-owned Kimi Code content in place.

## Release validation

Repository tests exercise real Kimi Code configuration validation and native schedulers when their prerequisites are available:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The Linux live check uses isolated Kimi Code and llmloot homes, runs setup and sync, activates the selected alias only inside the disposable Kimi Code home, invokes it through the real Kimi Code binary, requires a harmless shell-tool turn, and emits a redacted JSON report. It never changes the operator's Kimi Code configuration:

```sh
go build -o /tmp/llmloot-livecheck ./cmd/llmloot
go run ./tools/livecheck --llmloot /tmp/llmloot-livecheck --source all
```

The default is stealth-only for both cells. If a cell has no stealth candidate, the operator must explicitly choose a broader policy; the tool does not rerun or widen automatically:

```sh
go run ./tools/livecheck --llmloot /tmp/llmloot-livecheck --source all --openrouter-policy free --zenmux-policy free
```

Discounted checks additionally require one or more source-specific ceiling flags such as `--openrouter-ceiling 'prompt|per_token|USD=0.000001'`.

## License

llmloot is available under the [MIT License](LICENSE).
