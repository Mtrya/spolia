# Design

## Scope

spolia is a small cross-platform Go CLI that discovers temporary model opportunities from configured sources and exposes eligible models in supported coding harnesses without silently widening the user's cost policy or changing the harness's active model.

The supported product matrix is:

```text
(OpenRouter, ZenMux) × Kimi Code × (Linux, macOS, Windows)
```

The implementation is a standalone binary with compiled-in adapters. It has no daemon, file watcher, public plugin API, dynamic loading, self-update, telemetry, automated repair command, or user-facing inference probe.

## Pipeline

```text
source adapter → normalized facts and evidence → shared policy → desired models → harness adapter
                                                     ↓
                                            platform scheduler
```

A source adapter fetches and normalizes provider-specific data. It does not decide consent or render harness configuration. The shared policy evaluates stealth, free, compatibility, ranking, and limits identically across sources. The Kimi Code adapter maps desired models into the harness configuration while preserving user-owned content. Platform adapters own one native per-user schedule.

Interfaces remain internal and consumer-owned. Source packages do not import Kimi Code or scheduling packages, the target adapter contains no provider-specific eligibility logic, and build-tagged scheduling implementations share one small lifecycle interface.

## Jobs and consent

A job binds one source, one harness target, and one policy. Selection limits are per job and default to three. Source priority is explicit and resolves duplicate upstream aliases without renaming them.

Every job always considers the stealth class. The ordinary free class is additive and disabled by default. A run never changes those configured choices. Zero candidates is a successful outcome, while authentication, network, HTTP, parsing, incompatible schema, target validation, and write failures are errors.

A stealth candidate must also meet the free-price contract and have explicit or strongly corroborated official stealth, cloaked, anonymous, or temporary anonymous-preview evidence. A generic preview name or zero price is insufficient.

A free candidate has present, well-formed, zero prompt and completion prices across all tiers, plus zero for every other published billing dimension. Missing optional dimensions are not treated as malformed.

Paid models are never eligible. Neither supported source exposes authoritative machine-readable discount evidence in its public catalog, so there is no discounted class; spolia does not scrape badges, compare vendors, or maintain a reference price list.

Only concrete OpenAI-compatible chat models with text output and a positive context window are eligible. Routers, automatic selectors, fallback aliases, and utilities are excluded. Tool use is accepted unless authoritative metadata explicitly disables it. Capabilities are emitted only from explicit normalized metadata.

## Credentials and Kimi Code

A source participates only when its corresponding Kimi Code provider has an API key. Setup may adopt a compatible existing provider or copy a bootstrap environment credential into Kimi Code. Later discovery resolves the credential through Kimi Code so catalog access and inference use the same account. spolia does not persist credentials in its own files.

Provider IDs are `openrouter` and `zenmux`; model aliases are exact upstream IDs and the provider field determines routing. spolia never changes Kimi Code's default model, secondary-model settings, global thinking settings, permissions, or active sessions.

State records only provider and model entries created by spolia and the last non-secret semantic values it wrote. Existing providers and models remain user-owned. A conflict is reported only when the current operation would overwrite or remove a relevant semantic value changed by the user. Whole-entry deletion also protects newly added user fields.

Reconciliation holds one spolia process lock, validates a temporary target with the real Kimi Code binary, checks that the original did not change after planning, and atomically replaces it. State is written atomically after the target. Missing or corrupt ownership state stops writes; there is no automated reconstruction or write-ahead journal.

## Scheduling

One global daily schedule runs all enabled jobs at 09:00 local time by default. A successful full sync after the current local boundary satisfies it; partial, failed, or single-job runs do not. Plain `sync` always refreshes and `sync --if-due` may return an already-satisfied result.

Linux uses a systemd user timer, macOS uses a LaunchAgent, and Windows uses Task Scheduler. Each registration invokes the absolute executable with `sync --if-due --quiet`, uses native catch-up behavior, and remains per-user. There are no cron or system-wide fallbacks.

## Distribution

Release assets use a frozen naming contract:

```text
spolia_VERSION_linux_amd64.tar.gz
spolia_VERSION_linux_arm64.tar.gz
spolia_VERSION_darwin_amd64.tar.gz
spolia_VERSION_darwin_arm64.tar.gz
spolia_VERSION_windows_amd64.zip
spolia_VERSION_windows_arm64.zip
SHA256SUMS
```

`SHA256SUMS` contains one lowercase SHA-256 digest and asset filename per line, separated by two spaces and sorted by filename. Archives contain the binary, README, and MIT license under one versioned root directory. Archive metadata and build flags are deterministic.

Installers resolve the stable GitHub latest-release redirect, verify the chosen archive against `SHA256SUMS`, and install per-user without elevation. The manually dispatched release workflow validates an existing tag across native operating systems and the minimum/latest supported Kimi Code versions before packaging. Publishing is a separate explicit input; only that job receives release and provenance permissions.

## Release evidence

A release candidate must pass formatting, tests, vet, race checks, native scheduler registration and cleanup, real Kimi Code configuration validation, reproducible packaging, per-platform artifact execution, and installer checksum-failure tests.

Provider credentials are never placed in public CI. Before publication, an operator runs the two Linux live cells with isolated homes and real provider credentials. Each cell must select a model under its explicit policy and complete a harmless Kimi Code tool-use turn. Direct API requests and zero-candidate results do not satisfy that check.

## Upstream contracts

When an upstream contract changes, verify it against the current primary documentation and a real supported binary before changing behavior.

- Kimi Code: [configuration reference](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/config-files.md), [data locations](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/data-locations.md), and [releases](https://github.com/MoonshotAI/kimi-code/releases). The supported harness is `MoonshotAI/kimi-code`; legacy `kimi-cli` is unsupported.
- OpenRouter: [Models API](https://openrouter.ai/docs/api/api-reference/models/get-models), [model schema](https://openrouter.ai/docs/guides/overview/models), and the [stealth provider](https://openrouter.ai/provider/stealth) and [OpenRouter model family](https://openrouter.ai/openrouter) pages.
- ZenMux: [Models API](https://zenmux.ai/docs/api/openai/openai-list-models.html), [pricing and fees](https://zenmux.ai/docs/about/pricing-and-cost), and [tool calling](https://zenmux.ai/docs/guide/advanced/tool-calls.html). Website badges and filters never feed selection.
- Scheduling: [systemd timers](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html), [Apple scheduled jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/ScheduledJobs.html), and [Windows Task Scheduler triggers](https://learn.microsoft.com/en-us/windows/win32/taskschd/trigger-types) with [`StartWhenAvailable`](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-startwhenavailable-settingstype-element).
