// Package i18n provides a minimal two-language (tr/en) message catalog.
package i18n

import (
	"fmt"
	"sync"
)

var (
	mu   sync.RWMutex
	lang = "en"
)

// SetLang switches the active language ("tr" or "en").
func SetLang(l string) {
	mu.Lock()
	defer mu.Unlock()
	if l == "tr" || l == "en" {
		lang = l
	}
}

// Lang returns the active language code.
func Lang() string {
	mu.RLock()
	defer mu.RUnlock()
	return lang
}

// T returns the message for key in the active language, formatted with args.
// Unknown keys are returned verbatim so missing translations stay visible.
func T(key string, args ...any) string {
	mu.RLock()
	l := lang
	mu.RUnlock()
	msg, ok := catalog[key]
	if !ok {
		return key
	}
	s := msg.en
	if l == "tr" {
		s = msg.tr
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

type entry struct{ en, tr string }
