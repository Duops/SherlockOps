package tooling

import (
	"log/slog"
)

// EnvRegistry holds per-environment tool registries and falls back to "default"
// when a requested environment is not found.
type EnvRegistry struct {
	registries map[string]*Registry
	logger     *slog.Logger
}

// NewEnvRegistry creates a new environment-aware registry.
func NewEnvRegistry(logger *slog.Logger) *EnvRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &EnvRegistry{
		registries: make(map[string]*Registry),
		logger:     logger,
	}
}

// SetRegistry registers a tool registry for the given environment name.
// Use "default" for the fallback registry.
func (r *EnvRegistry) SetRegistry(env string, reg *Registry) {
	r.registries[env] = reg
	r.logger.Debug("environment registry set", "env", env)
}

// GetRegistry returns the registry for the given environment.
// If env is empty or not found, it falls back to "default".
// If "default" is also missing, it returns an empty registry.
func (r *EnvRegistry) GetRegistry(env string) *Registry {
	if env == "" {
		env = "default"
	}
	if reg, ok := r.registries[env]; ok {
		return reg
	}
	r.logger.Debug("environment not found, falling back to default", "requested", env)
	if reg, ok := r.registries["default"]; ok {
		return reg
	}
	// Return an empty registry so callers never get nil.
	return NewRegistry(r.logger)
}
