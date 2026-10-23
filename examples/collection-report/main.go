package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mrcl-oss/checkIndex/report"
)

type mappings []string

func (m *mappings) String() string { return strings.Join(*m, ",") }
func (m *mappings) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("host mapping must be discovered-host=reachable-host")
	}
	*m = append(*m, value)
	return nil
}

func main() {
	uri := flag.String("uri", "", "MongoDB URI")
	username := flag.String("username", "", "MongoDB username")
	database := flag.String("database", "", "database name")
	auth := flag.String("auth", "admin", "authentication source")
	collection := flag.String("collection", "", "collection to report")
	var mappingArgs mappings
	flag.Var(&mappingArgs, "host-mapping", "repeatable discovered-host=reachable-host mapping")
	flag.Parse()
	if *uri == "" || *username == "" || *database == "" || *collection == "" {
		flag.Usage()
		os.Exit(2)
	}
	password, ok := os.LookupEnv("MONGODB_PASSWORD")
	if !ok {
		fmt.Fprintln(os.Stderr, "set MONGODB_PASSWORD in the environment")
		os.Exit(2)
	}
	hostMapping := make(map[string]string)
	for _, value := range mappingArgs {
		parts := strings.SplitN(value, "=", 2)
		hostMapping[parts[0]] = parts[1]
	}
	result, err := report.Collect(context.Background(), report.Options{URI: *uri, Username: *username, Password: password, Database: *database, AuthSource: *auth, HostMapping: hostMapping, Collection: *collection})
	if err != nil && !result.Partial {
		fmt.Fprintf(os.Stderr, "collect report: %v\n", err)
		os.Exit(1)
	}
	indexes, ok := result.Collections[*collection]
	if !ok {
		fmt.Fprintf(os.Stderr, "no data returned for collection %q\n", *collection)
		if err != nil {
			fmt.Fprintf(os.Stderr, "collect report: %v\n", err)
		}
		os.Exit(1)
	}
	for name, index := range indexes {
		fmt.Printf("%s: total=%d\n", name, index.Total)
		for clusterName, cluster := range index.Clusters {
			fmt.Printf("  %s: total=%d\n", clusterName, cluster.Total)
			for nodeName, node := range cluster.Nodes {
				fmt.Printf("    %s: count=%d primary=%t\n", nodeName, node.Count, node.Primary)
			}
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "collection error: %v\n", err)
	}
}
