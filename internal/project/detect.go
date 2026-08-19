package project

import (
	"encoding/json"
	"os"
	"path/filepath"
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
