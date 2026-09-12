package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
)

type blockingBackupEngine struct {
	started chan struct{}
	finish  chan struct{}
}

func (e *blockingBackupEngine) wait() (backup.Manifest, error) {
	e.started <- struct{}{}
	<-e.finish
	return backup.Manifest{}, nil
}

func (e *blockingBackupEngine) Create(context.Context, io.Writer) (backup.Manifest, error) {
	return e.wait()
}

func (e *blockingBackupEngine) Restore(context.Context, io.Reader, backup.RestoreOptions) (backup.Manifest, error) {
	return e.wait()
}

func (e *blockingBackupEngine) Verify(context.Context, io.Reader) (backup.Manifest, error) {
	return backup.Manifest{}, nil
}

// scriptedEngine returns whatever the test tells it to.
type scriptedEngine struct {
	verifyErr  error
	restoreErr error
	restores   int
}

func (e *scriptedEngine) Create(_ context.Context, w io.Writer) (backup.Manifest, error) {
	_, err := io.WriteString(w, "archive")
	return backup.Manifest{}, err
}

func (e *scriptedEngine) Verify(context.Context, io.Reader) (backup.Manifest, error) {
	return backup.Manifest{}, e.verifyErr
}

func (e *scriptedEngine) Restore(context.Context, io.Reader, backup.RestoreOptions) (backup.Manifest, error) {
	e.restores++
	return backup.Manifest{}, e.restoreErr
}

func newTestHandler(t *testing.T, engine backupEngine) (systemHandler, *opctl.Controller) {
	t.Helper()
	dir := t.TempDir()
	controller, err := opctl.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return systemHandler{engine: engine, controller: controller, spoolDir: defaultSpoolDir(dir)}, controller
}

func restoreRequest(t *testing.T, contents string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "test.varya")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(file, contents); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/system/backup/restore", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	return r
}

// TestBackupOperationsDoNotOverlap is the API half of the coordination rule. It
// is now enforced by the installation's lease rather than an in-process mutex,
// which is what makes it hold against the CLI and the deploy script too.
func TestBackupOperationsDoNotOverlap(t *testing.T) {
	for _, first := range []string{"download", "restore"} {
		for _, second := range []string{"download", "restore"} {
			t.Run(first+"_then_"+second, func(t *testing.T) {
				engine := &blockingBackupEngine{started: make(chan struct{}, 2), finish: make(chan struct{})}
				h, _ := newTestHandler(t, engine)
				invoke := func(operation string, response *httptest.ResponseRecorder, request *http.Request) {
					if operation == "download" {
						h.download(response, request)
					} else {
						h.restore(response, request)
					}
				}
				firstRequest := restoreRequest(t, "archive")
				secondRequest := restoreRequest(t, "archive")
				firstDone, secondDone := make(chan struct{}), make(chan struct{})
				go func() {
					defer close(firstDone)
					defer func() { _ = recover() }() // download panics on a stream error
					invoke(first, httptest.NewRecorder(), firstRequest)
				}()
				t.Cleanup(func() { close(engine.finish); <-firstDone; <-secondDone })
				<-engine.started
				response := httptest.NewRecorder()
				go func() {
					defer close(secondDone)
					defer func() { _ = recover() }()
					invoke(second, response, secondRequest)
				}()
				select {
				case <-engine.started:
					t.Fatal("a second operation entered the engine while a backup operation was active")
				case <-secondDone:
					if response.Code != http.StatusConflict {
						t.Fatalf("status = %d, want 409", response.Code)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("busy backup operation did not return promptly")
				}
				if secondRequest.MultipartForm != nil {
					t.Fatal("busy restore parsed the upload before rejecting it")
				}
			})
		}
	}
}

// TestRestoreRejectsOversizedUploadBeforeSpooling keeps a request that is too
// large from being written to disk at all: the limit has to bite before the
// bytes land, not after.
func TestRestoreRejectsOversizedUploadBeforeSpooling(t *testing.T) {
	for _, unknownLength := range []bool{false, true} {
		h, _ := newTestHandler(t, &scriptedEngine{})
		r := restoreRequest(t, strings.Repeat("x", 4096))
		if unknownLength {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		_, err := h.spoolUpload(w, r)
		var maxErr *http.MaxBytesError
		if !errors.As(err, &maxErr) {
			t.Fatalf("oversized upload error = %v, want MaxBytesError", err)
		}
		entries, _ := os.ReadDir(h.spoolDir)
		if len(entries) != 0 {
			t.Fatalf("oversized upload left %d spool files behind", len(entries))
		}
	}
}

func TestSpoolUploadWritesTheArchiveToDisk(t *testing.T) {
	h, _ := newTestHandler(t, &scriptedEngine{})
	r := restoreRequest(t, "small archive")
	uploaded, err := h.spoolUpload(httptest.NewRecorder(), r)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(uploaded.path)
	if err != nil || string(body) != "small archive" {
		t.Fatalf("spooled = %q, %v", body, err)
	}
}

// TestSpoolUploadCollectsScalarFields: taking the multipart reader disables
// FormValue for the rest of the request, so the fields must come out of the
// same pass. Without this the force flag would be silently dropped and a
// deliberate --force restore would fail as if it had not been asked for.
func TestSpoolUploadCollectsScalarFields(t *testing.T) {
	h, _ := newTestHandler(t, &scriptedEngine{})
	for _, fieldFirst := range []bool{true, false} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		writeField := func() {
			if err := form.WriteField("force", "true"); err != nil {
				t.Fatal(err)
			}
		}
		writeFile := func() {
			file, err := form.CreateFormFile("file", "test.varya")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(file, "archive"); err != nil {
				t.Fatal(err)
			}
		}
		if fieldFirst {
			writeField()
			writeFile()
		} else {
			writeFile()
			writeField()
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/system/backup/restore", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		uploaded, err := h.spoolUpload(httptest.NewRecorder(), r)
		if err != nil {
			t.Fatal(err)
		}
		if uploaded.fields["force"] != "true" {
			t.Fatalf("force field lost (field first = %v): %v", fieldFirst, uploaded.fields)
		}
		_ = os.Remove(uploaded.path)
	}
}

// TestRestoreRejectsInvalidArchiveBeforeTouchingAnything: a bad archive must be
// refused by Verify, and Restore must never be reached.
func TestRestoreRejectsInvalidArchiveBeforeTouchingAnything(t *testing.T) {
	engine := &scriptedEngine{verifyErr: backup.ErrArchiveInvalid}
	h, controller := newTestHandler(t, engine)
	w := httptest.NewRecorder()
	h.restore(w, restoreRequest(t, "not a real archive"))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", w.Code, w.Body)
	}
	if engine.restores != 0 {
		t.Fatal("an unverified archive reached the restore engine")
	}
	status, err := controller.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Serviceable {
		t.Fatalf("a rejected upload left the installation out of service: %s", status.Reason)
	}
	if status.Operation.Phase != opctl.PhaseFailedUnchanged {
		t.Fatalf("phase = %s, want FAILED_UNCHANGED", status.Operation.Phase)
	}
}

// TestRestoreFailureAfterCommitBlocksStartup is the whole point of the journal:
// a failure past the point of no return must leave the installation refusing to
// serve until a person resolves it.
func TestRestoreFailureAfterCommitBlocksStartup(t *testing.T) {
	engine := &scriptedEngine{restoreErr: backup.ErrStoragePartial}
	h, controller := newTestHandler(t, engine)
	w := httptest.NewRecorder()
	h.restore(w, restoreRequest(t, "archive"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body)
	}
	if err := controller.GateStartup(); !errors.Is(err, opctl.ErrRecoveryRequired) {
		t.Fatalf("GateStartup = %v, want ErrRecoveryRequired", err)
	}
	// And a second restore must not be allowed to stack on top of the first.
	second := httptest.NewRecorder()
	h.restore(second, restoreRequest(t, "archive"))
	if second.Code != http.StatusConflict {
		t.Fatalf("second restore status = %d, want 409: %s", second.Code, second.Body)
	}
	if engine.restores != 1 {
		t.Fatalf("restore engine ran %d times, want 1", engine.restores)
	}

	// Resolving is what an operator does after checking; only then may the
	// installation come back.
	if err := controller.Resolve("checked by hand, database verified"); err != nil {
		t.Fatal(err)
	}
	if err := controller.GateStartup(); err != nil {
		t.Fatalf("GateStartup after resolve = %v, want nil", err)
	}
}

// TestRestoreIdempotencyKeyPreventsDoubleRestore covers the lost-response case:
// the user clicks again because the first answer never arrived, and the second
// click must report the first operation rather than start a new restore.
func TestRestoreIdempotencyKeyPreventsDoubleRestore(t *testing.T) {
	engine := &scriptedEngine{}
	h, controller := newTestHandler(t, engine)

	first := httptest.NewRecorder()
	request := restoreRequest(t, "archive")
	request.Header.Set("Idempotency-Key", "click-1")
	h.restore(first, request)
	if first.Code != http.StatusOK {
		t.Fatalf("first restore status = %d: %s", first.Code, first.Body)
	}
	operationID := first.Header().Get("X-Operation-Id")
	if operationID == "" {
		t.Fatal("no operation id returned")
	}

	second := httptest.NewRecorder()
	repeat := restoreRequest(t, "archive")
	repeat.Header.Set("Idempotency-Key", "click-1")
	h.restore(second, repeat)

	if engine.restores != 1 {
		t.Fatalf("restore ran %d times for one idempotency key, want 1", engine.restores)
	}
	if second.Code != http.StatusAccepted {
		t.Fatalf("repeat status = %d, want 202: %s", second.Code, second.Body)
	}
	var payload struct {
		Operation *opctl.Record `json:"operation"`
	}
	if err := json.NewDecoder(second.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Operation == nil || payload.Operation.ID != operationID {
		t.Fatalf("repeat reported operation %+v, want %s", payload.Operation, operationID)
	}
	_ = controller
}

// TestSpoolFilesAreRemoved: uploads are the whole installation's data. None may
// outlive the request that carried them.
func TestSpoolFilesAreRemoved(t *testing.T) {
	h, _ := newTestHandler(t, &scriptedEngine{})
	h.restore(httptest.NewRecorder(), restoreRequest(t, "archive"))
	entries, err := os.ReadDir(h.spoolDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("spool file left behind: %s", filepath.Join(h.spoolDir, entry.Name()))
	}
}

// TestBackupRoutesRequireBackupPermission pins the gate on the system routes.
//
// The permission is wider than its name: these routes act on the whole
// installation, so on a multi-company installation its holder can read and
// replace data belonging to companies they are not a member of. That is the
// chosen policy — an installation belongs to one operator, and companies are
// that operator's legal entities rather than separate tenants. The test exists
// so the gate cannot be removed or widened by accident.
func TestBackupRoutesRequireBackupPermission(t *testing.T) {
	h, _ := newTestHandler(t, &scriptedEngine{})
	reached := false
	guarded := h.requireBackupPermission(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	for name, tc := range map[string]struct {
		session identity.Session
		allow   bool
	}{
		"holder of the backup permission": {
			session: identity.Session{Permissions: []string{"system.backup.manage"}},
			allow:   true,
		},
		"other permissions only": {
			session: identity.Session{Permissions: []string{"party.read", "finance.payment.read"}},
			allow:   false,
		},
		"instance owner without the permission": {
			// Owning the installation is not itself the grant; the permission is.
			session: identity.Session{IsInstanceOwner: true},
			allow:   false,
		},
		"no session at all": {session: identity.Session{}, allow: false},
	} {
		t.Run(name, func(t *testing.T) {
			reached = false
			r := httptest.NewRequest(http.MethodGet, "/api/v1/system/backup", nil)
			r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, tc.session))
			w := httptest.NewRecorder()
			guarded.ServeHTTP(w, r)
			if tc.allow {
				if !reached || w.Code != http.StatusOK {
					t.Fatalf("permitted session was refused: status %d, reached %v", w.Code, reached)
				}
				return
			}
			if reached {
				t.Fatal("a session without the backup permission reached the system routes")
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
		})
	}
}
