package tui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7919 % 251)
	}
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func clipboardHolds(t *testing.T, data []byte, err error) {
	t.Helper()
	old := clipboardImage
	clipboardImage = func() ([]byte, error) { return data, err }
	t.Cleanup(func() { clipboardImage = old })
}

// TestImageAttachesTheClipboard: a terminal cannot paste a picture, so
// :image reads it from the clipboard, or a file, and the prompt shows only
// its token.
func TestImageAttachesTheClipboard(t *testing.T) {
	small := pngOf(t, 4, 4)
	clipboardHolds(t, small, nil)
	h := start(t)
	h.openDemo()
	h.press("i", "what is in", "<Esc>", ":image", "<CR>")
	if got := h.prompt(); got != "what is in [image 1] " {
		t.Fatalf("prompt = %q, want the token after the cursor", got)
	}
	file := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(file, small, 0o600); err != nil {
		t.Fatal(err)
	}
	h.press("and", "<Esc>", ":image "+file, "<CR>", "<CR>")
	if got := h.hub.sent[len(h.hub.sent)-1]; got != "coordinator: what is in [image 1] and [image 2]" {
		t.Fatalf("sent = %q", got)
	}
	if imgs := h.hub.images[len(h.hub.sent)-1]; len(imgs) != 2 || !bytes.Equal(imgs[0], small) {
		t.Fatalf("images = %d, want the clipboard's and the file's", len(imgs))
	}
}

func TestADeletedImageTokenDoesNotGo(t *testing.T) {
	clipboardHolds(t, pngOf(t, 4, 4), nil)
	h := start(t)
	h.openDemo()
	h.press("i", "a", "<Esc>", ":image", "<CR>", "b", "<Esc>", ":image", "<CR>")
	if got := h.prompt(); got != "a [image 1] b [image 2] " {
		t.Fatalf("prompt = %q", got)
	}
	h.press("<Esc>", "0", "w", "x", "x", "x", "x", "x", "x", "x", "x", "x", "x")
	if got := h.prompt(); got != "a b [image 2] " {
		t.Fatalf("prompt = %q after x on the first token", got)
	}
	h.press("A", "<CR>")
	if got := h.hub.sent[len(h.hub.sent)-1]; got != "coordinator: a b [image 1]" || len(h.hub.images[len(h.hub.sent)-1]) != 1 {
		t.Fatalf("sent = %q with %d images", got, len(h.hub.images[len(h.hub.sent)-1]))
	}
}

func TestImageSaysWhatWentWrong(t *testing.T) {
	clipboardHolds(t, nil, errors.New("the clipboard holds no image"))
	h := start(t)
	h.openDemo()
	h.press(":image", "<CR>")
	h.see("the clipboard holds no image")
	clipboardHolds(t, []byte("<svg/>"), nil)
	h.press(":image", "<CR>")
	h.see("png, jpeg, gif or webp")
	if h.prompt() != "" {
		t.Fatalf("prompt = %q, want nothing added", h.prompt())
	}
}

// TestABigImageIsMadeSmaller: a screenshot of a big screen is past what the
// model takes, and bigger than it works at.
func TestABigImageIsMadeSmaller(t *testing.T) {
	out, err := shrink(pngOf(t, 3000, 2000))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 1568 || cfg.Height != 1045 {
		t.Fatalf("size = %dx%d, %v; want 1568 on the long side", cfg.Width, cfg.Height, err)
	}
	small := pngOf(t, 4, 4)
	if out, _ := shrink(small); !bytes.Equal(out, small) {
		t.Error("a small image should go as it is")
	}
}

func TestTakingAMessageBackBringsItsImagesBack(t *testing.T) {
	small := pngOf(t, 4, 4)
	clipboardHolds(t, small, nil)
	h := start(t)
	h.openDemo()
	h.press("i", "see", "<Esc>", ":image", "<CR>", "<CR>", "<Esc>")
	chat := h.a.chat(model.Coordinator)
	chat.SetPending([]model.Event{{Kind: model.EventMessage, From: model.User, Text: "see [image 1]", At: time.Now()}})
	h.press("u")
	if got := string(chat.Prompt.Text); got != "see [image 1]" {
		t.Fatalf("prompt = %q", got)
	}
	h.press("A", "<CR>")
	if imgs := h.hub.images[len(h.hub.sent)-1]; len(imgs) != 1 || !bytes.Equal(imgs[0], small) {
		t.Fatalf("images = %d, want the one taken back", len(imgs))
	}
}
