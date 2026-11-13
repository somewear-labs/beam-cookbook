package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("GRID_WIZARD_TEST_CHILD"); mode != "" {
		fmt.Fprintln(os.Stdout, strings.Join(os.Args[1:], " "))
		if mode == "fail" {
			fmt.Fprintln(os.Stderr, "private diagnostic sample")
			os.Exit(7)
		}
		if mode == "wait" {
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
