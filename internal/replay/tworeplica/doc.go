// Package tworeplica tests two replay replicas sharing one PostgreSQL
// database: every store and worker here is real, so the races between
// replicas are the ones production would see. It has no non-test code.
package tworeplica
