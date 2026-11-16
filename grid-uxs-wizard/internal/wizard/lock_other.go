//go:build !linux && !darwin

package wizard

import "errors"

func lockUxS(string) (func(), error) { return nil, errors.New("UxS setup requires Linux") }

func lockRuntime(string) (func(), error) { return nil, errors.New("UxS runtime requires Linux") }
