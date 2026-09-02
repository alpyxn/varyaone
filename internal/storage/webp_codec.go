package storage

// WebPCodec combines the two boundary interfaces without making image
// processing depend on a concrete provider or C binding.
type WebPCodec interface {
	WebPEncoder
	WebPDecoder
}
