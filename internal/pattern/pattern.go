// Package pattern builds segment URLs from a base URL and a name template like "seg_{}.ts".
package pattern

import (
	"errors"
	"fmt"
	"strings"
)

const placeholder = "{}"

// Validate checks that the template contains exactly one "{}".
func Validate(tmpl string) error {
	switch strings.Count(tmpl, placeholder) {
	case 0:
		return errors.New("в шаблоне нет {}")
	case 1:
		return nil
	default:
		return errors.New("в шаблоне больше одного {}")
	}
}

// BuildURL joins base and template with a single "/" and puts n (zero-padded to pad digits) in place of "{}".
func BuildURL(base, tmpl string, n, pad int) (string, error) {
	if err := Validate(tmpl); err != nil {
		return "", err
	}
	name := strings.Replace(tmpl, placeholder, fmt.Sprintf("%0*d", pad, n), 1)
	base = strings.TrimSpace(base)
	if base == "" {
		return name, nil
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(name, "/"), nil
}
