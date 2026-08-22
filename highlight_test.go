package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
)

func TestNormalizePreviewTextPreservesUnicodeAndNormalizesWhitespace(t *testing.T) {
	input := "Привет 世界 👩‍💻 e\u0301\r\na\tb\rab\tc\nрадуга"
	want := "Привет 世界 👩‍💻 e\u0301\na   b\nab  c\nрадуга"
	got, err := normalizePreviewText([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("normalized text = %q, want %q", got, want)
	}
}

func TestNormalizePreviewTextStripsTerminalControls(t *testing.T) {
	input := []byte("safe\x00\x1b]0;owned\a title\x1b[31m red\x7f \xc2\x9b32mgreen\xc2\x80end")
	got, err := normalizePreviewText(input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') || strings.ContainsRune(got, '\x7f') || strings.ContainsRune(got, '\u009b') {
		t.Fatalf("terminal control survived normalization: %q", got)
	}
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "\x1b]") {
		t.Fatalf("terminal sequence survived normalization: %q", got)
	}
}

func TestNormalizePreviewTextRejectsInvalidUTF8(t *testing.T) {
	if _, err := normalizePreviewText([]byte{'a', 0xff, 'b'}); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}

func TestRenderTextPreviewPreservesUnicodeByteForByte(t *testing.T) {
	input := "Кириллица 世界 👩‍💻 e\u0301\n"
	got, err := renderTextPreview("unicode.txt", []byte(input), highlightOptions{Width: 80, Formatter: "terminal16m", Theme: defaultDarkHighlightTheme}, true)
	if err != nil {
		t.Fatal(err)
	}
	if stripped := ansi.Strip(got); stripped != input {
		t.Fatalf("rendered Unicode = %q, want %q", stripped, input)
	}
}

func TestWrapPreviewTokensUsesCellsAndKeepsGraphemesWhole(t *testing.T) {
	input := "界界e\u0301👩‍💻X"
	tokens := wrapPreviewTokens([]chroma.Token{
		{Type: chroma.String, Value: "界界e\u0301👩"},
		{Type: chroma.Name, Value: "\u200d"},
		{Type: chroma.String, Value: "💻X"},
	}, 4)
	var output strings.Builder
	for _, token := range tokens {
		output.WriteString(token.Value)
	}
	wrapped := output.String()
	if strings.ReplaceAll(wrapped, "\n", "") != input {
		t.Fatalf("wrapping corrupted graphemes: %q", wrapped)
	}
	for _, line := range strings.Split(wrapped, "\n") {
		if width := uniseg.StringWidth(line); width > 4 {
			t.Fatalf("wrapped line is %d cells: %q", width, line)
		}
	}
}

func TestLongTokensStayStyledAcrossSoftWraps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{name: "quoted string", source: "var s = \"abcdefghijklmnopqrstuvwxyz\""},
		{name: "multiline comment", source: "/* abcdefghijklmnopqrstuvwxyz */"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lexer, _ := selectPreviewLexer("sample.go", tc.source)
			iterator, err := lexer.Tokenise(nil, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			tokens, err := consumePreviewTokens(iterator)
			if err != nil {
				t.Fatal(err)
			}
			var wrappedTokenType chroma.TokenType
			for _, token := range tokens {
				if strings.Contains(token.Value, "abcdefgh") {
					wrappedTokenType = token.Type
					break
				}
			}
			if wrappedTokenType == chroma.None {
				t.Fatal("did not find the token expected to cross soft wraps")
			}
			expectedPrefix := terminalTokenStylePrefix(t, wrappedTokenType, defaultDarkHighlightTheme)

			got, err := renderTextPreview("sample.go", []byte(tc.source), highlightOptions{Width: 10, Formatter: "terminal16m", Theme: defaultDarkHighlightTheme}, true)
			if err != nil {
				t.Fatal(err)
			}
			rows := strings.Split(got, "\n")
			if len(rows) < 2 {
				t.Fatalf("test content did not soft-wrap: %q", got)
			}
			// These sources contain no real newline, so every row after the first
			// starts at an inserted soft wrap inside the same string/comment token.
			for rowNumber, row := range rows[1:] {
				if !strings.HasPrefix(row, expectedPrefix) {
					t.Fatalf("soft-wrapped row %d starts with %q, want token style %q in %q", rowNumber+1, row, expectedPrefix, got)
				}
				if row != "" && !strings.HasSuffix(row, "\x1b[0m") && !strings.HasSuffix(row, ansi.ResetStyle) {
					t.Fatalf("styled row lacks a reset: %q", row)
				}
			}
		})
	}
}

func terminalTokenStylePrefix(t *testing.T, tokenType chroma.TokenType, theme string) string {
	t.Helper()
	const marker = "X"
	var output bytes.Buffer
	if err := formatters.Get("terminal16m").Format(&output, styles.Get(theme), chroma.Literator(chroma.Token{Type: tokenType, Value: marker})); err != nil {
		t.Fatal(err)
	}
	prefix, found := strings.CutSuffix(output.String(), marker+"\x1b[0m")
	if !found || prefix == "" {
		t.Fatalf("could not resolve terminal style for token %v: %q", tokenType, output.String())
	}
	return prefix
}

func TestTerminalProfilesChooseCompatibleFormatters(t *testing.T) {
	cases := []struct {
		profile termenv.Profile
		name    string
		want    string
		absent  string
	}{
		{profile: termenv.TrueColor, name: "truecolor", want: "[38;2;"},
		{profile: termenv.ANSI256, name: "ansi256", want: "[38;5;"},
		{profile: termenv.ANSI, name: "ansi", want: "\x1b[", absent: "[38;5;"},
		{profile: termenv.Ascii, name: "ascii"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			formatter := formatterForProfile(tc.profile)
			got, err := renderTextPreview("sample.go", []byte("package main\n"), highlightOptions{Width: 80, Formatter: formatter, Theme: defaultDarkHighlightTheme}, true)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("%s output lacks %q: %q", formatter, tc.want, got)
			}
			if tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Fatalf("%s output contains incompatible color: %q", formatter, got)
			}
			if tc.profile == termenv.Ascii && (strings.ContainsRune(got, '\x1b') || got != "package main\n") {
				t.Fatalf("ASCII output did not degrade to plaintext: %q", got)
			}
		})
	}
}

func TestPreviewLexerSelectionPrefersFilenameThenAnalysesContent(t *testing.T) {
	lexer, filenameMatch := selectPreviewLexer("misleading.py", "#!/bin/sh\necho hello\n")
	if !filenameMatch || lexer.Config().Name != "Python" {
		t.Fatalf("filename lexer did not win: matched=%v lexer=%q", filenameMatch, lexer.Config().Name)
	}

	lexer, filenameMatch = selectPreviewLexer("script", "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello\") }\n")
	if filenameMatch || lexer == lexers.Fallback {
		t.Fatalf("extensionless content was not analysed: matched=%v lexer=%q", filenameMatch, lexer.Config().Name)
	}
}

func TestUnknownContentRendersReadablePlaintext(t *testing.T) {
	input := "ordinary prose with no recognizable syntax"
	got, err := renderTextPreview("unknown.walk-test", []byte(input), highlightOptions{Width: 80, Formatter: "terminal16m", Theme: defaultDarkHighlightTheme}, true)
	if err != nil {
		t.Fatal(err)
	}
	if ansi.Strip(got) != input {
		t.Fatalf("unknown content became unreadable: %q", got)
	}
}

func TestResolveHighlightTheme(t *testing.T) {
	if !backgroundIsLight("15;7") || backgroundIsLight("15;0") || backgroundIsLight("") {
		t.Fatal("COLORFGBG heuristic is not deterministic")
	}
	if got := resolveHighlightTheme("", "15;0"); got != defaultDarkHighlightTheme {
		t.Fatalf("dark default = %q", got)
	}
	if got := resolveHighlightTheme("", "15;7"); got != defaultLightHighlightTheme {
		t.Fatalf("light default = %q", got)
	}
	if got := resolveHighlightTheme("monokai", "15;7"); got != styles.Get("monokai").Name {
		t.Fatalf("theme override = %q", got)
	}
	if got := resolveHighlightTheme("not-a-real-theme", "15;0"); got != defaultDarkHighlightTheme {
		t.Fatalf("invalid theme did not fall back: %q", got)
	}
}

func TestTrimPartialUTF8Suffix(t *testing.T) {
	prefix := strings.Repeat("a", previewByteLimit-1)
	cut := append([]byte(prefix), []byte("界")[:1]...)
	trimmed := trimPartialUTF8Suffix(cut)
	if !utf8.Valid(trimmed) || string(trimmed) != prefix {
		t.Fatalf("partial UTF-8 suffix was not safely trimmed: valid=%v length=%d", utf8.Valid(trimmed), len(trimmed))
	}

	invalid := []byte{'a', 0xff}
	if got := trimPartialUTF8Suffix(invalid); len(got) != len(invalid) {
		t.Fatalf("complete invalid byte was hidden by trimming: %v", got)
	}
}
