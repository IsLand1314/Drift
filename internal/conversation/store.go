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

	"github.com/IsLand1314/Drift/internal/llm"
)

var (
	ErrNotFound        = errors.New("conversation: not found")
	ErrInvalidID       = errors.New("conversation: invalid id")
	ErrInvalidSnapshot = errors.New("conversation: invalid snapshot")
	idPattern          = regexp.MustCompile(`^conv-[a-z0-9-]{8,128}$`)
)

type Snapshot struct {
	Version      int
	ID           string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Focus        string
	ContextBytes int
	Messages     []llm.Message
}

type Metadata struct {
	Version      int
	ID           string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Focus        string
	MessageCount int
	ContextBytes int
}

type Store struct{ root string }

func NewStore(workspace string) *Store {
	return &Store{root: filepath.Join(workspace, ".drift", "conversations")}
}

type persistedSnapshot struct {
	Version      int                `json:"version"`
	ID           string             `json:"id"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	Focus        string             `json:"focus,omitempty"`
	ContextBytes int                `json:"context_bytes"`
	Messages     []persistedMessage `json:"messages"`
}

type persistedMessage struct {
	Role             string              `json:"role"`
	Content          string              `json:"content,omitempty"`
	ToolCalls        []persistedToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
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
	if err := ensureDirectory(s.root); err != nil {
		return err
	}
	path, err := s.pathFor(snapshot.ID)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(toPersisted(snapshot), "", "  ")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	tmp, err := os.CreateTemp(s.root, ".snapshot-*")
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

func (s *Store) Load(id string) (Snapshot, error) {
	path, err := s.pathFor(id)
	if err != nil {
		return Snapshot{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil || !info.Mode().IsRegular() {
		return Snapshot{}, ErrInvalidSnapshot
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
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]Metadata, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		snapshot, err := s.Load(id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, Metadata{Version: snapshot.Version, ID: snapshot.ID, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, MessageCount: len(snapshot.Messages), ContextBytes: snapshot.ContextBytes})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *Store) Delete(id string) error {
	path, err := s.pathFor(id)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil || !info.Mode().IsRegular() {
		return ErrInvalidSnapshot
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else {
		return err
	}
}

func (s *Store) pathFor(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", ErrInvalidID
	}
	return filepath.Join(s.root, id+".json"), nil
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
		return os.Mkdir(path, 0o700)
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInvalidSnapshot
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != 1 || !idPattern.MatchString(snapshot.ID) || snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() || snapshot.ContextBytes < 0 || validateFocus(snapshot.Focus) != nil {
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
	return nil
}

func toPersisted(snapshot Snapshot) persistedSnapshot {
	result := persistedSnapshot{Version: snapshot.Version, ID: snapshot.ID, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, ContextBytes: snapshot.ContextBytes, Messages: make([]persistedMessage, len(snapshot.Messages))}
	for i, message := range snapshot.Messages {
		result.Messages[i] = persistedMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ReasoningContent: message.ReasoningContent, ToolCalls: make([]persistedToolCall, len(message.ToolCalls))}
		for j, call := range message.ToolCalls {
			result.Messages[i].ToolCalls[j] = persistedToolCall{ID: call.ID, Type: call.Type, Name: call.Name, Arguments: call.Arguments}
		}
	}
	return result
}

func fromPersisted(snapshot persistedSnapshot) Snapshot {
	result := Snapshot{Version: snapshot.Version, ID: snapshot.ID, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt, Focus: snapshot.Focus, ContextBytes: snapshot.ContextBytes, Messages: make([]llm.Message, len(snapshot.Messages))}
	for i, message := range snapshot.Messages {
		result.Messages[i] = llm.Message{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ReasoningContent: message.ReasoningContent, ToolCalls: make([]llm.ToolCall, len(message.ToolCalls))}
		for j, call := range message.ToolCalls {
			result.Messages[i].ToolCalls[j] = llm.ToolCall{ID: call.ID, Type: call.Type, Name: call.Name, Arguments: call.Arguments}
		}
	}
	return result
}
