package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type selectorAction int

const (
	selectorContinue selectorAction = iota
	selectorChoose
	selectorCancel
)

func selectTarget(targets []discoveredTarget, input *os.File, output io.Writer) (discoveredTarget, bool, error) {
	if len(targets) == 0 {
		return discoveredTarget{}, false, nil
	}

	fd := int(input.Fd())
	if !term.IsTerminal(fd) {
		return discoveredTarget{}, false, fmt.Errorf("multiple targets found but stdin is not a terminal; rerun interactively or use --target-user")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return discoveredTarget{}, false, fmt.Errorf("enable terminal selector: %w", err)
	}
	defer term.Restore(fd, oldState)

	fmt.Fprint(output, "Select a Grid Remote Shell target:\r\n\r\n")
	selected := 0
	renderTargetSelector(output, targets, selected, false)

	for {
		key, err := readSelectorKey(input)
		if err != nil {
			return discoveredTarget{}, false, err
		}
		next, action := applySelectorKey(selected, len(targets), key)
		selected = next
		switch action {
		case selectorChoose:
			return targets[selected], true, nil
		case selectorCancel:
			return discoveredTarget{}, false, nil
		default:
			renderTargetSelector(output, targets, selected, true)
		}
	}
}

func renderTargetSelector(output io.Writer, targets []discoveredTarget, selected int, redraw bool) {
	rows := len(targets) + 1
	if redraw {
		fmt.Fprintf(output, "\033[%dA", rows)
	}
	for i, target := range targets {
		pointer := " "
		if i == selected {
			pointer = "❯"
		}
		resp := target.response
		fmt.Fprintf(output, "\r\033[2K%s %-24s %-8s %-12s account %d\r\n",
			pointer, resp.GetHostname(), resp.GetArch(), formatCapabilities(resp.GetCapabilities()), target.accountID)
	}
	fmt.Fprint(output, "\r\033[2K↑/↓ or j/k select • Enter connect • q cancel\r\n")
}

func readSelectorKey(input io.Reader) ([]byte, error) {
	key := make([]byte, 1)
	if _, err := io.ReadFull(input, key); err != nil {
		return nil, err
	}
	if key[0] != 0x1b {
		return key, nil
	}
	escape := make([]byte, 3)
	escape[0] = key[0]
	if _, err := io.ReadFull(input, escape[1:]); err != nil {
		return nil, err
	}
	return escape, nil
}

func applySelectorKey(selected, count int, key []byte) (int, selectorAction) {
	if count == 0 || len(key) == 0 {
		return selected, selectorContinue
	}
	switch {
	case key[0] == '\r' || key[0] == '\n':
		return selected, selectorChoose
	case key[0] == 'q' || key[0] == 3:
		return selected, selectorCancel
	case key[0] == 'j' || (len(key) == 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'B'):
		return (selected + 1) % count, selectorContinue
	case key[0] == 'k' || (len(key) == 3 && key[0] == 0x1b && key[1] == '[' && key[2] == 'A'):
		return (selected - 1 + count) % count, selectorContinue
	default:
		return selected, selectorContinue
	}
}
