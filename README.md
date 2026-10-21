# checkindex

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

- Linux/macOS terminal for the provided installer; Windows users can build with Go directly.
- Go 1.22 or newer to build from source.
- Network access from the host to the seed and discovered MongoDB nodes.
- A MongoDB account with permission to run aggregation with `$indexStats` on the target database and collections.

The runtime binary does **not** require Node.js or `mongosh`.

## Install

### Install from a source checkout

Clone or download this repository, then run the installer from the checkout:

```sh
git clone <repository-url>
cd checkIndex
./scripts/install.sh
```

The script builds `checkindex` locally and installs it to `${XDG_BIN_HOME:-$HOME/.local/bin}`. It does not require `sudo` and does not create or modify your MongoDB configuration. If the destination is not on `PATH`, add it to your shell configuration, for example:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Open a new shell or reload the shell configuration, then verify:

```sh
checkindex --help
```

### Build manually

```sh
go build -o bin/checkindex ./cmd/checkindex
./bin/checkindex --help
```

Prebuilt release downloads are not currently provided; the installer builds from source on the target machine.

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
      "password": "your-password"
    }
  }
}
```

Cluster profile names are user-defined. Each profile needs an `initialUri`, `username`, and `password`. Use `mongodb://` or `mongodb+srv://` URIs. `hostMapping` is an optional object mapping discovered node addresses to addresses reachable from this machine, for example when internal DNS names need local replacements.

### Interactive configuration editor

Run this from an interactive terminal:

```sh
checkindex config
```

The keyboard-driven editor lets you view and change shared settings, host mappings, and cluster profiles; add or remove profiles; and save or discard changes. Use **↑/↓** or **Tab** to navigate, **Enter** to select, and **Esc** to go back or cancel. Profile and host-mapping views also provide add/remove shortcuts shown on screen. Passwords are hidden while entering them, and credentials embedded in displayed URIs are redacted. Changes are validated and saved atomically only when you choose **Save and quit**.

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
```

`--cluster` selects a profile from the config. If omitted, the alphabetically first named profile is selected.

`--all` reports every configured cluster profile in alphabetical order, labeling each report with its profile name. In this mode, all other report and connection options are ignored (including `--cluster`, direct connection overrides, `--collection`, `--file`, and `--hostMapping`); reports go to standard output. Profile/report failures do not stop later profiles, but any failure makes the command exit nonzero.

Direct mode is selected when all five connection flags (`--uri`, `--username`, `--password`, `--database`, and `--auth`) are supplied without `--cluster`. It does not read or create the profile config, and works even when the config is missing or contains only the blank starter profile. Without `--cluster`, any connection flags supplied with named profiles override only their corresponding fields on the alphabetically first named profile. If no usable named profile exists, partial connection flags fail clearly rather than being treated as direct mode. If `--cluster` is supplied, profile mode takes precedence and supplied connection flags override that profile. `--hostMapping source=target` may be repeated in either mode; mappings override matching configured keys in profile mode. Host mappings are optional in direct mode.

**Password security:** `--password` is passed as a process argument and may be visible to other local users or captured in shell history/process monitoring. Avoid this flag on shared systems.

| Option | Description |
|--------|-------------|
| `--cluster NAME` | Select a configured cluster profile. |
| `--uri URI` | Direct connection URI or profile URI override. |
| `--username USER` | Direct connection username or profile override. |
| `--password PASSWORD`, `-p PASSWORD` | Direct password or profile override. Visible in process arguments; avoid on shared systems. |
| `--database NAME` | Direct database name or profile override. |
| `--auth SOURCE` | Direct authentication source or profile override. |
| `--hostMapping SOURCE=TARGET` | Optional, repeatable mapping; CLI values replace matching profile keys. |
| `--collection NAME`, `-c NAME` | Report only the named collection. Without it, all non-system collections are included. |
| `--file [FILENAME]` | Write Markdown instead of terminal tables. Defaults to `output.md`; an optional filename follows the flag. |
| `--all` | Report every configured profile in alphabetical order; other report and connection options are ignored. |
| `--help`, `-h` | Show help without reading configuration or connecting to MongoDB. |
| `config` | Open the interactive configuration editor. |

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
internal/checkindex/  config, MongoDB collection, reporting, and TUI
scripts/install.sh    per-user build-and-install script
```
