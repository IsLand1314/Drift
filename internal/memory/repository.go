package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Repository struct {
	root string
	mu   sync.Mutex
}

func NewRepository(workspace string) (*Repository, error) {
	if workspace == "" || filepath.IsAbs(workspace) && filepath.Clean(workspace) == string(filepath.Separator) {
		return nil, ErrInvalidItem
	}
	root := filepath.Join(workspace, ".drift", "memory")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("memory: create repository: %w", err)
	}
	return &Repository{root: root}, nil
}

func (r *Repository) Add(item Item) error {
	if err := validatePersistent(item); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.readKindLocked(item.Kind)
	if err != nil {
		return err
	}
	for index, existing := range items {
		if strings.EqualFold(existing.Text, item.Text) {
			if item.Status == "" || item.Status == existing.Status {
				return nil
			}
			items[index] = item
			if err := r.replaceKindLocked(item.Kind, items); err != nil {
				return err
			}
			return r.writeIndexLocked()
		}
	}
	item.UpdatedAt = time.Now().UTC()
	path := filepath.Join(r.root, kindFile(item.Kind))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("memory: open store: %w", err)
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(item); err != nil {
		return fmt.Errorf("memory: encode item: %w", err)
	}
	return r.writeIndexLocked()
}

func (r *Repository) Search(query string, limit int) ([]Item, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, ErrInvalidItem
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []Item
	for _, kind := range []Kind{KindFact, KindDecision, KindExperience} {
		items, err := r.readKindLocked(kind)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Text+" "+item.Source+" "+item.Status), query) {
				matches = append(matches, item)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].UpdatedAt.After(matches[j].UpdatedAt) })
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func (r *Repository) Delete(kind Kind, text string) error {
	if !validKind(kind) || strings.TrimSpace(text) == "" {
		return ErrInvalidItem
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.readKindLocked(kind)
	if err != nil {
		return err
	}
	filtered := items[:0]
	for _, item := range items {
		if !strings.EqualFold(item.Text, strings.TrimSpace(text)) {
			filtered = append(filtered, item)
		}
	}
	if err := r.replaceKindLocked(kind, filtered); err != nil {
		return err
	}
	return r.writeIndexLocked()
}

func (r *Repository) ReviewExperience(text, status string) error {
	status = strings.TrimSpace(status)
	if status != "candidate" && status != "verified" && status != "deprecated" && status != "rejected" {
		return ErrInvalidItem
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.readKindLocked(KindExperience)
	if err != nil {
		return err
	}
	found := false
	for index := range items {
		if strings.EqualFold(items[index].Text, strings.TrimSpace(text)) {
			items[index].Status = status
			items[index].UpdatedAt = time.Now().UTC()
			found = true
		}
	}
	if !found {
		return os.ErrNotExist
	}
	if err := r.replaceKindLocked(KindExperience, items); err != nil {
		return err
	}
	return r.writeIndexLocked()
}

func (r *Repository) readKindLocked(kind Kind) ([]Item, error) {
	path := filepath.Join(r.root, kindFile(kind))
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: open %s: %w", kind, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var items []Item
	for scanner.Scan() {
		var item Item
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil || validatePersistent(item) != nil {
			return nil, ErrInvalidItem
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("memory: read %s: %w", kind, err)
	}
	return items, nil
}

func (r *Repository) replaceKindLocked(kind Kind, items []Item) error {
	path := filepath.Join(r.root, kindFile(kind))
	tmp, err := os.CreateTemp(r.root, ".memory-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	for _, item := range items {
		if err := json.NewEncoder(tmp).Encode(item); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (r *Repository) writeIndexLocked() error {
	counts := map[string]int{}
	for _, kind := range []Kind{KindFact, KindDecision, KindExperience} {
		items, err := r.readKindLocked(kind)
		if err != nil {
			return err
		}
		counts[string(kind)] = len(items)
	}
	path := filepath.Join(r.root, "index.json")
	tmp, err := os.CreateTemp(r.root, ".index-*")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(tmp).Encode(counts); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func validatePersistent(item Item) error {
	if err := (&Store{}).Remember(item.Kind, item.Text, item.Source); err != nil {
		return err
	}
	if item.UpdatedAt.IsZero() {
		return nil
	}
	return nil
}

func kindFile(kind Kind) string {
	switch kind {
	case KindDecision:
		return "decisions.jsonl"
	case KindExperience:
		return "experiences.jsonl"
	default:
		return "facts.jsonl"
	}
}
