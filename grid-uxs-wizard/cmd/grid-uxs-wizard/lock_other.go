//go:build !linux && !darwin

package main

import "errors"

func lockUxS(string) (func(), error) { return nil, errors.New("UxS setup requires Linux") }

func lockRuntime(string) (func(), error) { return nil, errors.New("UxS runtime requires Linux") }
