package checkindex

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func escapeMarkdown(s string) string {
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "\r", " ", "\n", " ").Replace(s)
}

func renderTables(rows []IndexUsage, nodes []Node, markdown bool) (string, error) {
	var result strings.Builder
	// Two node columns per table keep wide replica sets readable without truncating keys.
	for start := 0; start < len(nodes) || start == 0; start += 2 {
		end := start + 2
		if end > len(nodes) {
			end = len(nodes)
		}
		last := end == len(nodes)
		headings := []string{"Index", "Key"}
		for _, n := range nodes[start:end] {
			headings = append(headings, n.Name+" ("+n.Role+")")
		}
		if last {
			headings = append(headings, "Total")
		}
		var table strings.Builder
		var writer *tabwriter.Writer
		if !markdown {
			writer = tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
		}
		writeRow := func(cells []string) error {
			if markdown {
				escaped := make([]string, len(cells))
				for i, c := range cells {
					escaped[i] = escapeMarkdown(c)
				}
				_, err := fmt.Fprintf(&table, "| %s |\n", strings.Join(escaped, " | "))
				return err
			} else {
				for i, c := range cells {
					if i > 0 {
						if _, err := fmt.Fprint(writer, "\t| "); err != nil {
							return err
						}
					}
					if _, err := fmt.Fprint(writer, strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(c)); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintln(writer); err != nil {
					return err
				}
			}
			return nil
		}
		if err := writeRow(headings); err != nil {
			return "", err
		}
		if markdown {
			cells := make([]string, len(headings))
			for i := range cells {
				cells[i] = "---"
			}
			if err := writeRow(cells); err != nil {
				return "", err
			}
		}
		for _, r := range rows {
			key, _ := bson.MarshalExtJSON(r.Key, false, false)
			cells := []string{r.Name, string(key)}
			for _, n := range nodes[start:end] {
				s := "N/A"
				if ops, ok := r.PerNode[n.Name]; ok {
					s = strconv.FormatInt(ops, 10)
				}
				cells = append(cells, s)
			}
			if last {
				cells = append(cells, strconv.FormatInt(r.Total, 10))
			}
			if err := writeRow(cells); err != nil {
				return "", err
			}
		}
		if writer != nil {
			if err := writer.Flush(); err != nil {
				return "", err
			}
		}
		result.WriteString(table.String())
		if !last {
			result.WriteString("\n")
		} else {
			break
		}
	}
	return result.String(), nil
}
