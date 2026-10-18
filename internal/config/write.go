package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// ProfileUpdate is what the interactive setup changes about one profile.
// An empty field is left as the file has it.
type ProfileUpdate struct {
	Name    string
	Kind    Kind
	BaseURL string
	// APIKey is written as given: a literal key, or a ${VAR} reference
	// that the loader expands at run time.
	APIKey string
	Model  string
}

// WriteProfile applies upd to the profile of that name in the config
// file at path, adding the profile if it is not there and creating the
// file (mode 0600, directory 0700) if it does not exist. The file is
// edited as a document, so comments, order and keys this function does
// not know survive. A new profile in a file without a default becomes the
// default.
func WriteProfile(path string, upd ProfileUpdate) error {
	if upd.Name == "" {
		return errors.New("config: a profile needs a name")
	}
	var doc yaml.Node
	switch data, err := os.ReadFile(path); {
	case errors.Is(err, os.ErrNotExist):
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	case err != nil:
		return fmt.Errorf("config: reading %s: %w", path, err)
	default:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("config: parsing %s: %w", path, err)
		}
		if doc.Kind == 0 { // an empty file
			doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config: %s is not a mapping at the top level", path)
	}

	providers := mapValue(root, "providers")
	if providers == nil {
		providers = &yaml.Node{Kind: yaml.SequenceNode}
		setKey(root, "providers", providers)
	}
	if providers.Kind != yaml.SequenceNode {
		return fmt.Errorf("config: %s: providers is not a list", path)
	}
	var profile *yaml.Node
	for _, item := range providers.Content {
		if name := mapValue(item, "name"); name != nil && name.Value == upd.Name {
			profile = item
		}
	}
	created := profile == nil
	if created {
		profile = &yaml.Node{Kind: yaml.MappingNode}
		setKey(profile, "name", scalar(upd.Name))
		kind := upd.Kind
		if kind == "" {
			kind = KindOpenAI
		}
		setKey(profile, "kind", scalar(string(kind)))
		providers.Content = append(providers.Content, profile)
	}
	for _, f := range []struct{ key, value string }{
		{"base_url", upd.BaseURL}, {"api_key", upd.APIKey}, {"model", upd.Model},
	} {
		if f.value != "" {
			setKey(profile, f.key, scalar(f.value))
		}
	}
	if created && mapValue(root, "default_provider") == nil {
		setKey(root, "default_provider", scalar(upd.Name))
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return fmt.Errorf("config: encoding %s: %w", path, err)
	}
	_ = enc.Close()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Write beside the file and rename, so a failure halfway leaves the
	// old config, not half a new one. The key may be in it: owner only.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
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
