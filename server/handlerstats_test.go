package server

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// flusherRecorder wraps httptest-style recording and additionally implements
// http.Flusher so we can verify sseWriteEvent invokes Flush().
type flusherRecorder struct {
	bytes.Buffer
	flushed    int
	writeErrAt int // 1-based byte index after which Write should fail
	err        error
}

func (f *flusherRecorder) Header() http.Header { return http.Header{} }

func (f *flusherRecorder) WriteHeader(statusCode int) {}

func (f *flusherRecorder) Write(p []byte) (int, error) {
	if f.err != nil && f.writeErrAt > 0 && f.Buffer.Len() >= f.writeErrAt {
		return 0, f.err
	}
	return f.Buffer.Write(p)
}

func (f *flusherRecorder) Flush() {
	f.flushed++
}

func TestSSEWriteEventFormat(t *testing.T) {
	rec := &flusherRecorder{}
	flusher, ok := any(rec).(http.Flusher)
	assert.True(t, ok, "recorder must implement http.Flusher")

	err := sseWriteEvent(rec, flusher, []byte(`{"v":1}`))
	assert.NoError(t, err)

	want := "event: update\ndata: {\"v\":1}\n\n"
	assert.Equal(t, want, rec.Buffer.String(), "SSE frame format must match the protocol")
	assert.Equal(t, 1, rec.flushed, "Flush must be called exactly once per write")
}

func TestSSEWriteEventError(t *testing.T) {
	// Inject a write error on the second Write call (the JSON payload write).
	rec := &flusherRecorder{
		err:        errors.New("connection reset by peer"),
		writeErrAt: len("event: update\ndata: "), // fail right when payload starts
	}
	flusher := any(rec).(http.Flusher)

	err := sseWriteEvent(rec, flusher, []byte(`{"v":1}`))
	assert.Error(t, err, "sseWriteEvent must propagate write errors")
	assert.Equal(t, 0, rec.flushed, "Flush must NOT be called when write fails")
}
