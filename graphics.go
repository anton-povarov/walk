package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	termimg "github.com/blacktop/go-termimg"
	"github.com/charmbracelet/x/ansi"
	"github.com/nfnt/resize"
	"golang.org/x/term"
)

type imageReadyMsg struct{}

type graphicsKey struct {
	path                                 string
	modified, size                       int64
	width, height, cellWidth, cellHeight int
}

type preparedGraphic struct {
	sequence      string
	fallback      string
	id            uint32
	width, height int
	err           error
}

// A single worker bounds decoding and encoding work. Its queue keeps only the
// newest selection; results are usable only for the exact requested geometry.
type imagePreview struct {
	protocol              termimg.Protocol
	cellWidth, cellHeight int
	mu                    sync.Mutex
	wanted, ready         graphicsKey
	result                *preparedGraphic
	requests              chan graphicsKey
	stop                  chan struct{}
	notify                func()
}

func newImagePreview() *imagePreview {
	mode := os.Getenv("WALK_IMAGE_PROTOCOL")
	if mode == "halfblocks" || !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	// Passthrough and placeholder support vary between multiplexers. Keep the
	// existing renderer there until we can validate the complete redraw path.
	if os.Getenv("TMUX") != "" || os.Getenv("STY") != "" {
		return nil
	}
	// Terminal queries can take hundreds of milliseconds when one or more
	// responses are unsupported. They also cannot safely be deferred because
	// Bubble Tea will be reading from the same terminal. Environment detection
	// is immediate; unknown terminals use the half-block fallback unless the
	// user explicitly chooses a graphics protocol.
	iterm, kitty := imageProtocolSupportFromEnvironment()
	p := selectImageProtocol(mode, iterm, kitty)
	if p == termimg.Auto {
		return nil
	}
	f := cacheTerminalFeaturesWithoutQueries(p)
	if f == nil {
		return nil
	}
	w, h := f.FontWidth, f.FontHeight
	if w <= 0 || h <= 0 {
		w, h = 8, 16
	}
	if p == termimg.ITerm2 && w > h {
		w, h = h, w
	}
	return &imagePreview{protocol: p, cellWidth: w, cellHeight: h,
		requests: make(chan graphicsKey, 1), stop: make(chan struct{})}
}

func imageProtocolSupportFromEnvironment() (iterm, kitty bool) {
	switch strings.ToLower(os.Getenv("TERMIMG_BYPASS_DETECTION")) {
	case "iterm2":
		iterm = true
	case "kitty":
		kitty = true
	}
	return iterm || termimg.DetectITerm2FromEnvironment(),
		kitty || termimg.DetectKittyFromEnvironment()
}

// go-termimg asks for terminal features again while rendering, even when its
// protocol and scale are explicit. Prime its process-wide cache through the
// library's documented bypass so background image preparation never queries
// the terminal concurrently with Bubble Tea's input reader.
func cacheTerminalFeaturesWithoutQueries(protocol termimg.Protocol) *termimg.TerminalFeatures {
	var mode string
	switch protocol {
	case termimg.ITerm2:
		mode = "iterm2"
	case termimg.Kitty:
		mode = "kitty"
	default:
		return nil
	}

	previous, existed := os.LookupEnv("TERMIMG_BYPASS_DETECTION")
	if err := os.Setenv("TERMIMG_BYPASS_DETECTION", mode); err != nil {
		return nil
	}
	features := termimg.QueryTerminalFeatures()
	if existed {
		_ = os.Setenv("TERMIMG_BYPASS_DETECTION", previous)
	} else {
		_ = os.Unsetenv("TERMIMG_BYPASS_DETECTION")
	}
	return features
}

func selectImageProtocol(mode string, iterm, kitty bool) termimg.Protocol {
	switch mode {
	case "iterm2":
		return termimg.ITerm2
	case "kitty":
		return termimg.Kitty
	case "", "auto":
		if iterm {
			return termimg.ITerm2
		}
		if kitty {
			return termimg.Kitty
		}
	}
	return termimg.Auto
}

func (p *imagePreview) run() {
	for {
		select {
		case <-p.stop:
			return
		case key := <-p.requests:
			result := prepareGraphic(key, p.protocol)
			p.mu.Lock()
			current := p.wanted == key
			if current {
				p.ready, p.result = key, result
			}
			p.mu.Unlock()
			if current {
				p.notify()
			}
		}
	}
}

func (p *imagePreview) request(path string, info os.FileInfo, width, height int) *preparedGraphic {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	w, h := terminalCellSize(p.cellWidth, p.cellHeight)
	key := graphicsKey{abs, info.ModTime().UnixNano(), info.Size(), width, height, w, h}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wanted != key {
		p.wanted = key
		select {
		case <-p.requests:
		default:
		}
		p.requests <- key
	}
	if p.ready == key {
		return p.result
	}
	return nil
}

func prepareGraphic(key graphicsKey, protocol termimg.Protocol) *preparedGraphic {
	result := &preparedGraphic{width: key.width, height: key.height}
	if key.width <= 0 || key.height <= 0 || key.cellWidth <= 0 || key.cellHeight <= 0 ||
		int64(key.width)*int64(key.height)*int64(key.cellWidth)*int64(key.cellHeight) > 32_000_000 {
		result.err = fmt.Errorf("preview dimensions exceed image limits")
		return result
	}
	f, err := os.Open(key.path)
	if err != nil {
		result.err = err
		return result
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	// Bound allocations for malformed or unexpectedly enormous source images.
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64_000_000 {
		result.err = fmt.Errorf("image exceeds preview limits or cannot be decoded")
		return result
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		result.err = err
		return result
	}
	source, _, err := image.Decode(f)
	if err != nil {
		result.err = err
		return result
	}
	canvas := fitGraphic(source, key.width*key.cellWidth, key.height*key.cellHeight)
	// Use the same normal placement path as the spike. v0.1.24's Unicode
	// placeholder table has incorrect column coordinates starting at 62.
	img := termimg.New(canvas).Protocol(protocol).Scale(termimg.ScaleNone).
		Width(key.width).Height(key.height).PNG(true)
	result.sequence, result.err = img.Render()
	if result.err != nil {
		result.fallback, _ = drawImage(key.path, key.width, key.height)
	}
	if result.err == nil && protocol == termimg.Kitty {
		renderer, err := img.GetRenderer()
		if err != nil {
			result.err = err
			return result
		}
		result.id = renderer.(*termimg.KittyRenderer).GetLastImageID()
	}
	return result
}

func fitGraphic(source image.Image, width, height int) image.Image {
	b := source.Bounds()
	scale := min(float64(width)/float64(b.Dx()), float64(height)/float64(b.Dy()), 5.0)
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	scaled := resize.Resize(uint(w), uint(h), source, resize.Lanczos3)
	clampGraphicAlpha(scaled)
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	x, y := (width-w)/2, (height-h)/2
	draw.Draw(canvas, image.Rect(x, y, x+w, y+h), scaled, scaled.Bounds().Min, draw.Src)
	return canvas
}

// Lanczos has negative lobes. nfnt/resize clamps each channel independently,
// which can leave premultiplied RGB greater than alpha at translucent edges.
// Clamp to the valid premultiplied range before draw.Draw unpremultiplies into
// NRGBA; otherwise that conversion overflows and produces colored speckles.
func clampGraphicAlpha(img image.Image) {
	switch img := img.(type) {
	case *image.RGBA:
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			start := img.PixOffset(img.Rect.Min.X, y)
			row := img.Pix[start : start+img.Rect.Dx()*4]
			for x := 0; x < len(row); x += 4 {
				for channel := 0; channel < 3; channel++ {
					row[x+channel] = min(row[x+channel], row[x+3])
				}
			}
		}
	case *image.RGBA64:
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			start := img.PixOffset(img.Rect.Min.X, y)
			row := img.Pix[start : start+img.Rect.Dx()*8]
			for x := 0; x < len(row); x += 8 {
				a := binary.BigEndian.Uint16(row[x+6:])
				for channel := 0; channel < 6; channel += 2 {
					value := binary.BigEndian.Uint16(row[x+channel:])
					binary.BigEndian.PutUint16(row[x+channel:], min(value, a))
				}
			}
		}
	}
}

type graphicsFrame struct {
	image *preparedGraphic
	x, y  int // One-based terminal coordinates.
}

// Bubble Tea sends a complete diff in each Write. A private zero-width OSC
// marker associates that diff with its image, even when newer Views have already
// been computed. Graphics are written after the diff under the same lock.
type graphicsOutput struct {
	mu     sync.Mutex
	out    io.Writer
	serial uint64
	frames map[uint64]graphicsFrame
	active graphicsFrame
}

// Preserve the terminal file interface: Bubble Tea needs its descriptor for
// initial sizing, resize events, and Windows console setup.
type graphicsTerminal struct {
	*os.File
	graphics *graphicsOutput
}

func (t *graphicsTerminal) Write(p []byte) (int, error)       { return t.graphics.Write(p) }
func (t *graphicsTerminal) WriteString(s string) (int, error) { return t.Write([]byte(s)) }

const graphicsMarker = "\x1b]777;walk-image="

func (w *graphicsOutput) frame(frame graphicsFrame) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.serial++
	if w.frames == nil {
		w.frames = make(map[uint64]graphicsFrame)
	}
	w.frames[w.serial] = frame
	// Bubble Tea can coalesce arbitrarily many Views before its next flush.
	for id := range w.frames {
		if id+8 < w.serial {
			delete(w.frames, id)
		}
	}
	return graphicsMarker + strconv.FormatUint(w.serial, 10) + "\a"
}

func (w *graphicsOutput) clear() string {
	f := w.active
	if f.image == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	if f.image.id != 0 {
		// Use the ANSI library for targeted deletion; go-termimg's deletion
		// methods write directly to stdout, which Walk reserves for its result.
		b.WriteString(ansi.KittyGraphics(nil, "a=d", "d=I", fmt.Sprintf("i=%d", f.image.id), "q=2"))
	}
	for row := 0; row < f.image.height; row++ {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[%dX", f.y+row, f.x, f.image.width)
	}
	b.WriteString("\x1b8")
	w.active = graphicsFrame{}
	return b.String()
}

func (w *graphicsOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(data)
	var frame graphicsFrame
	hasFrame := false
	for {
		start := strings.Index(s, graphicsMarker)
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start:], '\a')
		if end < 0 {
			break
		}
		end += start
		id, _ := strconv.ParseUint(s[start+len(graphicsMarker):end], 10, 64)
		frame = w.frames[id]
		for old := range w.frames {
			if old <= id {
				delete(w.frames, old)
			}
		}
		hasFrame = true
		s = s[:start] + s[end+1:]
	}
	var b strings.Builder
	// Release images before leaving the alternate screen (including ExecProcess).
	leaving := strings.Contains(s, "\x1b[?1049l")
	changed := hasFrame && w.active != frame
	cleared := strings.Contains(s, "\x1b[2J")
	if leaving || changed || cleared {
		b.WriteString(w.clear())
	}
	b.WriteString(s)
	if hasFrame && !leaving && frame.image != nil {
		b.WriteString("\x1b7")
		fmt.Fprintf(&b, "\x1b[%d;%dH", frame.y, frame.x)
		if frame.image.id == 0 || w.active.image != frame.image {
			b.WriteString(frame.image.sequence)
		}
		b.WriteString("\x1b8")
		w.active = frame
	}
	_, err := io.WriteString(w.out, b.String())
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *graphicsOutput) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := io.WriteString(w.out, w.clear())
	return err
}
