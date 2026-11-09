//go:build e2e

package e2etests

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

const harnessToolTaskPrompt = "Read the file task.txt in the current working directory. Create answer.txt in the current working directory containing exactly the contents of task.txt and nothing else."

const harnessThinkingQuestion = "how many positive integers below 1000 are divisible by 7 or by 11, but not by both?"

const harnessThinkingPrompt = "Think it through carefully without using any tools: " + harnessThinkingQuestion + " End your final reply with exactly this line: " + harnessThinkingMarker

const harnessSwitchPhase1 = "Do all of the following. Think it through carefully: " + harnessThinkingQuestion + "%s Read the file seed.txt in the current working directory, then create a file named stage1.txt in the current working directory containing exactly the contents of seed.txt and nothing else. Remember the word %s; I will ask about it later."

const harnessSwitchPhase2 = "Do all of the following. Think it through carefully: how many positive integers below 2000 are divisible by 9 or by 13, but not by both?%s Recall the word I asked you to remember. Read the file stage1.txt in the current working directory, then create a file named stage2.txt in the current working directory containing exactly two lines and nothing else: that word on line 1 and the first line of stage1.txt on line 2."

const harnessSwitchSearch1 = " Use web search to find the version number of the latest stable release of Python."

const harnessSwitchSearch2 = " Use web search to find the latest stable major version of Node.js."

var harnessDirections = []struct{ from, to string }{
	{harnessBaseten, harnessAnthropic},
	{harnessBaseten, harnessOpenAI},
	{harnessAnthropic, harnessBaseten},
	{harnessOpenAI, harnessBaseten},
}

func TestE2EHarnessThinking(t *testing.T) {
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		model := h.pickModel(t, harnessBaseten, h.thinkingNeed())
		prompt := "Reason through this step by step, showing your reasoning as you go, and compute 17 * 23. End your reply with exactly this line: " + harnessThinkingMarker
		args := append([]string{"-p", "--mode", "json", "--no-session", "--offline"}, h.thinkingArgs(model)...)
		stdout, stderr := h.run(t, t.TempDir(), append(args, prompt)...)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.Zero(t, piErrorTurns(objects), "pi ended a turn with an API error; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Contains(t, piAssistantText(objects), harnessThinkingMarker, "pi's final reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		piAssertThinking(t, model, objects, stdout)
	})
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		model := h.pickModel(t, harnessBaseten, h.thinkingNeed())
		args := append([]string{"-p", "--output-format", "stream-json", "--verbose"}, h.thinkingArgs(model)...)
		stdout, stderr := h.run(t, t.TempDir(), append(args, harnessThinkingPrompt)...)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		_, result, ok := claudeResult(events)
		require.True(t, ok, "stream-json has no result event; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		require.Contains(t, result, harnessThinkingMarker, "claude-code's final reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		claudeAssertThinking(t, model, events, stdout)
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		model := h.pickModel(t, harnessBaseten, h.thinkingNeed())
		workspace := t.TempDir()
		args := append([]string{"exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access"}, h.thinkingArgs(model)...)
		stdout, stderr := h.run(t, workspace, append(args, harnessThinkingPrompt)...)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.Contains(t, codexLastAgentMessage(objects), harnessThinkingMarker, "codex's final agent_message lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
		codexAssertThinking(t, model, objects, stdout)
	})
	for _, variant := range harnessOpenCodeVariants {
		t.Run(variant.label, func(t *testing.T) {
			h := newOpenCodeHarnessTest(t, variant)
			model := h.pickModel(t, harnessBaseten, h.thinkingNeed())
			args := append([]string{"run", "--format", "json"}, h.thinkingArgs(model)...)
			stdout, stderr := h.run(t, t.TempDir(), append(args, harnessThinkingPrompt)...)
			harnessAssertNoAPIError(t, stdout+stderr)
			objects := harnessJSONObjects(stdout)
			require.Empty(t, harnessEventsOf(objects, "error"), "opencode emitted error events; stdout:\n%s\nstderr:\n%s", stdout, stderr)
			require.Contains(t, openCodeEventText(objects, "text"), harnessThinkingMarker, "opencode's reply lacks the marker; stdout:\n%s\nstderr:\n%s", stdout, stderr)
			openCodeAssertThinking(t, model, objects, stdout)
		})
	}
}

func TestE2EHarnessToolCall(t *testing.T) {
	t.Run("claude-code", func(t *testing.T) {
		h := newHarnessTest(t, "claude-code", "claude")
		model := h.pickModel(t, harnessBaseten, harnessAnyModel)
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessClaudeToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits", "--model", model.id, harnessToolTaskPrompt)
		events := parseClaudeStream(stdout)
		harnessAssertNoAPIError(t, stdout+stderr)
		require.NotEmpty(t, claudeToolUses(events), "claude-code made no tool calls; stdout:\n%s", stdout)
		require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
	})
	t.Run("codex", func(t *testing.T) {
		h := newHarnessTest(t, "codex", "codex")
		model := h.pickModel(t, harnessBaseten, harnessAnyModel)
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessCodexToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-c", "sandbox_mode=danger-full-access", "-m", model.id, harnessToolTaskPrompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		step(t, "codex completed items: %v", codexCompletedItemTypes(objects))
		require.NotEmpty(t, codexCompletedTools(objects), "codex completed no command_execution or file_change item; stdout:\n%s", stdout)
		require.Equal(t, harnessReadWorkspaceFileTrimmed(t, workspace, "task.txt"), harnessReadWorkspaceFileTrimmed(t, workspace, "answer.txt"))
	})
	t.Run("pi", func(t *testing.T) {
		h := newHarnessTest(t, "pi", "pi")
		model := h.pickModel(t, harnessBaseten, harnessAnyModel)
		workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessPiToolWord + "\n"})
		stdout, stderr := h.run(t, workspace, "-p", "--mode", "json", "--no-session", "--offline", "--model", model.id, harnessToolTaskPrompt)
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
			model := h.pickModel(t, harnessBaseten, harnessAnyModel)
			workspace := harnessSeedWorkspace(t, map[string]string{"task.txt": harnessOpenCodeToolWord + "\n"})
			stdout, stderr := h.run(t, workspace, "run", "--format", "json", "--auto", "-m", model.id, harnessToolTaskPrompt)
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
		model := h.pickModel(t, harnessBaseten, harnessAnyModel)
		prompt := "Use the WebSearch tool to find the version number of the most recent stable release of Python. Reply with only the version number."
		stdout, stderr := h.run(t, t.TempDir(), "-p", prompt, "--output-format", "stream-json", "--verbose", "--allowedTools", "WebSearch", "--model", model.id)
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
		model := h.pickModel(t, harnessBaseten, harnessAnyModel)
		workspace := t.TempDir()
		prompt := "Use web search to find the version number of the most recent stable release of Python. Reply with only the version number."
		stdout, stderr := h.run(t, workspace, "exec", "--json", "--skip-git-repo-check", "-C", workspace, "-s", "workspace-write", "-c", "web_search=live", "-m", model.id, prompt)
		harnessAssertNoAPIError(t, stdout+stderr)
		objects := harnessJSONObjects(stdout)
		require.True(t, codexSearched(objects), "codex emitted no web_search event; stdout:\n%s", stdout)
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
	type switchCase struct {
		label        string
		start        func(t *testing.T) *harnessTest
		seed, recall string
	}
	cases := []switchCase{
		{"claude-code", func(t *testing.T) *harnessTest { return newHarnessTest(t, "claude-code", "claude") }, harnessClaudeSwitchWord, harnessClaudeRecallWord},
		{"codex", func(t *testing.T) *harnessTest { return newHarnessTest(t, "codex", "codex") }, harnessCodexSwitchWord, harnessCodexRecallWord},
		{"pi", func(t *testing.T) *harnessTest { return newHarnessTest(t, "pi", "pi") }, harnessPiSwitchWord, harnessPiRecallWord},
	}
	for _, variant := range harnessOpenCodeVariants {
		if !variant.v2Config {
			cases = append(cases, switchCase{variant.label, func(t *testing.T) *harnessTest { return newOpenCodeHarnessTest(t, variant) }, harnessOpenCodeSwitchWord, harnessOpenCodeRecallWord})
		}
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			h := c.start(t)
			search1, search2 := "", ""
			if h.searches() {
				search1, search2 = harnessSwitchSearch1, harnessSwitchSearch2
			}
			for _, direction := range harnessDirections {
				t.Run(direction.from+"-to-"+direction.to, func(t *testing.T) {
					modelA, modelB := h.pickModel(t, direction.from, h.thinkingNeed()), h.pickModel(t, direction.to, h.thinkingNeed())
					workspace := harnessSeedWorkspace(t, map[string]string{"seed.txt": c.seed + "\n"})
					session := h.switchTurn(t, workspace, modelA, "", fmt.Sprintf(harnessSwitchPhase1, search1, c.recall))
					require.Equal(t, c.seed, harnessReadWorkspaceFileTrimmed(t, workspace, "stage1.txt"))
					step(t, "%s resuming %s under %s", h.name, session, modelB.id)
					h.switchTurn(t, workspace, modelB, session, fmt.Sprintf(harnessSwitchPhase2, search2))
					require.Equal(t, c.recall+"\n"+c.seed, harnessReadWorkspaceFileTrimmed(t, workspace, "stage2.txt"))
				})
			}
		})
	}
}
