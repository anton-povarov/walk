package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"runtime"
	. "strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/expr-lang/expr"
)

const separator = "    " // Separator between columns.

func fitPaneWidth(content string, width int) string {
	lines := Split(content, "\n")
	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		lines[i] = line + Repeat(" ", max(0, width-ansi.StringWidth(line)))
	}
	return Join(lines, "\n")
}

func renderFullWidth(style lipgloss.Style, content string, width int) string {
	content = ansi.Truncate(content, width, "")
	content += Repeat(" ", max(0, width-ansi.StringWidth(content)))
	return style.Render(content)
}

func minimumPreviewLeftWidth(termWidth int) int {
	availableWidth := max(1, termWidth-1) // Reserve the RHS right margin.
	return (availableWidth*7 + 19) / 20   // 35%, rounded up.
}

func (m *model) View() (viewResult string) {
	var graphicFrame graphicsFrame
	defer func() {
		if m.graphics != nil {
			viewResult = m.graphics.frame(graphicFrame) + viewResult
		}
	}()
	if m.showHelp {
		out := &Builder{}
		out.WriteString(bar.Render("help") + "\n\n")
		usage(out, false)
		return out.String()
	}

	width := m.termWidth
	if m.previewMode {
		width = m.termWidth / 2
	}
	height := m.listHeight()

	var names [][]string
	names, m.rows, m.columns = wrap(m.files, width, height, func(name string, i, j int) {
		if m.findPrevName && m.prevName == name {
			m.c = i
			m.r = j
		}
	})

	// If we need to select previous directory on "up".
	if m.findPrevName {
		m.findPrevName = false
		m.updateOffset()
		m.saveCursorPosition()
	}

	// Get output rows width before coloring.
	outputWidth := strlen(path.Base(m.path)) // Use current dir name as default.
	if m.previewMode {
		row := make([]string, m.columns)
		for i := 0; i < m.columns; i++ {
			if len(names[i]) > 0 {
				row[i] = names[i][0]
			} else {
				outputWidth = width
			}
		}
		outputWidth = max(outputWidth, strlen(Join(row, separator)))
	} else {
		outputWidth = width
	}
	leftWidth := outputWidth
	if m.previewMode {
		maxLeftWidth := m.termWidth - m.previewStyle().GetHorizontalFrameSize() - 2
		leftWidth = max(leftWidth, minimumPreviewLeftWidth(m.termWidth))
		leftWidth = min(leftWidth, max(1, maxLeftWidth))
		m.resizePreviewViewportForLeftWidth(leftWidth)
	}

	// Preview after its width is derived from the actual LHS content width.
	m.preview()
	if m.previewMode && !m.quitting {
		graphicFrame = graphicsFrame{m.previewGraphic, leftWidth + m.previewStyle().GetHorizontalFrameSize() + 1, 2}
		if graphicFrame.image != nil && graphicFrame.x+graphicFrame.image.width-1 > m.termWidth {
			graphicFrame = graphicsFrame{}
		}
	}

	// Let's add colors to file names.
	output := make([]string, m.rows)
	for j := 0; j < m.rows; j++ {
		row := make([]string, m.columns)
		for i := 0; i < m.columns; i++ {
			if i == m.c && j == m.r {
				if m.deleteCurrentFile {
					row[i] = danger.Render(names[i][j])
				} else {
					row[i] = cursor.Render(names[i][j])
				}
			} else {
				row[i] = names[i][j]
			}
		}
		output[j] = Join(row, separator)
	}

	if len(output) >= m.offset+height {
		output = output[m.offset : m.offset+height]
	}

	// Preview pane.
	fileName, _ := m.currentFileName()
	previewHeader := m.renderPreviewHeader(fileName)
	previewPane := fitPaneWidth(previewHeader+"\n"+m.previewViewport.View(), m.previewViewport.Width)

	// Location bar (grey).
	location := m.path
	if userHomeDir, err := os.UserHomeDir(); err == nil {
		location = Replace(m.path, userHomeDir, "~", 1)
	}
	if runtime.GOOS == "windows" {
		location = ReplaceAll(Replace(location, "\\/", fileSeparator, 1), "/", fileSeparator)
	}

	// Filter bar (green).
	filter := ""
	if m.searchMode || fuzzyByDefault {
		filter = fileSeparator + m.search

		// If fuzzy is on and search is empty, don't show filter.
		if fuzzyByDefault && m.search == "" {
			filter = ""
		}
	}
	barLen := strlen(location) + strlen(filter)
	if barLen > leftWidth {
		location = location[min(barLen-leftWidth, strlen(location)):]
	}
	barStr := bar.Render(location) + search.Render(filter)

	main := barStr + "\n" + Join(output, "\n")

	if m.err != nil {
		main = barStr + "\n" + warning.Render(m.err.Error())
	} else if len(m.files) == 0 {
		main = barStr + "\n" + warning.Render("No files")
	}

	if m.showStatusBar() {
		// Only show one status bar.
		// TODO: Show most recent status bar.
		if m.errStatus != nil {
			errorBar := fmt.Sprintf("error %v", m.errStatus)
			main += "\n" + renderFullWidth(danger, errorBar, leftWidth)
		} else if len(m.toBeDeleted) > 0 {
			toDelete := m.toBeDeleted[len(m.toBeDeleted)-1]
			timeLeft := int(toDelete.at.Sub(time.Now()).Seconds())
			deleteBar := fmt.Sprintf("%v deleted. (u)ndo %v", path.Base(toDelete.path), timeLeft)
			main += "\n" + renderFullWidth(danger, deleteBar, leftWidth)
		} else if m.yankedFilePath != "" {
			yankBar := fmt.Sprintf("copied: %v", m.yankedFilePath)
			main += "\n" + renderFullWidth(bar, yankBar, leftWidth)
		} else if m.statusBar != nil {
			f, ok := m.currentFile()
			if ok {
				env := Env{
					Files:       m.files,
					CurrentFile: f,
				}
				statusBar, err := expr.Run(m.statusBar, env)
				if err != nil {
					main += "\n" + err.Error()
				} else {
					main += "\n" + renderFullWidth(bar, fmt.Sprintf("%v", statusBar), leftWidth)
				}
			}
		}
	}

	view := main
	if m.previewMode {
		view = lipgloss.JoinHorizontal(
			lipgloss.Top,
			fitPaneWidth(main, leftWidth),
			m.previewStyle().Render(previewPane),
		)
	}

	if m.quitting {
		view += "\n" // Keep the last line from disappearing.
	}

	return view
}

func (m *model) listHeight() int {
	h := m.termHeight - 1 // Subtract 1 for location bar.
	if m.showStatusBar() {
		h--
	}
	return h
}

func (m *model) showStatusBar() bool {
	if m.errStatus != nil {
		return true
	}
	if len(m.toBeDeleted) > 0 {
		return true
	}
	if m.yankedFilePath != "" {
		return true
	}
	if m.statusBar != nil {
		return true
	}
	return false
}

// TODO: Write tests for this function.
func wrap(files []os.DirEntry, width int, height int, callback func(name string, i, j int)) ([][]string, int, int) {
	// If the directory is empty, return no names, rows and columns.
	if len(files) == 0 {
		return nil, 0, 0
	}

	// If it's possible to fit all files in one column on a third of the screen,
	// just use one column. Otherwise, let's squeeze listing in half of screen.
	columns := len(files) / max(1, height/3)
	if columns <= 0 {
		columns = 1
	}

	// Max number of files to display in one column is 10 or 4 columns in total.
	columnsEstimate := int(math.Ceil(float64(len(files)) / 10))
	columns = max(columns, min(columnsEstimate, 4))

	// For large lists, don't use more than 2 columns.
	if len(files) > 100 {
		columns = 2
	}

	// Fifteenth column is enough for everyone.
	if columns > 15 {
		columns = 15
	}

start:
	// Let's try to fit everything in terminal width with this many columns.
	// If we are not able to do it, decrease column number and goto start.
	rows := int(math.Ceil(float64(len(files)) / float64(columns)))
	names := make([][]string, columns)
	n := 0

	for i := 0; i < columns; i++ {
		names[i] = make([]string, rows)
		maxNameSize := 0 // We will use this to determine max name size, and pad names in column with spaces.
		for j := 0; j < rows; j++ {
			if n >= len(files) {
				break // No more files to display.
			}
			if callback != nil {
				callback(files[n].Name(), i, j)
			}

			icon := ""
			if showIcons {
				info, err := files[n].Info()
				if err == nil {
					icon = icons.getIcon(info)
					if icon != "" {
						icon += " "
					}
				}
			}

			name := ""
			file, fileMode := files[n], files[n].Type()
			if fileMode&fs.ModeDir != 0 {
				name = fmt.Sprintf("%s[%s] ", icon, file.Name())
			} else if fileMode&fs.ModeSymlink != 0 {
				name = fmt.Sprintf("%s~%s ", icon, file.Name())
			} else {
				name = fmt.Sprintf("%s%s ", icon, file.Name())
			}

			n++ // Next file.

			if maxNameSize < strlen(name) {
				maxNameSize = strlen(name)
			}
			names[i][j] = name
		}

		// Append spaces to make all names in one column of same size.
		for j := 0; j < rows; j++ {
			names[i][j] += Repeat(" ", maxNameSize-strlen(names[i][j]))
		}
	}

	// Let's verify was all columns have at least one file.
	for i := 0; i < columns; i++ {
		if names[i] == nil {
			columns--
			goto start
		}
		columnHaveAtLeastOneFile := false
		for j := 0; j < rows; j++ {
			if names[i][j] != "" {
				columnHaveAtLeastOneFile = true
				break
			}
		}
		if !columnHaveAtLeastOneFile {
			columns--
			goto start
		}
	}

	for j := 0; j < rows; j++ {
		row := make([]string, columns)
		for i := 0; i < columns; i++ {
			row[i] = names[i][j]
		}
		if strlen(Join(row, separator)) > width && columns > 1 {
			// Yep. No luck, let's decrease number of columns and try one more time.
			columns--
			goto start
		}
	}
	return names, rows, columns
}
