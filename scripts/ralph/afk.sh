#!/bin/bash
set -eo pipefail

SCRIPT_DIR="$(dirname "${BASH_SOURCE[0]}")"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"

cd "$REPO_ROOT"

if [ -z "$1" ]; then
  echo "Usage: $0 <iterations>"
  exit 1
fi

for ((i=1; i<=$1; i++)); do
  echo ""
  echo "==============================="
  echo "  RALPH iteration $i / $1"
  echo "==============================="
  echo ""

  issues=$(gh issue list --state open --json number,title,body,comments --limit 100)
  ralph_commits=$(git log --grep="RALPH" -n 10 --format="%H%n%ad%n%B---" --date=short 2>/dev/null || echo "No RALPH commits found")

  result=$(claude --print --dangerously-skip-permissions \
    "$issues Previous RALPH commits: $ralph_commits @$SCRIPT_DIR/prompt.md")

  echo "$result"

  if [[ "$result" == *"<promise>COMPLETE</promise>"* ]]; then
    echo ""
    echo "All actionable tasks complete after $i iterations."
    exit 0
  fi
done
