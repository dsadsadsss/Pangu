package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"log"
	"os"
)

// ---- UUID v4 ----

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---- logger interface ----

type logger interface {
	Printf(format string, v ...any)
}

type silentLogger struct{}

func (silentLogger) Printf(string, ...any) {}

func newLogger(enable, debug bool) logger {
	if !enable {
		return silentLogger{}
	}
	flags := log.LstdFlags
	if debug {
		flags |= log.Lshortfile
	}
	return log.New(os.Stdout, "", flags)
}

// suppress io import warning
var _ = io.EOF
