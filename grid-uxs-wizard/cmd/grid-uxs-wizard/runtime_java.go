package main

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"time"
)

func validateJavaVersion(output []byte) error {
	if !regexp.MustCompile(`(?m)^(openjdk|java) version "21[."]`).Match(output) {
		return errors.New("JAR installations require Java 21")
	}
	return nil
}
func requireJava21() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "java", "-version").CombinedOutput()
	if err != nil {
		return errors.New("Java 21 runtime is unavailable")
	}
	return validateJavaVersion(output)
}
