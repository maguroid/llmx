package session

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

	"github.com/maguroid/llmx/internal/chat"
)

const SchemaVersion = 1

type Store struct {
	root string
	now  func() time.Time
}

type Session struct {
	SchemaVersion int            `json:"schema_version"`
	Profile       string         `json:"profile"`
	Model         string         `json:"model"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Messages      []chat.Message `json:"messages"`
}

type Loaded struct {
	ID      string
	Session Session
	Existed bool
	mtime   time.Time
	reset   bool
}

type Info struct {
	ID        string
	Profile   string
	Model     string
	UpdatedAt time.Time
	Messages  int
}

type Error struct {
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

type SaveResult struct {
	LastErr error
}

func NewStore(root string, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{root: root, now: now}
}

func (s *Store) Start(profile, model string, system *string) (Loaded, error) {
	id, err := s.newID()
	if err != nil {
		return Loaded{}, err
	}
	return s.newLoaded(id, profile, model, system), nil
}

func (s *Store) OpenNamed(name, profile, model string, reset bool, system *string) (Loaded, error) {
	if err := ValidateName(name); err != nil {
		return Loaded{}, err
	}
	id := name
	if reset {
		loaded := s.newLoaded(id, profile, model, system)
		loaded.reset = true
		return loaded, nil
	}
	loaded, err := s.load(id)
	if errors.Is(err, os.ErrNotExist) {
		return s.newLoaded(id, profile, model, system), nil
	}
	if err != nil {
		return Loaded{}, err
	}
	return loaded, nil
}

func (s *Store) ContinueNamed(name string) (Loaded, error) {
	if err := ValidateName(name); err != nil {
		return Loaded{}, err
	}
	loaded, err := s.load(name)
	if errors.Is(err, os.ErrNotExist) {
		return Loaded{}, &Error{Message: fmt.Sprintf("session %q does not exist", name)}
	}
	return loaded, err
}

func (s *Store) ContinueLast(profile, model string, system *string) (Loaded, bool, error) {
	last, err := os.ReadFile(s.lastPath())
	if errors.Is(err, os.ErrNotExist) {
		loaded, startErr := s.Start(profile, model, system)
		return loaded, true, startErr
	}
	if err != nil {
		return Loaded{}, false, err
	}
	id := strings.TrimSpace(string(last))
	if id == "" {
		loaded, startErr := s.Start(profile, model, system)
		return loaded, true, startErr
	}
	loaded, err := s.load(id)
	if errors.Is(err, os.ErrNotExist) {
		loaded, startErr := s.Start(profile, model, system)
		return loaded, true, startErr
	}
	return loaded, false, err
}

func (s *Store) Save(loaded Loaded, messages []chat.Message) error {
	result, err := s.SaveDetailed(loaded, messages)
	if err != nil {
		return err
	}
	if result.LastErr != nil {
		return result.LastErr
	}
	return nil
}

func (s *Store) SaveDetailed(loaded Loaded, messages []chat.Message) (SaveResult, error) {
	if err := s.ensureDirs(); err != nil {
		return SaveResult{}, err
	}
	if loaded.Existed {
		current, err := s.load(loaded.ID)
		if err != nil {
			return SaveResult{}, err
		}
		if !current.mtime.Equal(loaded.mtime) || !current.Session.UpdatedAt.Equal(loaded.Session.UpdatedAt) {
			return SaveResult{}, &Error{Message: fmt.Sprintf("session %q changed on disk", loaded.ID)}
		}
	}
	session := loaded.Session
	now := s.now().UTC()
	if session.SchemaVersion == 0 {
		session.SchemaVersion = SchemaVersion
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	session.Messages = messages
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return SaveResult{}, err
	}
	data = append(data, '\n')
	if loaded.Existed || loaded.reset {
		if err := writeAtomic(s.sessionsDir(), loaded.ID+".json", data); err != nil {
			return SaveResult{}, err
		}
	} else {
		if err := writeAtomicNoReplace(s.sessionsDir(), loaded.ID+".json", data); err != nil {
			if errors.Is(err, os.ErrExist) {
				return SaveResult{}, &Error{Message: fmt.Sprintf("session %q changed on disk", loaded.ID)}
			}
			return SaveResult{}, err
		}
	}
	return SaveResult{LastErr: writeAtomic(s.sessionsDir(), "last", []byte(loaded.ID+"\n"))}, nil
}

func (s *Store) List() ([]Info, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		loaded, err := s.load(id)
		if err != nil {
			continue
		}
		infos = append(infos, Info{
			ID:        id,
			Profile:   loaded.Session.Profile,
			Model:     loaded.Session.Model,
			UpdatedAt: loaded.Session.UpdatedAt,
			Messages:  len(loaded.Session.Messages),
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
	return infos, nil
}

func (s *Store) Remove(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	err := os.Remove(s.sessionPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) Clear() error {
	entries, err := os.ReadDir(s.sessionsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(s.sessionsDir(), entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) newLoaded(id, profile, model string, system *string) Loaded {
	now := s.now().UTC()
	messages := make([]chat.Message, 0)
	if system != nil {
		messages = append(messages, chat.Message{Role: chat.RoleSystem, Content: *system})
	}
	return Loaded{
		ID: id,
		Session: Session{
			SchemaVersion: SchemaVersion,
			Profile:       profile,
			Model:         model,
			CreatedAt:     now,
			UpdatedAt:     now,
			Messages:      messages,
		},
	}
}

func (s *Store) load(id string) (Loaded, error) {
	if err := ValidateName(id); err != nil {
		return Loaded{}, err
	}
	info, err := os.Stat(s.sessionPath(id))
	if err != nil {
		return Loaded{}, err
	}
	data, err := os.ReadFile(s.sessionPath(id))
	if err != nil {
		return Loaded{}, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return Loaded{}, err
	}
	if sess.SchemaVersion != SchemaVersion {
		return Loaded{}, &Error{Message: fmt.Sprintf("unsupported session schema_version %d", sess.SchemaVersion)}
	}
	return Loaded{ID: id, Session: sess, Existed: true, mtime: info.ModTime()}, nil
}

func (s *Store) newID() (string, error) {
	for i := 0; i < 10; i++ {
		suffix := make([]byte, 3)
		if _, err := rand.Read(suffix); err != nil {
			return "", err
		}
		id := s.now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(suffix)
		if _, err := os.Stat(s.sessionPath(id)); errors.Is(err, os.ErrNotExist) {
			return id, nil
		}
	}
	return "", &Error{Message: "could not allocate session id"}
}

func (s *Store) ensureDirs() error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(s.sessionsDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(s.sessionsDir(), 0o700)
}

func (s *Store) sessionsDir() string {
	return filepath.Join(s.root, "sessions")
}

func (s *Store) sessionPath(id string) string {
	return filepath.Join(s.sessionsDir(), id+".json")
}

func (s *Store) lastPath() string {
	return filepath.Join(s.sessionsDir(), "last")
}

var validName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") || !validName.MatchString(name) {
		return &Error{Message: "invalid session name"}
	}
	return nil
}

func writeAtomic(dir, name string, data []byte) error {
	return writeAtomicMode(dir, name, data, true)
}

func writeAtomicNoReplace(dir, name string, data []byte) error {
	return writeAtomicMode(dir, name, data, false)
}

func writeAtomicMode(dir, name string, data []byte, replace bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if replace {
		if err := os.Rename(tmpName, target); err != nil {
			return err
		}
		cleanup = false
	} else {
		if err := os.Link(tmpName, target); err != nil {
			return err
		}
		_ = os.Remove(tmpName)
		cleanup = false
	}
	if d, err := os.Open(dir); err == nil {
		_, _ = io.Copy(io.Discard, d)
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
