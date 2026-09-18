package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// maxReportPhotos caps how many screenshots ride along with one result.
	// Telegram media groups hold ten, but five already covers the score plus
	// both line-ups, and every extra image inflates the JSON body the captain
	// uploads over mobile data.
	maxReportPhotos = 5
	// maxReportPhotoBytes caps a single decoded screenshot. The mini app
	// downscales before sending, so anything near this ceiling means the
	// client-side resize did not run.
	maxReportPhotoBytes = 8 << 20
)

// decodedPhoto is one screenshot recovered from a data URL, ready to be
// handed to Telegram.
type decodedPhoto struct {
	Data []byte
	MIME string
}

// Ext returns a file extension for the photo, which Telegram uses to pick a
// decoder when the bytes are uploaded.
func (p decodedPhoto) Ext() string {
	switch p.MIME {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

// decodeReportPhotos turns the mini app's data URLs into raw images.
//
// The screenshots are the only evidence a referee gets when two captains
// disagree about a score, so this rejects anything it cannot hand to Telegram
// verbatim rather than passing it along and failing silently later.
func decodeReportPhotos(raw []string) ([]decodedPhoto, error) {
	present := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			present = append(present, s)
		}
	}
	if len(present) == 0 {
		return nil, errors.New("приложите хотя бы один скриншот результата")
	}
	if len(present) > maxReportPhotos {
		return nil, fmt.Errorf("можно приложить не более %d скриншотов, выбрано %d", maxReportPhotos, len(present))
	}

	photos := make([]decodedPhoto, 0, len(present))
	for i, s := range present {
		p, err := decodePhotoDataURL(s)
		if err != nil {
			return nil, fmt.Errorf("скриншот %d: %w", i+1, err)
		}
		photos = append(photos, p)
	}
	return photos, nil
}

// uploadReportPhotos hands every screenshot to Telegram and collects the file
// IDs it answers with, in the order the captain attached them.
//
// A failure anywhere aborts the whole batch: a half-uploaded report would show
// the referee some of the evidence while quietly dropping the rest.
func (s *AdminServer) uploadReportPhotos(ctx context.Context, matchID int, photos []decodedPhoto) ([]string, error) {
	if s.photoUploader == nil {
		return nil, errors.New("photo uploader is not configured")
	}
	ids := make([]string, 0, len(photos))
	for i, p := range photos {
		id, err := s.photoUploader(ctx, p.Data, fmt.Sprintf("match_%d_%d%s", matchID, i+1, p.Ext()))
		if err != nil {
			return nil, fmt.Errorf("screenshot %d: %w", i+1, err)
		}
		if id == "" {
			return nil, fmt.Errorf("screenshot %d: telegram returned an empty file ID", i+1)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// decodePhotoDataURL parses one "data:image/png;base64,..." value.
func decodePhotoDataURL(s string) (decodedPhoto, error) {
	const scheme = "data:"
	if !strings.HasPrefix(s, scheme) {
		return decodedPhoto{}, errors.New("передан в неизвестном формате")
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return decodedPhoto{}, errors.New("передан в неизвестном формате")
	}

	header := s[len(scheme):comma]
	if !strings.HasSuffix(header, ";base64") {
		return decodedPhoto{}, errors.New("ожидалась base64-кодировка")
	}
	mime := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(header, ";base64")))
	if !strings.HasPrefix(mime, "image/") {
		return decodedPhoto{}, fmt.Errorf("можно прикладывать только изображения (получено %q)", mime)
	}

	data, err := base64.StdEncoding.DecodeString(s[comma+1:])
	if err != nil {
		return decodedPhoto{}, errors.New("не удалось прочитать изображение")
	}
	if len(data) == 0 {
		return decodedPhoto{}, errors.New("файл пустой")
	}
	if len(data) > maxReportPhotoBytes {
		return decodedPhoto{}, fmt.Errorf("размер превышает %d МБ", maxReportPhotoBytes>>20)
	}
	return decodedPhoto{Data: data, MIME: mime}, nil
}
