# Shared logic for the fake claude/codex/copilot. Reads the prompt on stdin, prints the
# agent's final message on stdout. Knobs (all optional):
#   FAKE_STATE           dir for counters
#   FAKE_REVIEW_<WORKER> review verdict for that worker (approve|changes)
#   FAKE_INVALID_ONCE=1  first reply of the run is not JSON
#   FAKE_RATE_LIMIT=<worker>  that worker fails with a usage-limit error
#   FAKE_BUILD_STATUS    done|blocked|failed
#   FAKE_JUDGE_MISSED    true|false
#   FAKE_BUILD_HOOK      shell command run during the first build
#   FAKE_SPEC_QUESTIONS=1 the spec asks one question (suggested: spaces)
#   FAKE_EXPECT_IN_BUILD build fails unless its prompt contains this text
prompt=$(cat)
state="${FAKE_STATE:-/tmp}"
echo "$WORKER" >> "$state/agent_calls.log"
if [ "${FAKE_RATE_LIMIT:-}" = "$WORKER" ]; then
  echo "ERROR: You've hit your usage limit. Try again in 2 hours." >&2
  exit 1
fi
if [ "${FAKE_INVALID_ONCE:-}" = 1 ] && [ ! -e "$state/invalid_done" ]; then
  touch "$state/invalid_done"; agent_reply "sorry, I forgot the JSON"; exit 0
fi
case "$prompt" in
  *"You are the spec writer"*)
    q=''
    [ "${FAKE_SPEC_QUESTIONS:-}" = 1 ] && q=',"questions":[{"question":"Tabs or spaces?","suggested":"spaces"}]'
    agent_reply "{\"goal\":\"Add a greeting\",\"acceptance_criteria\":[\"greet() returns hello\"],\"assumptions\":[],\"risk\":\"low\",\"files_likely_touched\":[\"greet.txt\"]$q}" ;;
  *"You are the builder"*|*"You are the fixer"*)
    if [ -n "${FAKE_EXPECT_IN_BUILD:-}" ] && [[ "$prompt" != *"$FAKE_EXPECT_IN_BUILD"* ]]; then
      echo "build prompt is missing: $FAKE_EXPECT_IN_BUILD" >&2; exit 3
    fi
    n=$(( $(cat "$state/edits" 2>/dev/null || echo 0) + 1 )); echo $n > "$state/edits"
    echo "edit $n" >> greet.txt
    # FAKE_BUILD_HOOK runs once, during the first build (e.g. to move main).
    [ "$n" = 1 ] && [ -n "${FAKE_BUILD_HOOK:-}" ] && (cd / && bash -c "$FAKE_BUILD_HOOK")
    agent_reply "{\"status\":\"${FAKE_BUILD_STATUS:-done}\",\"summary\":\"edit $n\",\"files_changed\":[\"greet.txt\"],\"notes\":\"needs a decision\"}" ;;
  *"You are a code reviewer"*)
    v_var="FAKE_REVIEW_$(echo "$WORKER" | tr a-z A-Z)"
    v="${!v_var:-approve}"
    # Replay candidates: one flags everything, one flags only diffs containing "bad".
    case "$prompt" in *CANDIDATE_FLAGS_ALL*) v=changes ;; esac
    case "$prompt" in *CANDIDATE_SMART*) case "$prompt" in *+bad*) v=changes ;; esac ;; esac
    agent_reply "{\"verdict\":\"$v\",\"findings\":[{\"severity\":\"low\",\"area\":\"greet.txt\",\"issue\":\"x\"}],\"summary\":\"ok\"}" ;;
  *"You are judging a code reviewer"*)
    agent_reply "{\"missed\":${FAKE_JUDGE_MISSED:-false},\"reason\":\"r\"}" ;;
  *) echo "fake $WORKER: unknown prompt" >&2; exit 2 ;;
esac
