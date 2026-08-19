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

// DetectPHP reads composer.json and returns the PHP version the project needs
// (e.g. "8.4"; empty when unknown) and the non-bundled extensions to install.
func DetectPHP(dir string) (version string, extensions []string) {
	b, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return "", nil
	}
	var c struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(b, &c) != nil {
		return "", nil
	}
	// Pick the highest 8.x mentioned in the constraint: "^8.4" → 8.4,
	// "^8.2|^8.3" → 8.3. Anything a locked dependency additionally requires
	// is the app author's constraint to reflect in composer.json's php field.
	if constraint, ok := c.Require["php"]; ok {
		best := ""
		for _, m := range regexp.MustCompile(`8\.\d+`).FindAllString(constraint, -1) {
			if m > best {
				best = m
			}
		}
		version = best
	}
	extSet := map[string]bool{"intl": true, "exif": true, "gd": true} // Laravel-ecosystem baseline
	for k := range c.Require {
		if name, ok := strings.CutPrefix(k, "ext-"); ok {
			extSet[strings.ToLower(name)] = true
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
