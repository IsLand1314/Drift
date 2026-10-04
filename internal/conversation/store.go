// Package conversation stores local, resumable chat context separately from audit logs.
package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/IsLand1314/Drift/internal/layout"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/memory"
	"github.com/IsLand1314/Drift/internal/tool"
)

var (
	ErrNotFound        = errors.New("conversation: not found")
	ErrInvalidID       = errors.New("conversation: invalid id")
	ErrInvalidSnapshot = errors.New("conversation: invalid snapshot")
	ErrInvalidQuery    = errors.New("conversation: invalid search query")
	idPattern          = regexp.MustCompile(`^conv-[a-z0-9-]{8,128}$`)
)

type Snapshot struct {
	Version            int
	ID                 string
	Title              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Focus              string
	ContextBytes       int
	Messages           []llm.Message
	InputTokens        int
	OutputTokens       int
	ReportedRequests   int
	UnreportedRequests int
	Tasks              []tool.TaskState
	PlanID             string
	PlanPhase          string
	Plan               tool.Plan
	ShortTermMemory    []memory.Item
}

type Metadata struct {
	Version      int
	ID           string
	Title        string
	Preview      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Focus        string
	MessageCount int
	ContextBytes int
}

type SearchMatch struct {
	Index int
	Role  string
}

type SearchResult struct {
	ID           string
	Title        string
	UpdatedAt    time.Time
	MessageCount int
	ContextBytes int
	Matches      []SearchMatch
}

type Store struct {
	root string
}

func NewStore(workspace string) *Store {
	drift := filepath.Join(workspace, ".drift")
	return &Store{root: filepath.Join(drift, "sessions")}
}

type persistedSnapshot struct {
	Version            int                `json:"version"`
	ID                 string             `json:"id"`
	Title              string             `json:"title,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
	Focus              string             `json:"focus,omitempty"`
	ContextBytes       int                `json:"context_bytes"`
	Messages           []persistedMessage `json:"messages"`
	InputTokens        int                `json:"input_tokens,omitempty"`
	OutputTokens       int                `json:"output_tokens,omitempty"`
	ReportedRequests   int                `json:"reported_requests,omitempty"`
	UnreportedRequests int                `json:"unreported_requests,omitempty"`
	Tasks              []tool.TaskState   `json:"tasks,omitempty"`
	PlanID             string             `json:"plan_id,omitempty"`
	PlanPhase          string             `json:"plan_phase,omitempty"`
	Plan               tool.Plan          `json:"plan,omitempty"`
	ShortTermMemory    []memory.Item      `json:"short_term_memory,omitempty"`
}

type persistedMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []persistedToolCall `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type persistedToolCall struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func (s *Store) Create(focus string) (Snapshot, error) {
	if err := validateFocus(focus); err != nil {
		return Snapshot{}, err
	}
	id, err := newID()
	if err != nil {
		return Snapshot{}, err
	}
	now := time.Now().UTC()
	snapshot := Snapshot{Version: 1, ID: id, CreatedAt: now, UpdatedAt: now, Focus: focus}
	if err := s.Save(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) Save(snapshot Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	if err := ensureDirectory(filepath.Dir(s.root)); err != nil {
		return err
	}
	path := filepath.Join(layout.DateDir(s.root, snapshot.CreatedAt), "session-"+layout.FileTimestamp(snapshot.CreatedAt)+"-"+snapshot.ID+".json")
	if err := ensureDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(toPersisted(snapshot), "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows may not replace an existing file through Rename.
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		if retryErr := os.Rename(tmpName, path); retryErr != nil {
			return retryErr
		}
	}
	return nil
}

func (s *Store) Rename(id, title string) error {
	if err := validateTitle(title); err != nil {
		return err
	}
	snapshot, err := s.Load(id)
	if err != nil {
		return err
	}
	snapshot.Title = title
	snapshot.UpdatedAt = time.Now().UTC()
	return s.Save(snapshot)
}

func (s *Store) Load(id string) (Snapshot, error) {
	path, err := s.findPath(id)
	if err != nil {
		return Snapshot{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, err
	}
	defer file.Close()
	var persisted persistedSnapshot
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	if err := decoder.Decode(&persisted); err != nil {
		return Snapshot{}, ErrInvalidSnapshot
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Snapshot{}, ErrInvalidSnapshot
	}
	snapshot := fromPersisted(persisted)
	if snapshot.ID != id || validateSnapshot(snapshot) != nil {
		return Snapshot{}, ErrInvalidSnapshot
	}
	return snapshot, nil
}

func (s *Store) Latest() (Snapshot, error) {
	metadata, err := s.List()
	if err != nil {
		return Snapshot{}, err
	}
	if len(metadata) == 0 {
		return Snapshot{}, ErrNotFound
	}
	return s.Load(metadata[0].ID)
}

func (s *Store) List() ([]Metadata, error) {
	ids := make(map[string]struct{})
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") && strings.HasSuffix(entry.Name(), ".json") {
			if id, ok := sessionIDFromFilename(entry.Name()); ok {
				ids[id] = struct{}{}
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(ids))
	for id := range ids {
		snapshot, err := s.Load(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, Metadata{Version: snapshot.Version, ID: snapshot.ID, Title: snapshot.Title, Preview: previewOf(snapshot.Messages), CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, MessageCount: len(snapshot.Messages), ContextBytes: snapshot.ContextBytes})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

// Search returns bounded message locations without returning message bodies.
func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrInvalidQuery
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	needle := strings.ToLower(query)
	metadata, err := s.List()
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, minInt(limit, len(metadata)))
	for _, item := range metadata {
		snapshot, err := s.Load(item.ID)
		if err != nil {
			return nil, err
		}
		matches := make([]SearchMatch, 0, 8)
		for index, message := range snapshot.Messages {
			content := strings.ToLower(message.Content)
			if strings.Contains(content, needle) {
				matches = append(matches, SearchMatch{Index: index + 1, Role: message.Role})
			}
			for _, call := range message.ToolCalls {
				if strings.Contains(strings.ToLower(call.Name+" "+call.Arguments), needle) {
					matches = append(matches, SearchMatch{Index: index + 1, Role: message.Role})
					break
				}
			}
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 64 {
			matches = matches[:64]
		}
		results = append(results, SearchResult{ID: item.ID, Title: item.Title, UpdatedAt: item.UpdatedAt, MessageCount: item.MessageCount, ContextBytes: item.ContextBytes, Matches: matches})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sessionIDFromFilename(name string) (string, bool) {
	name = strings.TrimSuffix(strings.TrimPrefix(name, "session-"), ".json")
	index := strings.LastIndex(name, "-conv-")
	if index < 0 {
		return "", false
	}
	id := name[index+1:]
	return id, idPattern.MatchString(id)
}

func previewOf(messages []llm.Message) string {
	for _, message := range messages {
		if message.Role != "user" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		preview := strings.Join(strings.Fields(message.Content), " ")
		runes := []rune(preview)
		if len(runes) > 96 {
			preview = string(runes[:93]) + "..."
		}
		return preview
	}
	return ""
}

func (s *Store) Before(before time.Time) ([]Metadata, error) {
	metadata, err := s.List()
	if err != nil {
		return nil, err
	}
	filtered := metadata[:0]
	for _, item := range metadata {
		if item.UpdatedAt.Before(before) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (s *Store) Prune(before time.Time) ([]Metadata, error) {
	candidates, err := s.Before(before)
	if err != nil {
		return nil, err
	}
	removed := make([]Metadata, 0, len(candidates))
	for _, candidate := range candidates {
		current, err := s.Load(candidate.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !current.UpdatedAt.Before(before) {
			continue
		}
		if err := s.Delete(candidate.ID); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		candidate.Title = current.Title
		removed = append(removed, candidate)
	}
	return removed, nil
}

func (s *Store) Delete(id string) error {
	path, err := s.findPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

func (s *Store) findPath(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", ErrInvalidID
	}
	var found string
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "session-") && strings.HasSuffix(entry.Name(), "-"+id+".json") {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", ErrNotFound
	}
	return found, nil
}

func newID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "conv-" + hex.EncodeToString(bytes), nil
}

func validateFocus(focus string) error {
	if focus == "" {
		return nil
	}
	if filepath.IsAbs(focus) || filepath.Clean(focus) != focus || focus == "." || strings.HasPrefix(focus, ".."+string(filepath.Separator)) || focus == ".." || strings.HasPrefix(strings.ToLower(filepath.Base(focus)), ".env") {
		return ErrInvalidSnapshot
	}
	return nil
}

func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o700)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInvalidSnapshot
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != 1 || !idPattern.MatchString(snapshot.ID) || snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() || snapshot.ContextBytes < 0 || validateFocus(snapshot.Focus) != nil || validateTitle(snapshot.Title) != nil {
		return ErrInvalidSnapshot
	}
	seenTasks := make(map[string]struct{}, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if !tool.ValidateTaskState(task) {
			return ErrInvalidSnapshot
		}
		if _, exists := seenTasks[task.ID]; exists {
			return ErrInvalidSnapshot
		}
		seenTasks[task.ID] = struct{}{}
	}
	if snapshot.PlanID != "" && !strings.HasPrefix(snapshot.PlanID, "plan-") {
		return ErrInvalidSnapshot
	}
	for _, message := range snapshot.Messages {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" {
			return ErrInvalidSnapshot
		}
		for _, call := range message.ToolCalls {
			if call.ID == "" || call.Name == "" {
				return ErrInvalidSnapshot
			}
		}
	}
	validatedMemory := memory.New(nil)
	for _, item := range snapshot.ShortTermMemory {
		if err := validatedMemory.Remember(item.Kind, item.Text, item.Source); err != nil {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func validateTitle(title string) error {
	if len([]byte(title)) > 120 {
		return ErrInvalidSnapshot
	}
	for _, r := range title {
		if r == '\r' || r == '\n' || r == 0 || unicode.IsControl(r) {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func toPersisted(snapshot Snapshot) persistedSnapshot {
	result := persistedSnapshot{Version: snapshot.Version, ID: snapshot.ID, Title: snapshot.Title, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, ContextBytes: snapshot.ContextBytes, Messages: make([]persistedMessage, len(snapshot.Messages)), InputTokens: snapshot.InputTokens, OutputTokens: snapshot.OutputTokens, ReportedRequests: snapshot.ReportedRequests, UnreportedRequests: snapshot.UnreportedRequests, Tasks: append([]tool.TaskState(nil), snapshot.Tasks...), PlanID: snapshot.PlanID, PlanPhase: snapshot.PlanPhase, Plan: snapshot.Plan, ShortTermMemory: append([]memory.Item(nil), snapshot.ShortTermMemory...)}
	for i, message := range snapshot.Messages {
		result.Messages[i] = persistedMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ToolCalls: make([]persistedToolCall, len(message.ToolCalls))}
		for j, call := range message.ToolCalls {
			result.Messages[i].ToolCalls[j] = persistedToolCall{ID: call.ID, Type: call.Type, Name: call.Name, Arguments: call.Arguments}
		}
	}
	return result
}

func fromPersisted(snapshot persistedSnapshot) Snapshot {
	result := Snapshot{Version: snapshot.Version, ID: snapshot.ID, Title: snapshot.Title, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, ContextBytes: snapshot.ContextBytes, Messages: make([]llm.Message, len(snapshot.Messages)), InputTokens: snapshot.InputTokens, OutputTokens: snapshot.OutputTokens, ReportedRequests: snapshot.ReportedRequests, UnreportedRequests: snapshot.UnreportedRequests, Tasks: append([]tool.TaskState(nil), snapshot.Tasks...), PlanID: snapshot.PlanID, PlanPhase: snapshot.PlanPhase, Plan: snapshot.Plan, ShortTermMemory: append([]memory.Item(nil), snapshot.ShortTermMemory...)}
	for i, message := range snapshot.Messages {
		result.Messages[i] = llm.Message{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ToolCalls: make([]llm.ToolCall, len(message.ToolCalls))}
		for j, call := range message.ToolCalls {
			result.Messages[i].ToolCalls[j] = llm.ToolCall{ID: call.ID, Type: call.Type, Name: call.Name, Arguments: call.Arguments}
		}
	}
	return result
}
