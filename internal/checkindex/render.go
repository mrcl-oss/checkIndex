package checkindex

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
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
		var writer *tablewriter.Table
		headerWritten := false
		if !markdown {
			writer = tablewriter.NewTable(&table, tablewriter.WithMaxWidth(80), tablewriter.WithHeaderAutoWrap(tw.WrapBreak), tablewriter.WithHeaderMaxWidth(24), tablewriter.WithRowAutoWrap(tw.WrapBreak), tablewriter.WithRowMaxWidth(24))
			writer.Configure(func(c *tablewriter.Config) {
				c.Header.Formatting.AutoFormat = tw.Off
				c.Row.Formatting.AutoFormat = tw.Off
			})
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
				clean := make([]string, len(cells))
				for i, c := range cells {
					clean[i] = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(c)
				}
				if !headerWritten {
					header := make([]any, len(clean))
					for i := range clean {
						header[i] = clean[i]
					}
					writer.Header(header...)
					headerWritten = true
				} else if err := writer.Append(clean); err != nil {
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
			if err := writer.Render(); err != nil {
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
