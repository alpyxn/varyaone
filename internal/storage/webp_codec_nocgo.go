//go:build !cgo

package storage

// NewLibWebPCodec fails closed for binaries built without cgo. Production
// images enable cgo and ship the small libwebp runtime as documented in ADR
// 005; a no-cgo binary must not mislabel JPEG/PNG bytes as WebP.
func NewLibWebPCodec() (WebPCodec, error) {
	return nil, ErrWebPEncoderUnavailable
}
