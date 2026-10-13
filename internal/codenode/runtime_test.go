package codenode

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testLimits() Limits {
	return Limits{
		Timeout:        time.Second,
		MaxCallStack:   1000,
		MaxInputBytes:  1 << 20,
		MaxOutputBytes: 1 << 20,
	}
}

// TestC1_TimeoutInterruptsInfiniteLoop proves control C1. An operator
// accidentally writing `while(true){}` must fail that one request, not hang a
// goroutine forever.
func TestC1_TimeoutInterruptsInfiniteLoop(t *testing.T) {
	rt, err := NewRuntime("while (true) {}", Limits{Timeout: 100 * time.Millisecond,
		MaxCallStack: 1000, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = rt.Run(context.Background(), map[string]any{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run() = nil for an infinite loop; C1 (timeout) did not fire")
	}
	if elapsed > time.Second {
		t.Errorf("took %v to interrupt a 100ms budget; the watchdog is not working", elapsed)
	}
}

func TestC1_CanceledContextInterrupts(t *testing.T) {
	rt, err := NewRuntime("while (true) {}", testLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err = rt.Run(ctx, map[string]any{})
	if err == nil {
		t.Fatal("canceled context did not interrupt; C1+ctx did not fire")
	}
	if time.Since(start) > time.Second {
		t.Errorf("canceled run took %v; watchdog ignored ctx", time.Since(start))
	}
}

// TestC2_DeepRecursionHitsStackLimit proves control C2.
func TestC2_DeepRecursionHitsStackLimit(t *testing.T) {
	rt, err := NewRuntime("function f(n){return f(n+1)} f(0)", testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), nil); err == nil {
		t.Fatal("unbounded recursion did not error; C2 (call stack bound) did not fire")
	}
}

// TestC3_NoHostObjectsAreReachable proves control C3 -- the most important
// one. Each of these must be undefined inside the sandbox.
func TestC3_NoHostObjectsAreReachable(t *testing.T) {
	forbidden := []string{
		"require", "process", "globalThis.process", "fetch", "XMLHttpRequest",
		"setTimeout", "setInterval", "Buffer", "__dirname", "module", "exports",
	}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			rt, err := NewRuntime("return typeof "+name, testLimits())
			if err != nil {
				t.Fatal(err)
			}
			out, err := rt.Run(context.Background(), nil)
			if err != nil {
				t.Fatalf("Run() = %v", err)
			}
			if out != "undefined" {
				t.Errorf("typeof %s = %v, want undefined: the sandbox must expose no host objects", name, out)
			}
		})
	}
}

func TestC4_OversizeInputRefused(t *testing.T) {
	rt, err := NewRuntime("return input", Limits{
		Timeout: time.Second, MaxCallStack: 1000, MaxInputBytes: 32, MaxOutputBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Run(context.Background(), map[string]any{"payload": strings.Repeat("a", 64)})
	if err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatalf("err = %v, want C4 input size refusal", err)
	}
}

func TestC5_OversizeOutputRefused(t *testing.T) {
	rt, err := NewRuntime(`var s = ""; for (var i = 0; i < 1000; i++) s += "xxxx"; return s`, Limits{
		Timeout: time.Second, MaxCallStack: 1000, MaxInputBytes: 1 << 20, MaxOutputBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("err = %v, want C5 output size refusal", err)
	}
}

// TestC6_RuntimeIsFreshPerRequest proves control C6: no state may leak between
// requests, so one event cannot poison the next.
func TestC6_RuntimeIsFreshPerRequest(t *testing.T) {
	rt, err := NewRuntime("globalThis.leaked = (globalThis.leaked || 0) + 1; return globalThis.leaked", testLimits())
	if err != nil {
		t.Fatal(err)
	}
	first, err := rt.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("global state leaked between runs: got %v then %v; C6 requires a fresh runtime", first, second)
	}
}

// TestC7_ScriptPanicDoesNotKillTheProcess proves control C7.
func TestC7_ScriptPanicDoesNotKillTheProcess(t *testing.T) {
	rt, err := NewRuntime("null.foo.bar", testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Run(context.Background(), nil); err == nil {
		t.Fatal("a TypeError did not surface as an error")
	}
	// Surviving to this line is the assertion.
}

func TestNewRuntimeRejectsSyntaxError(t *testing.T) {
	_, err := NewRuntime("function (", testLimits())
	if err == nil {
		t.Fatal("syntax error compiled; validate would miss it")
	}
}
