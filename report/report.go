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
)

// Profile selects a saved profile or describes a direct connection. Secrets are
// accepted as input but are never copied into a Report or returned in errors.
type Profile struct {
	Name, URI, Username, Password, Database, AuthSource string
	HostMapping                                         map[string]string
}

// Index contains operation counts observed on each data node.
type Index map[string]int64

// Collection maps index names to node usage counts.
type Collection map[string]Index

// Report describes the available results; partial is true when collection was incomplete.
type Report struct {
	Collections map[string]Collection `json:"-"`
	Partial     bool                  `json:"-"`
}

func (r Report) MarshalJSON() ([]byte, error) { return json.Marshal(r.Collections) }

// Options configures collection. ProfileName selects a local profile. Otherwise,
// the direct fields are preferred; Direct is retained for compatibility. Do not
// combine ProfileName, direct fields, and Direct. Collection may be empty.
type Options struct {
	ProfileName                                   string
	Direct                                        Profile
	Collection                                    string
	URI, Username, Password, Database, AuthSource string
	HostMapping                                   map[string]string
}

// Collect uses a named local configuration profile or a direct profile. A
// report is always defined; on errors it contains any output collected so far.
func Collect(ctx context.Context, options Options) (Report, error) {
	r := Report{Collections: map[string]Collection{}}
	var profile internal.Profile
	if strings.TrimSpace(options.ProfileName) != "" {
		if options.URI != "" || options.Username != "" || options.Password != "" || options.Database != "" || options.AuthSource != "" || options.HostMapping != nil || options.Direct.URI != "" || options.Direct.Username != "" || options.Direct.Password != "" || options.Direct.Database != "" || options.Direct.AuthSource != "" || options.Direct.HostMapping != nil {
			return r, errors.New("use either ProfileName or direct connection options, not both")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return r, errors.New("resolve home directory")
		}
		config, err := internal.LoadConfig(filepath.Join(home, ".checkIndex", "config.json"))
		if err != nil {
			return r, errors.New("load configured profile")
		}
		profile, err = config.SelectProfile(options.ProfileName, nil)
		if err != nil {
			return r, errors.New("select configured profile")
		}
	} else {
		p := options.Direct
		if options.URI != "" || options.Username != "" || options.Password != "" || options.Database != "" || options.AuthSource != "" || options.HostMapping != nil {
			if p.Name != "" || p.URI != "" || p.Username != "" || p.Password != "" || p.Database != "" || p.AuthSource != "" || p.HostMapping != nil {
				return r, errors.New("use either direct options or Direct profile, not both")
			}
			p = Profile{URI: options.URI, Username: options.Username, Password: options.Password, Database: options.Database, AuthSource: options.AuthSource, HostMapping: options.HostMapping}
		}
		if strings.TrimSpace(p.URI) == "" || strings.TrimSpace(p.Username) == "" || strings.TrimSpace(p.Password) == "" || strings.TrimSpace(p.Database) == "" || strings.TrimSpace(p.AuthSource) == "" {
			return r, errors.New("direct connection requires URI, username, password, database, and auth source")
		}
		if !strings.HasPrefix(p.URI, "mongodb://") && !strings.HasPrefix(p.URI, "mongodb+srv://") {
			return r, errors.New("direct URI must be a MongoDB URI")
		}
		profile = internal.Profile{Credentials: internal.Credentials{InitialURI: p.URI, Username: p.Username, Password: p.Password}, DBName: p.Database, AuthSource: p.AuthSource, HostMapping: p.HostMapping}
	}
	var b strings.Builder
	err := internal.CollectReport(ctx, profile, options.Collection, &b)
	var values map[string]map[string]map[string]int64
	_ = json.Unmarshal([]byte(b.String()), &values)
	for collection, indexes := range values {
		r.Collections[collection] = make(Collection, len(indexes))
		for index, counts := range indexes {
			r.Collections[collection][index] = Index(counts)
		}
	}
	r.Partial = errors.Is(err, internal.ErrPartialReport)
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
