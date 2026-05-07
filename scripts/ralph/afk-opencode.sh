#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
PROMPT_FILE="$SCRIPT_DIR/afk-opencode.prompt.md"
MODEL="openai/gpt-5.5"
AGENT="${RALPH_OPENCODE_AGENT:-build}"
LOG_DIR="${RALPH_LOG_DIR:-${TMPDIR:-/tmp}/tack-ralph-opencode}"

usage() {
  printf 'Usage: %s <iterations> [issue-number]\n' "$0"
  printf '\n'
  printf 'Runs one OpenCode headless AFK pass per iteration.\n'
  printf 'The OpenCode model is fixed to %s.\n' "$MODEL"
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ -z "${1:-}" ]]; then
  usage >&2
  exit 1
fi

ITERATIONS="$1"
ISSUE_NUMBER="${2:-}"

if ! [[ "$ITERATIONS" =~ ^[0-9]+$ ]] || [[ "$ITERATIONS" -lt 1 ]]; then
  printf 'iterations must be a positive integer\n' >&2
  exit 1
fi

for required in gh git opencode; do
  if ! command -v "$required" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$required" >&2
    exit 1
  fi
done

if [[ ! -f "$PROMPT_FILE" ]]; then
  printf 'missing prompt file: %s\n' "$PROMPT_FILE" >&2
  exit 1
fi

mkdir -p "$LOG_DIR"

cd "$REPO_ROOT"

for ((i = 1; i <= ITERATIONS; i++)); do
  printf '\n===============================\n'
  printf '  RALPH OpenCode iteration %d / %d\n' "$i" "$ITERATIONS"
  printf '  model: %s\n' "$MODEL"
  printf '===============================\n\n'

  if [[ -n "$ISSUE_NUMBER" ]]; then
    issues="$(gh issue view "$ISSUE_NUMBER" --json number,title,body,comments,labels,state,url)"
  else
    issues="$(gh issue list --state open --json number,title,body,comments,labels,url --limit 100 --jq 'map(select((any(.labels[]; .name == "afk")) and (all(.labels[]; .name != "hitl"))))')"
  fi
  ralph_commits="$(git log --grep='RALPH' -n 10 --format='%H%n%ad%n%B---' --date=short 2>/dev/null || printf 'No RALPH commits found')"
  log_file="$LOG_DIR/ralph-opencode-$(date +%Y%m%d-%H%M%S)-$i.log"
  prompt_input="$(mktemp "${TMPDIR:-/tmp}/tack-ralph-opencode-prompt.XXXXXX")"
  trap 'rm -f "$prompt_input"' RETURN

  {
    printf '%s\n\n' "You are running in Tack's AFK issue runner."
    printf '%s\n' "OpenCode invocation details:"
    printf '%s\n' "- Command: opencode run"
    printf '%s\n' "- Model: $MODEL"
    printf '%s\n' "- Agent: $AGENT"
    printf '%s\n\n' "- Repository: $REPO_ROOT"
    printf '%s\n\n' "Open issues or selected issue JSON:"
    printf '```json\n%s\n```\n\n' "$issues"
    printf '%s\n\n' "Recent RALPH commits:"
    printf '```text\n%s\n```\n\n' "$ralph_commits"
    printf '%s\n\n' "AFK runner instructions:"
    printf '%s\n' "$(<"$PROMPT_FILE")"
  } >"$prompt_input"

  set +e
  opencode run \
    --dir "$REPO_ROOT" \
    --model "$MODEL" \
    --agent "$AGENT" \
    --title "RALPH AFK issue runner" \
    --dangerously-skip-permissions \
    <"$prompt_input" | tee "$log_file"
  status=$?
  set -e
  rm -f "$prompt_input"
  trap - RETURN

  result="$(<"$log_file")"
  printf '\nLog written to %s\n' "$log_file"

  if [[ "$result" == *"<promise>COMPLETE</promise>"* ]]; then
    printf '\nAll actionable tasks complete after %d iterations.\n' "$i"
    exit 0
  fi

  if [[ "$status" -ne 0 ]]; then
    printf '\nopencode run failed with exit code %d. See %s\n' "$status" "$log_file" >&2
    exit "$status"
  fi

  if [[ -n "$ISSUE_NUMBER" ]]; then
    printf '\nSingle issue mode complete after one iteration.\n'
    exit 0
  fi
done
