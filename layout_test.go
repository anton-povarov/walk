package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPreviewLeavesOneCellRightMargin(t *testing.T) {
	oldBorder := withBorder
	t.Cleanup(func() { withBorder = oldBorder })

	for _, tc := range []struct {
		name      string
		bordered  bool
		termWidth int
	}{
		{name: "plain-even", termWidth: 80},
		{name: "plain-odd", termWidth: 81},
		{name: "bordered-even", bordered: true, termWidth: 80},
		{name: "bordered-odd", bordered: true, termWidth: 81},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBorder = tc.bordered
			m := newTestModel(t, map[string]string{"file.txt": "short preview"})
			m.termWidth = tc.termWidth
			m.resizePreviewViewport()
			m.previewMode = true

			for row, line := range strings.Split(m.View(), "\n") {
				want := tc.termWidth - 1
				if got := ansi.StringWidth(line); got != want {
					t.Fatalf("row %d spans %d cells, want %d: %q", row, got, want, line)
				}
			}
		})
	}
}

func TestStatusBarFillsLHSWidth(t *testing.T) {
	m := newTestModel(t, map[string]string{"file.txt": "preview"})
	m.previewMode = true
	m.statusBar = compile(`"status"`)

	minLeftWidth := minimumPreviewLeftWidth(m.termWidth)
	leftWidth := max(max(strlen(filepath.Base(m.path)), strlen("file.txt ")), minLeftWidth)
	want := renderFullWidth(bar, "status", leftWidth)
	found := false
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.HasPrefix(line, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("status bar did not fill the %d-cell LHS: %q", leftWidth, m.View())
	}
}

func TestPreviewUsesDynamicLHSWidth(t *testing.T) {
	oldBorder := withBorder
	t.Cleanup(func() { withBorder = oldBorder })

	for _, bordered := range []bool{false, true} {
		t.Run(fmt.Sprintf("border-%v", bordered), func(t *testing.T) {
			withBorder = bordered
			m := newTestModel(t, map[string]string{"file.txt": "preview"})
			m.previewMode = true

			m.View()
			minLeftWidth := minimumPreviewLeftWidth(m.termWidth)
			leftWidth := max(max(strlen(filepath.Base(m.path)), strlen("file.txt ")), minLeftWidth)
			if leftWidth >= m.termWidth/2 {
				t.Fatalf("test setup did not produce a dynamic LHS width: %d", leftWidth)
			}
			wantRHSWidth := m.termWidth - leftWidth - m.previewStyle().GetHorizontalFrameSize() - 1
			if m.previewViewport.Width != wantRHSWidth {
				t.Fatalf("RHS width is %d, want %d after a %d-cell LHS", m.previewViewport.Width, wantRHSWidth, leftWidth)
			}
			actualLeftWidth := m.termWidth - m.previewViewport.Width - m.previewStyle().GetHorizontalFrameSize() - 1
			if actualLeftWidth < minLeftWidth {
				t.Fatalf("LHS width is %d, want at least %d", actualLeftWidth, minLeftWidth)
			}
		})
	}
}

func TestMinimumPreviewLeftWidthIsThirtyFivePercent(t *testing.T) {
	for _, tc := range []struct {
		termWidth int
		want      int
	}{
		{termWidth: 80, want: 28},
		{termWidth: 81, want: 28},
		{termWidth: 82, want: 29},
	} {
		if got := minimumPreviewLeftWidth(tc.termWidth); got != tc.want {
			t.Fatalf("minimum width at %d columns is %d, want %d", tc.termWidth, got, tc.want)
		}
	}
}
