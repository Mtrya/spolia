# spolia

spolia discovers temporary model opportunities from OpenRouter and ZenMux, applies an explicit local cost policy, and reconciles eligible models into Kimi Code.

The name is the architectural term *spolia*: stone stripped from older monuments and built into new ones. The tool does the same with free and stealth models that are lying around in provider catalogs.

The supported matrix is OpenRouter and ZenMux with Kimi Code 0.38.0 or newer on Linux, macOS, and Windows. Model aliases remain the exact upstream IDs. A run with no eligible model succeeds with zero candidates and never silently enables a broader policy.

## Install

Kimi Code must already be installed and available as `kimi` on `PATH`.

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Mtrya/spolia/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Mtrya/spolia/main/install.ps1 | iex
```

The scripts resolve the latest GitHub release, download the archive and `SHA256SUMS`, abort on a checksum mismatch, and install without elevation. The default destination is `~/.local/bin` on Linux/macOS and `%LOCALAPPDATA%\Programs\spolia\bin` on Windows.

The install command above always tracks the latest release. To pin a specific version, set `SPOLIA_INSTALL_VERSION`:

```sh
curl -fsSL https://raw.githubusercontent.com/Mtrya/spolia/main/install.sh | SPOLIA_INSTALL_VERSION=0.1.0 bash
```

```powershell
$env:SPOLIA_INSTALL_VERSION = "0.1.0"; irm https://raw.githubusercontent.com/Mtrya/spolia/main/install.ps1 | iex
```

You can also install from source with Go 1.26 or newer:

```sh
go install github.com/Mtrya/spolia/cmd/spolia@latest
```

## Set up

Make one or both provider credentials available for the initial setup:

```sh
export OPENROUTER_API_KEY=...
export ZENMUX_API_KEY=...
spolia setup
```

Interactive setup asks one question at a time, shows the target plan, performs an immediate sync, and enables one daily native per-user schedule by default. Use `spolia setup --no-schedule` to opt out. `setup --yes` accepts the existing configuration or stealth-only defaults, but it never supplies a missing credential or enables ordinary free models.

spolia adopts a compatible Kimi Code provider or copies the bootstrap credential into Kimi Code's normal `config.toml`. Later discovery reuses that provider credential. spolia's own configuration, state, output, and diagnostics do not store or print provider keys.

Scheduling uses a systemd user timer on Linux, a LaunchAgent on macOS, or a per-user Task Scheduler task on Windows. Scheduled runs execute `spolia sync --if-due --quiet`; they perform metadata discovery only and never start an inference turn. An `SPOLIA_HOME` override is intended for isolated manual testing and cannot be combined with native scheduling.

## Policy

Every enabled job considers free stealth models. Ordinary free models are an explicit opt-in:

```toml
[jobs.openrouter-kimi-code.policy]
include_free = false
```

Enabling `include_free` adds ordinary free models alongside stealth models. Paid models are never selected: neither supported source exposes authoritative machine-readable discount evidence in its public catalog, so spolia only tracks stealth and free eligibility.

See [config.example.toml](config.example.toml) for the complete two-source configuration.

## Commands

```text
spolia setup [--yes] [--no-schedule] [--json]
spolia sync [job] [--dry-run] [--if-due] [--quiet] [--json]
spolia doctor [--json]
spolia uninstall [--dry-run] [--yes] [--json]
```

`sync --dry-run` performs real authenticated discovery, policy evaluation, target inspection, and Kimi Code validation without writing. A failed job preserves its previous managed models while unrelated jobs may still update. `doctor` is read-only. `uninstall` removes only the owned scheduler, Kimi Code entries, configuration, and state; it leaves the executable and user-owned Kimi Code content in place.

## Release validation

Repository tests exercise real Kimi Code configuration validation and native schedulers when their prerequisites are available:

```sh
go test ./...
go test -race ./...
go vet ./...
```

The Linux live check uses isolated Kimi Code and spolia homes, runs setup and sync, activates the selected alias only inside the disposable Kimi Code home, invokes it through the real Kimi Code binary, requires a harmless shell-tool turn, and emits a redacted JSON report. It never changes the operator's Kimi Code configuration:

```sh
go build -o /tmp/spolia-livecheck ./cmd/spolia
go run ./tools/livecheck --spolia /tmp/spolia-livecheck --source all
```

The default is stealth-only for both cells. If a cell has no stealth candidate, the operator must explicitly choose a broader policy; the tool does not rerun or widen automatically:

```sh
go run ./tools/livecheck --spolia /tmp/spolia-livecheck --source all --openrouter-policy free --zenmux-policy free
```

To retry a different candidate without changing policy, use one source and name an exact model that the sync selected, such as `--source zenmux --zenmux-policy free --model '<exact-selected-model-id>'`. The check rejects models outside that run's eligible selection.

## License

spolia is available under the [MIT License](LICENSE).
