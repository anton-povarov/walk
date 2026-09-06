package main

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T, files map[string]string) *model {
	t.Helper()
	initStyles()

	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := &model{
		path:            dir,
		termWidth:       80,
		termHeight:      6,
		positions:       make(map[string]position),
		previewViewport: newPreviewViewport(),
	}
	m.resizePreviewViewport()
	m.list()
	return m
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func updateKey(t *testing.T, m *model, msg tea.KeyMsg) tea.Cmd {
	t.Helper()
	updated, cmd := m.Update(msg)
	if updated != m {
		t.Fatal("Update unexpectedly replaced the model")
	}
	return cmd
}
