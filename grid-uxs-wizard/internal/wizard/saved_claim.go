package wizard

import (
	"errors"
	"os"
	"strings"
)

var errNewClaimRequiresFreshState = errors.New("setup stopped without changes; the saved claim and running installation were preserved")

type claimSelection struct {
	Path, Code string
	Replace    bool
}

func selectClaim(saved, supplied string, input func(bool) (string, error)) (claimSelection, error) {
	var previous string
	if _, err := os.Lstat(saved); err == nil {
		if err := checkCodeFile(saved); err != nil {
			return claimSelection{}, err
		}
		data, err := os.ReadFile(saved)
		if err != nil {
			return claimSelection{}, err
		}
		previous = strings.TrimSpace(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return claimSelection{}, err
	}
	if supplied != "" {
		if err := checkCodeFile(supplied); err != nil {
			return claimSelection{}, err
		}
		data, err := os.ReadFile(supplied)
		if err != nil {
			return claimSelection{}, err
		}
		value := strings.TrimSpace(string(data))
		if !validClaimCode(value) {
			return claimSelection{}, errors.New("invalid claim file; use the entire code from the GCS")
		}
		if previous != "" && value != previous {
			return claimSelection{Code: value, Replace: true}, nil
		}
		return claimSelection{Path: supplied}, nil
	}
	value, err := input(previous != "")
	if err != nil {
		return claimSelection{}, err
	}
	if previous != "" && (value == "" || value == previous) {
		return claimSelection{Path: saved}, nil
	}
	if !validClaimCode(value) {
		return claimSelection{}, errors.New("paste the entire setup code from the GCS")
	}
	return claimSelection{Code: value, Replace: previous != ""}, nil
}

func selectSetup(saved, supplied string, replace func() (bool, error)) (claimSelection, error) {
	if supplied != "" {
		return selectClaim(saved, supplied, func(bool) (string, error) { return "", errors.New("explicit setup file is required") })
	}
	if _, err := os.Lstat(saved); errors.Is(err, os.ErrNotExist) {
		return claimSelection{}, nil
	} else if err != nil {
		return claimSelection{}, err
	}
	if err := checkCodeFile(saved); err != nil {
		return claimSelection{}, err
	}
	choice, err := replace()
	if err != nil {
		return claimSelection{}, err
	}
	if choice {
		return claimSelection{Replace: true}, nil
	}
	return claimSelection{Path: saved}, nil
}
