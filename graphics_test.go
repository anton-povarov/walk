package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	termimg "github.com/blacktop/go-termimg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	xterm "github.com/charmbracelet/x/term"
)

func TestGraphicsTerminalPreservesDescriptorAndInterceptsStrings(t *testing.T) {
	var out bytes.Buffer
	w := &graphicsOutput{out: &out}
	terminal := &graphicsTerminal{File: os.Stderr, graphics: w}
	var file xterm.File = terminal
	if file.Fd() != os.Stderr.Fd() {
		t.Fatal("terminal descriptor lost")
	}
	if _, err := io.WriteString(terminal, w.frame(graphicsFrame{})+"TEXT"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "TEXT" {
		t.Fatalf("write bypassed adapter: %q", out.String())
	}
}

func TestPrepareGraphicBackends(t *testing.T) {
	t.Setenv("TERMIMG_BYPASS_DETECTION", "kitty")
	var pngData bytes.Buffer
	source := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	draw.Draw(source, source.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if err := png.Encode(&pngData, source); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/test.png"
	if err := os.WriteFile(path, pngData.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise widths beyond column 62, where the library's placeholder table
	// diverges from the protocol and used to produce overlapping image strips.
	key := graphicsKey{path: path, width: 100, height: 3, cellWidth: 8, cellHeight: 16}
	for _, protocol := range []termimg.Protocol{termimg.Kitty, termimg.ITerm2} {
		got := prepareGraphic(key, protocol)
		if got.err != nil {
			t.Fatal(got.err)
		}
		if protocol == termimg.Kitty {
			if strings.Contains(got.sequence, "U=1") || strings.Contains(got.sequence, termimg.PLACEHOLDER_CHAR) ||
				!strings.Contains(got.sequence, "c=100,r=3") || got.id == 0 {
				t.Fatal("expected one normal Kitty placement with explicit dimensions")
			}
		} else if !strings.Contains(got.sequence, "1337;File=") || !strings.Contains(got.sequence, "width=800px;height=48px") {
			t.Fatalf("invalid iTerm sequence: %.150q", got.sequence)
		}
	}
	key.path = "missing.png"
	if got := prepareGraphic(key, termimg.Kitty); got.err == nil {
		t.Fatal("missing file accepted")
	}
	key.width = 0
	if got := prepareGraphic(key, termimg.Kitty); got.err == nil {
		t.Fatal("invalid dimensions accepted")
	}
}

type graphicsRenderTestModel struct {
	output *graphicsOutput
	frame  graphicsFrame
	step   int
}

func (m *graphicsRenderTestModel) Init() tea.Cmd { return nil }
func (m *graphicsRenderTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(imageReadyMsg); ok {
		m.step++
	}
	return m, nil
}
func (m *graphicsRenderTestModel) View() string {
	f := m.frame
	if m.step >= 2 {
		f = graphicsFrame{}
	}
	return m.output.frame(f) + fmt.Sprintf("step%d\n                    \n                    ", m.step)
}

type observedGraphicsWriter struct {
	bytes.Buffer
	frames chan string
}

func (w *observedGraphicsWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *observedGraphicsWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if strings.Contains(string(p), "step") {
		w.frames <- string(p)
	}
	return n, err
}

func TestGraphicsThroughBubbleTeaRenderer(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		t.Run(fmt.Sprintf("kitty=%v", kitty), func(t *testing.T) {
			out := &observedGraphicsWriter{frames: make(chan string, 10)}
			writer := &graphicsOutput{out: out}
			graphic := &preparedGraphic{sequence: "UPLOAD", width: 3, height: 2}
			if kitty {
				graphic.id = 42
			}
			m := &graphicsRenderTestModel{output: writer, frame: graphicsFrame{graphic, 10, 2}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(writer), tea.WithAltScreen(), tea.WithoutSignalHandler())
			done := make(chan error, 1)
			go func() { _, err := p.Run(); done <- err }()
			defer p.Kill()
			for step := 0; step < 3; step++ {
				var frame string
				select {
				case frame = <-out.frames:
				case <-ctx.Done():
					t.Fatal("renderer timed out")
				}
				if strings.Contains(frame, graphicsMarker) {
					t.Fatal("OSC marker leaked through renderer")
				}
				wantUpload := step == 0 || (!kitty && step == 1)
				if strings.Contains(frame, "UPLOAD") != wantUpload {
					t.Fatalf("step %d: %q", step, frame)
				}
				if step == 2 && kitty && !strings.Contains(frame, "d=I,i=42") {
					t.Fatal("image not deleted on text transition")
				}
				if step < 2 {
					p.Send(imageReadyMsg{})
				}
			}
			p.Quit()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImageProtocolSelection(t *testing.T) {
	for _, tc := range []struct {
		mode         string
		iterm, kitty bool
		want         termimg.Protocol
	}{
		{"auto", true, true, termimg.ITerm2},
		{"", false, true, termimg.Kitty},
		{"auto", false, false, termimg.Auto},
		{"halfblocks", true, true, termimg.Auto},
		{"kitty", true, false, termimg.Kitty},
		{"iterm2", false, true, termimg.ITerm2},
	} {
		if got := selectImageProtocol(tc.mode, tc.iterm, tc.kitty); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

func TestFitGraphic(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		source, canvas, occupied image.Point
	}{
		{"wide", image.Pt(200, 100), image.Pt(100, 100), image.Pt(100, 50)},
		{"tall", image.Pt(100, 200), image.Pt(100, 100), image.Pt(50, 100)},
		{"upscale cap", image.Pt(2, 1), image.Pt(100, 100), image.Pt(10, 5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := image.NewNRGBA(image.Rectangle{Max: tc.source})
			draw.Draw(src, src.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			got := fitGraphic(src, tc.canvas.X, tc.canvas.Y)
			var occupied image.Rectangle
			for y := 0; y < tc.canvas.Y; y++ {
				for x := 0; x < tc.canvas.X; x++ {
					_, _, _, a := got.At(x, y).RGBA()
					if a != 0 {
						occupied = occupied.Union(image.Rect(x, y, x+1, y+1))
					}
				}
			}
			if got.Bounds().Size() != tc.canvas || occupied.Size() != tc.occupied {
				t.Fatalf("canvas %v, occupied %v", got.Bounds(), occupied)
			}
			if occupied.Min != image.Pt((tc.canvas.X-tc.occupied.X)/2, (tc.canvas.Y-tc.occupied.Y)/2) {
				t.Fatalf("not centered: %v", occupied)
			}
		})
	}
}

func TestGraphicsOutputLifecycle(t *testing.T) {
	var out bytes.Buffer
	w := &graphicsOutput{out: &out}
	graphic := &preparedGraphic{sequence: "UPLOAD", id: 42, width: 3, height: 2}
	f := graphicsFrame{graphic, 10, 2}
	write := func(frame graphicsFrame, text string) string {
		t.Helper()
		out.Reset()
		data := w.frame(frame) + text
		n, err := w.Write([]byte(data))
		if err != nil || n != len(data) {
			t.Fatalf("write: %d, %v", n, err)
		}
		if strings.Contains(out.String(), graphicsMarker) {
			t.Fatal("private marker leaked")
		}
		return out.String()
	}
	first := write(f, "TEXT")
	if strings.Index(first, "UPLOAD") < strings.Index(first, "TEXT") || !strings.Contains(first, "\x1b[2;10HUPLOAD") {
		t.Fatalf("bad frame: %q", first)
	}
	redraw := write(f, "REDRAW")
	if strings.Contains(redraw, "UPLOAD") {
		t.Fatalf("bad cached redraw: %q", redraw)
	}
	reset := write(f, "\x1b[2JCLEAR")
	if strings.Count(reset, "UPLOAD") != 1 || !strings.Contains(reset, "d=I,i=42") {
		t.Fatalf("screen clear did not replace the placement exactly once: %q", reset)
	}
	clear := write(graphicsFrame{}, "HELP")
	if !strings.Contains(clear, "d=I,i=42") || strings.Index(clear, "d=I") > strings.Index(clear, "HELP") || strings.Contains(clear, "UPLOAD") {
		t.Fatalf("bad clear: %q", clear)
	}
	write(f, "RETURN")
	out.Reset()
	w.Write([]byte("\x1b[?1049l"))
	if !strings.Contains(out.String(), "d=I,i=42") || w.active.image != nil {
		t.Fatal("exit left image active")
	}
	if got := write(f, "RESUME"); !strings.Contains(got, "UPLOAD") {
		t.Fatal("resume did not upload")
	}
}

func TestITermRedrawAndCoalescing(t *testing.T) {
	var out bytes.Buffer
	w := &graphicsOutput{out: &out}
	f := graphicsFrame{&preparedGraphic{sequence: "ITERM", width: 2, height: 2}, 5, 2}
	for i := 0; i < 20; i++ {
		w.frame(graphicsFrame{})
	}
	if len(w.frames) > 9 {
		t.Fatal("unbounded pending frames")
	}
	for i := 0; i < 2; i++ {
		out.Reset()
		marker := w.frame(f)
		if ansi.StringWidth(marker+"hello") != 5 {
			t.Fatal("marker occupies cells")
		}
		w.Write([]byte(ansi.Truncate(marker+"hello", 5, "")))
		if strings.Count(out.String(), "ITERM") != 1 {
			t.Fatal("iTerm image not repainted")
		}
	}
}

func TestImageRequestsReplacePendingAndRejectStale(t *testing.T) {
	info, err := os.Stat("image.go")
	if err != nil {
		t.Fatal(err)
	}
	p := &imagePreview{cellWidth: 8, cellHeight: 16, requests: make(chan graphicsKey, 1)}
	p.request("a.png", info, 10, 10)
	a := p.wanted
	p.request("b.png", info, 20, 10)
	b := p.wanted
	if queued := <-p.requests; queued != b {
		t.Fatal("queue retained old request")
	}
	p.ready, p.result = a, &preparedGraphic{}
	if got := p.request("b.png", info, 20, 10); got != nil {
		t.Fatal("stale image returned")
	}
	p.ready = b
	if got := p.request("b.png", info, 20, 10); got != p.result {
		t.Fatal("cache miss")
	}
}

func TestGraphicPaneGeometryAndVisibility(t *testing.T) {
	oldBorder := withBorder
	t.Cleanup(func() { withBorder = oldBorder })
	for _, border := range []bool{false, true} {
		for _, width := range []int{3, 30, 80, 120} {
			withBorder = border
			m := newTestModel(t, map[string]string{"test.png": ""})
			m.termWidth = width
			m.previewMode = true
			m.images = &imagePreview{cellWidth: 8, cellHeight: 16, requests: make(chan graphicsKey, 1)}
			m.graphics = &graphicsOutput{out: io.Discard}
			m.View()
			key := m.images.wanted
			m.images.ready = key
			m.images.result = &preparedGraphic{width: key.width, height: key.height}
			m.View()
			frame := m.graphics.frames[m.graphics.serial]
			if width >= 30 {
				leftMargin := m.previewStyle().GetPaddingLeft()
				rightMargin := width - (frame.x + frame.image.width - 1)
				if rightMargin != leftMargin {
					t.Fatalf("border=%v width=%d: left margin %d, right margin %d", border, width, leftMargin, rightMargin)
				}
				if frame.image == nil || frame.y != 2 || frame.x <= 1 || frame.x+frame.image.width-1 > width || frame.y+frame.image.height-1 > m.termHeight {
					t.Fatalf("border=%v width=%d: invalid frame %+v", border, width, frame)
				}
			} else if frame.image != nil {
				t.Fatal("image rendered outside narrow terminal")
			}
			m.showHelp = true
			m.View()
			if m.graphics.frames[m.graphics.serial].image != nil {
				t.Fatal("image visible over help")
			}
			m.showHelp = false
			m.quitting = true
			m.View()
			if m.graphics.frames[m.graphics.serial].image != nil {
				t.Fatal("image visible while quitting")
			}
		}
	}
}

func TestKittyPlacementLeavesCursorRoom(t *testing.T) {
	m := newTestModel(t, map[string]string{"test.png": ""})
	m.previewMode = true
	m.images = &imagePreview{protocol: termimg.Kitty, cellWidth: 8, cellHeight: 16, requests: make(chan graphicsKey, 1)}
	m.View()
	if bottomCursorRow := 2 + m.images.wanted.height; bottomCursorRow > m.termHeight {
		t.Fatalf("placement advances cursor beyond terminal: %d > %d", bottomCursorRow, m.termHeight)
	}
}
