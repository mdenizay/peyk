package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DetectFramework inspects a checked-out source tree.
func DetectFramework(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "composer.json")); err == nil {
		var c struct {
			Require map[string]string `json:"require"`
		}
		if json.Unmarshal(b, &c) == nil {
			if _, ok := c.Require["laravel/framework"]; ok {
				return Laravel
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var p struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(b, &p) == nil {
			if _, ok := p.Dependencies["next"]; ok {
				return NextJS
			}
			if _, ok := p.DevDependencies["next"]; ok {
				return NextJS
			}
		}
	}
	return Static
}

// bundledExtensions ship with the serversideup/php base image already.
var bundledExtensions = map[string]bool{
	"bcmath": true, "ctype": true, "curl": true, "dom": true, "fileinfo": true,
	"filter": true, "hash": true, "iconv": true, "json": true, "libxml": true,
	"mbstring": true, "mysqli": true, "opcache": true, "openssl": true,
	"pcntl": true, "pcre": true, "pdo": true, "pdo_mysql": true,
	"pdo_pgsql": true, "pgsql": true, "phar": true, "posix": true,
	"readline": true, "redis": true, "session": true, "simplexml": true,
	"sodium": true, "tokenizer": true, "xml": true, "xmlreader": true,
	"xmlwriter": true, "zip": true, "zlib": true,
}

var phpVersionRe = regexp.MustCompile(`8\.\d+`)

// maxPHPMinor returns the highest 8.x mentioned in a constraint string:
// "^8.4" → "8.4", "^8.2|^8.3" → "8.3". Minor versions stay single-digit
// (8.10 would sort wrong lexically) for the foreseeable future.
func maxPHPMinor(current, constraint string) string {
	for _, m := range phpVersionRe.FindAllString(constraint, -1) {
		if m > current {
			current = m
		}
	}
	return current
}

// DetectPHP inspects composer.json AND composer.lock and returns the PHP
// version the project needs (e.g. "8.4"; empty when unknown) plus the
// non-bundled extensions to install.
//
// The lock file matters: it is often generated on a newer PHP than
// composer.json's constraint admits, locking package versions that require
// that newer PHP — the image must satisfy the lock, not just composer.json.
func DetectPHP(dir string) (version string, extensions []string) {
	extSet := map[string]bool{"intl": true, "exif": true, "gd": true} // Laravel-ecosystem baseline

	if b, err := os.ReadFile(filepath.Join(dir, "composer.json")); err == nil {
		var c struct {
			Require map[string]string `json:"require"`
		}
		if json.Unmarshal(b, &c) == nil {
			version = maxPHPMinor(version, c.Require["php"])
			for k := range c.Require {
				if name, ok := strings.CutPrefix(k, "ext-"); ok {
					extSet[strings.ToLower(name)] = true
				}
			}
		}
	}

	if b, err := os.ReadFile(filepath.Join(dir, "composer.lock")); err == nil {
		var l struct {
			Packages []struct {
				Require map[string]string `json:"require"`
			} `json:"packages"` // production packages only; packages-dev never ships
		}
		if json.Unmarshal(b, &l) == nil {
			for _, pkg := range l.Packages {
				version = maxPHPMinor(version, pkg.Require["php"])
				for k := range pkg.Require {
					if name, ok := strings.CutPrefix(k, "ext-"); ok {
						extSet[strings.ToLower(name)] = true
					}
				}
			}
		}
	}

	for name := range extSet {
		if !bundledExtensions[name] {
			extensions = append(extensions, name)
		}
	}
	sort.Strings(extensions)
	return version, extensions
}

// DetectLaravelExtras reports which optional services the codebase likely uses.
func DetectLaravelExtras(dir string) Services {
	s := Services{Queue: true, Scheduler: true} // near-universal in Laravel apps
	if b, err := os.ReadFile(filepath.Join(dir, "composer.json")); err == nil {
		var c struct {
			Require map[string]string `json:"require"`
		}
		if json.Unmarshal(b, &c) == nil {
			if _, ok := c.Require["laravel/reverb"]; ok {
				s.Reverb = true
			}
		}
	}
	return s
}

// DefaultsFor fills framework-dependent defaults on a project.
func DefaultsFor(p *Project) {
	switch p.Framework {
	case Laravel:
		if p.Port == 0 {
			p.Port = 8080
		}
		if p.HealthPath == "" {
			p.HealthPath = "/up"
		}
		if p.PHPVersion == "" {
			p.PHPVersion = "8.3"
		}
	case NextJS:
		if p.Port == 0 {
			p.Port = 3000
		}
		if p.HealthPath == "" {
			p.HealthPath = "/"
		}
		if p.NodeVersion == "" {
			p.NodeVersion = "22"
		}
	default:
		if p.Port == 0 {
			p.Port = 8080
		}
		if p.HealthPath == "" {
			p.HealthPath = "/"
		}
	}
}
