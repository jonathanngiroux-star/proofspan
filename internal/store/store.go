// Package store persists trajectories and eval runs in SQLite.
//
// The store is intentionally tiny: one file, one writer, no migrations
// framework. Schema changes bump the CREATE TABLE IF NOT EXISTS list in
// sqlite.go and stay additive; v0.1 data is throwaway-grade and the
// converter corpus is the real durable artifact.
package store
