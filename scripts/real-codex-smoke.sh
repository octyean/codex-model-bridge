#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/real-codex-smoke.sh MODEL [edit|delete|web|instruction|long]

Runs a real Codex CLI request through the currently configured Bridge.
USAGE
}

MODEL="${1:-}"
MODE="${2:-edit}"
if [ -z "$MODEL" ]; then
  usage >&2
  exit 1
fi
case "$MODE" in
  edit|delete|web|instruction|long) ;;
  *)
    usage >&2
    exit 1
    ;;
esac

CODEX_BIN="${CODEX_BIN:-codex}"
WORKDIR="$(mktemp -d /tmp/codex-bridge-smoke.XXXXXX)"
OUTPUT="$(mktemp /tmp/codex-bridge-smoke-output.XXXXXX)"
LAST_MESSAGE="$(mktemp /tmp/codex-bridge-smoke-last.XXXXXX)"
trap 'rm -rf "$WORKDIR" "$OUTPUT" "$LAST_MESSAGE"' EXIT

case "$MODE" in
  edit)
    PROMPT='必须使用 apply_patch 工具创建 bridge-smoke.txt，内容只有 bridge smoke v1。读取文件后，再用 apply_patch 把内容改成 bridge smoke v2，再次读取确认。最后只回复 bridge-smoke-ok。不能使用 shell 命令写文件。'
    ;;
  delete)
    PROMPT='必须使用 apply_patch 工具创建 bridge-smoke.txt，内容只有 bridge smoke v1。读取文件后，再用 apply_patch 删除它，确认文件不存在。最后只回复 bridge-smoke-deleted。不能使用 shell 命令写文件。'
    ;;
  web)
    PROMPT='必须使用网页搜索工具搜索 OpenAI Codex 官方页面。最后只回复页面标题和官方域名，不要修改文件。'
    ;;
  instruction)
    printf '%s\n' 'original' >"$WORKDIR/task.txt"
    printf '%s\n' 'do not change' >"$WORKDIR/protected.txt"
    cat >"$WORKDIR/AGENTS.md" <<'EOF'
# Test Rules

- Only modify `task.txt`.
- Do not modify or delete `protected.txt`.
- Inspect the current target before editing.
- File changes must go through the Codex apply_patch capability, never through shell redirection or an inline script.
- The final response must contain only `bridge-instruction-ok`.
EOF
    git -C "$WORKDIR" init -q
    git -C "$WORKDIR" add -A
    git -C "$WORKDIR" -c user.name=bridge-smoke -c user.email=bridge-smoke@example.invalid commit -qm baseline
    PROMPT='严格遵守 AGENTS.md。必须使用 apply_patch 把 task.txt 的内容改成只有 instruction followed，读取确认，不得修改其他文件。最后只回复 bridge-instruction-ok。'
    ;;
  long)
    mkdir -p "$WORKDIR/internal/rules" "$WORKDIR/cmd/check" "$WORKDIR/docs"
    cat >"$WORKDIR/go.mod" <<'EOF'
module bridgefixture

go 1.24
EOF
    cat >"$WORKDIR/internal/rules/rules.go" <<'EOF'
package rules

func Normalize(input string) string {
	return input
}

func Priority(plan string, retries int) int {
	return retries
}

func Label(name string, active bool) string {
	return name
}
EOF
    cat >"$WORKDIR/cmd/check/main.go" <<'EOF'
package main

import (
	"fmt"
	"os"

	"bridgefixture/internal/rules"
)

func main() {
	checks := []struct {
		ok   bool
		name string
	}{
		{rules.Normalize("  ALPHA Beta  ") == "alpha beta", "normalize"},
		{rules.Priority("pro", 5) == 13, "priority pro cap"},
		{rules.Priority("basic", -2) == 2, "priority basic floor"},
		{rules.Priority("unknown", 2) == 2, "priority unknown"},
		{rules.Label(" release ", true) == "[active] release", "active label"},
		{rules.Label(" release ", false) == "[inactive] release", "inactive label"},
	}
	for _, check := range checks {
		if !check.ok {
			fmt.Fprintln(os.Stderr, check.name)
			os.Exit(1)
		}
	}
	fmt.Println("fixture-ok")
}
EOF
    {
      printf '%s\n' '# Long Development Specification'
      printf '%s\n' 'REQUIREMENT-A: Normalize must trim surrounding whitespace and lowercase the remaining text.'
      for i in $(seq 1 1700); do
        printf 'Context section A-%04d: historical compatibility note with no executable requirement.\n' "$i"
      done
      printf '%s\n' 'REQUIREMENT-B: Priority uses base 10 for pro, base 2 for basic, base 0 otherwise; clamp retries to 0..3 and add it to the base.'
      for i in $(seq 1 1700); do
        printf 'Context section B-%04d: archived design discussion that must not override named requirements.\n' "$i"
      done
      printf '%s\n' 'REQUIREMENT-C: Label trims the name and prefixes [active] when active is true, otherwise [inactive].'
    } >"$WORKDIR/docs/long-spec.md"
    cat >"$WORKDIR/AGENTS.md" <<'EOF'
# Test Rules

- Read `docs/long-spec.md` completely before editing.
- Implement every named `REQUIREMENT-*` rule.
- Only modify `internal/rules/rules.go`; do not add files or change the checker, module, specification, or this file.
- Preserve all public function signatures.
- File changes must go through the Codex apply_patch capability, never through shell redirection or an inline script.
- Run `go run ./cmd/check` after editing.
- The final response must contain only `bridge-long-ok`.
EOF
    git -C "$WORKDIR" init -q
    git -C "$WORKDIR" add -A
    git -C "$WORKDIR" -c user.name=bridge-smoke -c user.email=bridge-smoke@example.invalid commit -qm baseline
    PROMPT='这是一次长上下文开发任务。严格遵守 AGENTS.md，完整读取规格，实现全部 REQUIREMENT，执行检查并只回复指定结果。'
    ;;
esac

"$CODEX_BIN" \
  --ask-for-approval never \
  --sandbox danger-full-access \
  exec \
  --json \
  --output-last-message "$LAST_MESSAGE" \
  --model "$MODEL" \
  --skip-git-repo-check \
  -C "$WORKDIR" \
  "$PROMPT" </dev/null | tee "$OUTPUT"

case "$MODE" in
  edit)
    [ "$(cat "$WORKDIR/bridge-smoke.txt")" = "bridge smoke v2" ]
    grep -q '"kind":"add"' "$OUTPUT"
    grep -q '"kind":"update"' "$OUTPUT"
    [ "$(cat "$LAST_MESSAGE")" = "bridge-smoke-ok" ]
    ;;
  delete)
    [ ! -e "$WORKDIR/bridge-smoke.txt" ]
    grep -q '"kind":"add"' "$OUTPUT"
    grep -q '"kind":"delete"' "$OUTPUT"
    [ "$(cat "$LAST_MESSAGE")" = "bridge-smoke-deleted" ]
    ;;
  instruction)
    [ "$(cat "$WORKDIR/task.txt")" = "instruction followed" ]
    [ "$(cat "$WORKDIR/protected.txt")" = "do not change" ]
    [ "$(git -C "$WORKDIR" diff --name-only)" = "task.txt" ]
    [ "$(cat "$LAST_MESSAGE")" = "bridge-instruction-ok" ]
    grep -q '"kind":"update"' "$OUTPUT"
    ;;
  long)
    go -C "$WORKDIR" run ./cmd/check >/dev/null
    [ "$(git -C "$WORKDIR" diff --name-only)" = "internal/rules/rules.go" ]
    [ "$(cat "$LAST_MESSAGE")" = "bridge-long-ok" ]
    grep -q '"kind":"update"' "$OUTPUT"
    ;;
esac

THREAD_ID="$(sed -nE 's/.*"thread_id":"([^"]+)".*/\1/p' "$OUTPUT" | head -n 1)"
if [ -n "$THREAD_ID" ]; then
  printf 'thread_id=%s\n' "$THREAD_ID"
  if [ "$MODE" = "web" ]; then
    PROMPT_LOG="$HOME/.codex-bridge/logs/sessions/$THREAD_ID/prompt-requests.jsonl"
    grep -q '"stage":"projected_internal_tool_followup"' "$PROMPT_LOG"
  fi
fi

printf 'smoke_ok model=%s mode=%s\n' "$MODEL" "$MODE"
