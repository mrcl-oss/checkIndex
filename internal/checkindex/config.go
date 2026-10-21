package checkindex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Credentials contains the connection settings for a configured cluster.
type Credentials struct {
	InitialURI string `json:"initialUri"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

// Config is the on-disk configuration for shared settings and cluster profiles.
type Config struct {
	DBName      string                 `json:"dbName"`
	AuthSource  string                 `json:"authSource"`
	HostMapping map[string]string      `json:"hostMapping"`
	Cluster     map[string]Credentials `json:"cluster"`
}

// Profile is a resolved cluster connection and its shared database settings.
type Profile struct {
	Credentials
	Name, DBName, AuthSource string
	HostMapping              map[string]string
}

func defaultConfig() Config {
	return Config{DBName: "database", AuthSource: "admin", HostMapping: map[string]string{"mongo:27017": "localhost:27017"}, Cluster: map[string]Credentials{"": {}}}
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return Config{}, fmt.Errorf("create config directory: %w", err)
		}
		if err = os.Chmod(filepath.Dir(path), 0700); err != nil {
			return Config{}, fmt.Errorf("restrict config directory: %w", err)
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(e) {
			return loadConfig(path)
		}
		if e != nil {
			return Config{}, fmt.Errorf("create config: %w", e)
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		e = enc.Encode(defaultConfig())
		closeErr := f.Close()
		if e != nil {
			return Config{}, e
		}
		if closeErr != nil {
			return Config{}, closeErr
		}
		return Config{}, fmt.Errorf("created %s. Edit the blank cluster name, initialUri, username and password before running again", path)
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (Config, error) {
	var c Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, errors.New("invalid config JSON or schema; expected dbName, authSource, hostMapping and cluster profiles")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	if strings.TrimSpace(c.DBName) == "" || strings.TrimSpace(c.AuthSource) == "" || c.HostMapping == nil || len(c.Cluster) == 0 {
		return c, errors.New("config requires dbName, authSource, hostMapping object and nonempty cluster object")
	}
	// encoding/json accepts null into string/struct fields. Explicit null is
	// nevertheless a schema error; omitted fields in unused profiles are allowed.
	var shape struct {
		Cluster map[string]map[string]json.RawMessage `json:"cluster"`
	}
	if json.Unmarshal(data, &shape) != nil {
		return c, errors.New("cluster profiles must be objects")
	}
	for _, profile := range shape.Cluster {
		if profile == nil {
			return c, errors.New("cluster profiles must be objects, not null")
		}
		for _, value := range profile {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return c, errors.New("cluster fields must be strings, not null")
			}
		}
	}
	for k, v := range c.HostMapping {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return c, errors.New("hostMapping requires nonempty hosts")
		}
	}
	return c, nil
}

func (c Config) selectProfile(name string, password *string) (Profile, error) {
	if name == "" {
		keys := make([]string, 0, len(c.Cluster))
		for k := range c.Cluster {
			if strings.TrimSpace(k) != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			name = keys[0]
		}
	}
	p, ok := c.Cluster[name]
	if !ok {
		return Profile{}, errors.New("unknown cluster; use a name from config.cluster")
	}
	if password != nil {
		p.Password = *password
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(p.InitialURI) == "" || strings.TrimSpace(p.Username) == "" || p.Password == "" {
		return Profile{}, errors.New("selected cluster requires a nonempty name, initialUri, username and password")
	}
	if !strings.HasPrefix(p.InitialURI, "mongodb://") && !strings.HasPrefix(p.InitialURI, "mongodb+srv://") {
		return Profile{}, errors.New("selected cluster initialUri must be a MongoDB URI")
	}
	return Profile{Credentials: p, Name: name, DBName: c.DBName, AuthSource: c.AuthSource, HostMapping: c.HostMapping}, nil
}
