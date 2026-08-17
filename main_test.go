package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestTabEnablesPreviewAndSwitchesFocus(t *testing.T) {
	initStyles()
	m := newTestModel(t, map[string]string{"file.txt": "one\ntwo\nthree\nfour\nfive\nsix\n"})
	m.deleteCurrentFile = true

	cmd := updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.previewMode || !m.previewFocused {
		t.Fatalf("Tab should enable and focus preview: mode=%v focused=%v", m.previewMode, m.previewFocused)
	}
	if cmd == nil {
		t.Fatal("enabling preview should enter the alternate screen")
	}
	if m.deleteCurrentFile {
		t.Fatal("entering preview should cancel an armed deletion")
	}

	m.View()
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	offset := m.previewViewport.YOffset
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.previewFocused || !m.previewMode {
		t.Fatal("Tab from RHS should return to LHS without disabling preview")
	}
	if m.previewViewport.YOffset != offset {
		t.Fatal("switching panes should retain preview scroll position")
	}

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.previewFocused {
		t.Fatal("Tab from LHS should focus the existing preview")
	}
	updateKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.previewMode || !m.previewFocused {
		t.Fatal("Space should be ignored while RHS is focused")
	}

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	cmd = updateKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.previewMode || m.previewFocused {
		t.Fatal("Space on LHS should disable preview and reset focus")
	}
	if cmd == nil {
		t.Fatal("disabling preview should exit the alternate screen")
	}
}

func TestPreviewFocusedKeymap(t *testing.T) {
	oldHighlight := withHighlight
	withHighlight = false
	t.Cleanup(func() { withHighlight = oldHighlight })

	m := newTestModel(t, map[string]string{
		"file.txt": strings.Join([]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}, "\n"),
	})
	m.previewMode = true
	m.previewFocused = true
	m.View()
	startC, startR := m.c, m.r

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	updateKey(t, m, runeKey('j'))
	if m.previewViewport.YOffset != 2 {
		t.Fatalf("line keys should scroll two lines, got offset %d", m.previewViewport.YOffset)
	}
	if m.c != startC || m.r != startR {
		t.Fatal("preview scrolling changed the selected file")
	}

	updateKey(t, m, runeKey('f'))
	if m.previewViewport.YOffset != 7 {
		t.Fatalf("f should scroll one page, got offset %d", m.previewViewport.YOffset)
	}
	updateKey(t, m, runeKey('b'))
	if m.previewViewport.YOffset != 2 {
		t.Fatalf("b should scroll one page up, got offset %d", m.previewViewport.YOffset)
	}
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.previewViewport.YOffset != 2 {
		t.Fatalf("page keys should round-trip to the same offset, got %d", m.previewViewport.YOffset)
	}

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeySpace},
		runeKey('u'),
		runeKey('d'),
		runeKey('/'),
		{Type: tea.KeyEnter},
		{Type: tea.KeyLeft},
	} {
		updateKey(t, m, msg)
	}
	if m.previewViewport.YOffset != 2 || m.deleteCurrentFile || m.searchMode {
		t.Fatal("an unlisted RHS key changed application state")
	}

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyUp})
	updateKey(t, m, runeKey('k'))
	updateKey(t, m, runeKey('k'))
	if m.previewViewport.YOffset != 0 {
		t.Fatalf("scrolling above the top should clamp at zero, got %d", m.previewViewport.YOffset)
	}
}

func TestPreviewPathChangeResetsScrollAndResizeClampsIt(t *testing.T) {
	oldHighlight := withHighlight
	withHighlight = false
	t.Cleanup(func() { withHighlight = oldHighlight })

	content := strings.Repeat("line\n", 20)
	m := newTestModel(t, map[string]string{"a.txt": content, "b.txt": content})
	m.previewMode = true
	m.previewFocused = true
	m.View()
	for i := 0; i < 8; i++ {
		updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.previewViewport.YOffset != 8 {
		t.Fatalf("expected offset 8, got %d", m.previewViewport.YOffset)
	}

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m.View()
	if m.previewViewport.YOffset != 0 || !strings.HasSuffix(m.previewPath, "b.txt") {
		t.Fatalf("selecting another file should reset preview: path=%q offset=%d", m.previewPath, m.previewViewport.YOffset)
	}

	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	for i := 0; i < 12; i++ {
		updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	before := m.previewViewport.YOffset
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.previewViewport.YOffset != before {
		t.Fatal("Tab switches should preserve the current offset")
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	maxOffset := max(0, m.previewViewport.TotalLineCount()-m.previewViewport.Height)
	if m.previewViewport.YOffset > maxOffset {
		t.Fatalf("resize did not clamp offset %d to maximum %d", m.previewViewport.YOffset, maxOffset)
	}
}

func TestFolderPreviewIsScrollable(t *testing.T) {
	oldBorder := withBorder
	t.Cleanup(func() { withBorder = oldBorder })

	for _, bordered := range []bool{false, true} {
		t.Run(fmt.Sprintf("border-%v", bordered), func(t *testing.T) {
			withBorder = bordered
			m := newTestModel(t, nil)
			folder := filepath.Join(m.path, "folder")
			if err := os.Mkdir(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 110; i++ {
				name := filepath.Join(folder, fmt.Sprintf("file-%03d", i))
				if err := os.WriteFile(name, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			m.list()
			m.previewMode = true
			m.previewFocused = true
			m.View()

			if m.previewViewport.TotalLineCount() <= m.previewViewport.Height {
				t.Fatalf("folder preview should contain scrollable rows: lines=%d height=%d", m.previewViewport.TotalLineCount(), m.previewViewport.Height)
			}
			updateKey(t, m, runeKey('j'))
			if m.previewViewport.YOffset != 1 {
				t.Fatalf("folder preview did not scroll, got offset %d", m.previewViewport.YOffset)
			}
		})
	}
}

func TestShortPreviewDoesNotScroll(t *testing.T) {
	oldHighlight := withHighlight
	withHighlight = false
	t.Cleanup(func() { withHighlight = oldHighlight })

	m := newTestModel(t, map[string]string{"file.txt": "one line"})
	m.previewMode = true
	m.previewFocused = true
	m.View()
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyDown})
	updateKey(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.previewViewport.YOffset != 0 {
		t.Fatalf("short preview should not scroll, got offset %d", m.previewViewport.YOffset)
	}
}

func TestPreviewExitKeysWorkWhileFocused(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      tea.KeyMsg
		exitCode int
	}{
		{name: "escape", key: tea.KeyMsg{Type: tea.KeyEsc}, exitCode: 0},
		{name: "q", key: runeKey('q'), exitCode: 0},
		{name: "control-q", key: tea.KeyMsg{Type: tea.KeyCtrlQ}, exitCode: 0},
		{name: "control-c", key: tea.KeyMsg{Type: tea.KeyCtrlC}, exitCode: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t, map[string]string{"file.txt": "content"})
			m.previewMode = true
			m.previewFocused = true
			cmd := updateKey(t, m, tc.key)
			if !m.quitting || m.exitCode != tc.exitCode || cmd == nil {
				t.Fatalf("exit key failed: quitting=%v code=%d cmd=%v", m.quitting, m.exitCode, cmd != nil)
			}
		})
	}
}

func TestFocusedPreviewHighlightsFilenameHeader(t *testing.T) {
	initStyles()
	m := newTestModel(t, map[string]string{"file.txt": "content"})
	if header := m.renderPreviewHeader("file.txt"); header != bar.Render("file.txt") {
		t.Fatal("unfocused preview filename header does not use the inactive style")
	}
	m.previewMode = true
	m.previewFocused = true
	if header := m.renderPreviewHeader("file.txt"); header != cursor.Render("file.txt") {
		t.Fatal("focused preview filename header does not use the active style")
	}
}
