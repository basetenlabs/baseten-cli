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

const harnessThinkingPrompt = "Think it through carefully without using any tools: how many positive integers below 1000 are divisible by 7 or by 11, but not by both? End your final reply with exactly this line: " + harnessThinkingMarker

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
		piThinking := piThinkingSeen(objects)
		require.True(t, piThinking, "pi's stream shows no thinking or reasoning event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		step(t, "pi thinking seen: %v", piThinking)
	})
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		model := h.claudeThinkingModel(t)
		stdout, stderr := h.run(t, t.TempDir(), "-p", "--output-format", "stream-json", "--verbose", "--model", model, harnessThinkingPrompt)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		_, result, ok := claudeResult(events)
		require.True(t, ok, "stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Contains(t, result, harnessThinkingMarker, "claude-code's final reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Greater(t, claudeThinkingBlocks(events), 0, "stream-json shows no thinking block, so thinking never engaged; stdout:\n%s", stdout)
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		model := h.codexThinkingModel(t)
		workspace := t.TempDir()
		stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access", "-m", model, "-c", "model_reasoning_effort=high", harnessThinkingPrompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.Contains(t, codexLastAgentMessage(objects), harnessThinkingMarker, "codex's final agent_message lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		step(t, "codex reasoning items seen: %d", len(codexCompletedItems(objects, "reasoning")))
		tokens := codexReasoningTokens(objects)
		step(t, "codex reasoning_output_tokens: %v", tokens)
		require.Greater(t, tokens, 0.0, "codex reports no reasoning_output_tokens under %s at high effort, so thinking never engaged; stdout:\n%s", model, stdout)
	})
	for _, variant := range harnessOpenCodeVariants {
		t.Run(variant.label, func(t *testing.T) {
			h := newOpenCodeHarnessTest(t, variant)
			model, level := h.openCodeThinkingModel(t)
			args := []string{"run", "--format", "json", "--thinking", "--variant", level, "-m", model, harnessThinkingPrompt}
			if variant.opencode2 {
				args = []string{"run", "--format", "json", "--thinking", "-m", model + "#" + level, harnessThinkingPrompt}
			}
			stdout, stderr := h.run(t, t.TempDir(), args...)
			harnessAssertNoAPIError(t, stdout+stderr)
			objects := harnessJSONObjects(stdout)
			require.Empty(t, harnessEventsOf(objects, "error"), "opencode emitted error events; stdout:\n%s\nstderr:\n%s", stdout, stderr)
			require.Contains(t, openCodeEventText(objects, "text"), harnessThinkingMarker, "opencode's reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
			events, reasoning, tokens := len(harnessEventsOf(objects, "reasoning")), openCodeEventText(objects, "reasoning"), openCodeReasoningTokens(objects)
			step(t, "opencode %s at %s: reasoning events %d, reasoning text %d chars, step_finish reasoning tokens %v", model, level, events, len(reasoning), tokens)
			require.NotEmpty(t, reasoning, "opencode streamed no reasoning text under %s at %s (reasoning events %d, step_finish reasoning tokens %v); stdout:\n%s", model, level, events, tokens, stdout)
		})
	}
}

func TestE2EHarnessToolCall(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessClaudeToolWord + "\n"})
		args := append([]string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}, harnessDefaultModelArgs(t, "--model", h.claudeDefault, h.claudeIDs())...)
		stdout, stderr := h.run(t, workspace, append(args, harnessToolTaskPrompt)...)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		require.NotEmpty(t, claudeToolUses(events), "claude-code made no tool calls; stdout:\n%s", stdout)
		require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessCodexToolWord + "\n"})
		args := append([]string{"exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access"}, harnessDefaultModelArgs(t, "-m", h.codexDefault, h.codexSlugs)...)
		stdout, stderr := h.run(t, workspace, append(args, harnessToolTaskPrompt)...)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		step(t, "codex completed items: %v", codexCompletedItemTypes(objects))
		require.NotEmpty(t, codexCompletedTools(objects), "codex completed no command_execution or file_change item; stdout:\n%s", stdout)
		require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
	})
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		model := h.piNonAnthropicModel(t)
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessPiToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "-p", "--mode", "json", "--no-session", "--offline", "--model", model, harnessToolTaskPrompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.Zero(t, piErrorTurns(objects), "pi ended a turn with an API error; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.NotEmpty(t, harnessEventsOf(objects, "tool_execution_start"), "pi made no tool calls; stdout:\n%s", stdout)
		require.Zero(t, piToolErrors(objects), "a pi tool call failed; stdout:\n%s", stdout)
		require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
	})
	for _, variant := range harnessOpenCodeVariants {
		t.Run(variant.label, func(t *testing.T) {
			h := newOpenCodeHarnessTest(t, variant)
			model := h.openCodeToolModel(t)
			workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessOpenCodeToolWord + "\n"})
			stdout, stderr := h.run(t, workspace, "run", "--format", "json", "--auto", "-m", model, harnessToolTaskPrompt)
			harnessAssertNoAPIError(t, stdout+stderr)
			objects := harnessJSONObjects(stdout)
			require.Empty(t, harnessEventsOf(objects, "error"), "opencode emitted error events; stdout:\n%s\nstderr:\n%s", stdout, stderr)
			require.NotEmpty(t, openCodeCompletedTools(objects), "opencode completed no tool calls; stdout:\n%s", stdout)
			require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
		})
	}
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
		workspace := t.TempDir()
		prompt := "Use web search to find the version number of the most recent stable release of Python. Reply with only the version number."
		stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-s", "workspace-write", "-c", "web_search=live", prompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		searched := false
		harnessWalkObjects(objects, func(object map[string]any) {
			kind, _ := object["type"].(string)
			if strings.Contains(kind, "web_search") {
				searched = true
			}
		})
		require.True(t, searched, "codex emitted no web_search event; stdout:\n%s", stdout)
		require.Regexp(t, regexp.MustCompile(`\d+\.\d+(\.\d+)?`), codexLastAgentMessage(objects), "codex's reply is not a version number; stdout:\n%s", stdout)
	})
	t.Run("pi", func(t *testing.T) {
		newHarnessTest(t, "pi", "pi")
		t.Skip("pi has no web search tool (built-ins: read, bash, edit, write, grep, find, ls) and grid setup configures none; Baseten's server-side search runs only when the client sends a web_search tool, which pi never does")
	})
	for _, variant := range harnessOpenCodeVariants {
		t.Run(variant.label, func(t *testing.T) {
			newOpenCodeHarnessTest(t, variant)
			t.Skip("grid setup configures no web search for opencode: v1 ships no search tool by default, v2's search asks the user to pick a provider, which a non-interactive run cancels, and opencode's search uses its own hosted providers, not Baseten routes")
		})
	}
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
		require.Equal(t, harnessClaudeSwitchWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage1.txt"))
		step(t, "claude-code resuming session %s under %s", sessionID, modelB)
		stdout2, stderr2 := h.run(t, workspace, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--resume", sessionID, "--model", modelB, harnessSwitchPhase2)
		events2 := parseClaudeStream(stdout2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		_, _, ok = claudeResult(events2)
		require.True(t, ok, "phase 2 stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout2, stderr2)
		require.Equal(t, harnessClaudeRecallWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage2.txt"))
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		modelA, modelB := h.codexPair(t)
		workspace := t.TempDir()
		stdout1, stderr1 := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access", "-m", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessCodexSwitchWord, harnessCodexRecallWord))
		harnessAssertNoAPIError(t, stdout1+stderr1)
		threadID := codexThreadID(harnessJSONObjects(stdout1))
		require.NotEmpty(t, threadID, "codex emitted no thread.started thread_id; stdout:\n%s", stdout1)
		require.Equal(t, harnessCodexSwitchWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage1.txt"))
		step(t, "codex resuming thread %s under %s", threadID, modelB)
		stdout2, stderr2 := h.run(t, workspace, "exec", "resume", threadID, "--skip-git-repo-check", "-m", modelB, "-c", "sandbox_mode=danger-full-access", harnessSwitchPhase2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		require.Equal(t, harnessCodexRecallWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage2.txt"))
	})
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		modelA, modelB := h.piPair(t)
		workspace := t.TempDir()
		sessionDir := filepath.Join(h.root, "pi-sessions")
		stdout1, stderr1 := h.run(t, workspace, "-p", "--mode", "json", "--offline", "--session-dir", sessionDir, "--model", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessPiSwitchWord, harnessPiRecallWord))
		harnessAssertNoAPIError(t, stdout1+stderr1)
		require.Equal(t, harnessPiSwitchWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage1.txt"))
		step(t, "pi continuing session dir %s under %s", sessionDir, modelB)
		stdout2, stderr2 := h.run(t, workspace, "-p", "--mode", "json", "--offline", "--session-dir", sessionDir, "--continue", "--model", modelB, harnessSwitchPhase2)
		harnessAssertNoAPIError(t, stdout2+stderr2)
		require.Zero(t, piErrorTurns(harnessJSONObjects(stdout2)), "pi phase 2 hit an API error; stdout:\n%s", stdout2)
		require.Equal(t, harnessPiRecallWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage2.txt"))
	})
	for _, variant := range harnessOpenCodeVariants {
		t.Run(variant.label, func(t *testing.T) {
			h := newOpenCodeHarnessTest(t, variant)
			modelA, modelB := h.openCodePair(t)
			workspace := t.TempDir()
			stdout1, stderr1 := h.run(t, workspace, "run", "--format", "json", "--auto", "-m", modelA, fmt.Sprintf(harnessSwitchPhase1, harnessOpenCodeSwitchWord, harnessOpenCodeRecallWord))
			harnessAssertNoAPIError(t, stdout1+stderr1)
			sessionID := openCodeSessionID(harnessJSONObjects(stdout1))
			require.NotEmpty(t, sessionID, "opencode phase 1 emitted no sessionID; stdout:\n%s\nstderr:\n%s", stdout1, stderr1)
			require.Equal(t, harnessOpenCodeSwitchWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage1.txt"))
			step(t, "opencode continuing session %s under %s", sessionID, modelB)
			stdout2, stderr2 := h.run(t, workspace, "run", "--format", "json", "--auto", "-s", sessionID, "-m", modelB, harnessSwitchPhase2)
			harnessAssertNoAPIError(t, stdout2+stderr2)
			require.Equal(t, harnessOpenCodeRecallWord, harnessReadWorkspaceFileTrimmed(t, workspace, "stage2.txt"))
		})
	}
}
