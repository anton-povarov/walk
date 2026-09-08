# 🥾 walk

<p align="center">
  <br>
  <img src=".github/images/demo.gif" width="600" alt="walk demo">
  <br>
</p>

**Walk** — a terminal navigator; a `cd` and `ls` replacement.

Run `lk`, navigate using arrows or hjkl. Press, `esc` to jump to a new location; or `ctrl+c` to stay.

## Install

```
go install github.com/anton-povarov/walk@latest
```

```
curl https://raw.githubusercontent.com/anton-povarov/walk/master/install.sh | sh
```

### Setup

Put the next function into the **.bashrc** or a similar config:

<table>
<tr>
  <th> Bash/Zsh </th>
  <th> Fish </th>
  <th> PowerShell </th>
</tr>
<tr>
<td>

```bash
function lk {
  cd "$(walk "$@")"
}
```

</td>
<td>

```fish
function lk
  set loc (walk $argv); and cd $loc;
end
```

</td>
<td>

```powershell
function lk() {
  cd $(walk $args)
}
```

</td>
</tr>
</table>


Now use `lk` command to start walking.

## Features

### Preview mode

Press `Space` to toggle preview mode.

<img src=".github/images/preview-mode.gif" width="600" alt="Walk Preview Mode">

### Delete file or directory

Press `dd` to delete file or directory. Press `u` to undo.

<img src=".github/images/rm-demo.gif" width="600" alt="Walk Deletes a File">

### Display icons

Install [Nerd Fonts](https://www.nerdfonts.com) and add `--icons` flag.

<img src=".github/images/demo-icons.gif" width="600" alt="Walk Icons Support">

### Image preview

No additional setup is required. Preview PNG, JPEG, and GIF files with `Space`
or `--preview`. GIF previews show a single frame.

Walk uses `go-termimg` for high-resolution images in iTerm2 and terminals detected
as supporting Kitty graphics. iTerm2 is explicitly preferred when both protocols
are detected. Other terminals, tmux, and screen use the existing Unicode
half-block renderer.

Images fit the preview pane without stretching, with Lanczos scaling and a 5×
enlargement limit. Preparation runs in the background and is cached until the
file or pane dimensions change. Images have equal two-cell left and right
padding inside the preview pane. Kitty uses normal placements (as in the spike),
with a trailing row reserved to prevent cursor movement from scrolling the screen.
Kitty preserves transparency; the pinned
`go-termimg` iTerm2 backend encodes JPEG, so transparent areas appear black.

Set `WALK_IMAGE_PROTOCOL=auto|iterm2|kitty|halfblocks` to choose a backend
(`auto` is the default). Explicit graphics selection is intended for compatible
terminals whose environment-based detection fails; multiplexers still use
half-blocks. Walk does not query terminal features during startup. Cell
dimensions use terminal-specific defaults and are refreshed from terminal
window metrics when available.

For implementation details, rendering constraints, known risks, and regression
checks, see the [image preview developer guide](IMAGE_PREVIEWS.md).

<img src=".github/images/images-mode.gif" width="600" alt="Walk Image Preview">

## Usage

| Key binding                          | Description        |
|--------------------------------------|--------------------|
| <kbd>arrows</kbd>, <kbd>hjkl</kbd>   | Move cursor        |
| <kbd>shift</kbd> + <kbd>arrows</kbd> | Jump to start/end  |
| <kbd>enter</kbd>                     | Enter directory    |
| <kbd>backspace</kbd>                 | Exit directory     |
| <kbd>space</kbd>                     | Toggle preview     |
| <kbd>tab</kbd>                       | Open/switch preview pane |
| <kbd>↑</kbd>/<kbd>↓</kbd>, <kbd>j</kbd>/<kbd>k</kbd> | Scroll focused preview |
| <kbd>page up</kbd>/<kbd>page down</kbd>, <kbd>b</kbd>/<kbd>f</kbd> | Page focused preview |
| <kbd>esc</kbd>, <kbd>q</kbd>         | Exit with cd       |
| <kbd>ctrl</kbd> + <kbd>c</kbd>       | Exit without cd    |
| <kbd>/</kbd>                         | Fuzzy search       |
| <kbd>d</kbd>, <kbd>delete</kbd>      | Delete file or dir |
| <kbd>y</kbd>                         | yank current dir   |
| <kbd>.</kbd>                         | Hide hidden files  |

## Configuration

The `EDITOR` or `WALK_EDITOR` environment variable used for opening files from
the walk.

```bash
export EDITOR=vim
```

To specify a command to be used to open files per extension, use the `WALK_OPEN_WITH` environment variable.

```bash
export WALK_OPEN_WITH="txt:less -N;go:vim;md:glow -p"
```

The `WALK_REMOVE_CMD` environment variable can be used to specify a command to
be used to remove files. This is useful if you want to use a different
command to remove files than the default `rm`.

```bash
export WALK_REMOVE_CMD=trash
```

Change main color with `WALK_MAIN_COLOR` environment variable. Available colors
are [here](https://github.com/charmbracelet/lipgloss#colors).

```bash
export WALK_MAIN_COLOR="#0000FF"
```

Set `WALK_HIGHLIGHT_THEME` to a Chroma style name to override the syntax
highlighting theme.

```bash
export WALK_HIGHLIGHT_THEME="monokai"
```

Set `WALK_NO_HIGHLIGHT` to disable syntax highlighting.

```bash
export WALK_NO_HIGHLIGHT=1
```

Use `WALK_STATUS_BAR` environment variable to specify a [status bar](STATUS_BAR.md) program.

```bash
export WALK_STATUS_BAR="Size() + ' ' + Mode()"
```

### Flags

Flags can be used to change the default behavior of the program.

| Flag            | Description                 |
|-----------------|-----------------------------|
| `--icons`       | Show icons                  |
| `--dir-only`    | Show dirs only              |
| `--hide-hidden` | Hide hidden files           |
| `--preview`     | Start with preview mode on  |
| `--with-border` | Show border in preview mode |
| `--fuzzy`       | Start with fuzzy search on  |

## License

[MIT](LICENSE)
