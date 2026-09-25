// Package redrivecontract tests that replay's executor and core's control
// listener agree on POST /internal/replay/v1/deliver end to end: a record core
// captured, redriven by replay's real executor through core's real listener.
// It has no non-test code.
package redrivecontract
