package reportdata

import (
	"errors"
	"math"
	"sort"
)

func add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errors.New("invalid or overflowing int64 operation counter")
	}
	return a + b, nil
}

func keys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Merge combines matching collection, index, cluster and node names. It checks
// all sums and input totals, and leaves dst unchanged if any counter is invalid.
func Merge(dst *Report, src Report) error {
	combined := Report{Collections: map[string]Collection{}, Partial: dst.Partial || src.Partial}
	for _, input := range []Report{*dst, src} {
		for _, collection := range keys(input.Collections) {
			if combined.Collections[collection] == nil {
				combined.Collections[collection] = Collection{}
			}
			for _, name := range keys(input.Collections[collection]) {
				incoming := input.Collections[collection][name]
				index := combined.Collections[collection][name]
				if index.Clusters == nil {
					index.Clusters = map[string]Cluster{}
				}
				var indexSum int64
				for _, clusterName := range keys(incoming.Clusters) {
					incomingCluster := incoming.Clusters[clusterName]
					cluster := index.Clusters[clusterName]
					if cluster.Nodes == nil {
						cluster.Nodes = map[string]NodeUsage{}
					}
					var nodeSum int64
					for _, nodeName := range keys(incomingCluster.Nodes) {
						node := incomingCluster.Nodes[nodeName]
						var err error
						nodeSum, err = add(nodeSum, node.Count)
						if err != nil {
							return err
						}
						previous := cluster.Nodes[nodeName]
						node.Count, err = add(previous.Count, node.Count)
						if err != nil {
							return err
						}
						node.Primary = node.Primary || previous.Primary
						cluster.Nodes[nodeName] = node
					}
					if nodeSum != incomingCluster.Total {
						return errors.New("cluster total does not match node counters")
					}
					var err error
					indexSum, err = add(indexSum, nodeSum)
					if err != nil {
						return err
					}
					cluster.Total, err = add(cluster.Total, nodeSum)
					if err != nil {
						return err
					}
					index.Clusters[clusterName] = cluster
				}
				if indexSum != incoming.Total {
					return errors.New("index total does not match cluster counters")
				}
				var err error
				index.Total, err = add(index.Total, indexSum)
				if err != nil {
					return err
				}
				combined.Collections[collection][name] = index
			}
		}
	}
	*dst = combined
	return nil
}
