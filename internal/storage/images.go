package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
)

var (
	ErrUnsupportedImage       = errors.New("unsupported image format")
	ErrHEICDisabled           = errors.New("HEIC/HEIF image uploads are disabled")
	ErrImageTooLarge          = errors.New("image exceeds the configured byte limit")
	ErrImageDimensions        = errors.New("image exceeds the configured dimensions")
	ErrWebPEncoderUnavailable = errors.New("WebP encoder is not configured")
)

type ImageFormat string

const (
	ImageJPEG ImageFormat = "JPEG"
	ImagePNG  ImageFormat = "PNG"
	ImageWEBP ImageFormat = "WEBP"
)

type ImageLimits struct {
	MaxBytes  int64
	MaxWidth  int
	MaxHeight int
	MaxPixels int64
}

func DefaultImageLimits() ImageLimits {
	return ImageLimits{MaxBytes: 20 << 20, MaxWidth: 10_000, MaxHeight: 10_000, MaxPixels: 40_000_000}
}

type ImageInfo struct {
	Format      ImageFormat `json:"format"`
	ContentType string      `json:"content_type"`
	Size        int64       `json:"size"`
	Width       int         `json:"width"`
	Height      int         `json:"height"`
	SHA256      string      `json:"sha256"`
	Orientation int         `json:"orientation"`
}

// InspectImage validates magic bytes and dimensions. It intentionally does
// not trust a filename or Content-Type header. HEIC/HEIF are recognized and
// rejected explicitly while the feature flag remains off.
func InspectImage(payload []byte, limits ImageLimits) (ImageInfo, error) {
	if limits.MaxBytes <= 0 || limits.MaxWidth <= 0 || limits.MaxHeight <= 0 || limits.MaxPixels <= 0 {
		defaults := DefaultImageLimits()
		if limits.MaxBytes <= 0 {
			limits.MaxBytes = defaults.MaxBytes
		}
		if limits.MaxWidth <= 0 {
			limits.MaxWidth = defaults.MaxWidth
		}
		if limits.MaxHeight <= 0 {
			limits.MaxHeight = defaults.MaxHeight
		}
		if limits.MaxPixels <= 0 {
			limits.MaxPixels = defaults.MaxPixels
		}
	}
	if int64(len(payload)) > limits.MaxBytes {
		return ImageInfo{}, fmt.Errorf("%w: %d byte sınırı aşıldı", ErrImageTooLarge, limits.MaxBytes)
	}
	format, contentType, err := imageFormat(payload)
	if err != nil {
		return ImageInfo{}, err
	}
	var width, height int
	if format == ImageWEBP {
		width, height, err = webPDimensions(payload)
	} else {
		var config image.Config
		config, _, err = image.DecodeConfig(bytes.NewReader(payload))
		width, height = config.Width, config.Height
	}
	if err != nil {
		return ImageInfo{}, fmt.Errorf("%w: %v", ErrUnsupportedImage, err)
	}
	if width <= 0 || height <= 0 || width > limits.MaxWidth || height > limits.MaxHeight || int64(width)*int64(height) > limits.MaxPixels {
		return ImageInfo{}, fmt.Errorf("%w: %dx%d", ErrImageDimensions, width, height)
	}
	orientation := 1
	if format == ImageJPEG {
		orientation = jpegOrientation(payload)
	}
	digest := sha256.Sum256(payload)
	return ImageInfo{
		Format:      format,
		ContentType: contentType,
		Size:        int64(len(payload)),
		Width:       width,
		Height:      height,
		SHA256:      hex.EncodeToString(digest[:]),
		Orientation: orientation,
	}, nil
}

func ValidateImage(ctx context.Context, source io.Reader, limits ImageLimits) ([]byte, ImageInfo, error) {
	if source == nil {
		return nil, ImageInfo{}, errors.New("image source is required")
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = DefaultImageLimits().MaxBytes
	}
	payload, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: source}, limits.MaxBytes+1))
	if err != nil {
		return nil, ImageInfo{}, err
	}
	info, err := InspectImage(payload, limits)
	if err != nil {
		return nil, ImageInfo{}, err
	}
	return payload, info, nil
}

// VariantPlan is deterministic metadata for the WebP master and responsive
// thumbnails. It never enlarges a source image. Actual encoding is injected so
// deployments can use a reviewed libwebp adapter without coupling the core to
// a specific C binding.
type VariantPlan struct {
	Name        string `json:"name"`
	StorageKey  string `json:"storage_key"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentType string `json:"content_type"`
}

var imageVariantWidths = []int{128, 320, 768, 1600}

func PlanImageVariants(prefix string, width, height int) ([]VariantPlan, error) {
	if err := ValidateKey(prefix); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("%w: image dimensions must be positive", ErrImageDimensions)
	}
	plans := []VariantPlan{{
		Name:        "master",
		StorageKey:  prefix + "/master.webp",
		Width:       width,
		Height:      height,
		ContentType: "image/webp",
	}}
	for _, targetWidth := range imageVariantWidths {
		if targetWidth >= width {
			// Never upscale. The master is already the smallest valid source for
			// this target, so no extra object is required.
			continue
		}
		targetHeight := int((int64(height)*int64(targetWidth) + int64(width)/2) / int64(width))
		if targetHeight < 1 {
			targetHeight = 1
		}
		plans = append(plans, VariantPlan{
			Name:        fmt.Sprintf("%d", targetWidth),
			StorageKey:  fmt.Sprintf("%s/%d.webp", prefix, targetWidth),
			Width:       targetWidth,
			Height:      targetHeight,
			ContentType: "image/webp",
		})
	}
	return plans, nil
}

type WebPEncoder interface {
	Encode(io.Writer, image.Image) error
}

// WebPDecoder is kept separate from WebPEncoder so tests and deployments that
// only need to validate/encode PNG and JPEG can continue to provide a small
// encoder seam. Production wiring uses the libwebp codec for both directions;
// the standard library intentionally does not include a WebP decoder.
type WebPDecoder interface {
	Decode([]byte) (image.Image, error)
}

type ProcessedImage struct {
	Info            ImageInfo
	Normalized      []byte
	Master          []byte
	VariantPayloads map[string][]byte
	Plans           []VariantPlan
}

type ImageProcessor struct {
	Limits  ImageLimits
	Encoder WebPEncoder
	Decoder WebPDecoder
}

// Process validates an upload, strips JPEG/PNG metadata by decoding and
// re-encoding, and returns the deterministic WebP variant plan. A real master
// encoder is intentionally injected; deployments without libwebp receive a
// clear error rather than storing a mislabeled JPEG as WebP.
func (p ImageProcessor) Process(ctx context.Context, payload []byte, prefix string) (ProcessedImage, error) {
	info, err := InspectImage(payload, p.Limits)
	if err != nil {
		return ProcessedImage{}, err
	}
	normalized, err := normalizeImage(payload, info, p.Decoder)
	if err != nil {
		return ProcessedImage{}, err
	}
	if p.Encoder == nil {
		return ProcessedImage{}, ErrWebPEncoderUnavailable
	}
	decoded, err := decodeImage(normalized, info, p.Decoder)
	if err != nil {
		return ProcessedImage{}, err
	}
	// Orientation normalization can swap width and height (EXIF 5–8). Build
	// the responsive plan from the normalized pixels, otherwise a portrait
	// source would be resized back to its pre-rotation dimensions.
	info.Width, info.Height = decoded.Bounds().Dx(), decoded.Bounds().Dy()
	plans, err := PlanImageVariants(prefix, info.Width, info.Height)
	if err != nil {
		return ProcessedImage{}, err
	}
	result := ProcessedImage{
		Info:            info,
		Normalized:      normalized,
		VariantPayloads: make(map[string][]byte, len(plans)),
		Plans:           plans,
	}
	for index, plan := range plans {
		variant := decoded
		if plan.Width != info.Width || plan.Height != info.Height {
			variant = resizeImage(decoded, plan.Width, plan.Height)
		}
		var encoded bytes.Buffer
		if err = p.Encoder.Encode(&encoded, variant); err != nil {
			return ProcessedImage{}, err
		}
		result.VariantPayloads[plan.Name] = encoded.Bytes()
		if index == 0 {
			result.Master = encoded.Bytes()
		}
	}
	return result, nil
}

func imageFormat(payload []byte) (ImageFormat, string, error) {
	if len(payload) >= 3 && payload[0] == 0xff && payload[1] == 0xd8 && payload[2] == 0xff {
		return ImageJPEG, "image/jpeg", nil
	}
	if len(payload) >= 8 && string(payload[:8]) == "\x89PNG\r\n\x1a\n" {
		return ImagePNG, "image/png", nil
	}
	if len(payload) >= 12 && string(payload[:4]) == "RIFF" && string(payload[8:12]) == "WEBP" {
		return ImageWEBP, "image/webp", nil
	}
	if isHEIF(payload) {
		return "", "", ErrHEICDisabled
	}
	return "", "", ErrUnsupportedImage
}

func isHEIF(payload []byte) bool {
	if len(payload) < 12 || string(payload[4:8]) != "ftyp" {
		return false
	}
	for offset := 8; offset+4 <= len(payload) && offset < 64; offset += 4 {
		brand := string(payload[offset : offset+4])
		switch brand {
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return true
		}
	}
	return false
}

func webPDimensions(payload []byte) (int, int, error) {
	if len(payload) < 21 {
		return 0, 0, errors.New("WebP başlığı eksik")
	}
	chunk := string(payload[12:16])
	switch chunk {
	case "VP8X":
		if len(payload) < 30 {
			return 0, 0, errors.New("VP8X başlığı eksik")
		}
		width := 1 + int(uint32(payload[24])|uint32(payload[25])<<8|uint32(payload[26])<<16)
		height := 1 + int(uint32(payload[27])|uint32(payload[28])<<8|uint32(payload[29])<<16)
		return width, height, nil
	case "VP8L":
		if len(payload) < 25 || payload[20] != 0x2f {
			return 0, 0, errors.New("VP8L başlığı eksik")
		}
		width := 1 + int(uint32(payload[21])|uint32(payload[22]&0x3f)<<8)
		height := 1 + int(uint32(payload[22]>>6)|uint32(payload[23])<<2|uint32(payload[24]&0x0f)<<10)
		return width, height, nil
	case "VP8 ":
		if len(payload) < 30 {
			return 0, 0, errors.New("VP8 başlığı eksik")
		}
		for offset := 20; offset+10 < len(payload) && offset < 64; offset++ {
			if payload[offset] == 0x9d && payload[offset+1] == 0x01 && payload[offset+2] == 0x2a {
				width := int(binary.LittleEndian.Uint16(payload[offset+3:offset+5]) & 0x3fff)
				height := int(binary.LittleEndian.Uint16(payload[offset+5:offset+7]) & 0x3fff)
				return width, height, nil
			}
		}
	}
	return 0, 0, errors.New("desteklenmeyen WebP kodlaması")
}

func normalizeImage(payload []byte, info ImageInfo, decoder WebPDecoder) ([]byte, error) {
	if info.Format == ImageWEBP {
		if decoder == nil {
			return nil, ErrWebPEncoderUnavailable
		}
		// WebP has no EXIF orientation path in this boundary. Keep the validated
		// bytes and decode them through the injected codec below; all generated
		// objects are encoded afresh, so source metadata cannot leak into them.
		return payload, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if info.Orientation != 1 {
		decoded = orientImage(decoded, info.Orientation)
	}
	var output bytes.Buffer
	switch info.Format {
	case ImageJPEG:
		err = jpeg.Encode(&output, decoded, &jpeg.Options{Quality: 90})
	case ImagePNG:
		err = png.Encode(&output, decoded)
	default:
		err = ErrUnsupportedImage
	}
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeImage(payload []byte, info ImageInfo, decoder WebPDecoder) (image.Image, error) {
	if info.Format == ImageWEBP {
		if decoder == nil {
			return nil, ErrWebPEncoderUnavailable
		}
		return decoder.Decode(payload)
	}
	decoded, _, err := image.Decode(bytes.NewReader(payload))
	return decoded, err
}

func orientImage(src image.Image, orientation int) image.Image {
	srcBounds := src.Bounds()
	sw, sh := srcBounds.Dx(), srcBounds.Dy()
	dw, dh := sw, sh
	if orientation >= 5 && orientation <= 8 {
		dw, dh = sh, sw
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx, sy := x, y
			switch orientation {
			case 2:
				sx = sw - 1 - x
			case 3:
				sx, sy = sw-1-x, sh-1-y
			case 4:
				sy = sh - 1 - y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, sh-1-x
			case 7:
				sx, sy = sw-1-y, sh-1-x
			case 8:
				sx, sy = sw-1-y, x
			}
			dst.Set(x, y, src.At(srcBounds.Min.X+sx, srcBounds.Min.Y+sy))
		}
	}
	return dst
}

func resizeImage(src image.Image, width, height int) image.Image {
	srcBounds := src.Bounds()
	sw, sh := srcBounds.Dx(), srcBounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := srcBounds.Min.Y + y*sh/height
		for x := 0; x < width; x++ {
			sx := srcBounds.Min.X + x*sw/width
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func jpegOrientation(payload []byte) int {
	for offset := 2; offset+4 <= len(payload); {
		if payload[offset] != 0xff {
			offset++
			continue
		}
		marker := payload[offset+1]
		offset += 2
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if offset+2 > len(payload) {
			break
		}
		segmentLength := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(payload) {
			break
		}
		segment := payload[offset+2 : offset+segmentLength]
		if marker == 0xe1 && len(segment) >= 14 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation := parseTIFFOrientation(segment[6:]); orientation >= 1 && orientation <= 8 {
				return orientation
			}
		}
		offset += segmentLength
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var byteOrder binary.ByteOrder = binary.LittleEndian
	if string(tiff[:2]) == "MM" {
		byteOrder = binary.BigEndian
	} else if string(tiff[:2]) != "II" {
		return 1
	}
	if byteOrder.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := int(byteOrder.Uint32(tiff[4:8]))
	if ifdOffset < 0 || ifdOffset+2 > len(tiff) {
		return 1
	}
	entries := int(byteOrder.Uint16(tiff[ifdOffset : ifdOffset+2]))
	for index := 0; index < entries; index++ {
		entry := ifdOffset + 2 + index*12
		if entry+12 > len(tiff) {
			return 1
		}
		if byteOrder.Uint16(tiff[entry:entry+2]) != 0x0112 {
			continue
		}
		return int(byteOrder.Uint16(tiff[entry+8 : entry+10]))
	}
	return 1
}
