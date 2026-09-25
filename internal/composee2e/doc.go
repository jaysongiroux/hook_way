// Package composee2e deploys the `make dev-up` stack
// (examples/docker-compose.yaml) in Docker and drives it from the outside:
// signed GitHub webhooks in, a destination taken down, the missed delivery
// found in the replay service, a retry through its API, and the event checked
// at both destinations.
//
// Run it with `make compose-e2e-test` (HOOKWAY_COMPOSE_E2E=1). By default the
// test compiles linux binaries on the host and copies them into scratch images
// that mirror the release Dockerfile's runtime (testdata/Dockerfile). Set
// HOOKWAY_COMPOSE_E2E_BUILD=dockerfile to build the release images from the
// Dockerfile instead, as CI does.
//
// The package has no production code.
package composee2e
