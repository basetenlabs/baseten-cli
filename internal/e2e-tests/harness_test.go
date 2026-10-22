//go:build e2e

package e2etests

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const harnessToolTaskPrompt = "Read the file task.txt in the current working directory. Create answer.txt in the current working directory containing exactly the contents of task.txt and nothing else."

const harnessSwitchPhase1 = "Do two things. First, create a file named stage1.txt in the current working directory containing exactly: %s. Second, remember the word %s; I will ask about it later."

const harnessSwitchPhase2 = "What word did I ask you to remember? Create a file named stage2.txt in the current working directory containing exactly that word and nothing else."

func TestE2EHarnessThinking(t *testing.T) {
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		model, level := h.piAnthropicThinkingModel(t)
		prompt := "Reason through this step by step, showing your reasoning as you go, and compute 17 * 23. End your reply with exactly this line: " + harnessThinkingMarker
		stdout, stderr := h.run(t, t.TempDir(), "-p", "--mode", "json", "--no-session", "--offline", "--thinking", level, "--model", model, prompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.Zero(t, piErrorTurns(objects), "pi ended a turn with an API error; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		reply := piAssistantText(objects)
		require.Contains(t, reply, harnessThinkingMarker, "pi's final reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		step(t, "pi thinking seen: %v", piThinkingSeen(objects))
	})
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		model := h.claudeThinkingModel(t)
		prompt := "Think hard, reasoning step by step as you go, and compute 17 * 23. End your final reply with exactly this line: " + harnessThinkingMarker
		stdout, stderr := h.run(t, t.TempDir(), "-p", "--output-format", "stream-json", "--verbose", "--model", model, prompt)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		_, result, ok := claudeResult(events)
		require.True(t, ok, "stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Contains(t, result, harnessThinkingMarker, "claude-code's final reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Greater(t, claudeThinkingBlocks(events), 0, "stream-json shows no thinking block, so thinking never engaged; stdout:\n%s", stdout)
	})
}

func TestE2EHarnessToolCall(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessClaudeToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", harnessToolTaskPrompt)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		require.NotEmpty(t, claudeToolUses(events), "claude-code made no tool calls; stdout:\n%s", stdout)
		require.Equal(t, harnessClaudeToolWord, harnessReadWorkspaceFile(t, workspace, "answer.txt"))
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessCodexToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access", harnessToolTaskPrompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		require.Equal(t, harnessCodexToolWord, harnessReadWorkspaceFile(t, workspace, "answer.txt"))
	})
}

func TestE2EHarnessWebSearch(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		prompt := "Use the WebSearch tool to find the version number of the most recent stable release of Python. Reply with only the version number."
		stdout, stderr := h.run(t, t.TempDir(), "-p", prompt, "--output-format", "stream-json", "--verbose", "--allowedTools", "WebSearch")
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		uses := claudeToolUses(events)
		require.Contains(t, uses, "WebSearch", "claude-code never invoked WebSearch; tool uses: %v; stdout:\n%s", uses, stdout)
		_, result, ok := claudeResult(events)
		require.True(t, ok, "stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Regexp(t, regexp.MustCompile(`\d+\.\d+(\.\d+)?`), result, "claude-code's reply is not a version number; stdout:\n%s", stdout)
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		if h.codexWebSearch {
			workspace := t.TempDir()
			prompt := "Use web search to find the version number of the most recent stable release of Python. Reply with only the version number."
			stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-s", "workspace-write", prompt)
			harnessAssertNoAPIError(t, stdout+stderr)
			objects := harnessJSONObjects(stdout)
			searched := false
			for _, object := range objects {
				kind, _ := object["type"].(string)
				if strings.Contains(kind, "web_search") {
					searched = true
				}
			}
			require.True(t, searched, "codex emitted no web_search event; stdout:\n%s", stdout)
			return
		}
		t.Skip("harness setup writes no web_search setting to codex config.toml, and codex's default web search mode does not work against Baseten routes (b10-bench pins tools.web_search = disabled)")
	})
}

func TestE2EHarnessModelSwitch(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		modelA, modelB := h.claudePair(t)
		workspace := t.TempDir()
		stdout1, stderr1 := h.run(t, workspace, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--model", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessClaudeSwitchWord, harnessClaudeRecallWord))
		events1 := parseClaudeStream(stdout1)
		harnessAssertNoAPIError(t, stdout1+stderr1)
		sessionID, _, ok := claudeResult(events1)
		require.True(t, ok, "phase 1 stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout1, stderr1)
		require.NotEmpty(t, sessionID, "phase 1 result event has no session_id")
		require.Equal(t, harnessClaudeSwitchWord, harnessReadWorkspaceFile(t, workspace, "stage1.txt"))
		step(t, "claude-code resuming session %s under %s", sessionID, modelB)
		stdout2, stderr2 := h.run(t, workspace, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--resume", sessionID, "--model", modelB, harnessSwitchPhase2)
		events2 := parseClaudeStream(stdout2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		_, _, ok = claudeResult(events2)
		require.True(t, ok, "phase 2 stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout2, stderr2)
		require.Equal(t, harnessClaudeRecallWord, harnessReadWorkspaceFile(t, workspace, "stage2.txt"))
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		modelA, modelB := h.codexPair(t)
		workspace := t.TempDir()
		stdout1, stderr1 := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access", "-m", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessCodexSwitchWord, harnessCodexRecallWord))
		harnessAssertNoAPIError(t, stdout1+stderr1)
		threadID := codexThreadID(harnessJSONObjects(stdout1))
		require.NotEmpty(t, threadID, "codex emitted no thread.started thread_id; stdout:\n%s", stdout1)
		require.Equal(t, harnessCodexSwitchWord, harnessReadWorkspaceFile(t, workspace, "stage1.txt"))
		step(t, "codex resuming thread %s under %s", threadID, modelB)
		stdout2, stderr2 := h.run(t, workspace, "exec", "resume", threadID, "--skip-git-repo-check", "-m", modelB, "-c", "sandbox_mode=danger-full-access", harnessSwitchPhase2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		require.Equal(t, harnessCodexRecallWord, harnessReadWorkspaceFile(t, workspace, "stage2.txt"))
	})
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		modelA, modelB := h.piPair(t)
		workspace := t.TempDir()
		sessionDir := filepath.Join(h.root, "pi-sessions")
		stdout1, stderr1 := h.run(t, workspace, "-p", "--mode", "json", "--offline", "--session-dir", sessionDir, "--model", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessPiSwitchWord, harnessPiRecallWord))
		harnessAssertNoAPIError(t, stdout1+stderr1)
		require.Equal(t, harnessPiSwitchWord, harnessReadWorkspaceFile(t, workspace, "stage1.txt"))
		step(t, "pi continuing session dir %s under %s", sessionDir, modelB)
		stdout2, stderr2 := h.run(t, workspace, "-p", "--mode", "json", "--offline", "--session-dir", sessionDir, "--continue", "--model", modelB, harnessSwitchPhase2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		require.Zero(t, piErrorTurns(harnessJSONObjects(stdout2)), "pi phase 2 hit an API error; stdout:\n%s", stdout2)
		require.Equal(t, harnessPiRecallWord, harnessReadWorkspaceFile(t, workspace, "stage2.txt"))
	})
}
