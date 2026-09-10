package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestRecordingDriverUsesRegisteredConfig(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgres://unused@127.0.0.1:5432/submission_import_test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("test intercepted dial without opening network")
	dialed := false
	cfg.DialFunc = func(context.Context, string, string) (net.Conn, error) { dialed = true; return nil, sentinel }
	name := stdlib.RegisterConnConfig(cfg)
	defer stdlib.UnregisterConnConfig(name)
	_, err = (recordingDriver{}).Open(name)
	if !dialed {
		t.Fatalf("recording driver did not resolve registered config: %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected intercepted dial, got %v", err)
	}
}
