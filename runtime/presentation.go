//go:build linux

package runtime

import "github.com/idolum-ai/aphelion/face"

var runtimeCompactPanelOptions = face.OperatorPanelCompactOptions{
	DetailLimit:   4,
	EvidenceLimit: 2,
}

func renderRuntimeCompactPanel(panel face.OperatorPanel) string {
	return face.RenderCompactOperatorPanel(panel, runtimeCompactPanelOptions)
}
