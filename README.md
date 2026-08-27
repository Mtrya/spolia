# llmloot

llmloot discovers temporary model opportunities from model aggregators, applies an explicit local eligibility policy, and reconciles selected models into an AI coding harness without changing its active or default model.

The initial adapters support OpenRouter and ZenMux as sources and Kimi Code 0.38.0 or newer as the target. Model aliases remain exact upstream IDs, and a run never widens the configured policy when no candidate is available.

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
export OPENROUTER_API_KEY=...
export ZENMUX_API_KEY=...
./llmloot setup --no-schedule
./llmloot sync --dry-run --json
./llmloot sync
./llmloot doctor
```

Interactive setup asks one question at a time and shows the target plan before confirmation. `setup --yes` uses the existing configuration or stealth-only defaults; it never implicitly enables ordinary free or discounted paid models. `--yes` also never substitutes for a missing credential.

During setup, llmloot adopts a compatible existing Kimi Code provider or copies the corresponding environment credential into a new provider entry in Kimi Code's normal `config.toml`. Later syncs authenticate catalog discovery with that same provider credential. Environment variables are setup bootstrap inputs, not a second runtime credential source. llmloot's own config, state, output, and diagnostics never contain provider keys.

Each sync preserves unrelated Kimi Code content, validates the proposed config with the real `kimi doctor config` command, checks that the file did not change after planning, and atomically replaces it. A source failure preserves that job's previous managed aliases while successful jobs can still reconcile. A valid zero-candidate result removes only unreferenced llmloot-owned aliases. Aliases referenced by `default_model` or `secondary_model.model` are retained and reported.

Model IDs remain exact upstream IDs. When both sources select the same ID for one target, `source_priority` decides which provider keeps it.

`llmloot doctor [--json]` is read-only. `llmloot uninstall --dry-run` previews exact cleanup, and `llmloot uninstall --yes` removes only entries and local files recorded as llmloot-owned. The executable and user-owned Kimi Code content are retained.

## Validation

```sh
go test ./...
go vet ./...
go run ./tools/livecheck --source all
```

The live check requires both `OPENROUTER_API_KEY` and `ZENMUX_API_KEY`. It prints only catalog counts and normalization failures.
