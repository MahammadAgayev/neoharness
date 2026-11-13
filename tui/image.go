package tui

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// A terminal pastes text only, so :image reads the picture from the
// clipboard, or from a file, and the prompt shows it as [image 1].

// clipboardImage reads a picture from the system clipboard. A variable, so
// the tests need no clipboard.
var clipboardImage = readClipboardImage

func readClipboardImage() ([]byte, error) {
	none := errors.New("the clipboard holds no image")
	switch runtime.GOOS {
	case "darwin":
		// AppleScript writes it as «data PNGf89504E…».
		out, err := exec.Command("osascript", "-e", "the clipboard as «class PNGf»").Output()
		s, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "«data PNGf")
		if err != nil || !ok {
			return nil, none
		}
		return hex.DecodeString(strings.TrimSuffix(s, "»"))
	case "linux":
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			cmd = exec.Command("wl-paste", "--type", "image/png")
		}
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			return nil, fmt.Errorf("the clipboard holds no image, or %s is missing", cmd.Args[0])
		}
		return out, nil
	}
	return nil, errors.New("no clipboard here · :image <file> takes a file")
}

// longEdge is the size the model works at; a bigger image costs more and
// shows it nothing more.
const longEdge = 1568

// maxImageFile is the biggest file :image reads before it makes it smaller.
const maxImageFile = 64 << 20

// shrink makes an image too big for the model smaller: as PNG, then as JPEG
// if that is still too big. Others go as they are; the hub checks them.
func shrink(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return data, nil
	}
	scale := min(1, float64(longEdge)/float64(max(cfg.Width, cfg.Height)))
	if scale == 1 && len(data) <= model.MaxImageSize {
		return data, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	dst := scaleDown(src, max(1, int(float64(cfg.Width)*scale+0.5)), max(1, int(float64(cfg.Height)*scale+0.5)))
	var b bytes.Buffer
	if err := png.Encode(&b, dst); err != nil {
		return nil, err
	}
	if b.Len() > model.MaxImageSize {
		b.Reset()
		if err := jpeg.Encode(&b, dst, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// scaleDown averages the source pixels under each pixel of a w×h image.
func scaleDown(src image.Image, w, h int) *image.RGBA {
	sb := src.Bounds()
	in := image.NewRGBA(image.Rect(0, 0, sb.Dx(), sb.Dy()))
	draw.Draw(in, in.Bounds(), src, sb.Min, draw.Src)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := sb.Dx(), sb.Dy()
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var sum [4]int
			for sy := y0; sy < y1; sy++ {
				row := in.Pix[sy*in.Stride:]
				for sx := x0; sx < x1; sx++ {
					for c := range 4 {
						sum[c] += int(row[sx*4+c])
					}
				}
			}
			n := (y1 - y0) * (x1 - x0)
			o := out.Pix[y*out.Stride+x*4:]
			for c := range 4 {
				o[c] = byte(sum[c] / n)
			}
		}
	}
	return out
}

var imageTokenRE = regexp.MustCompile(`\[image (\d+)\]`)

// usedImages are the images whose token is still in the text, in order.
func usedImages(text string, images map[int][]byte) []int {
	var out []int
	for _, m := range imageTokenRE.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		if _, ok := images[n]; ok && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// withImages is what goes to the hub: the images whose token is in the text,
// and the text with their tokens numbered 1, 2, … in the same order.
func withImages(text string, images map[int][]byte) (string, [][]byte) {
	keep := usedImages(text, images)
	to := map[int]int{}
	var out [][]byte
	for i, n := range keep {
		to[n] = i + 1
		out = append(out, images[n])
	}
	text = imageTokenRE.ReplaceAllStringFunc(text, func(tok string) string {
		n, _ := strconv.Atoi(imageTokenRE.FindStringSubmatch(tok)[1])
		if m, ok := to[n]; ok {
			return model.ImageToken(m)
		}
		return tok
	})
	return text, out
}

// attachImage is :image: the picture of the clipboard, or of the file at
// path, goes in the prompt after the cursor, and Insert goes on after it.
func (a *App) attachImage(path string) {
	w := a.cur
	p := w.Buf.Prompt
	if p == nil {
		a.notify(":image puts a picture in the prompt of a thread or the coordinator", a.th.Muted)
		return
	}
	data, err := readImage(path)
	if err == nil {
		data, err = shrink(data)
	}
	if err == nil {
		_, err = model.ImageType(data)
	}
	if err != nil {
		a.notify(err.Error(), a.th.Love)
		return
	}
	if p.Images == nil {
		p.Images = map[int][]byte{}
	}
	if len(usedImages(string(p.Text), p.Images)) >= model.MaxImages {
		a.notify(fmt.Sprintf("a message takes at most %d images", model.MaxImages), a.th.Love)
		return
	}
	// A token deleted and typed again never names another image.
	n := 1
	if len(p.Images) > 0 {
		n = slices.Max(slices.Collect(maps.Keys(p.Images))) + 1
	}
	p.Images[n] = data
	at := len(p.Text)
	if w.inPrompt {
		a.leavePrompt(w)
		at = min(p.Cur+1, len(p.Text))
	}
	tok := model.ImageToken(n) + " "
	if at > 0 && !unicode.IsSpace(p.Text[at-1]) {
		tok = " " + tok
	}
	p.Text = slices.Insert(p.Text, at, []rune(tok)...)
	a.enterPrompt(w)
	a.insert('i')
	p.Cur = at + len([]rune(tok))
}

func readImage(path string) ([]byte, error) {
	if path == "" {
		return clipboardImage()
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, rest)
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxImageFile {
		return nil, fmt.Errorf("%s is too big for an image", filepath.Base(path))
	}
	return os.ReadFile(path)
}
