# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues at **`workhold/moni-aigate`**. Use the `gh` CLI for all operations.

## Repo coordinate

This working copy's git remotes do not point at GitHub (they are local-path remotes by design). **Do not rely on `gh` inferring the repo from remotes here.** Pass the repo explicitly:

```sh
gh issue list -R workhold/moni-aigate ...
gh api repos/workhold/moni-aigate/...   # API calls work without a remote
```

`gh api` is always available; `gh issue`/`gh pr` subcommands need `-R workhold/moni-aigate` in this checkout.

## Conventions

- **Create an issue**: `gh issue create -R workhold/moni-aigate --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> -R workhold/moni-aigate --json number,title,body,labels,comments`.
- **List issues**: `gh issue list -R workhold/moni-aigate --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Make an issue a sub-issue of a parent**: `gh issue create --parent <parent> ...`, or `gh issue edit <parent> --add-sub-issue <child>` afterwards (`gh` 2.94+). Older `gh`: `gh api --method POST repos/workhold/moni-aigate/issues/<parent>/sub_issues -F sub_issue_id=<child-db-id>` (database id, as in **Blocking** below). Without sub-issues, put `Part of #<parent>` at the top of the child body.
- **Blocking**: GitHub's **native issue dependencies**, the canonical, UI-visible representation. Add an edge with `gh issue edit <child> -R workhold/moni-aigate --add-blocked-by <blocker>` (`gh` 2.94+). Older `gh`: `gh api --method POST repos/workhold/moni-aigate/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric **database id** (`gh api repos/workhold/moni-aigate/issues/<n> --jq .id`, _not_ the `#number` or `node_id`). Where dependencies aren't available, fall back to a `Blocked by: #<n>, #<n>` line at the top of the child body. A ticket is unblocked when every blocker is closed.
- **Count open blockers**: `gh api repos/workhold/moni-aigate/issues/<n> --jq .issue_dependencies_summary.blocked_by` (open blockers only; 0 means startable).
- **Comment on an issue**: `gh issue comment <number> -R workhold/moni-aigate --body "..."`
- **Apply / remove labels**: `gh issue edit <number> -R workhold/moni-aigate --add-label "..."` / `--remove-label "..."`
- **Close**: post the explanation with `gh issue comment <number> -R workhold/moni-aigate --body "..."`, then `gh issue close <number> -R workhold/moni-aigate`.

File issues only into `workhold/moni-aigate`. Never file into `mattpocock/skills` (the repo these skills come from).

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo treats external PRs as feature requests; `/triage` reads this flag.)_

## When a skill says "publish to the issue tracker"

Create a GitHub issue in `workhold/moni-aigate`.

## When a skill says "fetch the relevant ticket"

Read it as in **Read an issue** above.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues as tickets.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body.
- **Child ticket**: an issue linked to the map as a GitHub sub-issue (see **Make an issue a sub-issue of a parent**). Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`). Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: see **Blocking** under Conventions.
- **Frontier query**: list the map's open children, drop any with an open blocker (see **Count open blockers**) or an assignee; first in map order wins.
- **Claim**: `gh issue edit <n> -R workhold/moni-aigate --add-assignee @me`, the session's first write.
- **Resolve**: `gh issue comment <n> -R workhold/moni-aigate --body "<answer>"`, then `gh issue close <n>`, then append a context pointer to the map's Decisions-so-far.
