package checkindex

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type passwordReader func() (string, error)

func editConfig(input io.Reader, output io.Writer, readPassword passwordReader, initial Config) (Config, bool, error) {
	scanner := bufio.NewScanner(input)
	read := func(prompt string) (string, error) {
		if _, err := fmt.Fprint(output, prompt); err != nil {
			return "", err
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return "", err
			}
			return "", io.EOF
		}
		return scanner.Text(), nil
	}
	c := cloneConfig(initial)
	for {
		if _, err := fmt.Fprintln(output, "\nConfiguration"); err != nil {
			return initial, false, err
		}
		if _, err := fmt.Fprintln(output, "1) Edit shared settings"); err != nil {
			return initial, false, err
		}
		if _, err := fmt.Fprintln(output, "2) Manage cluster profiles"); err != nil {
			return initial, false, err
		}
		if _, err := fmt.Fprintln(output, "3) Save and quit"); err != nil {
			return initial, false, err
		}
		if _, err := fmt.Fprintln(output, "4) Quit without saving"); err != nil {
			return initial, false, err
		}
		choice, err := read("> ")
		if err != nil {
			return initial, false, err
		}
		switch choice {
		case "1":
			if err = editShared(read, output, &c); err != nil {
				return initial, false, err
			}
		case "2":
			if err = editProfiles(read, output, readPassword, &c); err != nil {
				return initial, false, err
			}
		case "3":
			if err = validateConfig(c); err != nil {
				return initial, false, err
			}
			return c, true, nil
		case "4":
			return initial, false, nil
		default:
			if _, err := fmt.Fprintln(output, "Invalid choice."); err != nil {
				return initial, false, err
			}
		}
	}
}

type prompt func(string) (string, error)

func redactURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return strings.Replace(raw, u.User.String()+"@", "***:***@", 1)
}

func editShared(read prompt, out io.Writer, c *Config) error {
	for {
		if _, err := fmt.Fprintf(out, "Shared settings: 1) dbName [%s] 2) authSource [%s] 3) hostMapping 0) Back\n", c.DBName, c.AuthSource); err != nil {
			return err
		}
		v, err := read("> ")
		if err != nil {
			return err
		}
		switch v {
		case "0":
			return nil
		case "1", "2":
			field := "dbName"
			target := &c.DBName
			if v == "2" {
				field = "authSource"
				target = &c.AuthSource
			}
			n, e := read(field + ": ")
			if e != nil {
				return e
			}
			old := *target
			*target = n
			if e = validateConfig(*c); e != nil {
				*target = old
				if _, writeErr := fmt.Fprintln(out, e); writeErr != nil {
					return writeErr
				}
			}
		case "3":
			keys := make([]string, 0, len(c.HostMapping))
			for key := range c.HostMapping {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if _, err := fmt.Fprintln(out, "Current host mappings:"); err != nil {
				return err
			}
			for _, key := range keys {
				if _, err := fmt.Fprintf(out, "- %s = %s\n", key, c.HostMapping[key]); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(out, "Host mapping: 1) Add or edit entry 2) Remove entry 0) Back"); err != nil {
				return err
			}
			action, e := read("> ")
			if e != nil {
				return e
			}
			if action == "0" {
				continue
			}
			if action != "1" && action != "2" {
				if _, err := fmt.Fprintln(out, "Invalid choice."); err != nil {
					return err
				}
				continue
			}
			key, e := read("Host key: ")
			if e != nil {
				return e
			}
			if action == "2" {
				if _, ok := c.HostMapping[key]; !ok {
					if _, err := fmt.Fprintln(out, "Unknown host key."); err != nil {
						return err
					}
					continue
				}
				delete(c.HostMapping, key)
				continue
			}
			val, e := read("Host value: ")
			if e != nil {
				return e
			}
			old, had := c.HostMapping[key]
			c.HostMapping[key] = val
			if e = validateConfig(*c); e != nil {
				if had {
					c.HostMapping[key] = old
				} else {
					delete(c.HostMapping, key)
				}
				if _, writeErr := fmt.Fprintln(out, e); writeErr != nil {
					return writeErr
				}
			}
		default:
			if _, err := fmt.Fprintln(out, "Invalid choice."); err != nil {
				return err
			}
		}
	}
}

func editProfiles(read prompt, out io.Writer, readPassword passwordReader, c *Config) error {
	for {
		names := make([]string, 0, len(c.Cluster))
		for n := range c.Cluster {
			names = append(names, n)
		}
		sort.Strings(names)
		if _, err := fmt.Fprintln(out, "Cluster profiles:"); err != nil {
			return err
		}
		for _, n := range names {
			p := c.Cluster[n]
			password := "<empty>"
			if p.Password != "" {
				password = "<configured>"
			}
			if _, err := fmt.Fprintf(out, "- %s: initialUri=%s username=%s password=%s\n", n, redactURI(p.InitialURI), p.Username, password); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out, "1) Choose profile 2) Add profile 0) Back"); err != nil {
			return err
		}
		v, e := read("> ")
		if e != nil {
			return e
		}
		switch v {
		case "0":
			return nil
		case "2":
			name, e := read("Profile name: ")
			if e != nil {
				return e
			}
			if strings.TrimSpace(name) == "" {
				if _, err := fmt.Fprintln(out, "Profile name must be nonempty."); err != nil {
					return err
				}
				continue
			}
			if _, ok := c.Cluster[name]; ok {
				if _, err := fmt.Fprintln(out, "Profile already exists."); err != nil {
					return err
				}
				continue
			}
			p := Credentials{}
			if p.InitialURI, e = read("initialUri: "); e != nil {
				return e
			}
			if p.Username, e = read("username: "); e != nil {
				return e
			}
			if _, e = fmt.Fprint(out, "password (input hidden): "); e != nil {
				return e
			}
			if p.Password, e = readPassword(); e != nil {
				return e
			}
			c.Cluster[name] = p
		case "1":
			name, e := read("Profile name: ")
			if e != nil {
				return e
			}
			p, ok := c.Cluster[name]
			if !ok {
				if _, err := fmt.Fprintln(out, "Unknown profile."); err != nil {
					return err
				}
				continue
			}
		profileLoop:
			for {
				password := "<empty>"
				if p.Password != "" {
					password = "<configured>"
				}
				if _, e = fmt.Fprintf(out, "1) initialUri [%s] 2) username [%s] 3) password [%s] 4) Delete profile 0) Back\n", redactURI(p.InitialURI), p.Username, password); e != nil {
					return e
				}
				field, e := read("> ")
				if e != nil {
					return e
				}
				switch field {
				case "0":
					break profileLoop
				case "1", "2":
					val, e := read(map[string]string{"1": "initialUri: ", "2": "username: "}[field])
					if e != nil {
						return e
					}
					if field == "1" {
						p.InitialURI = val
					} else {
						p.Username = val
					}
					c.Cluster[name] = p
				case "3":
					if _, e = fmt.Fprint(out, "password (input hidden): "); e != nil {
						return e
					}
					val, e := readPassword()
					if e != nil {
						return e
					}
					p.Password = val
					c.Cluster[name] = p
				case "4":
					if len(c.Cluster) == 1 {
						if _, err := fmt.Fprintln(out, "Cannot delete the final profile."); err != nil {
							return err
						}
						continue
					}
					confirm, e := read("Delete profile? [y/N]: ")
					if e != nil {
						return e
					}
					if strings.EqualFold(confirm, "y") {
						delete(c.Cluster, name)
					}
					break profileLoop
				default:
					if _, err := fmt.Fprintln(out, "Invalid choice."); err != nil {
						return err
					}
				}
			}
		default:
			if _, err := fmt.Fprintln(out, "Invalid choice."); err != nil {
				return err
			}
		}
	}
}

func cloneConfig(c Config) Config {
	out := c
	out.HostMapping = map[string]string{}
	for k, v := range c.HostMapping {
		out.HostMapping[k] = v
	}
	out.Cluster = map[string]Credentials{}
	for k, v := range c.Cluster {
		if v.HostMapping != nil {
			copied := map[string]string{}
			for key, value := range v.HostMapping {
				copied[key] = value
			}
			v.HostMapping = copied
		}
		out.Cluster[k] = v
	}
	return out
}
func validateConfig(c Config) error {
	b, e := json.Marshal(c)
	if e != nil {
		return e
	}
	_, e = decodeConfig(b)
	return e
}

// ValidateConfig validates a configuration using the same schema rules as the CLI.
func ValidateConfig(c Config) error { return validateConfig(c) }

func saveConfig(path string, c Config) (resultErr error) {
	if err := validateConfig(c); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("inspect config directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("refusing to write through a non-directory config path")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("restrict config directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to replace symlink config")
	}
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if removeErr := os.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary config: %w", removeErr))
		}
	}()
	if err = f.Chmod(0600); err == nil {
		var b []byte
		b, err = json.MarshalIndent(c, "", "  ")
		if err == nil {
			b = append(b, '\n')
			_, err = f.Write(b)
		}
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close config: %w", closeErr)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	if err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}
	return nil
}

// SaveConfig validates and safely persists a configuration using the CLI writer.
func SaveConfig(path string, c Config) error { return saveConfig(path, c) }

func runConfigCommand(home string, input io.Reader, output io.Writer, passwords passwordReader) error {
	if passwords == nil {
		return errors.New("password input requires an interactive terminal")
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	path := filepath.Join(home, ".checkIndex", "config.json")
	c, err := loadConfig(path)
	if err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return err
		}
		c, err = loadConfig(path)
		if err != nil {
			return err
		}
	}
	if input != os.Stdin {
		updated, save, err := editConfig(input, output, passwords, c)
		if err != nil || !save {
			return err
		}
		return saveConfig(path, updated)
	}
	model := newConfigModel(c)
	program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(output), tea.WithAltScreen())
	result, err := program.Run()
	if err != nil {
		return err
	}
	final := result.(configModel)
	if !final.save {
		return nil
	}
	return saveConfig(path, final.config)
}

func terminalPasswordReader() passwordReader {
	return func() (string, error) {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return "", errors.New("password input requires an interactive terminal")
		}
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		_, writeErr := fmt.Fprintln(os.Stdout)
		return string(b), errors.Join(err, writeErr)
	}
}
