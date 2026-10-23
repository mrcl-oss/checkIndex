# checkIndex

[![Go Version](https://img.shields.io/github/go-mod/go-version/mrcl-oss/checkIndex)](https://golang.org/)
[![Go Reference](https://pkg.go.dev/badge/github.com/mrcl-oss/checkIndex.svg)](https://pkg.go.dev/github.com/mrcl-oss/checkIndex)
[![CI](https://github.com/mrcl-oss/checkIndex/actions/workflows/ci.yml/badge.svg)](https://github.com/mrcl-oss/checkIndex/actions/workflows/ci.yml)

**See index usage across every MongoDB replica-set data node—not just the primary.**

`checkindex` is a small Go command-line tool that runs MongoDB's `$indexStats` stage against each healthy data-bearing node and presents the counters side by side, with a per-index total. It makes primary-versus-secondary usage visible in one report, without manually connecting to each member.

## Why checkindex?

Atlas Data Explorer's index **Usage** metric reports usage on the primary node only. MongoDB's documentation recommends running `$indexStats` on each node for a more complete cluster picture; `checkindex` automates that collection and comparison.

> **Atlas comparison:** This is a focused companion tool, not a replacement for Atlas monitoring or Data Explorer. Atlas documents that its Data Explorer Usage metric covers the primary only and may not represent workloads served by secondaries. `checkindex` connects directly to discovered nodes and reports each node's `$indexStats` counters in the same table.

Sources: [Atlas index usage metrics](https://www.mongodb.com/docs/atlas/atlas-ui/indexes/) · [`$indexStats` aggregation stage](https://www.mongodb.com/docs/manual/reference/operator/aggregation/indexStats/)

## What it reports

- Index name and key pattern.
- `$indexStats.accesses.ops` for each discovered healthy primary and secondary.
- A total that sums the reported node counters for that index.
- A separate report for each selected collection.

By default, it includes all non-system collections. Use `--collection` to select one collection, including a system collection if explicitly requested. Output can be rendered as a terminal table or Markdown.

### Important interpretation notes

- These are MongoDB's current `$indexStats` counters, not historical analytics or query-planner recommendations. Counter values can reset when an index is created or a node restarts; compare `accesses.since`/observation times accordingly.
- The total is a sum of the counters returned by the nodes in this run. It is not a deduplicated count of application operations.
- The tool connects directly to each discovered member. The running machine must be able to resolve and reach those node addresses, and the configured MongoDB user must have permission to run `$indexStats` on the selected collections.
- If a node cannot be queried, the report is partial and the failure is reported. Do not interpret missing node data as zero usage.
- Standalone MongoDB instances are supported. `mongos`/sharded clusters are not currently supported.

## Requirements

- Network access from the host to the seed and discovered MongoDB nodes.
- A MongoDB account with permission to run aggregation with `$indexStats` on the target database and collections.
- Go 1.27.3 or newer only when building from source. Prebuilt binaries and Debian packages do not require Go.

The runtime binary does **not** require Node.js or `mongosh`.

## Install

Choose the option that fits your system. The source installer and manual build require Go; release archives and Debian packages are ready to run.

### Prebuilt release archive (Linux, macOS, Windows)

1. Open the [GitHub Releases](https://github.com/mrcl-oss/checkIndex/releases) page and download the archive matching your OS and architecture: Linux and macOS (`amd64` or `arm64`), or Windows (`amd64`).
2. Extract the `.tar.gz` archive on Linux/macOS or the `.zip` archive on Windows.
3. Run the extracted `checkindex` executable, or move it to a directory on your `PATH`.

For example, on Linux or macOS, after extracting the archive in the current directory:

```sh
install -m 0755 checkindex "$HOME/.local/bin/checkindex"
checkindex --help
```

Create `$HOME/.local/bin` first if it does not exist, and ensure it is on `PATH`. The executable name inside each archive is `checkindex` (or `checkindex.exe` on Windows).

### Debian/Ubuntu package

Download the `.deb` for your Linux architecture (`amd64` or `arm64`) from the [GitHub Releases](https://github.com/mrcl-oss/checkIndex/releases) page, then install the downloaded local file:

```sh
sudo apt install ./checkindex_1.0.0_linux_amd64.deb
checkindex --help
```

Replace the example filename with the exact package you downloaded; for ARM64, choose the `arm64` asset instead. This installs a local `.deb` package; it is **not an APT repository** and does not configure APT to find or manage future checkindex updates. To update, download and install a newer release package.

### Build and install from source

Clone the repository, then run its installer from the checkout. Go 1.27.3 or newer must be installed and available on `PATH`.

```sh
git clone https://github.com/mrcl-oss/checkIndex.git
cd checkIndex
./scripts/install.sh
```

The script builds locally and installs to `${XDG_BIN_HOME:-$HOME/.local/bin}`. It does not use `sudo`. Add the destination to `PATH` if needed (for the default location, `export PATH="$HOME/.local/bin:$PATH"`), then verify:

```sh
checkindex --help
```

To build without installing, run from the repository root:

```sh
go build -o bin/checkindex ./cmd/checkindex
./bin/checkindex --help
```

## Configure

Configuration is stored outside the repository at:

```text
~/.checkIndex/config.json
```

On the first command that needs configuration, `checkindex` creates a starter file with a blank profile and exits with setup instructions. It does not overwrite an existing file. The configuration directory is restricted to mode `0700`, and the file to `0600`.

Example structure (replace placeholders locally; do not commit credentials):

```json
{
  "dbName": "database",
  "authSource": "admin",
  "hostMapping": {},
  "cluster": {
    "production": {
      "initialUri": "mongodb://your-seed-host:27017/database?authSource=admin",
      "username": "your-user",
      "password": "your-password",
      "hostMapping": {"mongo:27017": "localhost:27017"}
    },
    "container-network": {
      "initialUri": "mongodb://your-seed-host:27017/database?authSource=admin",
      "username": "your-user",
      "password": "your-password",
      "hostMapping": {"mongo:27017": "mongo:27017"}
    }
  }
}
```

Cluster profile names are user-defined. Each profile needs an `initialUri`, `username`, and `password`. Use `mongodb://` or `mongodb+srv://` URIs. A profile-level `hostMapping` fully replaces the top-level shared mapping: omitted or `null` inherits the shared map, while `{}` explicitly disables it. This lets profiles map the same discovered address differently. Newly generated starter configs have an empty shared mapping.

### Interactive configuration editor

Run this from an interactive terminal:

```sh
checkindex config
```

The keyboard-driven editor lets you view and change shared settings, host mappings, and cluster profiles; add or remove profiles; and save or discard changes. Use **↑/↓** or **Tab** to navigate, **Enter** to select, and **Esc** to go back or cancel. Profile mapping screens identify overrides and offer a way to restore shared mapping inheritance. Passwords are hidden while entering them, and credentials embedded in displayed URIs are redacted. Changes are validated and saved atomically only when you choose **Save and quit**.

## Usage

```sh
# Show help; does not read or create the config file
checkindex --help

# Report all non-system collections using the default profile
checkindex

# Report all configured profiles in alphabetical order
checkindex --all

# Select a profile and one collection
checkindex --cluster production --collection orders

# Override the selected profile's password for this run
checkindex --cluster production --password 'temporary-secret'

# One-off direct connection; no profile/config file is needed
checkindex --uri 'mongodb://seed.example:27017/?replicaSet=rs0' \
  --username report-user --password 'temporary-secret' \
  --database app --auth admin \
  --hostMapping 'mongo.internal:27017=10.0.0.12:27017' \
  --hostMapping 'mongo2.internal:27017=10.0.0.13:27017'

# Write Markdown to output.md, or choose a file name
checkindex --cluster production --file
checkindex --cluster production --file indexes.md

# Write machine-readable JSON to stdout or a file
checkindex --cluster production --json
checkindex --cluster production --json --file indexes.json

# Combine every configured profile into one JSON document
checkindex --all --json

# Select the same collection (or comma-separated collections) in every profile
checkindex --all --json --collection orders
```

`--cluster` selects a profile from the config. If omitted, the alphabetically first named profile is selected.

`--all` collects configured profiles concurrently and combines results in alphabetical profile order. Text reports are labeled with the profile name. `--collection` (including comma-separated names) filters each profile; without it, all non-system collections are included. `--json` selects one combined JSON document. Other connection/output options are ignored in this mode (including `--cluster`, direct connection overrides, `--file`, and `--hostMapping`); reports go to standard output. Profile/report failures do not stop other profiles, but any failure makes the command exit nonzero.

### JSON schema

Every JSON selection—one/all profiles and one/all collections—uses collection → index → `clusters` → cluster → `nodes` → discovered node name:

```json
{
  "items": {
    "ix": {
      "clusters": {
        "production": {
          "nodes": {
            "mongo.internal:27017": {"count": 123, "primary": true},
            "mongo2.internal:27017": {"count": 7, "primary": false}
          },
          "total": 130
        }
      },
      "total": 130
    }
  }
}
```

Counts and totals are checked `int64` values (including values above JavaScript's exact integer range). Cluster totals sum node counts; index totals sum cluster totals. Matching collection/index/cluster/node names merge by name. Node keys retain discovered identity, not host-mapping destinations. `primary` is true only for a discovered PRIMARY, not a secondary or standalone. Missing/failed nodes are omitted, not invented as zero. Empty maps encode as `{}`.

A direct connection uses the cluster name `direct` (or `report.Profile.Name` in the Go API). There is no wrapper or partial-status field. Errors signal incomplete reports while retaining available data. JSON stdout contains no progress messages; command diagnostics go to stderr. For single-profile runs, `--json --file` writes JSON instead of Markdown; `--all` always uses stdout. This report schema does not change the configuration schema.

### Importable Go API

`report.Collect` with `All: true` is the typed equivalent of `checkindex --all --json`. It reads the same `~/.checkIndex/config.json` and optionally filters collections:

```go
import (
    "context"
    "log"

    "github.com/mrcl-oss/checkIndex/report"
)

func inspect(ctx context.Context) {
    result, err := report.Collect(ctx, report.Options{All: true, Collection: "items"})
    if err != nil {
        log.Printf("incomplete report (partial=%t): %v", result.Partial, err)
    }
    var indexes report.Collection = result.Collections["items"]
    var index report.Index = indexes["ix"]
    var cluster report.Cluster = index.Clusters["production"]
    for name, usage := range cluster.Nodes { // usage is report.NodeUsage
        log.Printf("%s: count=%d primary=%t", name, usage.Count, usage.Primary)
    }
}
```

`report.Report.Collections` is `map[string]report.Collection`; each `Collection` is `map[string]report.Index`. `Index.Clusters` is `map[string]report.Cluster`, and `Cluster.Nodes` is `map[string]report.NodeUsage`. `Index.Total`, `Cluster.Total`, and `NodeUsage.Count` are `int64`; `NodeUsage.Primary` is `bool`. `Report.Partial` records incomplete collection and is not serialized. `report.Marshal` and `json.Marshal` emit the collections object; `json.Unmarshal` into `report.Report` restores typed counts without a floating-point conversion.

Use exactly one selection: `All`, `ProfileName`, flat direct fields, or `Direct: report.Profile{...}`. Conflicting selections return an error before collection. Named and direct entry points remain available.

Runnable Go API examples are in [`examples/`](examples/). The direct examples do not need a local profile:

```sh
MONGODB_PASSWORD='your-secret' go run ./examples/json-report \
  --uri 'mongodb://seed.example:27017/?replicaSet=rs0' --username report-user --database app --auth admin
MONGODB_PASSWORD='your-secret' go run ./examples/collection-report \
  --uri 'mongodb://seed.example:27017/?replicaSet=rs0' --username report-user --database app --auth admin --collection orders

# All saved profiles; no direct connection flags needed
go run ./examples/all-report --collection orders
```

Set `MONGODB_PASSWORD` through an appropriate secret-management mechanism; environment variables avoid command-line arguments and source code but are not inherently secret (they may be exposed through process environments, diagnostics, or inherited by child processes). Add repeatable `--host-mapping discovered-host=reachable-host` flags to the direct examples when discovered node names are not reachable locally. The JSON example prints `report.Marshal` output; the collection example traverses typed clusters and nodes; the all-profile example retains available JSON and exits nonzero on collection errors.

Direct mode is selected when all five connection flags (`--uri`, `--username`, `--password`, `--database`, and `--auth`) are supplied without `--cluster`. It does not read or create the profile config, and works even when the config is missing or contains only the blank starter profile. Without `--cluster`, any connection flags supplied with named profiles override only their corresponding fields on the alphabetically first named profile. If no usable named profile exists, partial connection flags fail clearly rather than being treated as direct mode. If `--cluster` is supplied, profile mode takes precedence and supplied connection flags override that profile. `--hostMapping source=target` may be repeated in either mode; mappings override matching configured keys in profile mode. Host mappings are optional in direct mode.

**Password security:** `--password` is passed as a process argument and may be visible to other local users or captured in shell history/process monitoring. Avoid this flag on shared systems.

| Option                               | Description                                                                                                |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| `--cluster NAME`                     | Select a configured cluster profile.                                                                       |
| `--uri URI`                          | Direct connection URI or profile URI override.                                                             |
| `--username USER`                    | Direct connection username or profile override.                                                            |
| `--password PASSWORD`, `-p PASSWORD` | Direct password or profile override. Visible in process arguments; avoid on shared systems.                |
| `--database NAME`                    | Direct database name or profile override.                                                                  |
| `--auth SOURCE`                      | Direct authentication source or profile override.                                                          |
| `--hostMapping SOURCE=TARGET`        | Optional, repeatable mapping; CLI values replace matching profile keys.                                    |
| `--collection NAME`, `-c NAME`       | Select one or comma-separated collection names, including with `--all`. Defaults to all non-system collections. |
| `--file [FILENAME]`                  | Write Markdown instead of terminal tables. Defaults to `output.md`; an optional filename follows the flag. |
| `--all`                              | Report all profiles; honors `--collection` and `--json`, ignores other connection/output options.           |
| `--json`                             | Emit JSON to stdout or, with `--file`, the named file.                                                     |
| `--help`, `-h`                       | Show help without reading configuration or connecting to MongoDB.                                          |
| `config`                             | Open the interactive configuration editor.                                                                 |

Markdown output is written with restrictive file permissions. The command refuses to overwrite the runtime configuration file. Status and warning messages remain on the console when the report itself is written to a file.

## Development

```sh
go test ./...
go vet ./...
go build -o bin/checkindex ./cmd/checkindex
```

Tests use fakes and temporary directories; they do not require a live MongoDB cluster. The project uses the official MongoDB Go driver, [urfave/cli](https://github.com/urfave/cli), and [Bubble Tea](https://github.com/charmbracelet/bubbletea).

## Project layout

```text
cmd/checkindex/       CLI entry point
report/               public Go report API and stable report types
examples/             standalone consumer programs for the public report API
internal/checkindex/  config, MongoDB collection, reporting, and TUI
internal/reportdata/  shared typed report schema and checked merging
scripts/install.sh    per-user build-and-install script
```
