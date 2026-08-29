# spolia

spolia adds currently free models from OpenRouter and ZenMux as selectable models in Kimi Code. It never touches your active or default model — it only gives you more choices, and keeps them fresh with a daily background check.

After setup, the new models are ordinary Kimi Code model aliases:

```sh
kimi --model 'some/selected-model'
```

The name is the architectural term *spolia*: stone stripped from older monuments and built into new ones. The tool does the same with free and stealth models that are lying around in provider catalogs.

The supported matrix is OpenRouter and ZenMux with Kimi Code 0.38.0 or newer on Linux, macOS, and Windows. Model aliases remain the exact upstream IDs. A run with no eligible model succeeds with zero candidates and never silently enables a broader policy.

## Prerequisites

1. Kimi Code 0.38.0 or newer, available as `kimi` on `PATH`. Install it with `curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash` (see the [Kimi Code docs](https://www.kimi.com/code/docs/en/)).
2. An API key for at least one provider — one is enough:
   - OpenRouter: create a key at [openrouter.ai/keys](https://openrouter.ai/keys)
   - ZenMux: sign up at [zenmux.ai](https://zenmux.ai) and create a key under User Console → API Keys

## Install

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Mtrya/spolia/main/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Mtrya/spolia/main/install.ps1 | iex
```

The scripts resolve the latest GitHub release, verify the archive against the published `SHA256SUMS`, and install without elevation. The default destination is `~/.local/bin` on Linux/macOS and `%LOCALAPPDATA%\Programs\spolia\bin` on Windows.

If `spolia` is not found afterwards, the destination is not on your `PATH` yet. Open a new terminal and try again; if it is still missing:

```sh
# Linux and macOS
export PATH="$HOME/.local/bin:$PATH"
```

```powershell
# Windows PowerShell (persists for future terminals)
[Environment]::SetEnvironmentVariable("PATH", "$env:PATH;$env:LOCALAPPDATA\Programs\spolia\bin", "User")
```

To pin a specific version, set `SPOLIA_INSTALL_VERSION`:

```sh
curl -fsSL https://raw.githubusercontent.com/Mtrya/spolia/main/install.sh | SPOLIA_INSTALL_VERSION=0.1.0 bash
```

You can also install from source with Go 1.26 or newer:

```sh
go install github.com/Mtrya/spolia/cmd/spolia@latest
```

## Set up

Export the key for the provider you use and run the setup wizard:

```sh
# Linux and macOS — one or both
export OPENROUTER_API_KEY=...
export ZENMUX_API_KEY=...
spolia setup
```

```powershell
# Windows PowerShell
$env:OPENROUTER_API_KEY = "..."
spolia setup
```

Setup asks which providers to enable (defaulting to the ones that have a credential), whether to also include ordinary free models alongside temporary stealth offers, and whether to check daily. It then shows the full plan — models, Kimi Code changes, and the schedule — before changing anything. Your answers are saved as the defaults for the next run, so rerunning `spolia setup` is how you change your mind later.

Use `spolia setup --yes` to accept the current configuration without questions; sources without a credential are skipped automatically, so a one-provider setup works with one key. Use `--no-schedule` to opt out of the daily check and `--advanced` to tune model limits, minimum context, and source priority.

spolia adopts a compatible Kimi Code provider or copies the bootstrap credential into Kimi Code's normal `config.toml`. Later discovery reuses that provider credential. spolia's own configuration, state, output, and diagnostics do not store or print provider keys.

## Use a model

A successful setup ends with the list of models it added and the exact command to try one:

```sh
kimi --model 'some/selected-model'
```

Inside an interactive Kimi Code session you can also switch with `/model`. spolia never changes your default model; these are extra choices, not a switch.

If setup completes with no matching models, that is a normal temporary state: the stealth catalog is empty right now, and the daily check will pick models up when they appear. To also consider ordinary free models, rerun `spolia setup` and enable them — when free models were skipped for this reason, the summary says so.

## What happens tomorrow

Setup installs one daily per-user schedule: a systemd user timer on Linux, a LaunchAgent on macOS, or a Task Scheduler task on Windows. It runs `spolia sync --if-due --quiet`, which performs metadata discovery only and never starts an inference turn. You can always refresh by hand with `spolia sync`.

## Status, customization, troubleshooting

`spolia doctor` shows what spolia currently manages — the models in Kimi Code, each job's last run, the next scheduled check — followed by health checks with a concrete remedy for anything broken.

The configuration lives at `~/.config/spolia/config.toml` on Linux, `~/Library/Application Support/spolia/config.toml` on macOS, and `%APPDATA%\spolia\config.toml` on Windows. Rerunning `spolia setup` is the supported way to change policy and schedule settings; direct edits apply on the next sync. See [config.example.toml](config.example.toml) for the complete two-source configuration.

Exit codes: `0` success (including "no models matched" and "already checked today"), `1` something failed at runtime (the message names the remedy), `2` the command line itself was wrong.

## Policy

Every enabled job considers free stealth models. Ordinary free models are an explicit opt-in, answered during setup or set directly:

```toml
[jobs.openrouter-kimi-code.policy]
include_free = false
```

Enabling `include_free` adds ordinary free models alongside stealth models. Paid models are never selected: neither supported source exposes authoritative machine-readable discount evidence in its public catalog, so spolia only tracks stealth and free eligibility.

## Commands

```text
spolia setup [--yes] [--no-schedule] [--advanced] [--json]
spolia sync [job] [--dry-run] [--if-due] [--quiet] [--json]
spolia doctor [--json]
spolia uninstall [--dry-run] [--yes] [--json]
```

Every command explains its options with `--help`. `sync --dry-run` performs real authenticated discovery, policy evaluation, target inspection, and Kimi Code validation without writing. A failed job preserves its previous managed models while unrelated jobs may still update. `doctor` is read-only.

## Remove

`spolia uninstall` removes only the owned scheduler, Kimi Code entries, configuration, and state; it leaves the executable and user-owned Kimi Code content in place. To remove spolia completely, also delete the executable:

```sh
# Linux and macOS
spolia uninstall
rm ~/.local/bin/spolia
```

```powershell
# Windows PowerShell
spolia uninstall
Remove-Item -Recurse "$env:LOCALAPPDATA\Programs\spolia"
```

## License

spolia is available under the [MIT License](LICENSE).
