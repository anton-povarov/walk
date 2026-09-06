package main

import (
	"io"
	"os"
	"path/filepath"
	. "strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type textPreviewCacheKey struct {
	path      string
	modTime   int64
	size      int64
	width     int
	formatter string
	theme     string
	highlight bool
}

type textPreviewCache struct {
	key     textPreviewCacheKey
	content string
	valid   bool
}

func newPreviewViewport() viewport.Model {
	v := viewport.New(1, 1)
	v.MouseWheelEnabled = false
	v.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys("pgdown", "f")),
		PageUp:       key.NewBinding(key.WithKeys("pgup", "b")),
		HalfPageUp:   key.NewBinding(key.WithDisabled()),
		HalfPageDown: key.NewBinding(key.WithDisabled()),
		Down:         key.NewBinding(key.WithKeys("down", "j")),
		Up:           key.NewBinding(key.WithKeys("up", "k")),
	}
	return v
}

func (m *model) togglePreview(focusOnEnable bool) tea.Cmd {
	m.previewMode = !m.previewMode
	m.previewFocused = false
	// Reset position history as c&r changes.
	m.positions = make(map[string]position)
	// Keep cursor at same place.
	fileName, ok := m.currentFileName()
	if !ok {
		return nil
	}
	m.prevName = fileName
	m.findPrevName = true

	if m.previewMode {
		m.previewFocused = focusOnEnable
		m.clearTransientState()
		return tea.EnterAltScreen
	}

	m.clearPreview()
	return tea.ExitAltScreen
}

func (m *model) previewStyle() lipgloss.Style {
	if withBorder {
		return previewSplit
	}
	return previewPlain
}

func (m *model) resizePreviewViewport() {
	m.resizePreviewViewportForLeftWidth(m.termWidth / 2)
}

func (m *model) resizePreviewViewportForLeftWidth(leftWidth int) {
	width := m.termWidth - leftWidth - m.previewStyle().GetHorizontalFrameSize() - 1
	m.previewViewport.Width = max(1, width)
	m.previewViewport.Height = max(1, m.termHeight-1) // Subtract 1 for the filename header.
	m.previewViewport.SetYOffset(m.previewViewport.YOffset)
}

func (m *model) setPreviewContent(filePath, content string) {
	m.setPreviewContentWrapped(filePath, ansi.Wrap(content, m.previewViewport.Width, ""))
}

func (m *model) setPreviewContentWrapped(filePath, content string) {
	pathChanged := m.previewPath != filePath
	m.previewPath = filePath
	// Reset each RHS row before panes are joined so neither source content nor
	// a syntax style can leak into the LHS on the following terminal row.
	content = ReplaceAll(content, "\n", ansi.ResetStyle+"\n") + ansi.ResetStyle
	m.previewViewport.SetContent(content)
	if pathChanged {
		m.previewViewport.GotoTop()
	} else {
		m.previewViewport.SetYOffset(m.previewViewport.YOffset)
	}
}

func (m *model) clearPreview() {
	m.previewPath = ""
	m.previewViewport.SetContent("")
	m.previewViewport.GotoTop()
}

func (m *model) renderPreviewHeader(fileName string) string {
	if m.previewFocused {
		return cursor.Render(fileName)
	}
	return bar.Render(fileName)
}

func (m *model) preview() {
	if !m.previewMode {
		return
	}
	filePath, ok := m.filePath()
	if !ok {
		// Normally this should not happen
		m.setPreviewContent("", warning.Render("Invalid file to preview"))
		return
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		m.setPreviewContent(filePath, warning.Render(err.Error()))
		return
	}

	width := m.previewViewport.Width
	height := m.previewViewport.Height

	if fileInfo.IsDir() {
		files, err := os.ReadDir(filePath)
		if err != nil {
			m.setPreviewContent(filePath, warning.Render(err.Error()))
			return
		}

		if len(files) == 0 {
			m.setPreviewContent(filePath, warning.Render("No files"))
			return
		}

		names, rows, columns := wrap(files, width, height, nil)

		output := make([]string, rows)
		for j := 0; j < rows; j++ {
			row := make([]string, columns)
			for i := 0; i < columns; i++ {
				row[i] = names[i][j]
			}
			output[j] = Join(row, separator)
		}
		m.setPreviewContent(filePath, Join(output, "\n"))
		return
	}

	if isImage(filePath) {
		img, err := drawImage(filePath, width, height)
		if err != nil {
			m.setPreviewContent(filePath, warning.Render("No image preview available"))
			return
		}
		m.setPreviewContent(filePath, img)
		return
	}

	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		absolutePath = filePath
	}
	cacheKey := textPreviewCacheKey{
		path:      absolutePath,
		modTime:   fileInfo.ModTime().UnixNano(),
		size:      fileInfo.Size(),
		width:     width,
		formatter: m.highlightFormatter,
		theme:     m.highlightTheme,
		highlight: withHighlight,
	}
	if m.previewCache.valid && m.previewCache.key == cacheKey {
		m.setPreviewContentWrapped(filePath, m.previewCache.content)
		return
	}

	var content []byte
	// If file is too big (> 100kb), read only first 100kb.
	if fileInfo.Size() > previewByteLimit {
		file, err := os.Open(filePath)
		if err != nil {
			m.setPreviewContent(filePath, err.Error())
			return
		}
		defer file.Close()
		content = make([]byte, previewByteLimit)
		_, err = io.ReadFull(file, content)
		if err != nil {
			m.setPreviewContent(filePath, err.Error())
			return
		}
	} else {
		content, err = os.ReadFile(filePath)
		if err != nil {
			m.setPreviewContent(filePath, err.Error())
			return
		}
	}

	if fileInfo.Size() > previewByteLimit {
		content = trimPartialUTF8Suffix(content)
	}
	if !utf8.Valid(content) {
		m.setPreviewContent(filePath, warning.Render("No preview available"))
		return
	}

	previewContent, err := renderTextPreview(filePath, content, highlightOptions{
		Width:     width,
		Formatter: m.highlightFormatter,
		Theme:     m.highlightTheme,
	}, withHighlight)
	if err != nil {
		m.setPreviewContent(filePath, warning.Render("No preview available"))
		return
	}
	m.previewCache = textPreviewCache{key: cacheKey, content: previewContent, valid: true}
	m.setPreviewContentWrapped(filePath, previewContent)
}
