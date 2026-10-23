package checkindex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mrcl-oss/checkIndex/internal/reportdata"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"
)

// Options controls report selection and output for one collection run.
type Options struct {
	Cluster, Collection, File string
	Password                  *string
	Direct                    bool
	JSON                      bool
}

// Reporter executes a report using the resolved profile and options.
type Reporter func(context.Context, Profile, Options, io.Writer) error

func newCommand(home string, out, errOut io.Writer, report Reporter) *cli.Command {
	command := &cli.Command{
		Name:        "checkindex",
		Usage:       "report MongoDB index usage",
		HideVersion: true,
		Description: "Collect index-usage statistics from a configured profile or a one-off direct connection. " +
			"Configuration is read from ~/.checkIndex/config.json. The default profile is the first named profile alphabetically. System collections are excluded by default.",
		ArgsUsage: "[filename] (only with --file)",
		Writer:    out, ErrWriter: errOut,
		Commands: []*cli.Command{{
			Name: "config", Usage: "interactively edit the local configuration",
			Action: func(_ context.Context, cmd *cli.Command) error {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return errors.New("checkindex config requires an interactive terminal")
				}
				return runConfigCommand(home, os.Stdin, cmd.Root().Writer, terminalPasswordReader())
			},
		}},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cluster", Usage: "cluster profile from configuration", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "uri", Usage: "MongoDB URI (direct connection or profile override)", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "username", Usage: "MongoDB username (direct connection or profile override)", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "password", Aliases: []string{"p"}, Usage: "password override (visible in process arguments)", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "database", Usage: "database name (direct connection or profile override)", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "auth", Usage: "authentication source (direct connection or profile override)", Category: "DATABASE OPTIONS"},
			&cli.StringSliceFlag{Name: "hostMapping", Usage: "host mapping source=target (repeatable)", Category: "DATABASE OPTIONS"},
			&cli.StringFlag{Name: "collection", Aliases: []string{"c"}, Usage: "report only this collection", Category: "REPORT OPTIONS"},
			&cli.BoolFlag{Name: "file", Usage: "write report to output.md or the optional filename argument", Category: "REPORT OPTIONS"},
			&cli.BoolFlag{Name: "all", Usage: "report every configured cluster profile", Category: "REPORT OPTIONS"},
			&cli.BoolFlag{Name: "json", Usage: "write machine-readable JSON", Category: "REPORT OPTIONS"},
		},
		ArgValidator: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 1 || (cmd.NArg() > 0 && !cmd.Bool("file")) {
				return errors.New("unexpected positional argument; use --file [filename]")
			}
			return nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("all") {
				homeDir := home
				if homeDir == "" {
					var err error
					homeDir, err = os.UserHomeDir()
					if err != nil {
						return err
					}
				}
				config, err := loadConfig(filepath.Join(homeDir, ".checkIndex", "config.json"))
				if err != nil {
					return err
				}
				if report == nil {
					return errors.New("reporter unavailable")
				}
				if cmd.Bool("json") {
					combined, collectErr := CollectConfigured(ctx, config, cmd.String("collection"), func(ctx context.Context, p Profile, collection string) (reportdata.Report, error) {
						var buffer bytes.Buffer
						reportErr := report(ctx, p, Options{Collection: collection, JSON: true}, &buffer)
						var data reportdata.Report
						if buffer.Len() == 0 && reportErr != nil {
							return data, reportErr
						}
						if err := json.Unmarshal(buffer.Bytes(), &data); err != nil {
							return reportdata.Report{}, errors.Join(reportErr, errors.New("invalid report JSON"))
						}
						return data, reportErr
					})
					writeErr := json.NewEncoder(cmd.Root().Writer).Encode(combined)
					return errors.Join(collectErr, writeErr)
				}
				names := make([]string, 0, len(config.Cluster))
				for name := range config.Cluster {
					names = append(names, name)
				}
				sort.Strings(names)

				type clusterResult struct {
					name       string
					profileErr error
					reportErr  error
					output     []byte
				}

				results := make([]clusterResult, len(names))
				var wg sync.WaitGroup
				wg.Add(len(names))
				for i, name := range names {
					go func(idx int, clusterName string) {
						defer wg.Done()
						res := clusterResult{name: clusterName}
						profile, profileErr := config.selectProfile(clusterName, nil)
						if profileErr != nil {
							res.profileErr = profileErr
							results[idx] = res
							return
						}
						var buffer bytes.Buffer
						res.reportErr = report(ctx, profile, Options{Collection: cmd.String("collection"), JSON: cmd.Bool("json")}, &buffer)
						res.output = buffer.Bytes()
						results[idx] = res
					}(i, name)
				}
				wg.Wait()

				var failures []error
				for _, res := range results {
					if res.profileErr != nil {
						failures = append(failures, fmt.Errorf("profile %q: %w", res.name, res.profileErr))
						continue
					}
					if !cmd.Bool("json") {
						if _, writeErr := fmt.Fprintf(cmd.Root().Writer, "Profile: %s\n", res.name); writeErr != nil {
							failures = append(failures, fmt.Errorf("profile %q: %w", res.name, writeErr))
							continue
						}
						if len(res.output) > 0 {
							if _, writeErr := cmd.Root().Writer.Write(res.output); writeErr != nil {
								failures = append(failures, fmt.Errorf("profile %q: %w", res.name, writeErr))
								continue
							}
						}
					}
					if res.reportErr != nil {
						failures = append(failures, fmt.Errorf("profile %q: %w", res.name, res.reportErr))
					}
				}
				return errors.Join(failures...)
			}
			coreSet := cmd.IsSet("uri") || cmd.IsSet("username") || cmd.IsSet("password") || cmd.IsSet("database") || cmd.IsSet("auth") || cmd.IsSet("hostMapping")
			completeCore := cmd.IsSet("uri") && cmd.IsSet("username") && cmd.IsSet("password") && cmd.IsSet("database") && cmd.IsSet("auth")
			direct := completeCore && !cmd.IsSet("cluster")
			mappings := make(map[string]string)
			for _, mapping := range cmd.StringSlice("hostMapping") {
				parts := strings.SplitN(mapping, "=", 2)
				if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
					return errors.New("hostMapping must use nonempty source=target")
				}
				mappings[parts[0]] = parts[1]
			}
			homeDir := home
			if homeDir == "" {
				var err error
				homeDir, err = os.UserHomeDir()
				if err != nil {
					return err
				}
			}
			options := Options{Cluster: cmd.String("cluster"), Collection: cmd.String("collection"), JSON: cmd.Bool("json")}
			if cmd.IsSet("password") {
				password := cmd.String("password")
				options.Password = &password
			}
			if cmd.Bool("file") {
				options.File = "output.md"
				if cmd.NArg() == 1 {
					options.File = cmd.Args().First()
				}
			}
			configPath := filepath.Join(homeDir, ".checkIndex", "config.json")
			var profile Profile
			if direct {
				profile = Profile{Credentials: Credentials{InitialURI: cmd.String("uri"), Username: cmd.String("username"), Password: cmd.String("password")}, DBName: cmd.String("database"), AuthSource: cmd.String("auth"), HostMapping: mappings}
				if strings.TrimSpace(profile.Username) == "" || strings.TrimSpace(profile.Password) == "" || strings.TrimSpace(profile.DBName) == "" || strings.TrimSpace(profile.AuthSource) == "" {
					return errors.New("direct connection requires nonempty URI, username, password, database and auth values")
				}
				if !strings.HasPrefix(profile.InitialURI, "mongodb://") && !strings.HasPrefix(profile.InitialURI, "mongodb+srv://") {
					return errors.New("--uri must be a MongoDB URI")
				}
			} else {
				if coreSet && !cmd.IsSet("cluster") {
					if _, statErr := os.Stat(configPath); os.IsNotExist(statErr) {
						return errors.New("partial connection flags require a named profile; complete all direct connection flags for direct mode")
					}
				}
				config, err := loadConfig(configPath)
				if err != nil {
					return err
				}
				if coreSet && !cmd.IsSet("cluster") {
					hasNamedProfile := false
					for name := range config.Cluster {
						if strings.TrimSpace(name) != "" {
							hasNamedProfile = true
							break
						}
					}
					if !hasNamedProfile {
						return errors.New("partial connection flags require a named profile; complete all direct connection flags for direct mode")
					}
				}
				profile, err = config.selectProfile(options.Cluster, options.Password)
				if err != nil {
					return err
				}
				if cmd.IsSet("uri") {
					profile.InitialURI = cmd.String("uri")
				}
				if cmd.IsSet("username") {
					profile.Username = cmd.String("username")
				}
				if cmd.IsSet("database") {
					profile.DBName = cmd.String("database")
				}
				if cmd.IsSet("auth") {
					profile.AuthSource = cmd.String("auth")
				}
				if !strings.HasPrefix(profile.InitialURI, "mongodb://") && !strings.HasPrefix(profile.InitialURI, "mongodb+srv://") {
					return errors.New("selected cluster initialUri must be a MongoDB URI")
				}
				if len(mappings) > 0 {
					merged := make(map[string]string, len(profile.HostMapping)+len(mappings))
					for key, value := range profile.HostMapping {
						merged[key] = value
					}
					for key, value := range mappings {
						merged[key] = value
					}
					profile.HostMapping = merged
				}
			}
			options.Direct = direct
			if options.File != "" {
				configInfo, configErr := os.Stat(configPath)
				outputInfo, outputErr := os.Stat(options.File)
				if configErr == nil && outputErr == nil && os.SameFile(configInfo, outputInfo) {
					return errors.New("report output must not overwrite the configuration file")
				}
			}
			if report == nil {
				return errors.New("reporter unavailable")
			}
			return report(ctx, profile, options, cmd.Root().Writer)
		},
	}
	return command
}

func run(args []string, home string, out io.Writer, report Reporter) error {
	err := newCommand(home, out, io.Discard, report).Run(context.Background(), append([]string{"checkindex"}, args...))
	if err != nil && hasHelpFlag(args) {
		return nil // urfave/cli has already rendered help; help historically wins over parse errors.
	}
	return err
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}
