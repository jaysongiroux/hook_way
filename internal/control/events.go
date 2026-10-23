package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/problem"
)

type EventOperator interface {
	Operate(context.Context, eventops.Request) (any, error)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		problem.Write(w, 405, "method_not_allowed", "POST required")
		return
	}
	if !s.authorized(r) {
		problem.Write(w, 401, "unauthorized", "A control credential is required.")
		return
	}
	if s.draining.Load() {
		problem.Write(w, 503, "not_started", "Core is shutting down.")
		return
	}
	select {
	case s.slots <- struct{}{}:
		s.running.Add(1)
		defer func() { <-s.slots; s.running.Done() }()
	default:
		problem.Write(w, 503, "busy", "Core control is busy.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	var req eventops.Request
	if err != nil || eventops.Decode(raw, &req) != nil || req.Actor == "" {
		problem.Write(w, 400, "invalid_request", "Invalid operator request.")
		return
	}
	if s.draining.Load() {
		problem.Write(w, 503, "not_started", "Core is shutting down.")
		return
	}
	var value any
	if s.opts.Events == nil {
		if req.Operation == "status" {
			value = eventops.Status{State: "disabled"}
		} else {
			problem.Write(w, 503, "events_disabled", "Event delivery is disabled.")
			return
		}
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		value, err = s.opts.Events.Operate(ctx, req)
		if err != nil {
			var op *eventops.Error
			if errors.As(err, &op) {
				problem.Write(w, op.Status, op.Code, op.Code)
			} else {
				problem.Write(w, 503, "events_unavailable", "Event operations are unavailable.")
			}
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
