package main

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/muesli/termenv"
	"github.com/rivo/uniseg"
)

const (
	previewByteLimit           = 100 * 1024
	defaultDarkHighlightTheme  = "nordic"
	defaultLightHighlightTheme = "catppuccin-latte"
	plainTextFormatter         = "noop"
)

var errInvalidPreviewUTF8 = errors.New("preview is not valid UTF-8")

type highlightOptions struct {
	Width     int
	Formatter string
	Theme     string
}

func formatterForProfile(profile termenv.Profile) string {
	switch profile {
	case termenv.TrueColor:
		return "terminal16m"
	case termenv.ANSI256:
		return "terminal256"
	case termenv.ANSI:
		return "terminal16"
	default:
		return plainTextFormatter
	}
}

func resolveHighlightTheme(override, colorFGBG string) string {
	if override != "" {
		requested := styles.Get(override)
		for _, name := range styles.Names() {
			if strings.EqualFold(name, override) {
				return requested.Name
			}
		}
	}

	if backgroundIsLight(colorFGBG) {
		return defaultLightHighlightTheme
	}
	return defaultDarkHighlightTheme
}

func backgroundIsLight(colorFGBG string) bool {
	parts := strings.Split(colorFGBG, ";")
	background, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil && background >= 7
}

// normalizePreviewText removes terminal controls while preserving printable
// Unicode. Tabs advance to the next four-cell stop.
func normalizePreviewText(content []byte) (string, error) {
	if !utf8.Valid(content) {
		return "", errInvalidPreviewUTF8
	}

	var safe strings.Builder
	for i := 0; i < len(content); {
		r, size := utf8.DecodeRune(content[i:])
		i += size
		switch r {
		case '\r':
			if i < len(content) && content[i] == '\n' {
				i++
			}
			safe.WriteByte('\n')
		case '\n', '\t':
			safe.WriteRune(r)
		default:
			// ZWJ/ZWNJ are non-printing format runes, but they are essential
			// parts of valid grapheme clusters such as joined emoji.
			if unicode.IsPrint(r) || r == '\u200c' || r == '\u200d' {
				safe.WriteRune(r)
			}
		}
	}

	var normalized strings.Builder
	column := 0
	graphemes := uniseg.NewGraphemes(safe.String())
	for graphemes.Next() {
		cluster := graphemes.Str()
		switch cluster {
		case "\n":
			normalized.WriteByte('\n')
			column = 0
		case "\t":
			spaces := 4 - column%4
			normalized.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		default:
			normalized.WriteString(cluster)
			column += graphemes.Width()
		}
	}
	return normalized.String(), nil
}

func selectPreviewLexer(path, content string) (chroma.Lexer, bool) {
	if lexer := lexers.Match(path); lexer != nil {
		return lexer, true
	}
	if lexer := lexers.Analyse(content); lexer != nil {
		return lexer, false
	}
	return lexers.Fallback, false
}

// renderTextPreview is the single file-text rendering path. Disabling
// highlighting selects Chroma's no-op formatter but retains identical Unicode
// normalization and grapheme-aware wrapping.
func renderTextPreview(path string, content []byte, opts highlightOptions, highlight bool) (string, error) {
	normalized, err := normalizePreviewText(content)
	if err != nil {
		return "", err
	}

	lexer := lexers.Fallback
	formatterName := plainTextFormatter
	if highlight {
		lexer, _ = selectPreviewLexer(path, normalized)
		formatterName = opts.Formatter
		if formatterName == "" {
			formatterName = plainTextFormatter
		}
	}

	iterator, err := lexer.Tokenise(nil, normalized)
	if err != nil {
		return "", err
	}
	tokens, err := consumePreviewTokens(iterator)
	if err != nil {
		return "", err
	}
	tokens = wrapPreviewTokens(tokens, opts.Width)

	theme := opts.Theme
	if theme == "" {
		theme = defaultDarkHighlightTheme
	}
	var output bytes.Buffer
	if err := formatters.Get(formatterName).Format(&output, styles.Get(theme), chroma.Literator(tokens...)); err != nil {
		return "", err
	}
	return output.String(), nil
}

func consumePreviewTokens(iterator chroma.Iterator) (tokens []chroma.Token, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tokenize preview: %v", recovered)
		}
	}()
	for token := iterator(); token != chroma.EOF; token = iterator() {
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func wrapPreviewTokens(tokens []chroma.Token, width int) []chroma.Token {
	if width < 1 {
		width = 1
	}

	type tokenSpan struct {
		tokenType chroma.TokenType
		start     int
		end       int
	}
	var source strings.Builder
	spans := make([]tokenSpan, 0, len(tokens))
	for _, token := range tokens {
		if token.Value == "" {
			continue
		}
		start := source.Len()
		source.WriteString(token.Value)
		spans = append(spans, tokenSpan{tokenType: token.Type, start: start, end: source.Len()})
	}

	wrapped := make([]chroma.Token, 0, len(tokens))
	text := source.String()
	column := 0
	appendValue := func(tokenType chroma.TokenType, value string) {
		if value == "" {
			return
		}
		last := len(wrapped) - 1
		if last >= 0 && wrapped[last].Type == tokenType {
			wrapped[last].Value += value
			return
		}
		wrapped = append(wrapped, chroma.Token{Type: tokenType, Value: value})
	}

	spanIndex := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		from, to := graphemes.Positions()
		for spanIndex < len(spans) && spans[spanIndex].end <= from {
			spanIndex++
		}
		if spanIndex == len(spans) {
			break
		}

		cluster := graphemes.Str()
		if cluster != "\n" && column > 0 && column+graphemes.Width() > width {
			appendValue(spans[spanIndex].tokenType, "\n")
			column = 0
		}

		position := from
		for position < to {
			span := spans[spanIndex]
			end := min(to, span.end)
			appendValue(span.tokenType, text[position:end])
			position = end
			if position == span.end {
				spanIndex++
			}
		}
		if cluster == "\n" {
			column = 0
		} else {
			column += graphemes.Width()
		}
	}
	return wrapped
}

func trimPartialUTF8Suffix(content []byte) []byte {
	if len(content) == 0 {
		return content
	}
	start := len(content) - 1
	for start > 0 && !utf8.RuneStart(content[start]) {
		start--
	}
	if !utf8.FullRune(content[start:]) {
		return content[:start]
	}
	return content
}
