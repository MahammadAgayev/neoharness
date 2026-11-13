package model

import (
	"bytes"
	"errors"
	"fmt"
)

// Images go to the agent with a message. Their bytes come from a clipboard
// or a file the person chose, so the hub trusts nothing about them but what
// it reads from the bytes.
const (
	// MaxImageSize keeps an image under 5 MB once it is base64, the most a
	// model API takes for one image.
	MaxImageSize = 5 << 20 * 3 / 4
	// MaxImages is the most images one message takes.
	MaxImages = 5
)

// ImageToken stands for the nth image of a message in its text, as
// `[image 1]`, so the agent knows which image the words are about.
func ImageToken(n int) string { return fmt.Sprintf("[image %d]", n) }

// ImageType reads an image's media type from its bytes: png, jpeg, gif or
// webp. Anything else is an error.
func ImageType(data []byte) (string, error) {
	if len(data) > MaxImageSize {
		return "", fmt.Errorf("an image is %.1f MB; the most is %.1f MB", float64(len(data))/(1<<20), float64(MaxImageSize)/(1<<20))
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", nil
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg", nil
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", nil
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return "image/webp", nil
	}
	return "", errors.New("an image must be png, jpeg, gif or webp")
}
