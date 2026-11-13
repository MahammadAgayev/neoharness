package model

import (
	"bytes"
	"testing"
)

func TestImageTypeReadsTheBytes(t *testing.T) {
	cases := map[string]string{
		"\x89PNG\r\n\x1a\nrest":        "image/png",
		"\xff\xd8\xff\xe0rest":         "image/jpeg",
		"GIF89arest":                   "image/gif",
		"RIFF\x00\x00\x00\x00WEBPVP8 ": "image/webp",
	}
	for data, want := range cases {
		if got, err := ImageType([]byte(data)); err != nil || got != want {
			t.Errorf("ImageType(%q) = %q, %v; want %q", data, got, err, want)
		}
	}
	for _, bad := range []string{"", "<svg>", "%PDF-1.7", "RIFF\x00\x00\x00\x00WAVE"} {
		if got, err := ImageType([]byte(bad)); err == nil {
			t.Errorf("ImageType(%q) = %q, want an error", bad, got)
		}
	}
	big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, MaxImageSize)...)
	if _, err := ImageType(big); err == nil {
		t.Error("an image over MaxImageSize should fail")
	}
}
