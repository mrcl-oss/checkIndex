// Package reportdata defines the shared, driver-independent report model.
package reportdata

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// NodeUsage is an observed node counter and its discovered primary status.
type NodeUsage struct {
	Count   int64 `json:"count"`
	Primary bool  `json:"primary"`
}

// Cluster contains observed nodes and their operation total.
type Cluster struct {
	Nodes map[string]NodeUsage `json:"nodes"`
	Total int64                `json:"total"`
}

// Index contains cluster observations and their operation total.
type Index struct {
	Clusters map[string]Cluster `json:"clusters"`
	Total    int64              `json:"total"`
}

// Collection maps index names to observations.
type Collection map[string]Index

// MarshalJSON keeps an empty collection an object, including its zero value.
func (c Collection) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]Index(c))
}

// MarshalJSON keeps the nodes map an object, including a zero-value cluster.
func (c Cluster) MarshalJSON() ([]byte, error) {
	type value Cluster
	if c.Nodes == nil {
		c.Nodes = map[string]NodeUsage{}
	}
	return json.Marshal(value(c))
}

// MarshalJSON keeps the clusters map an object, including a zero-value index.
func (i Index) MarshalJSON() ([]byte, error) {
	type value Index
	if i.Clusters == nil {
		i.Clusters = map[string]Cluster{}
	}
	return json.Marshal(value(i))
}

// Report retains available observations and whether collection was incomplete.
// JSON is the collections object alone; Partial is not serialized.
type Report struct {
	Collections map[string]Collection `json:"-"`
	Partial     bool                  `json:"-"`
}

// MarshalJSON encodes an empty report as an object, without metadata.
func (r Report) MarshalJSON() ([]byte, error) {
	if r.Collections == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(r.Collections)
}

// UnmarshalJSON decodes the collections object, resetting non-JSON metadata.
func (r *Report) UnmarshalJSON(data []byte) error {
	values := map[string]Collection{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return err
	}
	if values == nil {
		return errors.New("report must be a collections object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("report must contain one JSON object")
	}
	r.Collections, r.Partial = values, false
	return nil
}
