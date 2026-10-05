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

type SearchOptions struct {
	Kind   Kind
	Status string
	Limit  int
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
	// Automatic preference candidates with the same subject are kept for
	// explicit review instead of silently replacing a verified memory.
	if item.Kind == KindExperience && item.Status == "candidate" {
		if key := experienceConflictKey(item.Text); key != "" {
			for _, existing := range items {
				if existing.Kind == KindExperience && existing.Status != "rejected" && experienceConflictKey(existing.Text) == key {
					item.Status = "conflict"
					item.Source = "auto:user_turn:conflict"
					break
				}
			}
		}
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
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
	return r.SearchWithOptions(query, SearchOptions{Limit: limit})
}

func (r *Repository) SearchWithOptions(query string, options SearchOptions) ([]Item, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, ErrInvalidItem
	}
	if options.Limit <= 0 || options.Limit > 100 {
		options.Limit = 20
	}
	if options.Kind != "" && !validKind(options.Kind) {
		return nil, ErrInvalidItem
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []Item
	for _, kind := range []Kind{KindFact, KindDecision, KindExperience} {
		if options.Kind != "" && options.Kind != kind {
			continue
		}
		items, err := r.readKindLocked(kind)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if options.Status != "" && item.Status != options.Status {
				continue
			}
			if kind == KindExperience && item.Status != "verified" && options.Status == "" {
				continue
			}
			if strings.Contains(strings.ToLower(item.Text+" "+item.Source+" "+item.Status), query) {
				matches = append(matches, item)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].UpdatedAt.After(matches[j].UpdatedAt) })
	if len(matches) > options.Limit {
		matches = matches[:options.Limit]
	}
	return matches, nil
}

func (r *Repository) PruneBefore(cutoff time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for _, kind := range []Kind{KindFact, KindDecision, KindExperience} {
		items, err := r.readKindLocked(kind)
		if err != nil {
			return removed, err
		}
		kept := items[:0]
		for _, item := range items {
			if !item.UpdatedAt.IsZero() && item.UpdatedAt.Before(cutoff) {
				removed++
				continue
			}
			kept = append(kept, item)
		}
		if err := r.replaceKindLocked(kind, kept); err != nil {
			return removed, err
		}
	}
	return removed, r.writeIndexLocked()
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
	if status != "candidate" && status != "conflict" && status != "verified" && status != "deprecated" && status != "rejected" {
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

// experienceConflictKey is deliberately conservative: only explicit preference
// phrases get a subject key. Free-form memories remain untouched until the user
// explicitly resolves them.
func experienceConflictKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"默认", "以后", "今后", "请始终", "我偏好", "我喜欢", "更正", "纠正", "不要再", "请不要"} {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
			for _, word := range []string{"使用", "采用", "回答", "设置为", "改为"} {
				text = strings.TrimSpace(strings.TrimPrefix(text, word))
			}
			if text != "" {
				return prefix
			}
		}
	}
	// ExtractCandidates stores the value after the explicit prefix. Recognize
	// the small, high-confidence language preference family without guessing at
	// arbitrary free-form semantics.
	if strings.HasPrefix(text, "使用") {
		key := text
		for _, value := range []string{"中文", "英文", "英语", "chinese", "english"} {
			key = strings.ReplaceAll(key, value, "")
		}
		key = strings.TrimSpace(key)
		if key != text && key != "" {
			return key
		}
	}
	return ""
}

// ReplaceExperience explicitly retires one exact experience and verifies its
// replacement. It never guesses that two free-form memories conflict.
func (r *Repository) ReplaceExperience(oldText, newText string) error {
	oldText, newText = strings.TrimSpace(oldText), strings.TrimSpace(newText)
	if oldText == "" || (&Store{}).Remember(KindExperience, newText, "manual:replace") != nil {
		return ErrInvalidItem
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.readKindLocked(KindExperience)
	if err != nil {
		return err
	}
	for _, item := range items {
		if strings.EqualFold(item.Text, newText) && !strings.EqualFold(item.Text, oldText) {
			return ErrInvalidItem
		}
	}
	for index := range items {
		if strings.EqualFold(items[index].Text, oldText) {
			items[index].Text = newText
			items[index].Source = "manual:replace"
			items[index].Status = "verified"
			items[index].UpdatedAt = time.Now().UTC()
			if err := r.replaceKindLocked(KindExperience, items); err != nil {
				return err
			}
			return r.writeIndexLocked()
		}
	}
	return os.ErrNotExist
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
