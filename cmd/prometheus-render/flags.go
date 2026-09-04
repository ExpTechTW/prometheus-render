package main

import (
	"flag"
	"fmt"
	"net/url"
	"strings"
)

// settings collects render flags into the same key/value form the HTTP API
// uses, so the CLI and the server resolve them through one code path.
type settings struct{ values url.Values }

// str registers a string flag, reachable under any of names, storing into key.
func (s settings) str(fs *flag.FlagSet, key, def string, names ...string) {
	s.values.Set(key, def)
	for _, n := range names {
		fs.Func(n, "", func(v string) error { s.values.Set(key, v); return nil })
	}
}

// flag registers a boolean flag, accepted with or without a value.
func (s settings) flag(fs *flag.FlagSet, key string, names ...string) {
	for _, n := range names {
		fs.BoolFunc(n, "", func(v string) error { s.values.Set(key, v); return nil })
	}
}

// list registers a repeatable flag appending each occurrence to key.
func (s settings) list(fs *flag.FlagSet, key string, names ...string) {
	for _, n := range names {
		fs.Func(n, "", func(v string) error { s.values.Add(key, v); return nil })
	}
}

// strFlag and boolFlag register a plain flag under several names at once.
func strFlag(fs *flag.FlagSet, def string, names ...string) *string {
	p := new(string)
	for _, n := range names {
		fs.StringVar(p, n, def, "")
	}
	return p
}

func boolFlag(fs *flag.FlagSet, names ...string) *bool {
	p := new(bool)
	for _, n := range names {
		fs.BoolVar(p, n, false, "")
	}
	return p
}

// appendFlag registers a repeatable flag collecting into dst.
func appendFlag(fs *flag.FlagSet, dst *[]string, name string) {
	fs.Func(name, "", func(v string) error { *dst = append(*dst, v); return nil })
}

// headerFlag registers a repeatable "Name: value" HTTP header flag.
func headerFlag(fs *flag.FlagSet, dst map[string]string, name string) {
	fs.Func(name, "", func(v string) error {
		key, value, ok := strings.Cut(v, ":")
		if !ok {
			return fmt.Errorf("expected 'Name: value', got %q", v)
		}
		dst[strings.TrimSpace(key)] = strings.TrimSpace(value)
		return nil
	})
}
