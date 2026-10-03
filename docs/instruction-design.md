# Instruction Design

## Scope Map

- `AGENTS.md` owns cross-cutting scope, privacy, Git, validation, and document-routing rules.
- `internal/AGENTS.md` owns implementation boundaries for runtime and protocol code under `internal`.
- `cmd/AGENTS.md` owns the native C ABI boundary because the host adapter and exported symbols live under `cmd`.
- `integration/AGENTS.md` owns local-host setup and synthetic fixture rules for integration tests.
- `docs/engineering-contracts.md` owns stable runtime, routing, protocol, compaction, and security behavior.
- The path-specific files do not repeat root Git, privacy, or writing rules.
- Load additional references only when they match the changed boundary.

## Model and Prompt Sources

- The supplied model pages inform agent-facing model selection and remain external because model details can change.
- The repository does not set an agent session model or replace a configured fallback.
- The plugin's Copilot models remain separate from models used to perform repository work.
- The GPT-6 prompting article supports short, scoped instructions and progressive disclosure.
- The Claude prompting pages inform task scope, completion, progress, and verification rules where those rules fit this repository.
- The stop-slop phrase, structure, and example references informed concise wording and removal of filler, passive voice, false contrasts, and vague claims.
- Stop-slop guidance applies to repository prose and does not define protocol behavior or Go code semantics.

## Retrieved Sources

- The following official pages informed the instruction scope and wording.
- [GPT-6 Astra model page](https://developers.openai.com/api/docs/models/gpt-6-astra.md).
- [GPT-6.1 Sol model page](https://developers.openai.com/api/docs/models/gpt-6.1-sol.md).
- [GPT-6 Luna model page](https://developers.openai.com/api/docs/models/gpt-6-luna.md).
- [Rethinking skills and prompts for GPT-6 Astra](https://developers.openai.com/blog/rethinking-skills-and-prompts-for-gpt-6-astra.md).
- [Latest GPT-6 model guide](https://developers.openai.com/api/docs/guides/latest-model.md).
- [Claude Fable 5.1 model overview](https://platform.claude.com/docs/en/models/fable-5-1/overview.md).
- [Claude Opus 5.5 model overview](https://platform.claude.com/docs/en/models/opus-5-5/overview.md).
- [Claude Sonnet 5.5 model overview](https://platform.claude.com/docs/en/models/sonnet-5-5/overview.md).
- [Prompting Claude Fable 5.1](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1.md).
- [Prompting Claude Opus 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5.md).
- [Prompting Claude Sonnet 5.5](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5-5.md).
