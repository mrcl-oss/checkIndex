package checkindex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/mrcl-oss/checkIndex/internal/reportdata"
)

// DataCollector collects one resolved profile into the shared report model.
type DataCollector func(context.Context, Profile, string) (reportdata.Report, error)

// CollectConfigured concurrently collects configured profiles, merging results in
// name order. Available data survives profile, collection and merge failures.
func CollectConfigured(ctx context.Context, config Config, collection string, collect DataCollector) (reportdata.Report, error) {
	names := make([]string, 0, len(config.Cluster))
	for name := range config.Cluster {
		names = append(names, name)
	}
	sort.Strings(names)
	type result struct {
		report reportdata.Report
		err    error
	}
	results := make([]result, len(names))
	var workers sync.WaitGroup
	for i, name := range names {
		workers.Add(1)
		go func(i int, name string) {
			defer workers.Done()
			p, err := config.selectProfile(name, nil)
			if err != nil {
				results[i].err = err
				return
			}
			results[i].report, results[i].err = collect(ctx, p, collection)
		}(i, name)
	}
	workers.Wait()
	r := reportdata.Report{Collections: map[string]reportdata.Collection{}}
	var failures []error
	for i, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("profile %q: %w", names[i], result.err))
		}
		if err := reportdata.Merge(&r, result.report); err != nil {
			failures = append(failures, fmt.Errorf("profile %q: %w", names[i], err))
		}
	}
	err := errors.Join(failures...)
	r.Partial = r.Partial || err != nil
	return r, err
}
