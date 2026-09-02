//go:build !cgo || windows

package storage

import (
	"bytes"
	"image"
	"io"

	purewebp "github.com/deepteams/webp"
)

// portableWebPCodec keeps the Windows desktop build self-contained. It is also
// the fallback for other CGO-disabled builds, so image support never decides
// whether the API process itself can boot.
type portableWebPCodec struct{}

func (portableWebPCodec) Encode(w io.Writer, source image.Image) error {
	opts := purewebp.DefaultOptions()
	opts.Quality = 90
	return purewebp.Encode(w, source, opts)
}

func (portableWebPCodec) Decode(payload []byte) (image.Image, error) {
	return purewebp.Decode(bytes.NewReader(payload))
}

// NewLibWebPCodec returns a complete codec without a native runtime dependency.
// The historical name is kept so callers do not care which implementation is
// selected by the build tags.
func NewLibWebPCodec() (WebPCodec, error) {
	return portableWebPCodec{}, nil
}
