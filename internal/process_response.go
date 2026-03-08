package internal

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// tagInfo holds a parsed tag's metadata and precompiled regexes.
type tagInfo struct {
	name        string
	isBool      bool
	setField    func(*AIResponse, string)
	reMain      *regexp.Regexp // (?s)<Name>(.*?)</Name>
	reCodeBlock *regexp.Regexp // ```(?:xml)?\s*<Name>...</Name>\s*```
	reBacktick  *regexp.Regexp // `<Name>...</Name>`
	reLeftover  *regexp.Regexp // leftover bare tag lines
	reBoolSpec  *regexp.Regexp // self-closing / empty forms (bool tags only)
}

// parsedTags is initialised once at startup; all regexes are precompiled.
var parsedTags = func() []tagInfo {
	type rawTag struct {
		name     string
		isBool   bool
		setField func(*AIResponse, string)
	}
	raw := []rawTag{
		{"TmuxSendKeys", false, func(r *AIResponse, v string) { r.SendKeys = append(r.SendKeys, v) }},
		{"ExecCommand", false, func(r *AIResponse, v string) { r.ExecCommand = append(r.ExecCommand, v) }},
		{"PasteMultilineContent", false, func(r *AIResponse, v string) { r.PasteMultilineContent = v }},
		{"RequestAccomplished", true, func(r *AIResponse, v string) { r.RequestAccomplished = isTrue(v) }},
		{"ExecPaneSeemsBusy", true, func(r *AIResponse, v string) { r.ExecPaneSeemsBusy = isTrue(v) }},
		{"WaitingForUserResponse", true, func(r *AIResponse, v string) { r.WaitingForUserResponse = isTrue(v) }},
		{"NoComment", true, func(r *AIResponse, v string) { r.NoComment = isTrue(v) }},
		// Tool tags
		{"ReadFile", false, func(r *AIResponse, v string) { r.ReadFile = append(r.ReadFile, v) }},
		{"ExecAndRead", false, func(r *AIResponse, v string) { r.ExecAndRead = append(r.ExecAndRead, v) }},
		{"HttpRequest", false, func(r *AIResponse, v string) { r.HttpRequest = append(r.HttpRequest, v) }},
	}

	result := make([]tagInfo, len(raw))
	for i, t := range raw {
		result[i] = tagInfo{
			name:        t.name,
			isBool:      t.isBool,
			setField:    t.setField,
			reMain:      regexp.MustCompile(fmt.Sprintf(`(?s)<%s>(.*?)</%s>`, t.name, t.name)),
			reCodeBlock: regexp.MustCompile(fmt.Sprintf("(?s)```(?:xml)?\\s*<%s>.*?</%s>\\s*```", t.name, t.name)),
			reBacktick:  regexp.MustCompile(fmt.Sprintf("`<%s>.*?</%s>`", t.name, t.name)),
			reLeftover:  regexp.MustCompile(fmt.Sprintf("(?m)^\\s*(<%s>\\s*|```<%s>```)?\\s*$", t.name, t.name)),
		}
		if t.isBool {
			result[i].reBoolSpec = regexp.MustCompile(fmt.Sprintf(
				"(?s)(<%s>\\s*</%s>|<%s>\\s*|```<%s>```|<%s/>)",
				t.name, t.name, t.name, t.name, t.name,
			))
		}
	}
	return result
}()

// Precompiled WriteFile patterns.
var (
	writeFileRe        = regexp.MustCompile(`(?s)<WriteFile\s+path=["']([^"']+)["']>(.*?)</WriteFile>`)
	writeFileCodeBlock = regexp.MustCompile("(?s)```(?:xml)?\\s*<WriteFile[^>]*>.*?</WriteFile>\\s*```")
	writeFilePlain     = regexp.MustCompile(`(?s)<WriteFile[^>]*>.*?</WriteFile>`)
	collapseBlankRe    = regexp.MustCompile(`\n{3,}`)
)

func (m *Manager) parseAIResponse(response string) (AIResponse, error) {
	clean := response
	r := AIResponse{}
	cleanForMsg := clean

	for _, t := range parsedTags {
		for _, match := range t.reMain.FindAllStringSubmatch(clean, -1) {
			if len(match) < 2 {
				continue
			}
			val := strings.TrimSpace(match[1])
			if !t.isBool {
				val = html.UnescapeString(val)
			}
			t.setField(&r, val)
		}
		// Strip tag blocks from the display message.
		cleanForMsg = t.reCodeBlock.ReplaceAllString(cleanForMsg, "")
		cleanForMsg = t.reBacktick.ReplaceAllString(cleanForMsg, "")
		cleanForMsg = t.reMain.ReplaceAllString(cleanForMsg, "")
	}

	// WriteFile has an attribute: <WriteFile path="..."> or <WriteFile path='...'>
	for _, match := range writeFileRe.FindAllStringSubmatch(clean, -1) {
		if len(match) < 3 {
			continue
		}
		r.WriteFile = append(r.WriteFile, WriteFileRequest{
			Path:    strings.TrimSpace(match[1]),
			Content: html.UnescapeString(match[2]),
		})
	}
	cleanForMsg = writeFileCodeBlock.ReplaceAllString(cleanForMsg, "")
	cleanForMsg = writeFilePlain.ReplaceAllString(cleanForMsg, "")

	// Special handling: bool tags may appear as <TagName> or ```<TagName>``` (no value).
	for _, t := range parsedTags {
		if !t.isBool {
			continue
		}
		if t.reBoolSpec.MatchString(clean) {
			t.setField(&r, "1")
		}
	}

	// Build display message: trim, collapse excess blank lines, strip leftover tag lines.
	msg := strings.TrimSpace(cleanForMsg)
	msg = collapseBlankRe.ReplaceAllString(msg, "\n\n")
	for _, t := range parsedTags {
		msg = t.reLeftover.ReplaceAllString(msg, "")
	}
	msg = strings.TrimSpace(msg)
	r.Message = msg

	return r, nil
}

// isTrue reports whether s represents a truthy value ("1" or "true").
func isTrue(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true"
}

// collapseBlankLines collapses runs of 3+ newlines to a single blank line.
func collapseBlankLines(s string) string {
	return collapseBlankRe.ReplaceAllString(s, "\n\n")
}
