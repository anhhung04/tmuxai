package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/anhhung04/tmuxai/logger"
	"github.com/anhhung04/tmuxai/system"
	"github.com/briandowns/spinner"
)

type contextKey int

const toolDepthKey contextKey = iota

const maxToolRounds = 10

// Main function to process regular user messages
// Returns true if the request was accomplished and no further processing should happen
func (m *Manager) ProcessUserMessage(ctx context.Context, message string) bool {
	// Check if context management is needed before sending
	if m.needSquash() {
		m.Println("Exceeded context size, squashing history...")
		m.squashHistory()
	}

	s := spinner.New(spinner.CharSets[26], 100*time.Millisecond)
	s.Suffix = " Thinking..."
	s.Start()

	// check for status change before processing
	if m.Status == "" {
		s.Stop()
		return false
	}

	currentTmuxWindow := m.getTmuxPanesInXml(m.Config)
	execPaneEnv := ""
	if !m.ExecPane.IsSubShell {
		execPaneEnv = fmt.Sprintf("Keep in mind, you are working within the shell: %s and OS: %s", m.ExecPane.Shell, m.ExecPane.OS)
	}
	currentMessage := ChatMessage{
		Content:   currentTmuxWindow + "\n\n" + execPaneEnv + "\n\n" + message,
		FromUser:  true,
		Timestamp: time.Now(),
	}

	// build current chat history
	var history []ChatMessage
	switch {
	case m.WatchMode:
		history = []ChatMessage{m.watchPrompt()}
	case m.ExecPane.IsPrepared:
		history = []ChatMessage{m.chatAssistantPrompt(true)}
	default:
		history = []ChatMessage{m.chatAssistantPrompt(false)}
	}

	// Inject loaded knowledge bases after system prompt
	for kbName, kbContent := range m.LoadedKBs {
		history = append(history, ChatMessage{
			Content:   fmt.Sprintf("=== Knowledge Base: %s ===\n%s", kbName, kbContent),
			FromUser:  false,
			Timestamp: time.Now(),
		})
	}

	history = append(history, m.Messages...)

	sending := append(history, currentMessage)

	// Check if AI configuration is available before making the API call
	if !m.hasValidAIConfiguration() {
		s.Stop()
		m.Status = ""
		m.PrintError("No AI configuration found.")
		m.Println("Configure your AI settings in ~/.config/tmuxai/config.yaml:")
		m.Println("")
		m.Println("  default_model: 'my-model'")
		m.Println("  models:")
		m.Println("    my-model:")
		m.Println("      provider: 'openrouter'")
		m.Println("      model: 'google/gemini-2.5-flash-preview'")
		m.Println("      api_key: 'sk-or-your-api-key'")
		m.Println("")
		m.Println("Run '/model' to check available configurations.")
		return false
	}

	response, err := m.AiClient.GetResponseFromChatMessages(ctx, sending, m.GetModel())
	if err != nil {
		s.Stop()
		m.Status = ""

		if ctx.Err() == context.Canceled {
			return false
		}

		m.PrintError("Failed to get response from AI: " + err.Error())

		if m.Config.Debug {
			debugChatMessages(append(history, currentMessage), "ERROR: "+err.Error())
		}

		return false
	}

	// check for status change again
	if m.Status == "" {
		s.Stop()
		return false
	}

	r, err := m.parseAIResponse(response)
	if err != nil {
		s.Stop()
		m.Status = ""

		m.PrintError("Failed to parse AI response: " + err.Error())

		if m.Config.Debug {
			debugChatMessages(append(history, currentMessage), "PARSE ERROR: "+response)
		}

		return false
	}

	if m.Config.Debug {
		debugChatMessages(append(history, currentMessage), response)
	}

	logger.Debug("AIResponse: %s", r.String())

	s.Stop()

	responseMsg := ChatMessage{
		Content:   response,
		FromUser:  false,
		Timestamp: time.Now(),
	}

	// did AI follow our guidelines?
	guidelineError, validResponse := m.aiFollowedGuidelines(r)
	if !validResponse {
		m.Println("AI didn't follow guidelines, trying again...")
		m.Messages = append(m.Messages, currentMessage, responseMsg)
		return m.ProcessUserMessage(ctx, guidelineError)

	}

	// colorize code blocks in the response
	if r.Message != "" {
		fmt.Println(system.Cosmetics(r.Message))
	}

	// Don't append to history if AI is waiting for the pane or is watch mode no comment
	if r.ExecPaneSeemsBusy || r.NoComment {
	} else {
		m.Messages = append(m.Messages, currentMessage, responseMsg)
	}

	// Tool-tag gather phase: ReadFile, ExecAndRead, HttpRequest, WriteFile.
	// Results are injected back as the next user message so the AI can act on them.
	hasToolTags := len(r.ReadFile) > 0 || len(r.ExecAndRead) > 0 ||
		len(r.HttpRequest) > 0 || len(r.WriteFile) > 0
	if hasToolTags {
		depth, _ := ctx.Value(toolDepthKey).(int)
		if depth >= maxToolRounds {
			m.PrintError(fmt.Sprintf("Tool-tag loop exceeded %d rounds; stopping to avoid infinite recursion.", maxToolRounds))
			return false
		}
		results, aborted := m.executeToolTags(ctx, r)
		if aborted {
			return false
		}
		if len(results) > 0 {
			injectionMsg := buildInjectionMessage(results)
			return m.ProcessUserMessage(context.WithValue(ctx, toolDepthKey, depth+1), injectionMsg)
		}
	}

	// observe/prepared mode
	for i, execCommand := range r.ExecCommand {
		code, _ := system.HighlightCode("sh", execCommand)
		m.Println(code)

		isSafe := false
		command := execCommand
		if m.GetExecConfirm() {
			isSafe, command = m.confirmedToExec(execCommand, "Execute this command?", true)
		} else {
			isSafe = true
		}
		if isSafe {
			// Determine per-command timeout (seconds). 0 means no timeout.
			cmdTimeout := 0
			if i < len(r.ExecCommandTimeout) {
				cmdTimeout = r.ExecCommandTimeout[i]
			}
			if m.ExecPane.IsPrepared {
				if cmdTimeout > 0 {
					// Run ExecWaitCapture with timeout handling.
					resultCh := make(chan CommandExecHistory, 1)
					errCh := make(chan error, 1)
					go func() {
						hist, err := m.ExecWaitCapture(command)
						resultCh <- hist
						errCh <- err
					}()
					select {
					case <-time.After(time.Duration(cmdTimeout) * time.Second):
						// Timeout reached – ask user whether to continue waiting.
						if m.GetExecConfirm() {
							prompt := fmt.Sprintf("Command timed out after %d seconds. Continue waiting?", cmdTimeout)
							cont, _ := m.confirmedToExec(command, prompt, true)
							if cont {
								// Continue waiting without timeout.
								_, _ = m.ExecWaitCapture(command)
							} else {
								m.Status = ""
								return false
							}
						} else {
							m.Status = ""
							return false
						}
					case hist := <-resultCh:
						_ = hist // result ignored for now
					case err := <-errCh:
						if err != nil {
							m.PrintError(err.Error())
						}
					}
				} else {
					_, _ = m.ExecWaitCapture(command)
				}
			} else {
				_ = system.TmuxSendCommandToPane(m.ExecPane.Id, command, true)
				time.Sleep(1 * time.Second)
			}
		} else {
			m.Status = ""
			return false
			}
	}

	// Process SendKeys
	if len(r.SendKeys) > 0 {
		// Show preview of all keys
		keysPreview := "Keys to send:\n"
		for i, sendKey := range r.SendKeys {
			code, _ := system.HighlightCode("txt", sendKey)
			if i == len(r.SendKeys)-1 {
				keysPreview += code
			} else {
				keysPreview += code + "\n"
			}
			if m.Status == "" {
				return false
			}
		}

		m.Println(keysPreview)

		// Determine confirmation message based on number of keys
		confirmMessage := "Send this key?"
		if len(r.SendKeys) > 1 {
			confirmMessage = "Send all these keys?"
		}

		// Get confirmation if required
		var allConfirmed bool
		if m.GetSendKeysConfirm() {
			allConfirmed, _ = m.confirmedToExec("keys shown above", confirmMessage, true)
			if !allConfirmed {
				m.Status = ""
				return false
			}
		}

		// Send each key with delay
		for _, sendKey := range r.SendKeys {
			_ = system.TmuxSendCommandToPane(m.ExecPane.Id, sendKey, false)
			time.Sleep(1 * time.Second)
		}
	}

	if r.ExecPaneSeemsBusy {
		m.Countdown(m.GetWaitInterval())
		accomplished := m.ProcessUserMessage(ctx, "waited for 5 more seconds, here is the current pane(s) content")
		if accomplished {
			return true
		}
	}

	// observe or prepared mode
	if r.PasteMultilineContent != "" {
		code, _ := system.HighlightCode("txt", r.PasteMultilineContent)
		fmt.Println(code)

		isSafe := false
		if m.GetPasteMultilineConfirm() {
			isSafe, _ = m.confirmedToExec(r.PasteMultilineContent, "Paste multiline content?", false)
		} else {
			isSafe = true
		}

		if isSafe {
			m.Println("Pasting...")
			_ = system.TmuxSendCommandToPane(m.ExecPane.Id, r.PasteMultilineContent, true)
			time.Sleep(1 * time.Second)
		} else {
			m.Status = ""
			return false
		}
	}

	if r.RequestAccomplished {
		m.Status = ""
		return true
	}

	if r.WaitingForUserResponse {
		m.Status = "waiting"
		return false
	}

	// watch mode only
	if r.NoComment {
		return false
	}

	if !m.WatchMode {
		accomplished := m.ProcessUserMessage(ctx, "sending updated pane(s) content")
		if accomplished {
			return true
		}
	}
	return false
}

func (m *Manager) startWatchMode(desc string) {

	// check status
	if m.Status == "" {
		return
	}

	m.Countdown(m.GetWaitInterval())

	// Create a new background context since this is a separate process
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accomplished := m.ProcessUserMessage(ctx, desc)
	if accomplished {
		m.WatchMode = false
		m.Status = ""
	}

	// we continue running if status is still set
	if m.Status != "" && m.WatchMode {
		m.startWatchMode("")
	}
}

func (m *Manager) aiFollowedGuidelines(r AIResponse) (string, bool) {
	// At most one boolean flag set.
	boolCount := 0
	if r.RequestAccomplished {
		boolCount++
	}
	if r.ExecPaneSeemsBusy {
		boolCount++
	}
	if r.WaitingForUserResponse {
		boolCount++
	}
	if r.NoComment {
		boolCount++
	}
	if boolCount > 1 {
		return "You didn't follow the guidelines. Only one boolean flag should be set to true in your response. Pay attention!", false
	}

	// Tmux-action tags are mutually exclusive among themselves and with WriteFile.
	tmuxOnlyCount := 0
	if len(r.ExecCommand) > 0 {
		tmuxOnlyCount++
	}
	if len(r.SendKeys) > 0 {
		tmuxOnlyCount++
	}
	if r.PasteMultilineContent != "" {
		tmuxOnlyCount++
	}
	hasWriteFile := len(r.WriteFile) > 0
	tmuxActionCount := tmuxOnlyCount
	if hasWriteFile {
		tmuxActionCount++
	}
	if tmuxActionCount > 1 {
		return "You didn't follow the guidelines. You must only use one type of XML tag per response (ExecCommand, SendKeys, PasteMultilineContent, or WriteFile). Pay attention!", false
	}

	// Gather tags (ReadFile, ExecAndRead, HttpRequest) may mix freely with each other
	// and with WriteFile, but not with tmux-action tags.
	gatherCount := len(r.ReadFile) + len(r.ExecAndRead) + len(r.HttpRequest)
	if gatherCount > 0 && tmuxOnlyCount > 0 {
		return "You didn't follow the guidelines. Do not mix ReadFile/ExecAndRead/HttpRequest gather tags with ExecCommand/SendKeys/PasteMultilineContent in the same response. Pay attention!", false
	}

	// At least one tag must be present (gather tags count toward this).
	totalCount := tmuxActionCount + boolCount + gatherCount
	if !m.WatchMode && totalCount == 0 {
		return "You didn't follow the guidelines. You must use at least one XML tag in your response. Pay attention!", false
	}

	return "", true
}
