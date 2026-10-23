// Package report exposes stable, MongoDB-driver-independent report types.
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	internal "github.com/mrcl-oss/checkIndex/internal/checkindex"
	"github.com/mrcl-oss/checkIndex/internal/reportdata"
)

// Profile selects a saved profile or describes a direct connection. Secrets are
// accepted as input but are never copied into a Report or returned in errors.
type Profile struct {
	Name, URI, Username, Password, Database, AuthSource string
	HostMapping                                         map[string]string
}

// NodeUsage contains an exact counter and the discovered node's primary status.
type NodeUsage = reportdata.NodeUsage

// Cluster contains observed nodes and their total operation count.
type Cluster = reportdata.Cluster

// Index contains cluster observations and their total operation count.
type Index = reportdata.Index

// Collection maps index names to observations.
type Collection = reportdata.Collection

// Report describes available results. Partial indicates incomplete collection.
// JSON contains only the collection map, without a metadata wrapper.
type Report = reportdata.Report

// Options configures collection. ProfileName selects a local profile. Otherwise,
// the direct fields are preferred; Direct is retained for compatibility. Do not
// combine All, ProfileName, direct fields, and Direct. Collection may be empty,
// a collection name, or comma-separated names; it also applies when All is true.
type Options struct {
	All                                           bool
	ProfileName                                   string
	Direct                                        Profile
	Collection                                    string
	URI, Username, Password, Database, AuthSource string
	HostMapping                                   map[string]string
}

// Collect uses all configured profiles, a named local profile, or a direct profile. A
// report is always defined; on errors it contains any output collected so far.
func Collect(ctx context.Context, options Options) (Report, error) {
	return collectWith(ctx, options, internal.CollectData)
}

func collectWith(ctx context.Context, options Options, collector internal.DataCollector) (Report, error) {
	r := Report{Collections: map[string]Collection{}}
	flat := options.URI != "" || options.Username != "" || options.Password != "" || options.Database != "" || options.AuthSource != "" || options.HostMapping != nil
	p := options.Direct
	direct := p.Name != "" || p.URI != "" || p.Username != "" || p.Password != "" || p.Database != "" || p.AuthSource != "" || p.HostMapping != nil
	named := strings.TrimSpace(options.ProfileName) != ""
	if (options.All && (named || flat || direct)) || (named && (flat || direct)) || (flat && direct) {
		return r, errors.New("use only one of All, ProfileName, direct options, or Direct profile")
	}
	r.Partial = options.All
	var profile internal.Profile
	if options.All || named {
		home, err := os.UserHomeDir()
		if err != nil {
			return r, errors.New("resolve home directory")
		}
		config, err := internal.LoadConfig(filepath.Join(home, ".checkIndex", "config.json"))
		if err != nil {
			return r, errors.New("load configured profile")
		}
		if options.All {
			return internal.CollectConfigured(ctx, config, options.Collection, collector)
		}
		profile, err = config.SelectProfile(options.ProfileName, nil)
		if err != nil {
			return r, errors.New("select configured profile")
		}
	} else {
		if flat {
			p = Profile{URI: options.URI, Username: options.Username, Password: options.Password, Database: options.Database, AuthSource: options.AuthSource, HostMapping: options.HostMapping}
		}
		if strings.TrimSpace(p.URI) == "" || strings.TrimSpace(p.Username) == "" || strings.TrimSpace(p.Password) == "" || strings.TrimSpace(p.Database) == "" || strings.TrimSpace(p.AuthSource) == "" {
			return r, errors.New("direct connection requires URI, username, password, database, and auth source")
		}
		if !strings.HasPrefix(p.URI, "mongodb://") && !strings.HasPrefix(p.URI, "mongodb+srv://") {
			return r, errors.New("direct URI must be a MongoDB URI")
		}
		if p.Name == "" {
			p.Name = "direct"
		}
		profile = internal.Profile{Credentials: internal.Credentials{InitialURI: p.URI, Username: p.Username, Password: p.Password}, Name: p.Name, DBName: p.Database, AuthSource: p.AuthSource, HostMapping: p.HostMapping}
	}
	r, err := collector(ctx, profile, options.Collection)
	r.Partial = r.Partial || err != nil
	if r.Collections == nil {
		r.Collections = map[string]Collection{}
	}
	return r, err
}

// Marshal encodes a report as stable, indented JSON followed by a newline.
func Marshal(r Report) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetIndent("", "  ")
	err := e.Encode(r)
	return b.Bytes(), err
}
