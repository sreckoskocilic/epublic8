package model

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Profile stores cluster selections for a previously-analyzed book.
type Profile struct {
	Fingerprint string        `json:"fingerprint"`
	Title       string        `json:"title"`
	Clusters    []FontCluster `json:"clusters"`
	SelectedIDs []int         `json:"selected_ids"`
	CreatedAt   time.Time     `json:"created_at"`
	LastUsedAt  time.Time     `json:"last_used_at"`
}

// ProfileStore manages profile persistence via JSON files.
type ProfileStore struct {
	dir string
	mu  sync.RWMutex
}

// NewProfileStore creates a store backed by dir. Creates dir if needed.
func NewProfileStore(dir string) (*ProfileStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create profile dir: %w", err)
	}
	return &ProfileStore{dir: dir, mu: sync.RWMutex{}}, nil
}

// Save writes a profile to disk, keyed by fingerprint hash.
func (s *ProfileStore) Save(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}
	path := s.profilePath(p.Fingerprint)
	return os.WriteFile(path, data, 0600)
}

// Match finds the best matching profile for a fingerprint using fuzzy
// word-trigram Jaccard similarity. Returns nil if no match above threshold.
func (s *ProfileStore) Match(fingerprint string) (*Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	queryWords := wordSet(fingerprint)
	if len(queryWords) == 0 {
		return nil, false
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, false
	}

	const matchThreshold = 0.6
	var bestProfile *Profile
	bestScore := 0.0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := s.loadProfile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		storedWords := wordSet(p.Fingerprint)
		score := jaccardSimilarity(queryWords, storedWords)
		if score > bestScore {
			bestScore = score
			bestProfile = p
		}
	}

	if bestScore >= matchThreshold && bestProfile != nil {
		return bestProfile, true
	}
	return nil, false
}

// List returns all saved profiles.
func (s *ProfileStore) List() ([]Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var profiles []Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p, err := s.loadProfile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		profiles = append(profiles, *p)
	}
	return profiles, nil
}

func (s *ProfileStore) loadProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *ProfileStore) profilePath(fingerprint string) string {
	h := sha256.Sum256([]byte(fingerprint))
	name := fmt.Sprintf("%x.json", h[:8])
	return filepath.Join(s.dir, name)
}

// wordSet splits text into individual words for fuzzy matching.
// Word-level Jaccard is more OCR-tolerant than trigrams: a single
// misspelled word only removes one element instead of three.
func wordSet(text string) map[string]bool {
	words := tokenize(text)
	result := make(map[string]bool, len(words))
	for _, w := range words {
		result[w] = true
	}
	return result
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var words []string
	var word strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return words
}

func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
