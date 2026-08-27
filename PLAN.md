# llmloot Implementation Plan

## Goal

Build `llmloot`, a small cross-platform Go CLI that discovers temporary model opportunities from configured sources and exposes eligible models in supported coding harnesses without silently widening the user's cost policy or changing the harness's active/default model.

The first release supports this product matrix:

```text
(OpenRouter, ZenMux) × (Kimi Code) × (Linux, macOS, Windows)
```

The implementation must keep source, policy, harness, credential, and platform scheduling concerns behind small internal interfaces. This is an architectural boundary, not a public plugin system.

## Source Of Truth

This plan records the product decisions agreed on 2026-08-27. When an upstream contract changes, verify it against the current primary documentation and a real supported binary before changing behavior.

### Kimi Code

- [Configuration reference](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/config-files.md): current `providers`, `models`, credentials, aliases, capabilities, references, and override behavior.
- [Data locations](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/data-locations.md): default configuration discovery and `KIMI_CODE_HOME` behavior.
- [Kimi Code releases](https://github.com/MoonshotAI/kimi-code/releases): binaries used for native compatibility tests.
- The supported harness is `MoonshotAI/kimi-code`. Legacy `kimi-cli`, its paths, and its schema are explicitly unsupported.

### OpenRouter

- [Models API](https://openrouter.ai/docs/api/api-reference/models/get-models) and [model schema](https://openrouter.ai/docs/guides/overview/models): identifiers, prices, modalities, limits, parameters, and lifecycle metadata.
- [Stealth provider](https://openrouter.ai/provider/stealth) and [OpenRouter model family](https://openrouter.ai/openrouter): official evidence for stealth, cloaked, anonymous, and temporary preview models.
- [Provider discount metadata](https://openrouter.ai/docs/guides/community/for-providers): authoritative discount semantics when a machine-readable user discount is exposed.

### ZenMux

- [Models API](https://zenmux.ai/docs/api/openai/openai-list-models.html): model identifiers, modalities, context, reasoning, tiered pricing, units, and effective prices.
- [Pricing and fees](https://zenmux.ai/docs/about/pricing-and-cost): billing dimensions and effective cost records.
- [Tool calling](https://zenmux.ai/docs/guide/advanced/tool-calls.html): OpenAI-compatible tool-use behavior.
- Website badges and filters may inform investigation, but selection must not scrape them. Free or discounted status requires the documented API facts defined below.

### Platform scheduling

- [systemd timers](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html) for Linux user scheduling.
- [Apple scheduled jobs](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/ScheduledJobs.html) for a per-user LaunchAgent.
- [Windows Task Scheduler triggers](https://learn.microsoft.com/en-us/windows/win32/taskschd/trigger-types) and [`StartWhenAvailable`](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-startwhenavailable-settingstype-element) for a per-user scheduled task.

## Current State

- The repository has no commits, remote, implementation, tests, CI, or release artifacts.
- `PLAN.md` is the only project file currently present.
- The authenticated GitHub identity is `Mtrya`; use `github.com/Mtrya/llmloot` as the Go module path unless a future remote establishes a different owner.
- The local Go toolchain is `go1.26.1`; Go 1.27 is the current stable release. Start the module at Go 1.26 and test it with Go 1.26 plus the latest stable toolchain.
- The locally installed Kimi Code binary is version `0.38.0`. Treat `0.38.0` as the initial minimum supported version and test it alongside the latest release.
- On 2026-08-27, OpenRouter's catalog omitted the optional `pricing.request` field and ZenMux's catalog omitted per-model tool and maximum-output fields. The v1 policy below deliberately supports those real schemas.

## Fixed V1 Contract

### Product boundary

- Sources: OpenRouter and ZenMux.
- Harness: Kimi Code only.
- Platforms: Linux, macOS, and Windows.
- Implementation: standalone Go binary with compiled-in adapters.
- Public CLI commands: `setup`, `sync`, `doctor`, and `uninstall`, plus conventional help and version output.
- Distribution: GitHub release archives with checksums, `go install`, and one-line install scripts (`install.sh` for Linux/macOS, `install.ps1` for Windows) that verify checksums before installing per-user. Package-manager publication and self-update remain out of scope.
- One global daily schedule processes all enabled jobs. There is no daemon, resident service, file watcher, tray application, hook, or notification system.
- No public plugin API, dynamic plugin loading, self-update, package-manager publication, telemetry, automated repair command, or user-facing inference probe in v1.
- Do not support legacy `kimi-cli` or add compatibility code for its files.

### Core pipeline

```text
source adapter → normalized facts and evidence → shared policy → desired models → harness adapter
                                                     ↓
                                            platform scheduler
```

- A source adapter fetches and normalizes provider-specific data. It does not decide user consent or render harness configuration.
- The shared policy engine evaluates stealth, free, discounted, price ceilings, compatibility, ranking, and limits identically across sources.
- A harness adapter maps desired models into the harness's native configuration and protects unrelated user data.
- A scheduler adapter installs and removes one native per-user invocation.
- Interfaces remain internal and consumer-owned. Add future sources, harnesses, credential stores, or platform implementations as compiled adapters without putting hypothetical vendor fields into the core model.

### Jobs and source priority

- A job binds one source, one harness target, and one policy. Policy is per job, not global.
- A single invocation processes every enabled job unless the user names one explicitly.
- Selection limits apply per source/job. The default limit is three models per job.
- `source_priority` is an explicit ordered configuration list. Setup initially orders sources by the order in which the user enables them.
- Model aliases remain exact upstream model IDs. Do not add an llmloot prefix.
- Kimi Code model aliases are globally unique. If two enabled sources select the same upstream ID, keep only the result from the higher-priority source.
- A failed job preserves its previous managed models while unrelated successful jobs may update. The overall command exits nonzero when any requested job fails.
- Successful jobs targeting the same physical Kimi Code file are combined into one target plan and one atomic file replacement.

### Eligibility classes and consent

Every job always considers the stealth class. The two broader classes are independent, additive, and disabled by default:

```toml
include_free = false
include_discounted = false
```

- Enabling `include_free` adds ordinary free models; it does not replace stealth selection.
- Enabling `include_discounted` adds discounted paid models and requires explicit price ceilings.
- A run never broadens its configured classes automatically. Zero candidates remains zero candidates.
- Within each source limit, order classes as stealth, free, then discounted. Broader opt-ins fill remaining slots rather than displacing stealth models.
- A candidate that satisfies several classes is assigned to the highest-priority class only.

#### Stealth

- A stealth candidate must also satisfy the free-price contract.
- Require explicit or strongly corroborated official evidence that a model is stealth, cloaked, anonymous, or a temporary anonymous preview.
- A generic `alpha`, `beta`, `preview`, unfamiliar name, or free price is not stealth evidence by itself.
- Keep source-specific evidence interpretation in the source adapter and normalize it into typed evidence records that human and JSON output can explain.

#### Free

- Required prompt/input and completion/output prices must exist, parse as non-negative decimal values, use supported units/currency, and equal zero across every published tier.
- Every other billing dimension that the source reports must also be zero. An explicitly nonzero request, reasoning, cache, search, or other price disqualifies the candidate from the free class.
- A missing optional billing dimension means not applicable and does not disqualify the candidate. In particular, an omitted OpenRouter `pricing.request` field is not treated as malformed or nonzero.
- Malformed required price data excludes that candidate with an explanation. A catalog-wide incompatible schema is a source failure, not a valid zero result.

#### Discounted

- Require authoritative machine-readable discount evidence from the source. Do not infer a discount by comparing vendors, maintaining a hand-written price list, or scraping website badges.
- If a source publishes only an effective price and no discount evidence, it has zero discounted candidates until that contract changes.
- No minimum discount percentage is required.
- Every nonzero billing dimension must have a matching configured ceiling with compatible currency and unit. A missing ceiling, incomparable tier, malformed value, or price above the ceiling makes the candidate ineligible.
- Store monetary values as decimal strings and compare them without binary floating-point conversion.

### Harness compatibility and ranking

- Consider only concrete models. Exclude routers, automatic selectors, fallback aliases, and utilities such as `openrouter/free` even when they are free.
- Require an OpenAI-compatible chat path, text output, and a positive context window.
- Maximum output size is optional. Preserve it when the source publishes it; do not invent or require it.
- Treat tool use as supported unless authoritative source metadata explicitly disables it. This matches current Kimi Code catalog behavior and avoids rejecting ZenMux solely because its list API omits a per-model tool flag.
- Emit reasoning, thinking, image, audio, and video capabilities only from explicit normalized metadata. Do not infer them from model names or marketing prose.
- Default minimum context is 131072 tokens and is configurable per job.
- Within an eligibility class, rank by source creation/publication time, then local `first_seen`, then canonical model ID for deterministic ties.

### Credentials

- A source participates in sync only when the corresponding harness provider has an API key. Anonymous catalog access does not make an uncredentialed source enabled.
- Authenticate catalog discovery with the same provider credential Kimi Code will use for inference, then treat that credential-visible catalog and effective prices as authoritative.
- Treat API keys as opaque. Do not infer ZenMux plan or account type from the key.
- During setup, preserve a compatible existing Kimi Code provider key; otherwise copy `OPENROUTER_API_KEY` or `ZENMUX_API_KEY` into the corresponding Kimi Code provider entry. If no key is available, interactive setup may request one through hidden input; non-interactive setup fails. Environment variables are bootstrap inputs, not a second runtime credential source.
- llmloot's own config, state, output, and logs never store or print API keys.
- Resolve runtime source credentials through a small credential interface backed in v1 by the corresponding Kimi Code provider created or adopted during setup. This keeps source code independent of Kimi, ensures discovery and inference use the same account, and avoids a second persistent secret store.

### Kimi Code integration

- Edit Kimi Code's normal user configuration discovered through its current official rules. Do not require a wrapper, alternate home, temporary registry, or `kimi provider add`.
- Provider IDs are `openrouter` and `zenmux`; each keeps the official base URL and uses the current Kimi Code `openai` provider type unless real-binary validation proves a different current contract is required.
- Model aliases are exact upstream IDs. The model entry's `provider` field determines the route.
- A compatible pre-existing provider is adopted. Preserve its credential, headers, and unknown user fields. An incompatible provider ID is a conflict; do not rewrite it.
- Never change `default_model`, global thinking settings, permissions, active sessions, or unrelated providers/models.
- Preserve unrelated formatting, comments, fields, model override tables, and other user additions.
- If a managed model becomes ineligible but is referenced by Kimi Code's default, secondary-model, or another known model-reference field, retain it as protected and report it. The user must change the reference before a later sync removes it.
- Generated configuration must parse and pass the real supported Kimi Code configuration validator before replacement.

### Small ownership model

- State records the provider entries llmloot created, the model aliases it created, and the last non-secret semantic values llmloot wrote for those entries.
- Existing adopted provider fields and pre-existing models remain user-owned.
- A desired alias that already belongs to a user-owned model is a conflict, not an overwrite.
- Conflict checks are operation-scoped. Added comments, formatting, overrides, and unknown fields do not conflict during an unrelated update.
- Report a conflict only when the current operation would overwrite or remove a semantic value that changed since llmloot last wrote it. A user addition becomes relevant if deleting the whole entry would delete that addition.
- Use one llmloot process lock, validate a temporary target file, verify that the original file has not changed since planning, and atomically replace it. Do not add retries or cross-file transaction machinery in v1.
- Write state atomically after a successful target write. Do not add a write-ahead journal. An interrupted run should converge on the next idempotent sync when the current target unambiguously matches the desired state; otherwise fail clearly and let the real case justify a later recovery design.
- If ownership state is missing or corrupt, `sync` and target cleanup stop before writes. `doctor` explains the condition. V1 has no automated reconstruction command.
- Do not persist full target backups, catalog bodies, inference content, or rolling logs.
- `uninstall` removes the native scheduler, llmloot-created model/provider entries when still safe, llmloot config, and state. It never removes the binary or user-owned target content. If safe target cleanup cannot complete, retain the ownership state needed to diagnose or retry.

### Runtime and scheduling

- Default schedule is 09:00 in the machine's local timezone and is configurable globally.
- Interactive setup enables native scheduling by default and offers an explicit no-schedule option.
- Setup performs an immediate sync before installing the schedule.
- The scheduler invokes the absolute executable path with `llmloot sync --if-due --quiet`.
- Any successful full sync after the current local schedule boundary satisfies that boundary, whether it was manual or scheduled. A partial or failed run does not.
- Plain `llmloot sync` always refreshes. `--if-due` may no-op successfully when the boundary is already satisfied.
- Use native per-user scheduling with catch-up behavior: systemd user timer on Linux, LaunchAgent on macOS, and Task Scheduler on Windows. Do not add cron or system-wide fallbacks in v1.
- Scheduled sync is metadata-only. It never performs inference, changes the active/default model, reloads Kimi Code, or emits desktop notifications.

### Success, zero, and failure semantics

- A valid catalog with zero eligible candidates is a successful `zero_candidates` outcome. It satisfies the schedule boundary and removes unreferenced llmloot-managed models for that job.
- A user can explicitly change policy and rerun, but the current run never widens policy itself.
- Authentication, network, HTTP, parsing, incompatible schema, target validation, or write failures are errors. Preserve the affected job's previous managed set.
- Do not turn an error into an empty catalog.
- A partial multi-job run reports each outcome and exits nonzero while retaining successful changes.

## Public CLI Contract

### `llmloot setup`

- Discover the current Kimi Code binary/configuration, available source credentials, and existing compatible providers.
- Guide the user through enabled sources, ordered source priority, per-job policy, discounted ceilings, selection limits, context threshold, schedule time, and scheduling opt-out.
- Show the planned target changes before confirmation, apply one immediate sync, validate Kimi Code, then install scheduling when enabled.
- `--yes` may accept non-secret confirmations but never supplies a missing credential or enables free/discounted classes implicitly.
- Re-running setup updates llmloot configuration and the one existing scheduler registration idempotently.

### `llmloot sync`

```text
llmloot sync [job] [--dry-run] [--if-due] [--quiet] [--json]
```

- Without a job, process every enabled job.
- `--dry-run` performs real discovery, normalization, selection, target inspection, and validation without writing target, state, or scheduler files.
- `--json` returns a versioned, redacted result containing per-job outcome, selected models, eligibility class, evidence, exclusions summary, and target plan.
- `--quiet` suppresses normal output but preserves exit status and compact state updates. It is mutually exclusive with `--json`.

### `llmloot doctor`

- Read-only checks: llmloot config/state validity, source credentials and authenticated catalog schema, Kimi Code discovery/version/config validation, provider compatibility, managed aliases, protected/conflicting entries, scheduler registration, executable path, and last-run outcomes.
- Human output explains concrete remediation. `--json` exposes the same checks without credentials or raw provider objects.
- No repair or inference-probe mode in v1.

### `llmloot uninstall`

- Preview exact scheduler and target removals and require confirmation unless `--yes` is supplied.
- `--dry-run` performs the same inspection and validation without mutation.
- Remove only llmloot-owned artifacts. Keep state when target cleanup fails so the user can diagnose and retry.

### Exit codes and output

- `0`: the requested operation completed successfully, including valid zero-candidate and already-not-due outcomes.
- `1`: operational failure, unhealthy doctor result, or partial multi-job failure.
- `2`: invalid command usage or invalid llmloot configuration detected before work begins.
- Human prose is not a test contract. JSON decisions, exit codes, state transitions, and target structure are contracts.
- Never output credentials, authorization headers, raw credential-bearing provider objects, hidden input, or unredacted environment values.

## Configuration And State

Use `LLMLOOT_HOME` when set; otherwise use `os.UserConfigDir()/llmloot`. Keep the persistent footprint small:

```text
config.toml
state.json
llmloot.lock
```

An initial two-source configuration should be structurally equivalent to:

```toml
schema_version = 1
source_priority = ["openrouter", "zenmux"]

[schedule]
enabled = true
local_time = "09:00"

[sources.openrouter]
adapter = "openrouter"
credential_env = "OPENROUTER_API_KEY"

[sources.zenmux]
adapter = "zenmux"
credential_env = "ZENMUX_API_KEY"

[targets.kimi-code]
adapter = "kimi-code"

[jobs.openrouter-kimi-code]
enabled = true
source = "openrouter"
target = "kimi-code"
limit = 3
min_context = 131072

[jobs.openrouter-kimi-code.policy]
include_free = false
include_discounted = false

[jobs.zenmux-kimi-code]
enabled = true
source = "zenmux"
target = "kimi-code"
limit = 3
min_context = 131072

[jobs.zenmux-kimi-code.policy]
include_free = false
include_discounted = false
```

When discounted selection is enabled, the job also requires a decimal-string ceiling map keyed by normalized billing dimension and unit. Unknown config versions and unknown policy keys fail clearly; do not silently reinterpret them.

`state.json` contains only:

- Schema and llmloot versions.
- Per-source `first_seen` timestamps.
- Per-job last attempt, last success, outcome, selected IDs, and compact redacted error.
- Last successful global schedule boundary.
- The small ownership manifest and last non-secret semantic values written.
- Native scheduler kind, identifier, executable path, and last observed status.

## Architecture And Package Boundaries

Start with this compact layout and change it only when implementation evidence shows a simpler boundary:

```text
cmd/llmloot/
internal/app/
internal/cli/
internal/config/
internal/model/
internal/policy/
internal/source/openrouter/
internal/source/zenmux/
internal/target/kimicode/
internal/credential/
internal/state/
internal/schedule/
internal/output/
internal/testdata/
tools/livecheck/
```

- Keep interfaces close to their consumers in `internal/app`, `internal/target`, and `internal/schedule`; do not create a framework package full of speculative abstractions.
- The normalized model includes only cross-source facts required by the agreed policy: identity, display name, source timestamps, typed price items and tiers, context/output limits, modalities, protocol, lifecycle, capabilities, and typed opportunity evidence.
- Source packages must not import Kimi Code or scheduler packages.
- The Kimi Code target must not contain OpenRouter- or ZenMux-specific eligibility logic.
- Platform scheduling implementations may use build-tagged files behind one small lifecycle interface.
- Use the standard library by default. Add dependencies only for a demonstrated need, pin them, and keep unstable APIs behind a narrow local wrapper.

## Validation Strategy

- Follow the repository rule against fake tests and logic-duplicating tests. Exercise public behavior and actual implementations.
- Parser and policy tests use minimized, provenance-documented fixtures derived from real source responses, including malformed records, zero candidates, tiered prices, optional missing dimensions, routers, duplicate IDs, and every eligibility class.
- Live metadata tests use real authenticated OpenRouter and ZenMux endpoints when credentials are explicitly available. Ordinary CI does not require provider secrets.
- Kimi Code integration tests use downloaded real supported binaries, isolated `KIMI_CODE_HOME` directories, real TOML parsing/validation, dummy credentials for non-inference tests, and byte-level checks that unrelated user content survives.
- Native scheduler tests use unique temporary identifiers on real Linux, macOS, and Windows runners and clean them up even on failure.
- Cross-platform CI runs formatting, vet, tests, race checks where supported, native target/config/scheduler tests on all three operating systems, and cross-builds the intended release artifacts.
- Tests assert decisions, structures, state transitions, and redaction. Do not freeze incidental human wording.

## Implementation Phases

Keep the roadmap in this file. Separate phase documents are unnecessary unless implementation grows enough that a fresh session cannot execute one phase from this dossier.

### Phase 1: Foundation, Both Sources, And Shared Policy

#### Goal

Produce a real `sync --dry-run` path that authenticates to either source, normalizes actual catalogs, applies the agreed per-job policy, resolves source collisions, and emits deterministic human/JSON plans without touching Kimi Code.

#### Inputs To Read

- This plan's source, eligibility, credential, and job contracts.
- Current primary OpenRouter and ZenMux model/pricing documentation and one authenticated response from each source.
- Current Go release support policy before choosing CI toolchain versions.

#### In Scope

- Go module, CLI skeleton, versioned config parser/validation, small state store, process lock, normalized model/price/evidence types, internal source interface, OpenRouter adapter, ZenMux adapter, shared policy, source-priority collision resolution, ranking, redacted output, and dry-run orchestration.
- Real-data fixtures and opt-in authenticated metadata checks for both sources.

#### Out Of Scope

- Kimi Code writes, scheduler installation, inference, release artifacts, automated repair, and dynamic plugins.

#### Validation

- Decision tests cover default stealth-only, additive free/discounted opt-ins, mandatory ceilings, optional missing prices, tiered prices, source-authoritative discounts, routers, explicit tool disablement, source collisions, per-source limits, deterministic ranking, source errors, and valid zero.
- `go test ./...`, `go vet ./...`, and a fixture-backed `llmloot sync --dry-run --json` pass without creating state or target files.
- Authenticated metadata checks parse current OpenRouter and ZenMux responses without logging keys or raw catalogs.

#### Exit Criteria

- Both real source adapters produce the same normalized contract.
- One shared policy explains every inclusion/exclusion and never silently widens.
- A source failure is distinguishable from valid zero.
- No Kimi Code or OS-specific type leaks into source or policy packages.

#### Suggested Prompt

> Read `PLAN.md` completely, verify the repository and live source schemas before editing, and implement only Phase 1. Build the smallest real dry-run vertical path for both authenticated sources and the shared policy. Use provenance-documented real fixtures, keep zero valid, avoid fake tests and target-specific fields, and run the phase validation before reporting completion.

### Phase 2: Kimi Code Target And V1 Lifecycle

#### Goal

Make `setup`, `sync`, `doctor`, and `uninstall` work end to end against the current Kimi Code configuration contract, without native scheduling yet.

#### Inputs To Read

- This plan's Kimi Code, credentials, ownership, CLI, and failure contracts.
- Current Kimi Code configuration/data-location documentation and source.
- Real Kimi Code `0.38.0` and latest binaries, including current help and config-validation behavior.
- Completed Phase 1 implementation and tests.

#### In Scope

- Kimi Code discovery/version check, credential copying and later retrieval through the credential interface, compatible provider adoption/creation, source-priority alias collision behavior, model rendering, capability mapping, protected references, small ownership manifest, syntax-preserving edits, target validation, hash-before-replace, atomic replacement, full public CLI lifecycle, and redacted doctor output.

#### Out Of Scope

- Native scheduler installation, inference probes, automated ownership repair, write-ahead recovery, public plugin APIs, and release publication.

#### Validation

- Real Kimi Code binaries validate isolated configurations on available native runners; tests do not substitute an internal parser for the harness validator.
- Fixtures cover compatible/incompatible providers, manual alias collision, relevant versus unrelated user edits, unknown fields/comments/overrides, duplicate-source aliases, protected references, valid zero removal, source failure preservation, dry-run, idempotence, uninstall, missing/corrupt state, and a concurrent file change detected before replacement.
- Credentials appear only in isolated Kimi Code provider entries and never in llmloot files, output, or failure diagnostics.

#### Exit Criteria

- Both sources can expose selected models in normal Kimi Code configuration using exact upstream aliases.
- Setup never changes Kimi Code defaults or active behavior.
- Sync preserves unrelated content, validates before replacement, and isolates failed jobs.
- Doctor diagnoses every intentionally supported failure without mutating files.
- Uninstall removes only llmloot-owned artifacts.

#### Suggested Prompt

> Read `PLAN.md`, verify Phase 1 from code and tests, inspect current Kimi Code 0.38.0 and latest contracts, then implement only Phase 2. Use real isolated Kimi Code validation, preserve unrelated user content, keep ownership small and operation-scoped, add no repair or transaction subsystem, and run every lifecycle and redaction check before declaring the phase complete.

### Phase 3: Native Scheduling And Cross-Platform Proof

#### Goal

Deliver one reliable daily per-user sync on Linux, macOS, and Windows and prove the claimed platform support with native tests.

#### Inputs To Read

- This plan's schedule, due-boundary, platform, and cleanup contracts.
- Current primary scheduler documentation for all three operating systems.
- Completed lifecycle implementation from Phase 2.

#### In Scope

- Global due-boundary logic, absolute-path scheduled invocation, systemd user timer, LaunchAgent, Windows Task Scheduler integration, setup opt-out/default behavior, doctor status, idempotent schedule update, uninstall cleanup, and native CI.

#### Out Of Scope

- Cron, system-wide tasks, per-job schedules, notifications, a resident process, inference, and repository publication.

#### Validation

- Boundary tests cover setup's immediate sync, before/after schedule time, manual success, partial failure, already satisfied boundary, timezone/DST behavior, and repeated native wake/logon invocation.
- Native runners register, inspect, trigger where practical, update, and remove uniquely named temporary tasks using the real platform implementation.
- Kimi Code configuration/reconciliation tests also run natively on Linux, macOS, and Windows.

#### Exit Criteria

- Setup creates at most one native per-user task and can leave scheduling disabled explicitly.
- Missed runs catch up through native behavior and remain idempotent through `--if-due`.
- Doctor and uninstall identify and manage only the exact llmloot scheduler artifact.
- No supported platform relies solely on cross-compilation or mocked scheduler behavior.

#### Suggested Prompt

> Read `PLAN.md`, verify Phases 1 and 2, inspect current native scheduler behavior on each runner, and implement only Phase 3. Keep one per-user task, use absolute argument-safe invocation, validate actual registration and cleanup with unique temporary names, and do not add fallbacks or background services.

### Phase 4: Release Gate, Distribution, And Public Handoff

#### Goal

Produce reproducible artifacts and pass the complete pre-public validation contract before the repository becomes public or v1 is released.

#### Inputs To Read

- This entire plan and the completed implementation/tests.
- Current Kimi Code releases and the actual artifact architectures available for Linux, macOS, and Windows.
- Current GitHub Actions and artifact-provenance guidance before writing workflows.

#### In Scope

- Secure CI, target-appropriate release builds, checksums/provenance, MIT license, concise public README, installation instructions, and a repository-owned Linux live-check tool.
- One-line install scripts: `install.sh` for Linux/macOS (`curl ... | bash`) and `install.ps1` for Windows (`irm ... | iex`). Each resolves the latest release through the stable release redirect rather than API negotiation, verifies the downloaded archive against the published checksums before installing, installs per-user without elevated privileges, and prints PATH guidance. Once the scripts ship, release asset names and the checksum file format become a frozen compatibility contract.
- The live-check tool runs the two required cells: OpenRouter → Kimi Code → Linux and ZenMux → Kimi Code → Linux.
- Each cell uses an isolated Kimi Code home, runs actual setup/sync output, starts the real Kimi Code binary with the selected model, completes a harmless end-to-end tool-use turn, and emits one redacted machine-readable report.
- The human operator may explicitly change a cell's policy between runs from stealth to free and then discounted with declared ceilings. The tool never widens policy automatically.

#### Out Of Scope

- Provider secrets in public CI, automatic live inference schedules, package-manager submissions, self-update, system-wide or elevated installation, code signing/notarization unless separately requested, and creating/publishing the GitHub repository before the gate passes.

#### Validation

- Clean-checkout CI passes on Linux, macOS, and Windows with real Kimi Code minimum/latest validation and native scheduler coverage.
- Release artifacts execute `version`, fixture dry-run, and doctor on their target operating systems; release architectures are claimed only where current Kimi Code support and native/cross-build evidence exist.
- Native runners execute each install script in a clean environment on its target operating system: the installed binary runs, and a deliberately corrupted archive fails checksum verification and aborts the install.
- The two-cell live report records source, selected model, eligibility class, price ceilings when applicable, llmloot version, Kimi Code version, OS, tool-use success, and redacted failure information.
- Audit public docs, workflow logs, test output, and artifacts for credentials, local paths, stale legacy names, and internal roadmap terminology.

#### Exit Criteria

- Both Linux live cells pass with actual Kimi Code tool use. A zero-candidate cell or substituted direct API request does not pass.
- Native CI passes for configuration, reconciliation, scheduling, and uninstall on all three supported operating systems.
- Both install scripts pass their native smoke checks, including checksum-failure abortion.
- Release artifacts, checksums, provenance, license, and docs reproduce from a clean tag.
- The repository remains private until these checks pass; the first public release is created only afterward through an explicitly authorized repository/release workflow.

#### Suggested Prompt

> Read `PLAN.md` and verify Phases 1 through 3 from the checkout and CI evidence. Implement only Phase 4: secure cross-platform release automation, public documentation, reproducible artifacts, and the two-cell Linux live-check tool. Run real Kimi Code end-to-end tests with local credentials, emit a redacted report, and stop before making the repository public or publishing a release unless that external action is explicitly authorized.

## Cross-Cutting Risks And Deliberate Deferrals

### Upstream schema drift

Source and Kimi Code contracts will change. Keep parsing strict enough to distinguish incompatible schema from valid zero, but do not build a generic compatibility layer. Live metadata checks and minimum/latest real-binary tests provide the evidence for future changes.

### Discount evidence may be absent

The discounted option may legitimately select nothing because current catalog APIs expose effective prices without a machine-readable promotion marker. Keep the option and report the missing evidence; do not work around it with scraping or inferred vendor comparisons.

### Direct target editing

Kimi Code is not aware of llmloot's lock. The v1 boundary is a single-process lock plus validate/hash-check/atomic-replace. Do not add retry loops, a daemon, or a transaction coordinator until live evidence requires one.

### State and target are not one transaction

Both files are atomic individually, but v1 has no write-ahead journal or automated ownership reconstruction. Make operations idempotent and self-converging where ownership is unambiguous; otherwise stop and surface the real mismatch for a future design decision.

### Credentials live in Kimi Code

Kimi Code intentionally stores provider keys in its configuration. llmloot reuses that v1 credential store for scheduled authenticated discovery and must never duplicate keys into its own files, backups, diagnostics, or test artifacts.

### Platform differences

POSIX file and scheduler assumptions do not apply to Windows. Platform claims require native runner evidence, and release architectures must follow actual Kimi Code availability rather than an aspirational matrix.

### Install script contract

The one-line install scripts turn release asset names and the checksum file format into a permanent compatibility contract, and `curl | bash` distribution makes the scripts the most scrutinized files in the repository. Keep them minimal: latest release only, per-user install, mandatory checksum verification, no elevated privileges. SmartScreen and Gatekeeper friction for unsigned binaries remains accepted v1 behavior; revisit code signing only with user evidence.

## Overall Exit Criteria

V1 is complete only when all of the following are true:

- OpenRouter and ZenMux both feed the shared policy and normal Kimi Code configuration on Linux, macOS, and Windows.
- Default setup is stealth-only; free and discounted classes require explicit independent opt-ins, discounted prices are fully capped, and no run widens policy automatically.
- Duplicate upstream aliases resolve through explicit source priority while preserving the original alias.
- Valid zero removes unreferenced managed models and succeeds; real source failures preserve the previous affected set and fail visibly.
- llmloot never changes Kimi Code's default/active model, never performs scheduled inference, and never supports legacy `kimi-cli`.
- Target updates preserve unrelated user content, use small operation-scoped ownership, validate with real Kimi Code, and uninstall cleanly.
- One native per-user schedule works and catches up on all three supported operating systems without a resident process.
- Native CI proves configuration, scheduling, reconciliation, and uninstall on Linux, macOS, and Windows.
- Before publication, both required Linux source-to-Kimi live cells complete a real harmless tool-use turn and produce a redacted report.
- No public plugin system, automated repair/recovery subsystem, inference probe command, or other deferred complexity enters v1 without new evidence and an explicit plan change.
