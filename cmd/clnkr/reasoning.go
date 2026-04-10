package main

import "charm.land/bubbles/v2/viewport"

// reasoningModel is a modal overlay that shows model reasoning content.
type reasoningModel struct {
	viewport viewport.Model
	visible  bool
	content  string
	styles   *styles
}

func newReasoningModel(s *styles) reasoningModel {
	vp := viewport.New()
	vp.SoftWrap = true
	return reasoningModel{
		viewport: vp,
		styles:   s,
	}
}

func (r *reasoningModel) show(content string, width, height int) {
	r.visible = true
	r.content = content
	r.viewport.SetWidth(width)
	r.viewport.SetHeight(height)
	r.viewport.SetContent(r.content)
	r.viewport.GotoTop()
}

func (r *reasoningModel) hide() {
	r.visible = false
}

func (r *reasoningModel) resize(width, height int) {
	r.viewport.SetWidth(width)
	r.viewport.SetHeight(height)
	if r.visible {
		r.viewport.SetContent(r.content)
	}
}

func (r *reasoningModel) view() string {
	if !r.visible {
		return ""
	}
	return r.viewport.View()
}
