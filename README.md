# llmloot

llmloot discovers temporary model opportunities from model aggregators, applies an explicit local eligibility policy, and prepares deterministic model plans for AI coding harnesses.

The current implementation provides a read-only `sync --dry-run` path for OpenRouter and ZenMux. It never widens the configured policy when no candidate is available. Target configuration writes, setup, diagnostics, uninstall, and native scheduling are still under development.

## Policy

Every job always considers free stealth models. Ordinary free and discounted paid models are independent, additive opt-ins:

```toml
include_free = false
include_discounted = false
```

Stealth models must also be free. Free means every published price is zero, with prompt and completion prices present. Discounted models require an authoritative machine-readable discount marker from the source and an explicit ceiling for every nonzero billing dimension. Effective price differences and website badges are not treated as discount evidence.

Ceiling keys use `dimension|unit|currency`, matching the normalized prices shown by `sync --dry-run --json`. For example:

```toml
include_discounted = true
price_ceilings = { "prompt|perMTokens|USD" = "1", "completion|perMTokens|USD" = "3" }
```

## Build and run

Go 1.26 or newer is required.

```sh
go build -o llmloot ./cmd/llmloot
export LLMLOOT_HOME=/path/to/your/llmloot-config
mkdir -p "$LLMLOOT_HOME"
cp config.example.toml "$LLMLOOT_HOME/config.toml"
./llmloot sync --dry-run --json
```

The read-only discovery command currently resolves source credentials from the `credential_env` names in `config.toml`. Keys are sent only as bearer credentials to the corresponding catalog API and are not stored or printed by llmloot.

Model IDs remain exact upstream IDs. When both sources select the same ID for one target, `source_priority` decides which provider keeps it.

## Validation

```sh
go test ./...
go vet ./...
go run ./tools/livecheck --source all
```

The live check requires both `OPENROUTER_API_KEY` and `ZENMUX_API_KEY`. It prints only catalog counts and normalization failures.
