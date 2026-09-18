package web

import (
	"encoding/base64"
	"strings"
	"testing"
)

// pngDataURL builds a data URL carrying n bytes of payload.
func pngDataURL(n int) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, n))
}

func TestDecodeReportPhotosAcceptsSeveralImages(t *testing.T) {
	// The mini app sends screenshots as data URLs. Every one of them has to
	// survive decoding, because a dropped screenshot is a dispute the referee
	// cannot settle.
	photos, err := decodeReportPhotos([]string{pngDataURL(64), pngDataURL(128), pngDataURL(32)})
	if err != nil {
		t.Fatalf("decodeReportPhotos returned error: %v", err)
	}
	if len(photos) != 3 {
		t.Fatalf("decoded %d photos, want 3", len(photos))
	}
	wantSizes := []int{64, 128, 32}
	for i, p := range photos {
		if len(p.Data) != wantSizes[i] {
			t.Errorf("photo %d: decoded %d bytes, want %d", i, len(p.Data), wantSizes[i])
		}
		if p.MIME != "image/png" {
			t.Errorf("photo %d: MIME = %q, want image/png", i, p.MIME)
		}
	}
}

func TestDecodeReportPhotosRejectsEmptySelection(t *testing.T) {
	// A report with no proof is the thing we are trying to stamp out, so an
	// empty list must fail loudly rather than save a bare score.
	cases := []struct {
		name string
		in   []string
	}{
		{"nil", nil},
		{"empty slice", []string{}},
		{"blank strings only", []string{"", "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeReportPhotos(tc.in); err == nil {
				t.Fatal("decodeReportPhotos accepted a report without screenshots")
			}
		})
	}
}

func TestDecodeReportPhotosEnforcesCount(t *testing.T) {
	// Telegram media groups hold ten, but the product cap is five: past that
	// the JSON body grows faster than it helps the referee.
	in := make([]string, maxReportPhotos+1)
	for i := range in {
		in[i] = pngDataURL(16)
	}
	_, err := decodeReportPhotos(in)
	if err == nil {
		t.Fatal("decodeReportPhotos accepted more than the cap")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error should tell the captain the limit, got %q", err)
	}
}

func TestDecodeReportPhotosEnforcesSize(t *testing.T) {
	// The cap exists so one captain cannot wedge the request handler with a
	// raw 40 MB burst screenshot.
	_, err := decodeReportPhotos([]string{pngDataURL(maxReportPhotoBytes + 1)})
	if err == nil {
		t.Fatal("decodeReportPhotos accepted an oversized image")
	}
}

func TestDecodeReportPhotosRejectsNonImages(t *testing.T) {
	// Whatever reaches this function is forwarded to Telegram as a photo, so
	// anything that is not an image is refused before it gets that far.
	cases := []struct {
		name string
		in   string
	}{
		{"pdf", "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("%PDF"))},
		{"html", "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte("<script>"))},
		{"bare base64 without header", base64.StdEncoding.EncodeToString([]byte("nope"))},
		{"not base64", "data:image/png;base64,@@@not-base64@@@"},
		{"header only", "data:image/png;base64,"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeReportPhotos([]string{tc.in}); err == nil {
				t.Fatalf("decodeReportPhotos accepted %s", tc.name)
			}
		})
	}
}

func TestDecodeReportPhotosSkipsBlanksAroundRealImages(t *testing.T) {
	// The form can hand back empty slots when a captain removes a thumbnail;
	// those are noise, not an error, as long as a real screenshot remains.
	photos, err := decodeReportPhotos([]string{"", pngDataURL(48), ""})
	if err != nil {
		t.Fatalf("decodeReportPhotos returned error: %v", err)
	}
	if len(photos) != 1 {
		t.Fatalf("decoded %d photos, want 1", len(photos))
	}
}
