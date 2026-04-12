# Lazygit Benchmark V1 Design

**Date:** 2026-04-11  
**Status:** Proposed next step  
**Purpose:** Concrete first benchmark spec for Tack's context-engineering work

## Goal

Turn the previously deferred benchmark idea into a concrete first benchmark target using a popular, mature open-source repository.

The benchmark should answer:

> Can Tack, given a mature repo with strong conventions and tests, gather the right context, produce a good plan, and recreate a removed historical feature in a repo-native way with limited human help?

## Chosen repository

Use `lazygit` as the first benchmark repository.

Why `lazygit` is the best first target:

- high credibility and popularity
- strong contributor guidance and codebase docs
- substantial unit and integration test surface
- clear repo-native conventions
- smaller and more tractable than `n8n` or `trigger.dev`
- better benchmark signal than a content-heavy repo

Supporting evidence:

- popularity and maintenance cues: `lazygit/README.md`
- contributor guidance: `lazygit/CONTRIBUTING.md`
- codebase guide: `lazygit/docs/dev/Codebase_Guide.md`
- CI including integration tests: `lazygit/.github/workflows/ci.yml`
- integration testing system: `lazygit/pkg/integration/README.md`

## Benchmark philosophy

This benchmark should evaluate Tack as a harness, not as a patch regurgitator.

It should not optimize for exact diff matching. The benchmark should care about:

- behavioral equivalence
- repo idiom fidelity
- passing tests and quality checks
- scope discipline
- planning quality
- review quality
- amount of human help required

## First benchmark family

Use **feature resurrection** as the first benchmark family.

The benchmark flow is:

1. choose a historical feature that landed cleanly
2. reset the repo to the state immediately before that feature existed
3. give Tack the feature objective, with only the context the harness is allowed to gather
4. run the full Tack loop
5. score the result against behavior, fit, and review outcome

## First recommended feature family

Use **undo/redo via reflog** as the first `lazygit` benchmark candidate.

Why this is a strong first candidate:

- it is user-visible and meaningful, not just internal plumbing
- it is documented: `lazygit/docs/Undoing.md`
- it has dedicated integration tests:
  - `lazygit/pkg/integration/tests/undo/undo_commit.go`
  - `lazygit/pkg/integration/tests/undo/undo_drop.go`
  - `lazygit/pkg/integration/tests/undo/undo_checkout_and_drop.go`
- it has a reasonably bounded implementation surface:
  - `lazygit/pkg/gui/controllers/undo_controller.go`
  - `lazygit/pkg/commands/git_commands/reflog_commit_loader.go`
  - `lazygit/pkg/i18n/english.go`
- it exercises multiple capabilities that matter for Tack:
  - repo discovery
  - planning
  - UI/controller understanding
  - git/reflog reasoning
  - integration-test awareness

## Historical anchor for the feature

The undo/reflog capability evolved across several commits rather than one tiny atomic patch. That is acceptable for v1.

Useful historical anchors include:

- `b1941c33f` - `undo via rebase`
- `c3aefdb98` - `stateless undos and redos`
- `708a07841` - `document undo`
- `4065175a5` - `Improve undo action to restore files upon undoing a commit`

For benchmark setup, the exact baseline commit should be the commit immediately before the chosen feature slice is introduced. The first setup task of the benchmark should therefore be a short git archaeology pass that selects one feature slice and freezes:

- the pre-feature baseline commit
- the canonical feature commit range
- the expected user-visible behavior
- the tests and docs associated with that slice

V1 does not need this to be fully automated yet.

## Benchmark slice recommendation

For the first run, prefer a **narrow undo slice**, not the entire undo subsystem history.

Recommended slice:

- resurrect the basic `undo/redo a commit` capability
- require the benchmark to pass `undo_commit`
- allow additional supporting edits to reflog loading, UI prompts, and i18n where needed

Why this is better than resurrecting the whole undo family at once:

- smaller blast radius
- clearer behavior target
- easier to debug benchmark failures
- still rich enough to stress context gathering and planning

## Allowed context for the run

The benchmark should explicitly define what context Tack is allowed to use.

For v1:

- allowed:
  - checked-out repo contents
  - repo-local docs
  - tests
  - config files
  - file and symbol search
  - planning and review artifacts produced by Tack
- not allowed by default:
  - direct access to the historical feature commit diff
  - `git log` or commit-history inspection during the agent run
  - external issue/PR discussions about the feature

Rationale: we want to evaluate discovery and dossier quality from the current repo state, not reward retrieving the answer from git history.

## Benchmark run phases

### Phase 1: Repo preparation

1. clone `lazygit` into a clean benchmark workspace
2. identify the chosen feature slice and pre-feature baseline commit
3. check out the pre-feature baseline into a benchmark branch or worktree
4. confirm the baseline does not already contain the target behavior
5. record the exact benchmark metadata:
   - repo commit
   - feature slice
   - expected tests
   - expected docs

### Phase 2: Tack execution

1. register the benchmark repo with Tack
2. give Tack a feature objective phrased as a normal engineering request
3. let Tack run discovery, planning, execution, review, and merge
4. capture:
   - first-pass context
   - dossier if present
   - plan
   - review output
   - retries / escalations / failures

### Phase 3: Evaluation

1. run targeted unit and integration tests
2. inspect changed files and compare against expected feature surface
3. review the generated PR / review output
4. score the run with the rubric below

## Task prompt shape

The benchmark should use a realistic prompt, not a benchmark-flavored prompt.

Example shape:

> Add support for undoing and redoing recent commit-oriented actions using git reflog. The feature should work from the commits view, present clear confirmation prompts, preserve repo safety, and fit lazygit's existing controller and integration-test conventions.

The exact prompt can be tightened once the feature slice is finalized.

## Scoring rubric

Use a 5-part rubric, each scored `0-2`.

### 1. Behavioral correctness

- `2`: target behavior works and key tests pass
- `1`: partial behavior works but some important case is missing
- `0`: behavior is incorrect or non-functional

### 2. Repo-native fit

- `2`: solution matches lazygit idioms, package boundaries, controller style, and docs/test conventions
- `1`: mostly fits but shows some unnatural structure
- `0`: fights the codebase or introduces obviously unidiomatic structure

### 3. Scope discipline

- `2`: edits stay close to expected feature surface
- `1`: some avoidable spillover
- `0`: broad or noisy changes unrelated to the feature

### 4. Planning / review quality

- `2`: plan and review correctly identify main files, risks, and tests
- `1`: partially correct but misses important surface area
- `0`: poor plan or low-signal review

### 5. Autonomy quality

- `2`: completed with little or no human rescue
- `1`: required some steering but stayed on track
- `0`: required heavy intervention or failed to converge

Maximum score: `10`

## Success criteria for benchmark v1

Benchmark v1 is successful if:

- the benchmark repo and feature slice are frozen in a repeatable spec
- Tack can be run end-to-end against the benchmark setup
- the rubric can be applied consistently
- the run produces useful insight about where context, planning, or execution breaks down

V1 does **not** require:

- multiple repos
- full automation
- historical diff matching
- leaderboard infrastructure

## Why this benchmark is strategically useful

This benchmark will directly shape the next Tack work:

- first-pass context
- dossiers
- adaptive mode routing
- execution contracts
- planner quality evaluation

It is a better investment than broad benchmark automation right now because it provides one hard, credible, end-to-end case that can anchor context-engineering work.

## Immediate next tasks

1. freeze the repo choice: `lazygit`
2. choose the exact undo feature slice
3. identify the exact pre-feature baseline commit
4. write a benchmark runbook with shell commands
5. run the first manual benchmark and record the result

## Recommendation

Use `lazygit` as the first benchmark repo and `undo/redo via reflog` as the first benchmark feature family.

Keep the first slice narrow, manual, and inspectable. The point of v1 is not automation. The point is to expose what Tack is missing before context-engineering lands.
