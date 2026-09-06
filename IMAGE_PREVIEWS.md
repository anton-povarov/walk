# Image previews: developer guide

This describes the production implementation as of September 2026. Start with
[graphics.go](graphics.go), then follow its integration in
[preview.go](preview.go), [layout.go](layout.go), and [main.go](main.go).

## Scope and design constraints

Walk previews PNG, JPEG, and GIF files. GIFs show a single decoded frame; there
is no animation, image scrolling, or interactive zoom. High-resolution previews
fit inside the RHS pane. The original Unicode half-block implementation in
[image.go](image.go) remains the backup renderer, unchanged.

Use `github.com/blacktop/go-termimg` for graphics encoding. Do not introduce a
custom Kitty encoder or a second half-block renderer. iTerm2 support is a required
backend, not an optional follow-up. The current dependency is pinned to v0.1.24;
several details below are specific to that version.

## Backend selection and terminal discovery

`newImagePreview()` runs before `tea.Program.Run()`. Terminal capability queries
must finish before Bubble Tea starts reading input. `go-termimg` caches its
features; later `Render()` calls reuse them.

| `WALK_IMAGE_PROTOCOL` | Selection |
| --- | --- |
| Unset, empty, or `auto` | Prefer iTerm2 when detected, then Kitty, otherwise half-blocks |
| `iterm2` | Force the library's iTerm2 backend |
| `kitty` | Force the library's Kitty backend |
| `halfblocks` | Use the existing renderer without graphics detection |
| Other value | Fall back to half-blocks; currently no configuration error is shown |

All modes fall back to half-blocks when stderr is not a terminal, or `TMUX` or
`STY` is set. Explicit protocol selection does not override those guards.
Multiplexer passthrough is deliberately unvalidated and disabled. No Sixel
backend is integrated even if the library reports support.

The iTerm2 preference is intentional: the spike encountered detection that also
reported Kitty support for iTerm2. Do not replace the explicit choice with the
library's `Auto` renderer.

Cell pixel dimensions come from the startup feature query. Invalid dimensions
fall back to 8×16. For iTerm2, width and height are swapped when width exceeds
height, matching the spike's workaround for the library's reversed dimensions.
On Unix, each image request also checks stderr's `TIOCGWINSZ` pixel/cell metrics;
usable values override the startup dimensions. Windows retains startup values.
This avoids another terminal-input query during rendering.

## Preparation and model integration

1. `View()` derives the actual LHS width and resizes the preview viewport before
   calling `preview()`. Image dimensions therefore follow the real pane layout,
   not an assumed 50/50 split.
2. For a supported image, `imagePreview.request()` constructs a key from absolute
   path, modification time, file size, image area in cells, and cell pixel size.
3. A single worker decodes, scales, and encodes. A one-entry queue replaces a
   pending request with the latest selection. Work already executing is not
   cancelled. The worker publishes only if its key still equals `wanted`.
4. Completion sends `imageReadyMsg` through `Program.Send()`, causing a new view.
   The message carries no image; the result is held behind the preview mutex.
   A result is returned only when its key matches the current request.
5. While pending, the image pane is blank. A ready image is attached to the
   frame; its text viewport remains blank to reserve layout space.

This is a single-result cache, not an LRU or a decoded-source cache. Resizing can
decode the source again. Repeated views with the same key reuse the result;
failed results are cached too. File content changes that preserve both size and
modification time are not detected. Protocol is fixed for the process and is
therefore not part of the key.

If graphics encoding fails, the worker calls the existing `drawImage()` and
caches that fallback text. Open/decode/size-limit failures instead show
`No image preview available`. Unsupported terminals use `drawImage()` directly
in the original synchronous preview path. A terminal silently rejecting a
graphics command does not produce a Go error or trigger fallback.

## Scaling, geometry, and alpha

The image area is `previewViewport.Width - 1` cells wide, clamped to at least one
cell. The viewport already reserves one terminal cell on the right; the extra
cell makes graphics match the two-cell left padding. Text and half-block preview
widths are unchanged. With borders, distinguish the inter-pane margin and border
from the padding inside the RHS pane.

Image coordinates are one-based: column `leftWidth + horizontalFrameSize + 1`,
row 2 below the filename header. This assumes the current styles have their
horizontal frame entirely on the left and no top frame. Revisit the calculation
if adding right padding, a top border, or another header. Images that would
extend beyond the terminal's right edge are suppressed on very narrow windows.

iTerm2 uses the viewport's height. Kitty reserves one additional bottom row:
normal placements advance the cursor below the image, and an image reaching the
last terminal row could scroll the screen before the saved cursor is restored.

`fitGraphic()` uses one scale factor, bounded by pane width, pane height, and a
5× enlargement cap. `nfnt/resize` applies Lanczos3 interpolation. The result is
centered on an NRGBA canvas whose pixels exactly match the cell placement;
integer rounding can leave one pixel of asymmetry inside that canvas.
`ScaleNone` prevents `go-termimg` from doing another resize.

**Keep `clampGraphicAlpha()` between resizing and conversion to NRGBA.** Lanczos
can overshoot at translucent edges. The resizer clamps channels independently,
so its premultiplied RGBA output can contain RGB greater than alpha. Converting
that invalid value into straight-alpha NRGBA can overflow and wrap the color,
creating dark or colored speckles. Clamp each RGB component to alpha first,
including 16-bit RGBA. `img/1.png` reproduced this around the heart boundary at
5× enlargement; `graphics_alpha_test.go` preserves that regression case.

Kitty requests PNG transport to preserve alpha. The pinned iTerm2 implementation
encodes JPEG even though `PNG(true)` is requested on the image builder. It is
lossy and transparent areas become black. Fixing this requires a suitable
library change or upgrade, not an assumption that the PNG option applies to
both backends.

## Coordinating graphics with Bubble Tea

Do not put graphics payloads through the viewport, ANSI wrapping, truncation,
or Lip Gloss pane joining. Do not write from the worker directly to the terminal.

`graphicsOutput.frame()` registers an immutable image/position snapshot and
returns a private zero-width OSC marker containing its serial number. `View()`
prepends this marker to every view, including help and quitting views with no
image. The changing marker ensures Bubble Tea emits the first line even when
the text is otherwise unchanged.

`graphicsOutput.Write()` associates the renderer's text diff with that snapshot,
removes the marker, and serializes these operations under one output mutex:

1. Clear the old image when the frame changes, the screen is cleared, or the
   alternate screen is being left.
2. Write Bubble Tea's text output.
3. Save the cursor, move to the image origin, emit graphics when needed, and
   restore the cursor.

Kitty uses **normal placements**, uploaded on an image/placement change and
after clearing. Unchanged Kitty frames do not upload again. iTerm2 images are
emitted again on every marked frame because text redraws can erase inline images.
Their encoded bytes are cached, but terminal transport still costs bandwidth.

Kitty cleanup uses `ansi.KittyGraphics` to delete only the active image ID and
its data (`d=I`). Cell erasure clears the old rectangle for both protocols.
Do not use `go-termimg`'s global clear helpers: they are too broad, and its
direct clear methods write to stdout. Help, text selections, preview toggling,
resize, editor handoff, and quit must all release or replace the active image.
The adapter recognizes `ESC[2J` and `ESC[?1049l`; shutdown also attempts cleanup.

**stdout is Walk's shell integration result.** All UI and image output belongs
on stderr. `graphicsTerminal` wraps stderr while preserving the terminal-file
interface Bubble Tea needs for sizing and console setup. Override both `Write`
and `WriteString`: an inherited `os.File.WriteString` would bypass the adapter.
A plain `io.Writer` wrapper also loses Bubble Tea's terminal-size detection.

## Why Unicode placeholders were removed

The v0.1.24 placeholder table diverges from Kitty's coordinate table at index 62:
it includes U+0653/U+0654 where the protocol proceeds to U+0657. Wide images then
encode incorrect columns and appear as overlapping, horizontally shifted strips.
The library's virtual-placement path also omits explicit cell dimensions.
Normal placements use explicit `c`/`r` and bypass the faulty table entirely.

The discrepancy was checked against Kitty's
[official coordinate table](https://sw.kovidgoyal.net/kitty/_downloads/f0a0de9ec8d9ff4456206db8e0814937/rowcolumn-diacritics.txt)
and [graphics specification](https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders).
Users confirmed the tiling and unequal-margin fixes in cmux/Ghostty; iTerm2 was
reported working before this change. Do not re-enable placeholders merely
because the library exposes `Virtual(true)`. Any future adoption needs library
corrections and real terminal validation across widths beyond 62 cells.

## Limits and risks

- Source images are limited to 64 million pixels via `DecodeConfig`; destination
  canvases to 32 million pixels. These are allocation guards, not memory budgets.
  Decoding, resize intermediates, canvas, PNG/JPEG, base64, and terminal storage
  can coexist. There is no compressed-file byte limit or decode timeout.
- The worker bounds concurrency but cannot interrupt a slow decode. Closing its
  stop channel does not join an in-flight operation; process exit ends it.
- The output adapter depends on Bubble Tea v1.3.2 emitting a complete diff per
  `Write` and preserving the OSC marker. It is not a general streaming escape
  parser: markers split across writes are unsupported. The frame registry keeps
  at most nine recent snapshots; an older pruned marker resolves to no image.
- Screen clearing, suspend/resume, and editor return depend on the terminal
  sequences Bubble Tea emits. Out-of-band terminal resets are not independently
  detected. Review these assumptions when upgrading Bubble Tea or changing its
  renderer/output wrappers.
- Capability and cell-size discovery is heuristic. Font or display-scale changes
  may leave stale dimensions when the OS does not report usable pixel metrics.
  The iTerm2 width/height swap is also a heuristic, not a general font model.
- Kitty commands are quiet; there is no runtime acknowledgement/health check.
  For a blank preview, compare the forced backend and `halfblocks` mode.
- `go-termimg` brings transitive updates to ANSI/width and terminal libraries.
  Run the existing text/layout suite on dependency upgrades, not just image tests.

## Verification and troubleshooting

Run `go test -race ./...`, `go vet ./...`, and `go build ./...` after changes.
The image tests cover backend selection, encoded placements beyond 62 columns,
scaling and centering, alpha overshoot, request replacement/cache keys, margins,
bottom-row safety, targeted deletion, and terminal descriptor preservation.
They also run the actual Bubble Tea renderer with a captured output writer to
exercise markers, redraws, and image-to-text transitions.

Those tests verify geometry, pixel values, and output bytes; they do not emulate
terminal graphics or prove visual behavior. macOS/Linux/Windows builds have been
checked during implementation, but cross-compilation is not runtime coverage.
The heart-edge fix has a passing pixel regression test; visual confirmation is
separate. Keep manual verification in the development workflow:

1. Run `go run . --preview img` in iTerm2 and Ghostty/cmux. Inspect `1.png` for
   edge speckles and use a larger image to check gradients, aspect ratio, and
   equal margins. Exercise pane widths above 62 columns.
2. Navigate rapidly through images and text, toggle preview, switch focus, show
   help, resize with/without borders, open an external editor, and return.
3. Check that only the current image remains, nothing scrolls or covers the
   filename/LHS, and quitting restores the shell without image remnants.
4. Repeat with `WALK_IMAGE_PROTOCOL=halfblocks`; check multiplexer fallback.
   Redirect stdout to a temporary file and verify it contains only the selected
   path on a normal exit, never image data or control sequences.

For shifted strips, inspect placement mode before blaming decoding. For colored
speckles, inspect premultiplied-alpha invariants before changing interpolation.
For clipped/asymmetric images, check cell metrics and frame geometry. For missing
images after a redraw or editor return, check marker delivery and cleanup state.
