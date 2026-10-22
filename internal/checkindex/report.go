package checkindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type jsonReport map[string]map[string]map[string]int64

var ErrPartialReport = errors.New("report is partial; one or more node queries failed")

// CollectReport runs the same collector used by the CLI, returning its JSON representation.
func CollectReport(ctx context.Context, p Profile, collection string, out io.Writer) error {
	return collect(ctx, p, Options{Collection: collection, JSON: true}, out, dialMongo)
}

// Node identifies a MongoDB data node and its topology role.
type Node struct{ Name, Role string }

// Hello contains the topology fields returned by the MongoDB hello command.
type Hello struct {
	Me      string   `bson:"me"`
	Hosts   []string `bson:"hosts"`
	Primary string   `bson:"primary"`
	Msg     string   `bson:"msg"`
}

// Member describes a replica-set member returned by replSetGetStatus.
type Member struct {
	Name     string  `bson:"name"`
	StateStr string  `bson:"stateStr"`
	Health   float64 `bson:"health"`
}

// Status contains replica-set members.
type Status struct {
	Members []Member `bson:"members"`
}

// Accesses contains index operation counters and their observation start time.
type Accesses struct {
	Ops   int64     `bson:"ops"`
	Since time.Time `bson:"since"`
}

// IndexStat is one index's $indexStats result.
type IndexStat struct {
	Name     string   `bson:"name"`
	Key      bson.D   `bson:"key"`
	Accesses Accesses `bson:"accesses"`
}

// IndexUsage aggregates one index's operation counts across nodes.
type IndexUsage struct {
	Name    string
	Key     bson.D
	Total   int64
	PerNode map[string]int64
}

// Database is the subset of MongoDB operations required by report collection.
type Database interface {
	Hello(context.Context) (Hello, error)
	Status(context.Context) (Status, error)
	Collections(context.Context, string) ([]string, error)
	Stats(context.Context, string, string) ([]IndexStat, error)
	Close(context.Context) error
}

// Dial opens a database client for a profile and optional node address.
type Dial func(context.Context, Profile, string) (Database, error)

func discover(ctx context.Context, db Database, fallback string) ([]Node, error) {
	h, err := db.Hello(ctx)
	if err != nil {
		return nil, errors.New("topology discovery failed (hello); check connectivity and permissions")
	}
	if h.Msg == "isdbgrid" {
		return nil, errors.New("mongos is not supported; select a replica set or standalone URI")
	}
	s, err := db.Status(ctx)
	var nodes []Node
	if err == nil {
		for _, m := range s.Members {
			if m.Health == 1 && (m.StateStr == "PRIMARY" || m.StateStr == "SECONDARY") {
				nodes = append(nodes, Node{m.Name, m.StateStr})
			}
		}
	}
	if err != nil || len(s.Members) == 0 {
		for _, host := range h.Hosts {
			role := "SECONDARY"
			if host == h.Primary {
				role = "PRIMARY"
			}
			nodes = append(nodes, Node{host, role})
		}
		if len(nodes) == 0 {
			host := h.Me
			if host == "" {
				host = fallback
			}
			if host == "" {
				return nil, errors.New("unable to identify standalone host")
			}
			nodes = []Node{{host, "STANDALONE"}}
		}
	}
	if len(nodes) == 0 {
		return nil, errors.New("no healthy data nodes discovered")
	}
	return nodes, nil
}

func addOps(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errors.New("invalid or overflowing int64 operation counter")
	}
	return a + b, nil
}

func collect(ctx context.Context, p Profile, o Options, out io.Writer, dial Dial) error {
	if p.Name == "" {
		p.Name = "direct"
	}
	result := make(jsonReport)
	var opened []Database
	defer func() {
		for _, db := range opened {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = db.Close(closeCtx)
			cancel()
		}
	}()
	seed, err := dial(ctx, p, "")
	if err != nil {
		return errors.New("seed client setup failed; check selected cluster settings")
	}
	opened = append(opened, seed)
	u, _ := url.Parse(p.InitialURI)
	fallback := ""
	if u != nil {
		fallback = strings.Split(u.Host, ",")[0]
	}
	nodes, err := discover(ctx, seed, fallback)
	if err != nil {
		return err
	}
	names := []string{o.Collection}
	if o.Collection == "" {
		names, err = seed.Collections(ctx, p.DBName)
		if err != nil {
			return errors.New("collection listing failed; check permissions")
		}
		filtered := names[:0]
		for _, n := range names {
			if !strings.HasPrefix(n, "system.") {
				filtered = append(filtered, n)
			}
		}
		names = filtered
	}
	sort.Strings(names)
	if !o.JSON {
		if _, err := fmt.Fprintf(out, "[INFO] Found %d active data node(s); %d collection(s).\n", len(nodes), len(names)); err != nil {
			return err
		}
	}
	clients := make(map[string]Database)
	failed := false
	for _, n := range nodes {
		if _, exists := clients[n.Name]; exists {
			continue
		}
		db, e := dial(ctx, p, n.Name)
		clients[n.Name] = db
		if e != nil {
			clients[n.Name] = nil
			failed = true
			if !o.JSON {
				if _, err := fmt.Fprintln(out, "[WARN] Direct node connection failed; check connectivity, TLS and credentials."); err != nil {
					return err
				}
			}
			continue
		}
		opened = append(opened, db)
	}
	var markdown strings.Builder
	var grandTotal int64
	for _, name := range names {
		usage := make(map[string]*IndexUsage)
		for _, n := range nodes {
			db := clients[n.Name]
			if db == nil {
				continue
			}
			stats, e := db.Stats(ctx, p.DBName, name)
			if e != nil {
				failed = true
				if !o.JSON {
					if _, err := fmt.Fprintln(out, "[WARN] Node index statistics unavailable; check connectivity and permissions."); err != nil {
						return err
					}
				}
				continue
			}
			for _, s := range stats {
				entry := usage[s.Name]
				if entry == nil {
					entry = &IndexUsage{Name: s.Name, Key: s.Key, PerNode: make(map[string]int64)}
					usage[s.Name] = entry
				}
				entry.Total, e = addOps(entry.Total, s.Accesses.Ops)
				if e != nil {
					return e
				}
				entry.PerNode[n.Name] = s.Accesses.Ops
			}
		}
		rows := make([]IndexUsage, 0, len(usage))
		jsonIndexes := make(map[string]map[string]int64, len(usage))
		var total int64
		for _, v := range usage {
			rows = append(rows, *v)
			jsonIndexes[v.Name] = map[string]int64{p.Name: v.Total, "total": v.Total}
			total, err = addOps(total, v.Total)
			if err != nil {
				return err
			}
		}
		result[name] = jsonIndexes
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Total == rows[j].Total {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].Total < rows[j].Total
		})
		grandTotal, err = addOps(grandTotal, total)
		if err != nil {
			return err
		}
		if o.JSON {
			continue
		}
		if o.File != "" {
			table, renderErr := renderTables(rows, nodes, true)
			if renderErr != nil {
				return renderErr
			}
			fmt.Fprintf(&markdown, "## %s.%s\n\n%s\nTotal operations for %s: %d\n\n", escapeMarkdown(p.DBName), escapeMarkdown(name), table, escapeMarkdown(name), total)
		} else {
			table, renderErr := renderTables(rows, nodes, false)
			if renderErr != nil {
				return renderErr
			}
			if _, err := fmt.Fprintf(out, "\n=== %s.%s ===\n%s\nTotal operations for %s: %d\n", p.DBName, name, table, name, total); err != nil {
				return err
			}
		}
	}
	if o.JSON {
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			return encodeErr
		}
		if o.File != "" {
			if err = os.WriteFile(o.File, append(encoded, '\n'), 0600); err != nil {
				return fmt.Errorf("write report: %w", err)
			}
		} else if _, err = fmt.Fprintln(out, string(encoded)); err != nil {
			return err
		}
		if failed {
			return ErrPartialReport
		}
		return nil
	}
	suffix := ""
	if failed {
		suffix = " (partial; some node statistics unavailable)"
	}
	if o.File != "" {
		fmt.Fprintf(&markdown, "Final sum of operations: %d%s\n", grandTotal, suffix)
		if err = os.WriteFile(o.File, []byte(markdown.String()), 0600); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		if _, err := fmt.Fprintf(out, "[INFO] Markdown report written to %s\n", o.File); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(out, "Final sum of operations: %d%s\n", grandTotal, suffix); err != nil {
			return err
		}
	}
	if failed {
		return ErrPartialReport
	}
	return nil
}
