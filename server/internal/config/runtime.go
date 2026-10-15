package config

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"sync/atomic"
)

// Runtime publishes immutable configurations. Gate coordinates settings writes
// with job submissions/retries; readers of Current never need to hold it.
type Runtime struct {
	Gate  sync.RWMutex
	value atomic.Pointer[Config]
}

func (c *Config) Clone() *Config {
	v := *c
	v.runtime = nil
	v.Downloads.ExtraArgs = slices.Clone(c.Downloads.ExtraArgs)
	v.Categories = slices.Clone(c.Categories)
	v.MediaServers = slices.Clone(c.MediaServers)
	for i := range v.MediaServers {
		v.MediaServers[i].PathMap = maps.Clone(v.MediaServers[i].PathMap)
	}
	return &v
}

// EnableRuntime must run during startup, before the config is shared.
func (c *Config) EnableRuntime() *Runtime {
	r := &Runtime{}
	r.Publish(c)
	c.runtime = r
	return r
}

// Publish takes ownership of a private copy, so callers cannot mutate readers.
func (r *Runtime) Publish(c *Config) { r.value.Store(c.Clone()) }

func (c *Config) Current() *Config {
	if c.runtime != nil {
		return c.runtime.value.Load()
	}
	return c
}

func (c *Config) ReadSettings() func() {
	if c.runtime == nil {
		return func() {}
	}
	c.runtime.Gate.RLock()
	return c.runtime.Gate.RUnlock
}

// LoadDeployment validates only startup-owned values. Mutable settings are
// validated after the database has been opened and its authoritative state read.
func LoadDeployment(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c, err := parse(data, false)
	if err != nil {
		return nil, err
	}
	if err := c.MakeAbsolute(); err != nil {
		return nil, err
	}
	deployment := Default()
	deployment.Server, deployment.DataDir, deployment.Ytdlp.Binary = c.Server, c.DataDir, c.Ytdlp.Binary
	if err := deployment.Validate(); err != nil {
		return nil, err
	}
	if err := deployment.ValidateEnvironment(Options{}); err != nil {
		return nil, err
	}
	return c, nil
}

// ValidateDestinations checks only the mutable filesystem paths, without
// creating directories or probing unrelated deployment dependencies.
func (c *Config) ValidateDestinations() error {
	for _, cat := range c.Categories {
		if err := checkWritableDir(cat.Path); err != nil {
			return fmt.Errorf("category %s: %w", cat.ID, err)
		}
	}
	return nil
}
