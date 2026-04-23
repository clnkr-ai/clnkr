package main

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
)

type helpModel struct {
	viewport viewport.Model
	visible  bool
	content  string
}

func newHelpModel() helpModel {
	vp := viewport.New()
	vp.SoftWrap = true
	content := helpContent()
	vp.SetContent(content)
	return helpModel{viewport: vp, content: content}
}

func (h *helpModel) show(width, height int) {
	h.visible = true
	h.resize(width, height)
	h.viewport.GotoTop()
}

func (h *helpModel) hide() {
	h.visible = false
}

func (h *helpModel) resize(width, height int) {
	if height < 1 {
		height = 1
	}
	h.viewport.SetWidth(width)
	h.viewport.SetHeight(height)
	h.viewport.SetContent(h.content)
}

func (h helpModel) view() string {
	if !h.visible {
		return ""
	}
	return h.viewport.View()
}

func helpContent() string {
	lines := []string{
		"clnkr help",
		"",
		"Workflow",
		"  /compact [instructions]  summarize older transcript context",
		"  /delegate <task>         run a bounded child task",
		"  Ctrl+Y                  reasoning trace; approve during APPROVAL",
		"  y Enter                 approve during APPROVAL",
		"  guidance text           revise command during APPROVAL",
		"",
		"Modes",
		"  INPUT: type tasks, workflow commands, approvals, or clarifications",
		"  SCROLL: read transcript without moving the input cursor",
		"  RUNNING: agent or workflow command is active",
		"  APPROVAL: approve command or type guidance",
		"  CLARIFY: answer the agent's question",
		"  HELP: read key and workflow help",
		"  SEARCH: find text in the committed transcript",
		"  DIFF: review changed-file diff",
		"  REASONING: read latest reasoning trace",
		"",
		"Global",
		"  ?        help when input is empty or scroll mode is active",
		"  Esc      enter scroll mode, or close overlays",
		"  i        return to input mode from scroll mode",
		"  Ctrl+C   cancel running work; quit when idle; double-press to force quit",
		"",
		"Input",
		"  Enter    submit",
		"  Ctrl+J   insert newline",
		"  Up/Down  browse history at input edges",
		"",
		"Scroll",
		"  j/k              line down/up",
		"  gg/G             transcript top/bottom",
		"  g/G              help overlay top/bottom",
		"  Ctrl+D/Ctrl+U    half page down/up",
		"  PgDn/PgUp        page down/up",
		"  End/Home         bottom/top",
		"  Ctrl+F           transcript search",
		"  d                changed-file diff",
		"",
		"Overlays",
		"  Enter    next search result in SEARCH",
		"  Shift+Enter previous search result in SEARCH",
		"  Ctrl+Y   show latest reasoning trace; approve during APPROVAL",
		"  Esc      close overlay",
	}
	return strings.Join(lines, "\n")
}
