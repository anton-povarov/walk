package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

var Version = "v1.16.0-antoxa"

var (
	fileSeparator  = string(filepath.Separator)
	showIcons      = false
	dirOnly        = false
	fuzzyByDefault = false
	withBorder     = false
	withHighlight  = true
	strlen         = runewidth.StringWidth
)

func main() {
	startPath, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	if s, ok := os.LookupEnv("WALK_OPEN_WITH"); ok {
		parseOpenWith(s)
	}

	if color, ok := os.LookupEnv("WALK_MAIN_COLOR"); ok {
		mainColor = lipgloss.Color(color)
	}

	if _, ok := os.LookupEnv("WALK_NO_HIGHLIGHT"); ok {
		withHighlight = false
	}

	initStyles()

	m := &model{
		termWidth:       80,
		termHeight:      60,
		positions:       make(map[string]position),
		previewViewport: newPreviewViewport(),
	}
	m.resizePreviewViewport()

	if statusBar, ok := os.LookupEnv("WALK_STATUS_BAR"); ok {
		m.statusBar = compile(statusBar)
	}

	argsWithoutFlags := make([]string, 0)
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--help" || os.Args[1] == "-h" {
			usage(os.Stderr, true)
			os.Exit(1)
		}
		if os.Args[i] == "--version" || os.Args[1] == "-v" {
			fmt.Printf("%s\n", Version)
			os.Exit(0)
		}
		if os.Args[i] == "--icons" {
			showIcons = true
			parseIcons()
			continue
		}
		if os.Args[i] == "--dir-only" {
			dirOnly = true
			continue
		}
		if os.Args[i] == "--preview" {
			m.previewMode = true
			continue
		}
		if os.Args[i] == "--fuzzy" {
			fuzzyByDefault = true
			continue
		}
		if os.Args[i] == "--hide-hidden" {
			m.hideHidden = true
			continue
		}
		if os.Args[i] == "--with-border" {
			withBorder = true
			continue
		}
		argsWithoutFlags = append(argsWithoutFlags, os.Args[i])
	}
	m.resizePreviewViewport()

	if len(argsWithoutFlags) > 0 {
		startPath, err = filepath.Abs(argsWithoutFlags[0])
		if err != nil {
			panic(err)
		}
	}

	output := termenv.NewOutput(os.Stderr)
	profile := output.ColorProfile()
	lipgloss.SetColorProfile(profile)
	m.highlightFormatter = formatterForProfile(profile)
	m.highlightTheme = resolveHighlightTheme(os.Getenv("WALK_HIGHLIGHT_THEME"), os.Getenv("COLORFGBG"))

	m.path = startPath
	m.list()

	opts := []tea.ProgramOption{
		tea.WithOutput(os.Stderr),
	}
	if m.previewMode {
		opts = append(opts, tea.WithAltScreen())
	}

	p := tea.NewProgram(m, opts...)
	lastM, err := p.Run()
	if err != nil {
		panic(err)
	}

	m = lastM.(*model)
	if m.exitCode == 0 {
		fmt.Println(m.path) // Write to cd.
	}

	os.Exit(m.exitCode)
}

func lookup(names []string, val string) string {
	for _, name := range names {
		val, ok := os.LookupEnv(name)
		if ok && val != "" {
			return val
		}
	}
	return val
}
