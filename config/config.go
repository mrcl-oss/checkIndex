// Package config exposes checkIndex configuration operations to Go consumers.
package config

import "github.com/mrcl-oss/checkIndex/internal/checkindex"

// Config is the on-disk configuration for shared settings and cluster profiles.
type Config = checkindex.Config

// Credentials contains the connection settings for a configured cluster.
type Credentials = checkindex.Credentials

// LoadConfig loads a configuration file without exposing credential values in errors.
func LoadConfig(path string) (Config, error) { return checkindex.LoadConfig(path) }

// ValidateConfig validates a configuration using the same rules as the CLI.
func ValidateConfig(c Config) error { return checkindex.ValidateConfig(c) }

// SaveConfig validates and safely persists a configuration using the CLI writer.
func SaveConfig(path string, c Config) error { return checkindex.SaveConfig(path, c) }
