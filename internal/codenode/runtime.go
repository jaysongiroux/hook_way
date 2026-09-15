// Package codenode runs operator-authored JavaScript in a sandboxed goja VM.
//
// The sandbox defends against operator accidents (infinite loops, runaway
// recursion, accidental host I/O), not a hostile tenant. Flow authors are
// already inside the trust boundary. See v2/02-design.md §6.
package codenode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Limits are the C1–C5 budgets applied to one compiled script. A zero Timeout
// or MaxCallStack is rejected at NewRuntime so a code node cannot be loaded
// without those controls.
type Limits struct {
	Timeout        time.Duration // C1
	MaxCallStack   int           // C2
	MaxInputBytes  int           // C4
	MaxOutputBytes int           // C5
}

// Runtime is a compiled script. Run uses a fresh goja VM each call (C6).
type Runtime struct {
	prog   *goja.Program
	limits Limits
}

// NewRuntime compiles script once at config load as a function body so a
// top-level `return` is legal. A syntax error fails here so hookway validate
// catches it, not the first request.
func NewRuntime(script string, l Limits) (*Runtime, error) {
	if l.Timeout <= 0 {
		return nil, fmt.Errorf("codenode: timeout must be positive")
	}
	if l.MaxCallStack <= 0 {
		return nil, fmt.Errorf("codenode: max_call_stack must be positive")
	}
	if l.MaxInputBytes <= 0 {
		return nil, fmt.Errorf("codenode: max_input_bytes must be positive")
	}
	if l.MaxOutputBytes <= 0 {
		return nil, fmt.Errorf("codenode: max_output_bytes must be positive")
	}
	prog, err := goja.Compile("code", "(function() {\n"+script+"\n})()", false)
	if err != nil {
		return nil, fmt.Errorf("codenode: compile: %w", err)
	}
	return &Runtime{prog: prog, limits: l}, nil
}

// Run evaluates the compiled program against input. A fresh goja.Runtime is
// created per call; do not pool VMs — they retain global state (C6).
func (rt *Runtime) Run(ctx context.Context, input any) (out any, err error) {
	if err := checkSize(input, rt.limits.MaxInputBytes, "input"); err != nil {
		return nil, err
	}

	vm := goja.New()
	// C3: a bare goja.New() has no require, fetch, timers, or filesystem.
	// Do not register host objects. TestC3_NoHostObjectsAreReachable is the
	// guard against adding them later for convenience.
	vm.SetMaxCallStackSize(rt.limits.MaxCallStack)
	if err := vm.Set("input", input); err != nil {
		return nil, fmt.Errorf("codenode: set input: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, rt.limits.Timeout)
	defer cancel()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-runCtx.Done():
			vm.Interrupt("codenode: interrupted")
		case <-done:
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		vm.ClearInterrupt()
	}()

	defer func() {
		if p := recover(); p != nil {
			out = nil
			err = fmt.Errorf("codenode: panicked: %v", p)
		}
	}()

	val, err := vm.RunProgram(rt.prog)
	if err != nil {
		return nil, fmt.Errorf("codenode: %w", err)
	}
	if val == nil {
		return nil, nil
	}
	exported := val.Export()
	if err := checkSize(exported, rt.limits.MaxOutputBytes, "output"); err != nil {
		return nil, err
	}
	return exported, nil
}

func checkSize(v any, max int, what string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("codenode: %s is not serializable: %w", what, err)
	}
	if len(b) > max {
		return fmt.Errorf("codenode: %s exceeds %d bytes (%d)", what, max, len(b))
	}
	return nil
}
