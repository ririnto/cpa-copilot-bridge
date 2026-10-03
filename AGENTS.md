# Repository Instructions

## Scope and Ownership

- Follow the current user request and the designated plan when one applies.
- Check the worktree before editing and preserve changes outside your assigned scope.
- Keep one writer per shared resource and report ownership conflicts before editing.
- Complete authorized implementation and validation before reporting completion.
- Use the smallest existing interface that meets the requirement.

## Privacy and Git

- Keep machine paths, credentials, tokens, real auth files, and private provider payloads out of committed content.
- Use synthetic credentials and local mock servers for tests and examples.
- Preserve user-authored commit authorship and committer metadata.
- Use the current Git configuration for new commits and do not rewrite existing history.
- Use branch names or release tags as durable review references instead of relying on a commit hash alone.
- Do not force-push or publish changes outside the authorized target.

## Validation and Writing

- Use `Taskfile.yaml` through `go tool task` for repository tasks defined there.
- Format changed Go files with `gofmt` and run focused tests for changed behavior.
- Run the build task when a change affects the native plugin artifact.
- Report the commands, results, and any checks that could not run.
- Write technical documentation in plain English with one complete sentence per Markdown source line.
- Name the actor, state the specific rule, and remove filler, passive phrasing, and repeated instructions.
- Do not force a session model or replace configured model fallbacks.
- Follow references that match the changed boundary instead of loading every project document.

- Use [contribution commands](CONTRIBUTING.md) for validation and delivery.
