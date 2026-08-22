# Syntax highlighting

Walk renders syntax-highlighted text previews in the right-hand pane using
[Chroma v2](https://github.com/alecthomas/chroma). Highlighting is in-process
and pure Go; it does not invoke an external pager or highlighter.

The implementation lives primarily in `highlight.go`, with file loading,
caching, viewport integration, and row isolation in `main.go`.

## Rendering pipeline

Every text file uses the same rendering path, whether highlighting is enabled
or disabled:

```text
file bytes
  -> UTF-8 validation and safe normalization
  -> filename/content lexer selection
  -> tokenize the complete source
  -> grapheme- and cell-aware token wrapping
  -> profile-appropriate Chroma formatter (or no-op formatter)
  -> per-row SGR reset
  -> preview viewport
```

Disabling highlighting selects Chroma's no-op formatter. It does not bypass
Unicode normalization or cell-aware wrapping.

Directory listings, generated warnings, and image previews are not source text,
so they remain on their existing rendering paths. They still pass through the
shared viewport row-reset boundary.

## Input handling and terminal safety

`normalizePreviewText` accepts only valid UTF-8. Invalid UTF-8 retains the
existing binary-file behavior and displays `No preview available`.

For valid text, normalization:

- preserves printable Unicode, combining characters, wide glyphs, emoji, and
  the ZWJ/ZWNJ characters used by joined grapheme clusters;
- converts CRLF and lone CR to LF;
- expands tabs to the next four-cell tab stop, resetting the column after each
  newline;
- removes NUL, ESC, DEL, C0/C1 controls, and terminal-sequence introducers.

Source files therefore cannot inject OSC, CSI, or other terminal commands into
the preview. ANSI emitted later by Chroma or Walk's own UI remains trusted.

Walk reads at most 100 KiB of a text file. If the byte limit lands inside a
UTF-8 encoding, `trimPartialUTF8Suffix` removes only that incomplete trailing
rune before validation. Complete invalid bytes are not hidden by truncation.

## Lexer selection

`selectPreviewLexer` uses this order:

1. `lexers.Match(path)` for filename and extension matching.
2. `lexers.Analyse(content)` for recognizable extensionless content.
3. `lexers.Fallback` for readable plaintext.

Filename matching intentionally wins over content analysis. Chroma tokenizes
the complete normalized source before Walk inserts any soft line breaks, so a
wrap cannot alter lexer state inside strings, comments, fenced blocks, or other
multiline constructs.

Token iterator panics are recovered and returned as preview errors rather than
crashing the application.

## Grapheme- and cell-aware wrapping

`wrapPreviewTokens` wraps the token stream to the current RHS display width.
It uses `github.com/rivo/uniseg` to iterate Unicode grapheme clusters and count
terminal cells.

The wrapper:

- never splits a grapheme cluster, including clusters crossing Chroma token
  boundaries;
- handles zero-width combining clusters and double-width glyphs;
- preserves source whitespace and indentation;
- performs deterministic hard wrapping rather than prose reflow;
- resets the display column after real and inserted newlines;
- retains the original Chroma token type for every source byte.

An inserted newline receives the type of the token being wrapped. Chroma's
terminal formatters consequently reset styling at the line ending and reapply
the same token style at the first visible character of the continuation row.

Highlighted text is already wrapped when it reaches
`setPreviewContentWrapped`, so it is not passed through a second wrapping step.

## Terminal color profiles

Startup detects the terminal's `termenv.Profile` once and passes an explicit
formatter name into the renderer:

| termenv profile | Chroma formatter | Output |
| --- | --- | --- |
| `termenv.TrueColor` | `terminal16m` | 24-bit color |
| `termenv.ANSI256` | `terminal256` | 256 colors |
| `termenv.ANSI` | `terminal16` | 16 colors |
| `termenv.Ascii` | `noop` | Plain text |

The renderer does not inspect TTY state or environment variables itself. This
keeps it deterministic in tests and allows ASCII terminals to degrade safely.

## Themes and background detection

`WALK_HIGHLIGHT_THEME` accepts a Chroma style name. Walk validates the value
against Chroma's registered styles; an unknown name falls back to the default
for the detected background.

Default themes are defined by `defaultDarkHighlightTheme` and
`defaultLightHighlightTheme` in `highlight.go`. Currently they are:

| Background | Theme |
| --- | --- |
| Dark or unknown | `nordic` |
| Light | `catppuccin-latte` |

Background detection uses the last numeric component of `COLORFGBG`. Values 7
and above are treated as light. A missing or malformed value defaults to dark.
Walk deliberately does not issue a synchronous OSC background query, which
could delay startup.

An explicit valid `WALK_HIGHLIGHT_THEME` always wins. `WALK_NO_HIGHLIGHT`
selects plain output while retaining the rest of the text pipeline.

## Viewport integration and ANSI isolation

`setPreviewContentWrapped` adds an SGR reset before every newline and after the
final row before content enters the viewport. This is a second safety boundary
in addition to Chroma's own per-token resets: no RHS style may leak into the LHS
when Bubble Tea and Lip Gloss join the panes.

The highlighting implementation does not change pane sizing, the one-cell RHS
margin, dynamic LHS/RHS width allocation, scrolling keys, or focus behavior.
Changing the selected path resets the RHS offset. Redrawing the same path keeps
the offset, and resizing rewraps content and clamps the offset to the new range.

## Preview cache

`model.preview` runs during `View`, so Walk caches the most recently rendered
text preview. The cache key contains:

- absolute file path;
- modification time in nanoseconds;
- file size;
- preview width;
- Chroma formatter/color profile;
- selected theme;
- highlighting-enabled state.

The cached value is the fully rendered, already wrapped text. A matching cache
entry is checked before opening or reading the file. Width, file metadata,
profile, theme, or highlighting changes force a reread and rerender.

Errors are not cached, so a file that later becomes readable is retried.
Directory and image previews retain their existing behavior rather than sharing
the text-preview cache.

## Tests

Pure rendering behavior is covered in `highlight_test.go`:

- Unicode preservation, newline normalization, tab stops, and invalid UTF-8;
- stripping ESC, OSC/CSI introducers, C0/C1 controls, DEL, and NUL;
- grapheme-safe, terminal-cell-aware wrapping across token boundaries;
- exact token-style continuation after soft wraps in strings and comments;
- truecolor, ANSI256, ANSI16, and ASCII formatter output;
- filename precedence, content analysis, and plaintext fallback;
- dark/light defaults, explicit theme overrides, and invalid themes;
- safe UTF-8 truncation at the 100 KiB boundary.

Integration and regression behavior is covered in `main_test.go`:

- every rendered RHS row ends behind an SGR reset barrier;
- cached previews are reused and invalidated on file or width changes;
- path changes reset scrolling while resize and focus behavior remain stable;
- directory, image, warning, and binary previews retain their behavior;
- pane widths, borders, dynamic layout, and the one-cell margin are unchanged.

Run the complete verification suite after modifying this pipeline:

```sh
gofmt -w main.go main_test.go highlight.go highlight_test.go utils.go usage.go
go test ./...
go vet ./...
go build ./...
```

For visual changes, also inspect a Go file with a long string, Markdown with a
fenced block, YAML, Unicode-heavy text, and an extensionless recognizable file
in a truecolor terminal.

## Dependencies

The highlighting implementation requires Go 1.25 and uses:

- `github.com/alecthomas/chroma/v2` v2.27.0 for lexing, styles, and terminal
  formatting;
- `github.com/rivo/uniseg` for grapheme segmentation and terminal-cell widths;
- `github.com/muesli/termenv` for terminal profile detection;
- `github.com/charmbracelet/x/ansi` for ANSI-aware viewport operations and
  reset handling.
