package main

import (
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	. "strings"
	"time"

	"github.com/antonmedv/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/expr-lang/expr/vm"
	"github.com/sahilm/fuzzy"
)

type model struct {
	path                  string              // Current dir path we are looking at.
	files                 []fs.DirEntry       // Files we are looking at.
	err                   error               // Error while listing files.
	errStatus             error               // Minor error while executing actions - shown in statusbar
	c, r                  int                 // Selector position in columns and rows.
	columns, rows         int                 // Displayed amount of rows and columns.
	termWidth, termHeight int                 // Terminal size.
	offset                int                 // Scroll position.
	positions             map[string]position // Map of cursor positions per path.
	search                string              // Type to select files with this value.
	searchMode            bool                // Whether type-to-select is active.
	searchId              int                 // Search id to indicate what search we are currently on.
	matchedIndexes        []int               // List of char found indexes.
	prevName              string              // Base name of previous directory before "up".
	findPrevName          bool                // On View(), set c&r to point to prevName.
	exitCode              int                 // Exit code.
	previewMode           bool                // Whether preview is active.
	previewFocused        bool                // Whether the preview pane receives navigation keys.
	previewPath           string              // Path whose content is loaded in the preview viewport.
	previewViewport       viewport.Model      // Scrollable preview pane.
	highlightFormatter    string              // Chroma formatter matching the terminal color profile.
	highlightTheme        string              // Validated Chroma theme name.
	previewCache          textPreviewCache    // Most recently rendered file preview.
	images                *imagePreview
	graphics              *graphicsOutput
	previewGraphic        *preparedGraphic
	deleteCurrentFile     bool        // Whether to delete current file.
	toBeDeleted           []toDelete  // Map of files to be deleted.
	yankedFilePath        string      // Show yank info
	hideHidden            bool        // Hide hidden files
	showHelp              bool        // Show help
	statusBar             *vm.Program // Status bar program.
	quitting              bool        // Whether we are quitting the program.
}

type position struct {
	c, r   int
	offset int
}

type toDelete struct {
	path string
	at   time.Time
}

type (
	clearSearchMsg int
	toBeDeletedMsg int
)

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
		m.termHeight = msg.Height
		if m.termHeight < 3 {
			m.termHeight = 3
		}
		m.resizePreviewViewport()
		// Reset position history as c&r changes.
		m.positions = make(map[string]position)
		// Keep cursor at same place.
		fileName, ok := m.currentFileName()
		if ok {
			m.prevName = fileName
			m.findPrevName = true
		}
		// Also, m.c&r no longer point to the correct indexes.
		m.c = 0
		m.r = 0
		return m, nil

	case tea.KeyMsg:
		if m.previewFocused {
			switch {
			case key.Matches(msg, keyForceQuit):
				return m.quit(true)

			case key.Matches(msg, keyQuit, keyQuitQ, keyQuitCQ):
				return m.quit(false)

			case key.Matches(msg, keyTab):
				m.previewFocused = false
				m.clearTransientState()
				return m, nil

			default:
				var cmd tea.Cmd
				m.previewViewport, cmd = m.previewViewport.Update(msg)
				return m, cmd
			}
		}

		// Make undo work even if we are in fuzzy mode.
		if key.Matches(msg, keyUndo) && len(m.toBeDeleted) > 0 {
			m.toBeDeleted = m.toBeDeleted[:len(m.toBeDeleted)-1]
			m.list()
			m.clearPreview()
			return m, nil
		}

		if fuzzyByDefault {
			if key.Matches(msg, keyBack) {
				if len(m.search) > 0 {
					m.search = m.search[:strlen(m.search)-1]
					return m, nil
				}
			} else if msg.Type == tea.KeyRunes {
				m.updateSearch(msg)
				// Save search id to clear only current search after delay.
				// User may have already started typing next search.
				m.searchId++
				searchId := m.searchId
				return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
					return clearSearchMsg(searchId)
				})
			}
		} else if m.searchMode {
			if key.Matches(msg, keySearch) {
				m.searchMode = false
				return m, nil
			} else if key.Matches(msg, keyBack) {
				if len(m.search) > 0 {
					m.search = m.search[:strlen(m.search)-1]
				} else {
					m.searchMode = false
				}
				return m, nil
			} else if msg.Type == tea.KeyRunes {
				m.updateSearch(msg)
				return m, nil
			}
		}

		switch {
		case key.Matches(msg, keyForceQuit):
			return m.quit(true)

		case key.Matches(msg, keyQuit, keyQuitQ, keyQuitCQ):
			return m.quit(false)

		case key.Matches(msg, keyOpen):
			m.search = ""
			m.searchMode = false
			filePath, ok := m.filePath()
			if !ok {
				return m, nil
			}

			fileInfo, err := os.Stat(filePath)
			if err != nil {
				m.errStatus = err
				return m, nil
			}

			if fileInfo.IsDir() {
				// Enter subdirectory.
				m.path = filePath
				if p, ok := m.positions[m.path]; ok {
					m.c = p.c
					m.r = p.r
					m.offset = p.offset
				} else {
					m.c = 0
					m.r = 0
					m.offset = 0
				}
				m.list()
			} else {
				// Open file. This will block until complete.
				return m, m.open()
			}

		case key.Matches(msg, keyBack):
			m.search = ""
			m.searchMode = false
			m.prevName = filepath.Base(m.path)
			m.path = filepath.Join(m.path, "..")
			if p, ok := m.positions[m.path]; ok {
				m.c = p.c
				m.r = p.r
				m.offset = p.offset
			} else {
				m.findPrevName = true
			}
			m.list()
			return m, nil

		case key.Matches(msg, keyUp):
			m.moveUp()

		case key.Matches(msg, keyTop, keyPageUp, keyVimTop):
			m.moveTop()

		case key.Matches(msg, keyBottom, keyPageDown, keyVimBottom):
			m.moveBottom()

		case key.Matches(msg, keyLeftmost):
			m.moveLeftmost()

		case key.Matches(msg, keyRightmost):
			m.moveRightmost()

		case key.Matches(msg, keyHome):
			m.moveStart()

		case key.Matches(msg, keyEnd):
			m.moveEnd()

		case key.Matches(msg, keyVimUp):
			m.moveUp()

		case key.Matches(msg, keyDown):
			m.moveDown()

		case key.Matches(msg, keyVimDown):
			m.moveDown()

		case key.Matches(msg, keyLeft):
			m.moveLeft()

		case key.Matches(msg, keyVimLeft):
			m.moveLeft()

		case key.Matches(msg, keyRight):
			m.moveRight()

		case key.Matches(msg, keyVimRight):
			m.moveRight()

		case key.Matches(msg, keySearch):
			m.searchMode = true
			m.searchId++
			m.search = ""

		case key.Matches(msg, keyTab):
			if m.previewMode {
				if _, ok := m.currentFileName(); !ok {
					return m, nil
				}
				m.previewFocused = true
				m.clearTransientState()
				return m, nil
			}

			return m, m.togglePreview(true)

		case key.Matches(msg, keyPreview):
			return m, m.togglePreview(false)

		case key.Matches(msg, keyDelete, keyFnDelete):
			filePathToDelete, ok := m.filePath()
			if ok {
				if m.deleteCurrentFile {
					m.deleteCurrentFile = false
					m.toBeDeleted = append(m.toBeDeleted, toDelete{
						path: filePathToDelete,
						at:   time.Now().Add(6 * time.Second),
					})
					m.list()
					m.clearPreview()
					return m, tea.Tick(time.Second, func(time.Time) tea.Msg {
						return toBeDeletedMsg(0)
					})
				} else {
					m.deleteCurrentFile = true
				}
			}
			return m, nil

		case key.Matches(msg, keyYank):
			filePath, ok := m.filePath()
			if ok {
				clipboard.WriteAll(filePath)
				m.yankedFilePath = filePath
				m.updateOffset()
			}
			return m, nil

		case key.Matches(msg, keyHelp):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, keyHidden):
			m.hideHidden = !m.hideHidden
			m.list()

		} // End of switch statement for key presses.

		m.clearTransientState()
		m.updateOffset()
		m.saveCursorPosition()

	case clearSearchMsg:
		if m.searchId == int(msg) {
			m.search = ""
			m.searchMode = false
		}

	case toBeDeletedMsg:
		toBeDeleted := make([]toDelete, 0)
		for _, td := range m.toBeDeleted {
			if td.at.After(time.Now()) {
				toBeDeleted = append(toBeDeleted, td)
			} else {
				remove(td.path)
			}
		}
		m.toBeDeleted = toBeDeleted
		if len(m.toBeDeleted) > 0 {
			return m, tea.Tick(time.Second, func(time.Time) tea.Msg {
				return toBeDeletedMsg(0)
			})
		}
	}

	return m, nil
}

func (m *model) quit(force bool) (tea.Model, tea.Cmd) {
	m.quitting = true
	if force {
		m.exitCode = 2
		m.dontDoPendingDeletions()
	} else {
		m.exitCode = 0
		m.performPendingDeletions()
	}
	return m, tea.Quit
}

func (m *model) clearTransientState() {
	m.errStatus = nil
	m.deleteCurrentFile = false
	m.showHelp = false
	m.yankedFilePath = ""
}

func (m *model) updateSearch(msg tea.KeyMsg) {
	m.search += string(msg.Runes)
	names := make([]string, len(m.files))
	for i, fi := range m.files {
		names[i] = fi.Name()
	}
	matches := fuzzy.Find(m.search, names)
	if len(matches) > 0 {
		m.matchedIndexes = matches[0].MatchedIndexes
		index := matches[0].Index
		m.c = index / m.rows
		m.r = index % m.rows
	}
	m.updateOffset()
	m.saveCursorPosition()
}

func (m *model) moveUp() {
	m.r--
	if m.r < 0 {
		m.r = m.rows - 1
		m.c--
	}
	if m.c < 0 {
		m.r = m.rows - 1 - (m.columns*m.rows - len(m.files))
		m.c = m.columns - 1
	}
}

func (m *model) moveDown() {
	m.r++
	if m.r >= m.rows {
		m.r = 0
		m.c++
	}
	if m.c >= m.columns {
		m.c = 0
	}
	if m.c == m.columns-1 && (m.columns-1)*m.rows+m.r >= len(m.files) {
		m.r = 0
		m.c = 0
	}
}

func (m *model) moveLeft() {
	m.c--
	if m.c < 0 {
		m.c = m.columns - 1
	}
	if m.c == m.columns-1 && (m.columns-1)*m.rows+m.r >= len(m.files) {
		m.r = m.rows - 1 - (m.columns*m.rows - len(m.files))
		m.c = m.columns - 1
	}
}

func (m *model) moveRight() {
	m.c++
	if m.c >= m.columns {
		m.c = 0
	}
	if m.c == m.columns-1 && (m.columns-1)*m.rows+m.r >= len(m.files) {
		m.r = m.rows - 1 - (m.columns*m.rows - len(m.files))
		m.c = m.columns - 1
	}
}

func (m *model) moveTop() {
	m.r = 0
}

func (m *model) moveBottom() {
	m.r = m.rows - 1
	if m.c == m.columns-1 && (m.columns-1)*m.rows+m.r >= len(m.files) {
		m.r = m.rows - 1 - (m.columns*m.rows - len(m.files))
	}
}

func (m *model) moveLeftmost() {
	m.c = 0
}

func (m *model) moveRightmost() {
	m.c = m.columns - 1
	if (m.columns-1)*m.rows+m.r >= len(m.files) {
		m.r = m.rows - 1 - (m.columns*m.rows - len(m.files))
	}
}

func (m *model) moveStart() {
	m.moveLeftmost()
	m.moveTop()
}

func (m *model) moveEnd() {
	m.moveRightmost()
	m.moveBottom()
}

func (m *model) list() {
	var err error
	m.files = nil

	// ReadDir already returns files and dirs sorted by filename.
	files, err := os.ReadDir(m.path)
	if err != nil {
		m.err = err
		return
	} else {
		m.err = nil
	}

files:
	for _, file := range files {
		if m.hideHidden && HasPrefix(file.Name(), ".") {
			continue files
		}
		if dirOnly && !file.IsDir() {
			continue files
		}
		for _, toDelete := range m.toBeDeleted {
			if path.Join(m.path, file.Name()) == toDelete.path {
				continue files
			}
		}
		m.files = append(m.files, file)
	}

	// dirs before files
	slices.SortFunc(m.files, func(l, r os.DirEntry) int {
		ld, rd := l.IsDir(), r.IsDir()
		if ld == rd {
			return cmp.Compare(l.Name(), r.Name())
		}
		if ld {
			return -1
		}
		return 1
	})
}

func (m *model) updateOffset() {
	height := m.listHeight()
	// Scrolling down.
	if m.r >= m.offset+height {
		m.offset = m.r - height + 1
	}
	// Scrolling up.
	if m.r < m.offset {
		m.offset = m.r
	}
	// Don't scroll more than there are rows.
	if m.offset > m.rows-height && m.rows > height {
		m.offset = m.rows - height
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *model) saveCursorPosition() {
	m.positions[m.path] = position{
		c:      m.c,
		r:      m.r,
		offset: m.offset,
	}
}

func (m *model) currentFile() (fs.DirEntry, bool) {
	i := m.c*m.rows + m.r
	if i >= len(m.files) || i < 0 {
		return nil, false
	}
	return m.files[i], true
}

func (m *model) currentFileName() (string, bool) {
	f, ok := m.currentFile()
	if !ok {
		return "", false
	}
	return f.Name(), true
}

func (m *model) filePath() (string, bool) {
	fileName, ok := m.currentFileName()
	if !ok {
		return fileName, false
	}
	return path.Join(m.path, fileName), true
}

func (m *model) open() tea.Cmd {
	filePath, ok := m.filePath()
	if !ok {
		return nil
	}

	var commandString string
	if commandString, ok = openWith[extension(filePath)]; ok {
	} else {
		commandString = lookup([]string{"WALK_EDITOR", "EDITOR"}, "less")
	}

	commandSlice := append(Split(commandString, " "), filePath)
	execCmd := exec.Command(commandSlice[0], commandSlice[1:]...)
	return tea.ExecProcess(execCmd, func(err error) tea.Msg {
		// Note: we could return a message here indicating that editing is
		// finished and altering our application about any errors. For now,
		// however, that's not necessary.
		return nil
	})
}

func (m *model) dontDoPendingDeletions() {
	for _, toDelete := range m.toBeDeleted {
		fmt.Fprintf(os.Stderr, "Was not deleted: %v\n", toDelete.path)
	}
}

func (m *model) performPendingDeletions() {
	for _, toDelete := range m.toBeDeleted {
		remove(toDelete.path)
	}
	m.toBeDeleted = nil
}

func remove(path string) {
	go func() {
		cmd, ok := os.LookupEnv("WALK_REMOVE_CMD")
		if !ok {
			_ = os.RemoveAll(path)
		} else {
			_ = exec.Command(cmd, path).Run()
		}
	}()
}
