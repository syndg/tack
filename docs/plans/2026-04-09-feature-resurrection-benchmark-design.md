# Feature Resurrection Benchmark Design

**Date:** 2026-04-09  
**Status:** Deferred future work  
**Purpose:** Harness evaluation, regression tracking, and demo value

## Goal

Create an end-to-end benchmark that evaluates Tack as a harness, not just as a wrapper around a coding model.

The benchmark should answer a specific question:

> Given a mature repository, can Tack gather the right context, shape it into a high-signal execution packet, and recreate a removed historical feature in a repo-native way?

## Why this benchmark

This is a good fit for Tack's direction because it tests the exact future thesis behind discovery, context dossiers, execution contracts, planning, review, and recovery.

It should not optimize for exact patch reproduction. Senior engineers can implement the same feature differently. The benchmark should care more about:

- behavioral equivalence
- repo idiom fidelity
- quality gate success
- scope discipline
- review quality
- amount of human help needed

## Proposed first shape

Start with one canonical benchmark case.

1. Choose a mature OSS repository with strong tests and clear conventions.
2. Identify a historical feature that landed cleanly and can be isolated.
3. Reset the repo to the state immediately before that feature existed.
4. Give Tack the feature objective plus whatever context the harness is allowed to gather.
5. Run the full Tack loop: discovery, planning, execution, review, merge, and evaluation.
6. Score the result against behavior, idiom fit, gates, and review outcome.

## Rollout plan

Do not build full benchmark infrastructure first.

Use a staged rollout:

- Stage 1: manual or semi-manual benchmark spec for one repository
- Stage 2: use it to shape discovery and dossier design
- Stage 3: automate corpus setup, scoring, and regression tracking once the context flow stabilizes

## Key open questions

- Should benchmark runs be allowed to inspect commit history, or only the checked-out codebase?
- How much operator-supplied context is allowed before discovery and dossier work is implemented?
- What rubric best captures repo-native quality without overfitting to exact diffs?
- Which repository profile produces the best first signal: small mature app, CLI tool, or frontend-heavy app?

## Non-goals for the first version

- large benchmark corpus
- exact AST or diff matching
- full automation
- leaderboard-style evaluation

The first version should exist mainly to expose where Tack's current context flow breaks down and to guide the design of the future context-engineering layer.
