package storage

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
)

func TestInspectImageUsesMagicBytesAndHash(t *testing.T) {
	var encoded bytes.Buffer
	input := image.NewRGBA(image.Rect(0, 0, 640, 320))
	input.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatal(err)
	}
	info, err := InspectImage(encoded.Bytes(), DefaultImageLimits())
	if err != nil {
		t.Fatal(err)
	}
	if info.Format != ImagePNG || info.Width != 640 || info.Height != 320 || len(info.SHA256) != 64 {
		t.Fatalf("unexpected image info: %+v", info)
	}
	if _, err = InspectImage([]byte("not-a-png"), DefaultImageLimits()); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("expected unsupported image, got %v", err)
	}
	if _, err = InspectImage([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}, DefaultImageLimits()); !errors.Is(err, ErrHEICDisabled) {
		t.Fatalf("expected HEIC rejection, got %v", err)
	}
}

func TestValidateImageAndVariantPlanNeverUpscale(t *testing.T) {
	var encoded bytes.Buffer
	input := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	if err := png.Encode(&encoded, input); err != nil {
		t.Fatal(err)
	}
	payload, info, err := ValidateImage(context.Background(), bytes.NewReader(encoded.Bytes()), DefaultImageLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != len(encoded.Bytes()) || info.Width != 1000 {
		t.Fatalf("unexpected validation result: %d bytes %+v", len(payload), info)
	}
	plans, err := PlanImageVariants("company/product/image-id", info.Width, info.Height)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 4 { // master + 128/320/768; 1600 would upscale
		t.Fatalf("unexpected plans: %+v", plans)
	}
	if plans[0].Name != "master" || plans[len(plans)-1].Width >= info.Width {
		t.Fatalf("variant plan upscales source: %+v", plans)
	}
	if _, err = (ImageProcessor{}).Process(context.Background(), payload, "company/product/image-id"); !errors.Is(err, ErrWebPEncoderUnavailable) {
		t.Fatalf("expected explicit WebP encoder contract error, got %v", err)
	}
	processed, err := (ImageProcessor{Encoder: fakeWebPEncoder{}}).Process(context.Background(), payload, "company/product/image-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(processed.Master) == 0 || len(processed.VariantPayloads) != len(processed.Plans) {
		t.Fatalf("encoder output missing: %+v", processed)
	}
}

func TestLibWebPCodecRoundTripWhenRuntimeIsInstalled(t *testing.T) {
	codec, err := NewLibWebPCodec()
	if err != nil {
		t.Skipf("libwebp runtime is unavailable: %v", err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 48, 24))
	source.Set(3, 4, color.RGBA{R: 240, G: 20, B: 10, A: 255})
	var pngPayload bytes.Buffer
	if err = png.Encode(&pngPayload, source); err != nil {
		t.Fatal(err)
	}
	processed, err := (ImageProcessor{Encoder: codec, Decoder: codec}).Process(context.Background(), pngPayload.Bytes(), "company/product/image-id")
	if err != nil {
		t.Fatal(err)
	}
	info, err := InspectImage(processed.Master, DefaultImageLimits())
	if err != nil || info.Format != ImageWEBP || info.Width != 48 || info.Height != 24 {
		t.Fatalf("unexpected WebP master: info=%+v err=%v", info, err)
	}
	decoded, err := codec.Decode(processed.Master)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 48 || decoded.Bounds().Dy() != 24 {
		t.Fatalf("unexpected WebP decode bounds: %v", decoded.Bounds())
	}
}

type fakeWebPEncoder struct{}

func (fakeWebPEncoder) Encode(writer io.Writer, source image.Image) error {
	_, err := io.WriteString(writer, "fake-webp")
	return err
}
