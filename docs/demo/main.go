// Command demo records docs/demo.gif: it runs prpr against the made-up data
// of ./gh in a pseudo-terminal, plays a fixed sequence of keys, and draws the
// screen to an animated GIF. Run it from this directory on Linux or macOS:
//
//	go run . -font /path/to/DejaVuSansMono.ttf -bold /path/to/DejaVuSansMono-Bold.ttf -emoji /path/to/png/72
//
// -icons nerd records Nerd Font icons, which -font must then hold.
//
// The font must cover the box-drawing and status glyphs prpr draws; DejaVu
// Sans Mono does. Emoji such as 🙏 are drawn from Noto Emoji's color images
// (2D/png/72 of github.com/googlefonts/noto-emoji), which no font here holds.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	cols, rows = 140, 24
	fontSize   = 14
	padding    = 12
	tick       = 50 * time.Millisecond
)

var (
	background = color.RGBA{0x1a, 0x1b, 0x26, 0xff}
	foreground = color.RGBA{0xc8, 0xcc, 0xd8, 0xff}
	// ansiColors replaces the 16 basic colors, which prpr uses for statuses.
	ansiColors = [16]color.RGBA{
		{0x15, 0x16, 0x1e, 0xff}, {0xf7, 0x76, 0x8e, 0xff}, {0x9e, 0xce, 0x6a, 0xff}, {0xe0, 0xaf, 0x68, 0xff},
		{0x7a, 0xa2, 0xf7, 0xff}, {0xbb, 0x9a, 0xf7, 0xff}, {0x7d, 0xcf, 0xff, 0xff}, {0xa9, 0xb1, 0xd6, 0xff},
		{0x41, 0x48, 0x68, 0xff}, {0xff, 0x7a, 0x93, 0xff}, {0xb9, 0xf2, 0x7c, 0xff}, {0xff, 0x9e, 0x64, 0xff},
		{0x7d, 0xa6, 0xff, 0xff}, {0xbb, 0x9a, 0xf7, 0xff}, {0x0d, 0xb9, 0xd7, 0xff}, {0xc0, 0xca, 0xf5, 0xff},
	}
)

// step waits, then sends keys to the app.
type step struct {
	wait time.Duration
	keys string
}

// The cursor rests on an unchanged row during the refresh, because leaving a
// row clears its mark.
var script = []step{
	// The list, then the details of the pull request with conflicts.
	{2500 * time.Millisecond, "j"},
	{600 * time.Millisecond, "j"},
	{1200 * time.Millisecond, "\r"},
	{2500 * time.Millisecond, "\x1b"},
	// A refresh marks what changed.
	{600 * time.Millisecond, "G"},
	{1000 * time.Millisecond, "r"},
	// Review requests, one asking again with a blinking 🙏.
	{4500 * time.Millisecond, "\t"},
	{1200 * time.Millisecond, "j"},
	{900 * time.Millisecond, "j"},
	// The keys, grouped.
	{2000 * time.Millisecond, "?"},
	{2800 * time.Millisecond, "?"},
	// Requesting reviews: choosing one member covers both code-owner teams.
	{800 * time.Millisecond, "\t"},
	{600 * time.Millisecond, "g"},
	{600 * time.Millisecond, "R"},
	{1800 * time.Millisecond, "\t"},
	{700 * time.Millisecond, "j"},
	{700 * time.Millisecond, " "},
	{2800 * time.Millisecond, "\x1b"},
	{2000 * time.Millisecond, ""},
}

func main() {
	regular := flag.String("font", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", "monospace TrueType font")
	bold := flag.String("bold", "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", "bold variant of -font")
	fallback := flag.String("fallback", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", "font for glyphs -font lacks, such as the braille spinner")
	icons := flag.String("icons", "unicode", "prpr's icon set: unicode or nerd")
	emoji := flag.String("emoji", "", "directory of Noto Emoji images named emoji_u<codepoint>.png, for 🙏; without it, 🙏 is a box")
	out := flag.String("o", "../demo.gif", "output GIF")
	flag.Parse()
	if err := record(*regular, *bold, *fallback, *emoji, *icons, *out); err != nil {
		log.Fatal(err)
	}
}

func record(regularPath, boldPath, fallbackPath, emojiPath, icons, out string) error {
	faces, err := loadFaces(regularPath, boldPath, fallbackPath, emojiPath)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "prpr-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	bin := filepath.Join(work, "bin")
	state := filepath.Join(work, "state")
	config := filepath.Join(work, "config")
	for _, dir := range []string{bin, state, filepath.Join(config, "prpr")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	// A saved All repositories choice skips the first-run scope prompt.
	if err := os.WriteFile(filepath.Join(config, "prpr", "preferences.json"), []byte(`{"github.com/alice": ""}`), 0o600); err != nil {
		return err
	}
	for _, build := range [][]string{
		{"go", "build", "-o", filepath.Join(bin, "gh"), "./gh"},
		{"go", "build", "-C", "../..", "-o", filepath.Join(bin, "prpr"), "./cmd/prpr"},
	} {
		cmd := exec.Command(build[0], build[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%v: %w", build, err)
		}
	}

	cmd := exec.Command(filepath.Join(bin, "prpr"))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_CONFIG_HOME="+config, "DEMO_STATE="+state, "TERM=xterm-256color", "COLORTERM=truecolor", "PRPR_ICONS="+icons)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	emu := vt.NewSafeEmulator(cols, rows)
	go func() { _, _ = io.Copy(emu, tty) }()
	go func() { _, _ = io.Copy(tty, emu) }()

	var (
		mu     sync.Mutex
		frames []*image.RGBA
		delays []int
	)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			frame := render(emu, faces)
			mu.Lock()
			if n := len(frames); n > 0 && bytes.Equal(frames[n-1].Pix, frame.Pix) {
				delays[n-1] += int(tick / (10 * time.Millisecond))
			} else {
				frames = append(frames, frame)
				delays = append(delays, int(tick/(10*time.Millisecond)))
			}
			mu.Unlock()
		}
	}()
	for _, s := range script {
		time.Sleep(s.wait)
		if s.keys != "" {
			if _, err := tty.WriteString(s.keys); err != nil {
				return err
			}
		}
	}
	close(stop)
	<-done
	_, _ = tty.WriteString("q")
	_ = cmd.Wait()
	return writeGIF(out, frames, delays)
}

// faces are the fonts cells are drawn in, and the emoji images.
type faces struct {
	regular, bold, fallback font.Face
	emoji                   emojiImages
}

// emojiImages are color emoji images in a directory, named as Noto Emoji
// names them: emoji_u, then each code point in hex, without variation
// selectors, joined by underscores.
type emojiImages struct {
	dir    string
	images map[string]image.Image
}

// image returns the image for a cell's content, or nil for none.
func (e emojiImages) image(content string) image.Image {
	if e.dir == "" {
		return nil
	}
	if img, ok := e.images[content]; ok {
		return img
	}
	var points []string
	for _, r := range content {
		if r != 0xfe0f {
			points = append(points, fmt.Sprintf("%x", r))
		}
	}
	var img image.Image
	if file, err := os.Open(filepath.Join(e.dir, "emoji_u"+strings.Join(points, "_")+".png")); err == nil {
		img, _ = png.Decode(file)
		file.Close()
	}
	e.images[content] = img
	return img
}

func loadFaces(regularPath, boldPath, fallbackPath, emojiPath string) (faces, error) {
	load := func(path string) (font.Face, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := opentype.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return opentype.NewFace(f, &opentype.FaceOptions{Size: fontSize, DPI: 72, Hinting: font.HintingFull})
	}
	regular, err := load(regularPath)
	if err != nil {
		return faces{}, err
	}
	bold, err := load(boldPath)
	if err != nil {
		return faces{}, err
	}
	fallback, err := load(fallbackPath)
	if err != nil {
		return faces{}, err
	}
	return faces{regular, bold, fallback, emojiImages{dir: emojiPath, images: map[string]image.Image{}}}, nil
}

func cellSize(f font.Face) (int, int, int) {
	advance, _ := f.GlyphAdvance('M')
	m := f.Metrics()
	return advance.Ceil(), (m.Ascent + m.Descent).Ceil() + 2, m.Ascent.Ceil() + 1
}

func rgba(c color.Color, fallback color.RGBA) color.RGBA {
	switch c := c.(type) {
	case nil:
		return fallback
	case ansi.BasicColor:
		return ansiColors[c&15]
	case ansi.IndexedColor:
		if c < 16 {
			return ansiColors[c]
		}
	}
	r, g, b, _ := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 0xff}
}

// cellColors collects every cell's foreground and background, so the palette
// keeps them exactly.
var cellColors = map[color.RGBA]bool{}

// render draws the emulator screen with each cell's colors and attributes.
func render(emu *vt.SafeEmulator, f faces) *image.RGBA {
	w, h, ascent := cellSize(f.regular)
	img := image.NewRGBA(image.Rect(0, 0, cols*w+2*padding, rows*h+2*padding))
	draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := emu.CellAt(x, y)
			if cell == nil || cell.Width == 0 {
				continue
			}
			fg, bg := rgba(cell.Style.Fg, foreground), rgba(cell.Style.Bg, background)
			attrs := cell.Style.Attrs
			if attrs&uv.AttrReverse != 0 {
				fg, bg = bg, fg
			}
			if attrs&uv.AttrFaint != 0 {
				fg = blend(fg, bg, 0.45)
			}
			cellColors[fg], cellColors[bg] = true, true
			width := max(cell.Width, 1)
			box := image.Rect(padding+x*w, padding+y*h, padding+(x+width)*w, padding+(y+1)*h)
			draw.Draw(img, box, image.NewUniform(bg), image.Point{}, draw.Src)
			if emoji := f.emoji.image(cell.Content); emoji != nil {
				// The emoji fills the height of its cells, centered across them.
				side := min(box.Dx(), box.Dy())
				at := image.Pt(box.Min.X+(box.Dx()-side)/2, box.Min.Y+(box.Dy()-side)/2)
				xdraw.CatmullRom.Scale(img, image.Rectangle{at, at.Add(image.Pt(side, side))}, emoji, emoji.Bounds(), xdraw.Over, nil)
			} else if cell.Content != "" && cell.Content != " " {
				face := f.regular
				if attrs&uv.AttrBold != 0 {
					face = f.bold
				}
				for _, r := range cell.Content {
					if _, ok := face.GlyphAdvance(r); !ok {
						face = f.fallback
					}
				}
				d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face,
					Dot: fixed.P(box.Min.X, box.Min.Y+ascent)}
				d.DrawString(cell.Content)
			}
			if attrs&uv.AttrStrikethrough != 0 {
				mid := box.Min.Y + h/2
				draw.Draw(img, image.Rect(box.Min.X, mid, box.Max.X, mid+1), image.NewUniform(fg), image.Point{}, draw.Src)
			}
		}
	}
	return img
}

// writeGIF encodes the frames with a shared palette, storing only the part of
// each frame that changed.
func writeGIF(path string, frames []*image.RGBA, delays []int) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames recorded")
	}
	palette := buildPalette(frames)
	anim := &gif.GIF{Config: image.Config{ColorModel: palette, Width: frames[0].Bounds().Dx(), Height: frames[0].Bounds().Dy()}}
	for i, frame := range frames {
		bounds := frame.Bounds()
		if i > 0 {
			bounds = changed(frames[i-1], frame)
		}
		paletted := image.NewPaletted(bounds, palette)
		draw.Draw(paletted, bounds, frame, bounds.Min, draw.Src)
		anim.Image = append(anim.Image, paletted)
		anim.Delay = append(anim.Delay, delays[i])
		anim.Disposal = append(anim.Disposal, gif.DisposalNone)
	}
	anim.Delay[len(anim.Delay)-1] += 200
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gif.EncodeAll(file, anim); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// changed returns the smallest rectangle holding every pixel that differs.
func changed(prev, next *image.RGBA) image.Rectangle {
	b := next.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := next.PixOffset(x, y)
			if !bytes.Equal(prev.Pix[i:i+4], next.Pix[i:i+4]) {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x+1), max(maxY, y+1)
			}
		}
	}
	if minX >= maxX {
		return image.Rect(0, 0, 1, 1)
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// buildPalette holds every cell color, then the most used anti-aliased edge
// colors up to 256. Other edges map to the nearest entry.
func buildPalette(frames []*image.RGBA) color.Palette {
	palette := color.Palette{}
	for c := range cellColors {
		palette = append(palette, c)
	}
	counts := map[color.RGBA]int{}
	for _, frame := range frames {
		for i := 0; i < len(frame.Pix); i += 4 {
			if c := (color.RGBA{frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], 0xff}); !cellColors[c] {
				counts[c]++
			}
		}
	}
	edges := make([]color.RGBA, 0, len(counts))
	for c := range counts {
		edges = append(edges, c)
	}
	slices.SortFunc(edges, func(a, b color.RGBA) int {
		if counts[a] != counts[b] {
			return counts[b] - counts[a]
		}
		return int(a.R)<<16 + int(a.G)<<8 + int(a.B) - (int(b.R)<<16 + int(b.G)<<8 + int(b.B))
	})
	for _, c := range edges {
		if len(palette) >= 256 {
			break
		}
		palette = append(palette, c)
	}
	return palette[:min(len(palette), 256)]
}
