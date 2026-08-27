# Catalog fixtures

These fixtures are minimized catalog records, not hand-designed substitutes for the source schemas.

- `openrouter-models.json` was captured from `GET https://openrouter.ai/api/v1/models` on 2026-08-27. The current free model, router, and override-priced model retain the fields consumed by the adapter. The historical `openrouter/quasar-alpha` record uses OpenRouter's archived model page for its official cloaked-model description and catalog facts so stealth classification remains covered even when no stealth model is currently listed.
- `zenmux-models.json` was captured from `GET https://zenmux.ai/api/v1/models` on 2026-08-27. Repeated identical zero-price tiers were collapsed; the paid model retains its real tier conditions.

API keys, account data, unrelated descriptive prose, benchmark data, and fields not consumed by the adapters were removed.
