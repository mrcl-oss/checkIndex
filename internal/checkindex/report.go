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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrcl-oss/checkIndex/internal/reportdata"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type jsonReport = map[string]reportdata.Collection

var ErrPartialReport = errors.New("report is partial; one or more node queries failed")

// CollectReport runs the same collector used by the CLI, returning its JSON representation.
func CollectReport(ctx context.Context, p Profile, collection string, out io.Writer) error {
	return collect(ctx, p, Options{Collection: collection, JSON: true}, out, dialMongo)
}

// CollectData returns typed observations without a JSON round trip.
func CollectData(ctx context.Context, p Profile, collection string) (reportdata.Report, error) {
	return collectData(ctx, p, collection, dialMongo)
}

func collectData(ctx context.Context, p Profile, collection string, dial Dial) (reportdata.Report, error) {
	r := reportdata.Report{Collections: make(jsonReport)}
	err := collectInto(ctx, p, Options{Collection: collection, JSON: true}, nil, dial, r.Collections)
	r.Partial = err != nil
	return r, err
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
	return collectInto(ctx, p, o, out, dial, make(jsonReport))
}

func collectInto(ctx context.Context, p Profile, o Options, out io.Writer, dial Dial, result jsonReport) error {
	if p.Name == "" {
		p.Name = "direct"
	}
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
	uniqueNodes := nodes[:0]
	seenNodes := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if _, exists := seenNodes[node.Name]; exists {
			continue
		}
		seenNodes[node.Name] = struct{}{}
		uniqueNodes = append(uniqueNodes, node)
	}
	nodes = uniqueNodes
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
	} else if strings.Contains(o.Collection, ",") {
		raw := strings.Split(o.Collection, ",")
		names = make([]string, 0, len(raw))
		for _, item := range raw {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				names = append(names, trimmed)
			}
		}
	}
	sort.Strings(names)
	if !o.JSON {
		if _, err := fmt.Fprintf(out, "[INFO] Found %d active data node(s); %d collection(s).\n", len(nodes), len(names)); err != nil {
			return err
		}
	}
	nodeClients := make([]Database, len(nodes))
	nodeDialFailed := make([]bool, len(nodes))
	var workers sync.WaitGroup
	workers.Add(len(nodes))
	for nodeIdx, node := range nodes {
		go func(idx int, n Node) {
			defer workers.Done()
			db, dialErr := dial(ctx, p, n.Name)
			if dialErr != nil {
				nodeDialFailed[idx] = true
				return
			}
			nodeClients[idx] = db
		}(nodeIdx, node)
	}
	workers.Wait()
	defer func() {
		for _, db := range nodeClients {
			if db == nil {
				continue
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = db.Close(closeCtx)
			cancel()
		}
	}()

	failed := false
	for nodeIdx := range nodes {
		if nodeDialFailed[nodeIdx] {
			failed = true
			if !o.JSON {
				if _, err := fmt.Fprintln(out, "[WARN] Direct node connection failed; check connectivity, TLS and credentials."); err != nil {
					return err
				}
			}
		}
	}

	type collectionResult struct {
		name        string
		jsonIndexes reportdata.Collection
		total       int64
		err         error
		failed      bool
		warnLines   []string
		fileOutput  string
		termOutput  string
	}

	results := make([]collectionResult, len(names))
	var collectionWorkers sync.WaitGroup
	collectionWorkers.Add(len(names))
	for i, name := range names {
		go func(i int, name string) {
			defer collectionWorkers.Done()
			results[i] = func() collectionResult {
				res := collectionResult{name: name}
				usage := make(map[string]*IndexUsage)
				type nodeStatsResult struct {
					stats []IndexStat
					err   error
				}
				nodeResults := make([]nodeStatsResult, len(nodes))
				var statsWorkers sync.WaitGroup
				for nodeIdx, db := range nodeClients {
					if db == nil {
						continue
					}
					statsWorkers.Add(1)
					go func(nodeIdx int, db Database) {
						defer statsWorkers.Done()
						nodeResults[nodeIdx].stats, nodeResults[nodeIdx].err = db.Stats(ctx, p.DBName, name)
					}(nodeIdx, db)
				}
				statsWorkers.Wait()

				for nodeIdx, n := range nodes {
					if nodeClients[nodeIdx] == nil {
						continue
					}
					nodeResult := nodeResults[nodeIdx]
					if nodeResult.err != nil {
						res.failed = true
						if !o.JSON {
							diagnostic := safeStatsError(nodeResult.err, p)
							res.warnLines = append(res.warnLines, fmt.Sprintf("[WARN] $indexStats failed for node %s, collection %s (%s): %s.", n.Name, name, fmt.Sprintf("%T", nodeResult.err), diagnostic))
						}
						continue
					}
					for _, s := range nodeResult.stats {
						entry := usage[s.Name]
						if entry == nil {
							entry = &IndexUsage{Name: s.Name, Key: s.Key, PerNode: make(map[string]int64)}
							usage[s.Name] = entry
						}
						var addErr error
						entry.Total, addErr = addOps(entry.Total, s.Accesses.Ops)
						if addErr != nil {
							res.err = addErr
							return res
						}
						entry.PerNode[n.Name], addErr = addOps(entry.PerNode[n.Name], s.Accesses.Ops)
						if addErr != nil {
							res.err = addErr
							return res
						}
					}
				}

				rows := make([]IndexUsage, 0, len(usage))
				res.jsonIndexes = make(reportdata.Collection, len(usage))
				for _, v := range usage {
					rows = append(rows, *v)
					observed := make(map[string]reportdata.NodeUsage, len(v.PerNode))
					for _, n := range nodes {
						if count, ok := v.PerNode[n.Name]; ok {
							observed[n.Name] = reportdata.NodeUsage{Count: count, Primary: n.Role == "PRIMARY"}
						}
					}
					res.jsonIndexes[v.Name] = reportdata.Index{Clusters: map[string]reportdata.Cluster{p.Name: {Nodes: observed, Total: v.Total}}, Total: v.Total}
					if !o.JSON {
						var addErr error
						res.total, addErr = addOps(res.total, v.Total)
						if addErr != nil {
							res.err = addErr
							return res
						}
					}
				}

				sort.Slice(rows, func(i, j int) bool {
					if rows[i].Total == rows[j].Total {
						return rows[i].Name < rows[j].Name
					}
					return rows[i].Total < rows[j].Total
				})

				if o.JSON {
					return res
				}

				if o.File != "" {
					table, renderErr := renderTables(rows, nodes, true)
					if renderErr != nil {
						res.err = renderErr
						return res
					}
					res.fileOutput = fmt.Sprintf("## %s.%s\n\n%s\nTotal operations for %s: %d\n\n", escapeMarkdown(p.DBName), escapeMarkdown(name), table, escapeMarkdown(name), res.total)
				} else {
					table, renderErr := renderTables(rows, nodes, false)
					if renderErr != nil {
						res.err = renderErr
						return res
					}
					res.termOutput = fmt.Sprintf("\n=== %s.%s ===\n%s\nTotal operations for %s: %d\n", p.DBName, name, table, name, res.total)
				}
				return res
			}()
		}(i, name)
	}
	collectionWorkers.Wait()

	var markdown strings.Builder
	var grandTotal int64
	var failures []error
	for _, res := range results {
		if res.err != nil {
			failed = true
			failures = append(failures, fmt.Errorf("collection %q: %w", res.name, res.err))
			continue
		}
		if res.failed {
			failed = true
		}
		if !o.JSON {
			for _, warn := range res.warnLines {
				if _, err := fmt.Fprintln(out, warn); err != nil {
					return err
				}
			}
		}
		result[res.name] = res.jsonIndexes
		grandTotal, err = addOps(grandTotal, res.total)
		if err != nil {
			return err
		}
		if o.JSON {
			continue
		}
		if o.File != "" {
			markdown.WriteString(res.fileOutput)
		} else {
			if _, err := io.WriteString(out, res.termOutput); err != nil {
				return err
			}
		}
	}
	if o.JSON {
		if out == nil {
			if failed {
				return errors.Join(append(failures, ErrPartialReport)...)
			}
			return nil
		}
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
			return errors.Join(append(failures, ErrPartialReport)...)
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
		return errors.Join(append(failures, ErrPartialReport)...)
	}
	return nil
}

var mongoURIInError = regexp.MustCompile(`(?i)mongodb(?:\+srv)?://[^\s"']+`)

func safeStatsError(err error, p Profile) string {
	message := mongoURIInError.ReplaceAllString(err.Error(), "[redacted MongoDB URI]")
	for _, secret := range []string{p.Username, p.Password} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	lower := strings.ToLower(message)
	for _, class := range []string{"unauthorized", "not authorized", "authentication failed", "network timeout", "server selection", "unsupported", "decode", "cursor", "$indexstats"} {
		if strings.Contains(lower, class) {
			return message
		}
	}
	return "statistics query failed (cause text omitted for safety)"
}
