package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// Uploader limits (spec "Collection and upload"). Constants, not
// configuration.
const (
	uploadWorkers     = 4
	maxPendingRecords = 128
	maxPendingBytes   = 64 << 20
	maxUploadAttempts = 3
	uploadBudget      = 5 * time.Second
	defaultDrainWait  = 6 * time.Second
	recordsPath       = "/v1/records"
)

// Drop reasons (hookway_capture_dropped_total).
const (
	DropBufferFull   = "buffer_full"
	DropUploadFailed = "upload_failed"
	DropCapacity     = "capacity"
	DropRejected     = "rejected"
	DropShutdown     = "shutdown"
)

// UploaderMetrics is the uploader's instrumentation (internal/metrics
// implements it).
type UploaderMetrics interface {
	CaptureUploaded(flow string)
	CaptureDropped(flow, reason string)
	SetCaptureBufferBytes(n int64)
}

// UploaderOptions configures an Uploader.
type UploaderOptions struct {
	URL     string
	Token   string
	TLS     tlsfiles.Files
	Metrics UploaderMetrics
	Logger  *slog.Logger
}

// pending is a failure waiting to become a record and be uploaded. estimate
// is held against the byte budget until the record is built.
type pending struct {
	flow, nodeID, eventID string
	inspection            bool
	estimate              int64
	build                 func() (Record, bool)
}

// Uploader sends records to the replay service on a few workers. Submitting
// never blocks: past the buffer limits a record is dropped and counted.
type Uploader struct {
	client    *http.Client
	url       string
	token     string
	metrics   UploaderMetrics
	logger    *slog.Logger
	mu        sync.Mutex
	closed    bool
	records   int
	bytes     int64
	jobs      chan pending
	wg        sync.WaitGroup
	stop      context.Context
	cancel    context.CancelFunc
	abandon   atomic.Bool
	backoff   time.Duration // first retry delay; tests shorten it
	drainWait time.Duration // Close's bound; tests shorten it
}

func NewUploader(o UploaderOptions) (*Uploader, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("capture: http.DefaultTransport is not an *http.Transport")
	}
	transport := base.Clone()
	if o.TLS.Configured() {
		cfg, err := o.TLS.Build("replay tls")
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = cfg
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}
	stop, cancel := context.WithCancel(context.Background())
	u := &Uploader{
		client: &http.Client{Transport: transport}, url: strings.TrimRight(o.URL, "/") + recordsPath, token: o.Token,
		metrics: o.Metrics, logger: logger, jobs: make(chan pending, maxPendingRecords),
		stop: stop, cancel: cancel, backoff: 100 * time.Millisecond, drainWait: defaultDrainWait,
	}
	for range uploadWorkers {
		u.wg.Add(1)
		go u.work()
	}
	return u, nil
}

// submit queues a failure without blocking.
func (u *Uploader) submit(p pending) {
	if u.reserve(p) {
		u.submitReserved(p)
	}
}

// reserve charges captures awaiting request settlement to the same bounded buffer.
func (u *Uploader) reserve(p pending) bool {
	u.mu.Lock()
	reason := ""
	switch {
	case u.closed:
		reason = DropShutdown
	case u.records+1 > maxPendingRecords || u.bytes+p.estimate > maxPendingBytes:
		reason = DropBufferFull
	default:
		u.records++
		u.bytes += p.estimate
		u.metrics.SetCaptureBufferBytes(u.bytes)
	}
	u.mu.Unlock()
	if reason != "" {
		u.drop(p, reason, nil)
		return false
	}
	return true
}

func (u *Uploader) submitReserved(p pending) {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		u.release(p.estimate)
		u.drop(p, DropShutdown, nil)
		return
	}
	u.jobs <- p
	u.mu.Unlock()
}

// Close stops intake and waits for queued records, at most drainWait; past
// that, in-flight uploads are cancelled and the rest dropped as shutdown.
func (u *Uploader) Close() error {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		return nil
	}
	u.closed = true
	close(u.jobs)
	u.mu.Unlock()
	done := make(chan struct{})
	go func() {
		u.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(u.drainWait):
		u.abandon.Store(true)
		u.cancel()
		<-done
	}
	u.cancel()
	return nil
}

func (u *Uploader) work() {
	defer u.wg.Done()
	for p := range u.jobs {
		u.process(p)
	}
}

func (u *Uploader) process(p pending) {
	held := p.estimate
	defer func() { u.release(held) }()
	if u.abandon.Load() {
		u.drop(p, DropShutdown, nil)
		return
	}
	rec, ok, err := build(p)
	p.build = nil // release the envelope the failure kept alive
	if err != nil {
		u.drop(p, DropRejected, err)
		return
	}
	if !ok {
		return
	}
	// An upload holds the record and the encoded multipart copy of it.
	u.mu.Lock()
	u.bytes += 2*rec.size() - held
	held = 2 * rec.size()
	over := u.bytes > maxPendingBytes
	u.metrics.SetCaptureBufferBytes(u.bytes)
	u.mu.Unlock()
	if over {
		u.drop(p, DropBufferFull, nil)
		return
	}
	if err := rec.Metadata.Validate(); err != nil {
		u.drop(p, DropRejected, err)
		return
	}
	if reason, err := u.upload(rec); reason != "" {
		if u.abandon.Load() {
			reason = DropShutdown
		}
		u.drop(p, reason, err)
		return
	}
	if p.inspection {
		if m, ok := u.metrics.(InspectionMetrics); ok {
			m.InspectionUploaded(p.flow)
		}
	} else {
		u.metrics.CaptureUploaded(p.flow)
	}
}

// build runs p's builder, turning a panic into an error: records are built
// off the request path, and a builder bug must cost one record, not the
// process.
func build(p pending) (rec Record, ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("building the record panicked: %v", r)
		}
	}()
	rec, ok = p.build()
	return rec, ok, nil
}

func (u *Uploader) release(n int64) {
	u.mu.Lock()
	u.records--
	u.bytes -= n
	u.metrics.SetCaptureBufferBytes(u.bytes)
	u.mu.Unlock()
}

// upload sends a record up to maxUploadAttempts times within uploadBudget,
// with the same body (and so the same record id) every time. It returns a
// drop reason, or "" once replay has the record.
func (u *Uploader) upload(rec Record) (string, error) {
	body, contentType, err := encodeUpload(rec)
	if err != nil {
		return DropRejected, err
	}
	ctx, cancel := context.WithTimeout(u.stop, uploadBudget)
	defer cancel()
	for attempt := 1; ; attempt++ {
		status, err := u.post(ctx, body, contentType)
		switch {
		case err == nil && (status == http.StatusCreated || status == http.StatusOK):
			return "", nil
		case err == nil && status == http.StatusInsufficientStorage:
			return DropCapacity, errors.New("replay is at capacity (507)")
		case err == nil && status < 500 && status != http.StatusTooManyRequests:
			return DropRejected, fmt.Errorf("replay refused the record (%d)", status)
		case err == nil:
			err = fmt.Errorf("replay answered %d", status)
		}
		if attempt == maxUploadAttempts {
			return DropUploadFailed, err
		}
		wait := u.backoff << (attempt - 1)
		wait += time.Duration(rand.Int64N(int64(wait) + 1)) // #nosec G404 -- retry jitter, not a credential
		select {
		case <-ctx.Done():
			return DropUploadFailed, err
		case <-time.After(wait):
		}
	}
}

func (u *Uploader) post(ctx context.Context, body []byte, contentType string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+u.token)
	resp, err := u.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

func encodeUpload(rec Record) ([]byte, string, error) {
	meta, err := json.Marshal(rec.Metadata)
	if err != nil {
		return nil, "", err
	}
	parts := []struct {
		name string
		data []byte
	}{{replaywire.PartMetadata, meta}}
	if rec.Metadata.Snapshot != nil {
		parts = append(parts, struct {
			name string
			data []byte
		}{replaywire.PartSnapshot, rec.Snapshot})
	}
	if rec.Metadata.Payload != nil {
		parts = append(parts, struct {
			name string
			data []byte
		}{replaywire.PartPayload, rec.Payload})
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		pw, err := w.CreateFormField(p.name)
		if err != nil {
			return nil, "", err
		}
		if _, err := pw.Write(p.data); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func (u *Uploader) drop(p pending, reason string, err error) {
	if p.inspection {
		if m, ok := u.metrics.(InspectionMetrics); ok {
			m.InspectionDropped(p.flow, reason)
		}
	} else {
		u.metrics.CaptureDropped(p.flow, reason)
	}
	attrs := []any{"reason", reason, "flow", p.flow, "node_id", p.nodeID, "event_id", p.eventID}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	kind := replaywire.KindFailure
	message := "failure capture dropped a record"
	if p.inspection {
		kind = replaywire.KindInspection
		message = "inspection capture dropped a record"
	}
	attrs = append(attrs, "kind", kind)
	u.logger.Error(message, attrs...)
}
