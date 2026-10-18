package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Locations says where configuration lives for a run: the file the
// interactive setup writes, and the files that already exist and take
// part in the merge.
type Locations struct {
	// Global is the canonical global config path, whether the file exists
	// or not; "" when neither a home directory nor XDG_CONFIG_HOME is
	// known. Setup writes here: a secret does not belong in a file that
	// sits in a repository.
	Global string
	// Read are the files that exist, lowest priority first — the global
	// one, then the project's — under their current or legacy names.
	Read []string
}

// Locate resolves Locations with the same rules LoadFrom reads files by.
func Locate(home, workingDir string, lookupEnv func(string) (string, bool)) Locations {
	var loc Locations
	if dir := GlobalDir(home, lookupEnv); dir != "" {
		loc.Global = filepath.Join(dir, "config.yaml")
		loc.Read = appendExisting(loc.Read, loc.Global, legacyGlobal(home))
	}
	if workingDir != "" {
		loc.Read = appendExisting(loc.Read,
			filepath.Join(workingDir, ".hint", "config.yaml"),
			filepath.Join(workingDir, "hint.yaml"))
	}
	return loc
}

func legacyGlobal(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "hint.yaml")
}

// appendExisting adds the first of candidates that is a file.
func appendExisting(list []string, candidates ...string) []string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return append(list, c)
		}
	}
	return list
}

// Document is a config file being edited: a YAML tree, so that comments,
// key order and keys this package does not know survive the change.
type Document struct {
	root *yaml.Node
}

// Edit applies fn to the config file at path and writes the result, all
// or nothing: a missing file is created (directory 0700, file 0600), the
// edited document must still decode as a config before anything is
// written, and the file is replaced atomically. fn sees one document, so
// several changes cost one read and one write.
func Edit(path string, fn func(*Document) error) error {
	var doc yaml.Node
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("config: reading %s: %w", path, err)
	default:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("config: parsing %s: %w", path, err)
		}
	}
	if doc.Kind == 0 { // a missing or empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	d := &Document{root: doc.Content[0]}
	if d.root.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s is not a mapping at the top level", path)
	}
	if err := fn(d); err != nil {
		return err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("config: encoding %s: %w", path, err)
	}
	_ = enc.Close()
	if err := checkDecodes(buf.Bytes()); err != nil {
		return fmt.Errorf("config: the change would break %s: %w", path, err)
	}
	return writeAtomic(path, buf.Bytes())
}

// checkDecodes is the loader's own reading of a file, strict about types
// and about duplicate profile names, so that a change that would make the
// next start fail is refused now.
func checkDecodes(data []byte) error {
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range fc.Providers {
		if p.Name == "" {
			return errors.New("a provider profile has no name")
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate provider profile %q", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// writeAtomic writes data beside path and renames it over path, so a
// failure halfway leaves the old file, not half a new one. The file may
// hold a key: owner only.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// Set writes one key, given as the user types it (see [LookupKey]),
// after checking the value against the key's type. Setting a field of a
// profile that does not exist creates the profile (kind openai) but does
// not make it the default: in a project file that would switch the
// repository to it silently. default_provider is a key of its own.
func (d *Document) Set(path, value string) error {
	k, profile, err := LookupKey(path)
	if err != nil {
		return err
	}
	v, err := k.Normalize(value)
	if err != nil {
		return err
	}
	parent, key := d.root, k.Path
	if field, ok := k.Profile(); ok {
		if parent, err = d.profile(profile, true); err != nil {
			return err
		}
		key = field
	} else if section, leaf, nested := strings.Cut(k.Path, "."); nested {
		parent, key = d.section(section), leaf
	}
	node := scalar(v)
	if k.Type == TypeString || k.Type == TypeSecret || k.Type == TypeEnum {
		// A model named "8" stays a string when it is read back.
		node.Tag = "!!str"
	}
	setKey(parent, key, node)
	return nil
}

// Unset removes one key, so that the next layer down applies. Unsetting
// what is not there is not an error. A profile's name cannot be unset;
// remove the profile by hand.
func (d *Document) Unset(path string) error {
	k, profile, err := LookupKey(path)
	if err != nil {
		return err
	}
	parent, key := d.root, k.Path
	if field, ok := k.Profile(); ok {
		if parent, err = d.profile(profile, false); err != nil || parent == nil {
			return err
		}
		key = field
	} else if section, leaf, nested := strings.Cut(k.Path, "."); nested {
		if parent = mapValue(d.root, section); parent == nil {
			return nil
		}
		key = leaf
	}
	deleteKey(parent, key)
	return nil
}

// profile returns the mapping of the named profile, creating it (kind
// openai) when create is set and it is missing.
func (d *Document) profile(name string, create bool) (*yaml.Node, error) {
	providers := mapValue(d.root, "providers")
	if providers == nil {
		if !create {
			return nil, nil
		}
		providers = &yaml.Node{Kind: yaml.SequenceNode}
		setKey(d.root, "providers", providers)
	}
	if providers.Kind != yaml.SequenceNode {
		return nil, errors.New("config: providers is not a list")
	}
	for _, item := range providers.Content {
		if n := mapValue(item, "name"); n != nil && n.Value == name {
			return item, nil
		}
	}
	if !create {
		return nil, nil
	}
	p := &yaml.Node{Kind: yaml.MappingNode}
	setKey(p, "name", scalar(name))
	setKey(p, "kind", scalar(string(KindOpenAI)))
	providers.Content = append(providers.Content, p)
	return p, nil
}

// section returns the mapping under a top-level key, creating it.
func (d *Document) section(name string) *yaml.Node {
	if m := mapValue(d.root, name); m != nil && m.Kind == yaml.MappingNode {
		return m
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	setKey(d.root, name, m)
	return m
}

// WriteProfile sets several fields of one profile in one edit. values
// maps a field name (kind, base_url, api_key, …) to its value; an empty
// value leaves the field as the file has it. This is the setup form, which
// writes a whole profile, so a file without a default_provider gets this
// one as its default.
func WriteProfile(path, name string, values map[string]string) error {
	if name == "" {
		return errors.New("config: a profile needs a name")
	}
	return Edit(path, func(d *Document) error {
		if _, err := d.profile(name, true); err != nil {
			return err
		}
		if mapValue(d.root, "default_provider") == nil {
			setKey(d.root, "default_provider", scalar(name))
		}
		// Table order, so that a new profile's fields read the way the
		// example lists them.
		for _, k := range ProfileKeys() {
			field, _ := k.Profile()
			if v := values[field]; v != "" {
				if err := d.Set("providers."+name+"."+field, v); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

// mapValue returns the value node of key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// deleteKey removes key from a mapping node.
func deleteKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// setKey sets key in a mapping node, keeping its position if it exists.
func setKey(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			// Keep the comments that hang on the old value.
			value.HeadComment, value.LineComment, value.FootComment =
				m.Content[i+1].HeadComment, m.Content[i+1].LineComment, m.Content[i+1].FootComment
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, scalar(key), value)
}
