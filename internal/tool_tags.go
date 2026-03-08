package internal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anhhung04/tmuxai/system"
)

// ToolResult holds the outcome of a single tool-tag operation.
type ToolResult struct {
	Tag   string // "ReadFile", "ExecAndRead", "HttpRequest", "WriteFile"
	Input string // path, command, or URL
	Out   string // captured content; empty on error
	Err   string // non-empty on failure
}

// executeToolTags processes all gather tags (ReadFile, ExecAndRead, HttpRequest) and
// action tags (WriteFile) from an AIResponse.
// Returns collected results and an abort flag (true when the user cancels a confirmation).
func (m *Manager) executeToolTags(ctx context.Context, r AIResponse) ([]ToolResult, bool) {
	var results []ToolResult

	// ReadFile — always safe, no confirmation needed.
	for _, path := range r.ReadFile {
		res := ToolResult{Tag: "ReadFile", Input: path}
		data, err := os.ReadFile(path)
		if err != nil {
			res.Err = err.Error()
		} else {
			res.Out = string(data)
		}
		results = append(results, res)
	}

	// ExecAndRead — runs in TmuxAI process (not in the pane), captures stdout.
	for _, cmd := range r.ExecAndRead {
		highlighted, _ := system.HighlightCode("sh", cmd)
		m.Println(highlighted)
		if m.GetExecConfirm() {
			confirmed, _ := m.confirmedToExec(cmd, "Run this command to gather context?", false)
			if !confirmed {
				m.Status = ""
				return nil, true
			}
		}
		res := ToolResult{Tag: "ExecAndRead", Input: cmd}
		out, err := shellCapture(cmd)
		if err != nil {
			res.Err = err.Error()
			res.Out = out
		} else {
			res.Out = out
		}
		results = append(results, res)
	}

	// HttpRequest — network call, requires confirmation.
	for _, url := range r.HttpRequest {
		m.Println(fmt.Sprintf("HTTP GET: %s", url))
		confirmed, _ := m.confirmedToExec(url, "Perform this HTTP GET request?", false)
		if !confirmed {
			m.Status = ""
			return nil, true
		}
		res := ToolResult{Tag: "HttpRequest", Input: url}
		body, err := httpGet(url)
		if err != nil {
			res.Err = err.Error()
		} else {
			res.Out = body
		}
		results = append(results, res)
	}

	// WriteFile — destructive, requires confirmation.
	for _, wf := range r.WriteFile {
		m.Println(fmt.Sprintf("Write file: %s", wf.Path))
		confirmed, _ := m.confirmedToExec(
			fmt.Sprintf("path: %s\n---\n%s", wf.Path, wf.Content),
			"Write this file?",
			false,
		)
		if !confirmed {
			m.Status = ""
			return nil, true
		}
		res := ToolResult{Tag: "WriteFile", Input: wf.Path}
		if err := os.WriteFile(wf.Path, []byte(wf.Content), 0644); err != nil {
			res.Err = err.Error()
		} else {
			res.Out = "written successfully"
		}
		results = append(results, res)
	}

	return results, false
}

// buildInjectionMessage formats ToolResults into a structured string that is
// re-injected as the next user message so the AI can reason about the results.
func buildInjectionMessage(results []ToolResult) string {
	var sb strings.Builder
	sb.WriteString("Here are the results of the tool calls you requested:\n\n")
	for _, res := range results {
		sb.WriteString(fmt.Sprintf("<ToolResult tag=%q input=%q>\n", res.Tag, res.Input))
		if res.Err != "" {
			sb.WriteString(fmt.Sprintf("ERROR: %s\n", res.Err))
			if res.Out != "" {
				sb.WriteString(res.Out)
				sb.WriteString("\n")
			}
		} else {
			sb.WriteString(res.Out)
			sb.WriteString("\n")
		}
		sb.WriteString("</ToolResult>\n\n")
	}
	return strings.TrimSpace(sb.String())
}

// shellCapture runs cmd via sh -c and returns trimmed combined stdout.
func shellCapture(cmd string) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	c.Env = os.Environ()
	out, err := c.Output()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		return trimmed, fmt.Errorf("command failed: %w", err)
	}
	return trimmed, nil
}

// httpGet performs an HTTP GET with a 30 s timeout and a 1 MB body cap.
func httpGet(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	const maxBytes = 1 << 20 // 1 MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", err
	}
	result := string(body)
	if len(body) > maxBytes {
		result = result[:maxBytes] + "\n[response truncated at 1 MB]"
	}
	return result, nil
}
