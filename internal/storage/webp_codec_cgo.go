//go:build cgo

package storage

/*
#cgo LDFLAGS: -ldl
#include <stdint.h>
#include <stddef.h>
#include <dlfcn.h>
#include <pthread.h>

// Keep the boundary deliberately small. The runtime library is loaded by
// name, so the Go package remains buildable in no-libwebp development images;
// production wiring still fails closed if the documented runtime is absent.
typedef size_t (*varya_encode_fn)(const uint8_t*, int, int, int, float, uint8_t**);
typedef uint8_t* (*varya_decode_fn)(const uint8_t*, size_t, int*, int*);
typedef void (*varya_free_fn)(void*);

static void *varya_webp_handle;
static varya_encode_fn varya_encode;
static varya_decode_fn varya_decode;
static varya_free_fn varya_free;
static pthread_once_t varya_webp_once = PTHREAD_ONCE_INIT;

static void varya_webp_initialize(void) {
    varya_webp_handle = dlopen("libwebp.so.7", RTLD_LAZY | RTLD_LOCAL);
    if (varya_webp_handle == NULL) return;
    *(void **)(&varya_encode) = dlsym(varya_webp_handle, "WebPEncodeRGBA");
    *(void **)(&varya_decode) = dlsym(varya_webp_handle, "WebPDecodeRGBA");
    *(void **)(&varya_free) = dlsym(varya_webp_handle, "WebPFree");
}

static int varya_webp_load(void) {
    pthread_once(&varya_webp_once, varya_webp_initialize);
    return varya_encode != NULL && varya_decode != NULL && varya_free != NULL;
}

static int varya_webp_available(void) { return varya_webp_load(); }
static size_t varya_webp_encode(const uint8_t *rgba, int width, int height,
    int stride, float quality_factor, uint8_t **output) {
    if (!varya_webp_load()) return 0;
    return varya_encode(rgba, width, height, stride, quality_factor, output);
}
static uint8_t *varya_webp_decode(const uint8_t *data, size_t data_size,
    int *width, int *height) {
    if (!varya_webp_load()) return NULL;
    return varya_decode(data, data_size, width, height);
}
static void varya_webp_free(void *ptr) {
    if (ptr != NULL && varya_webp_load()) varya_free(ptr);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"unsafe"
)

// libWebPCodec is the production WebP boundary. libwebp allocates encoded
// buffers with WebP malloc; ownership is released immediately after copying
// the bytes into Go memory.
type libWebPCodec struct{}

// NewLibWebPCodec returns the application codec when cgo and the small
// libwebp runtime are present. The no-cgo build returns a clear configuration
// error from the companion file instead of silently storing another format.
func NewLibWebPCodec() (WebPCodec, error) {
	if C.varya_webp_available() == 0 {
		return nil, ErrWebPEncoderUnavailable
	}
	return libWebPCodec{}, nil
}

func (libWebPCodec) Encode(writer io.Writer, source image.Image) error {
	if writer == nil || source == nil {
		return errors.New("WebP encoder requires writer and image")
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return errors.New("WebP encoder requires positive dimensions")
	}
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(rgba, rgba.Bounds(), source, bounds.Min, draw.Src)
	var output *C.uint8_t
	size := C.varya_webp_encode(
		(*C.uint8_t)(unsafe.Pointer(&rgba.Pix[0])),
		C.int(width), C.int(height), C.int(rgba.Stride), C.float(90), &output,
	)
	if size == 0 || output == nil {
		return errors.New("libwebp failed to encode image")
	}
	defer C.varya_webp_free(unsafe.Pointer(output))
	encoded := C.GoBytes(unsafe.Pointer(output), C.int(size))
	if _, err := writer.Write(encoded); err != nil {
		return err
	}
	return nil
}

func (libWebPCodec) Decode(payload []byte) (image.Image, error) {
	if len(payload) == 0 {
		return nil, errors.New("WebP decoder requires payload")
	}
	var width, height C.int
	decoded := C.varya_webp_decode(
		(*C.uint8_t)(unsafe.Pointer(&payload[0])), C.size_t(len(payload)), &width, &height,
	)
	if decoded == nil || width <= 0 || height <= 0 {
		return nil, errors.New("libwebp failed to decode image")
	}
	defer C.varya_webp_free(unsafe.Pointer(decoded))
	widthInt, heightInt := int(width), int(height)
	byteCount := widthInt * heightInt * 4
	if byteCount <= 0 {
		return nil, fmt.Errorf("WebP decoded dimensions are invalid: %dx%d", widthInt, heightInt)
	}
	pixels := C.GoBytes(unsafe.Pointer(decoded), C.int(byteCount))
	return &image.RGBA{Pix: pixels, Stride: widthInt * 4, Rect: image.Rect(0, 0, widthInt, heightInt)}, nil
}
